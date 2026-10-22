// Command remedy-import-sqlite copies the data of a SQLite database of Remedy (schema 018, the last SQLite release) into an
// empty PostgreSQL database, ids unchanged, so that every sealed value still opens with the same master key. It is a one-time
// tool for the move to PostgreSQL (docs/specs/2026-10-22-postgres-only-design.md, decision 9; runbook
// docs/runbook/postgres-cutover.md) and is kept for one release.
//
//	REMEDY_DATABASE_URL=postgres://... remedy-import-sqlite --from /path/to/copy-of-remedy.db [--dry-run] [--replace-invalid-text]
//
// The URL is read from the environment only: in an argument its password would show in the process list. It is never
// printed. The destination must be a dedicated database for Remedy: its current schema may hold no table at all, or only
// Remedy's own tables (made by the server, with the baseline migration recorded); any other table is refused before anything
// is created (store.CheckImportTarget). The destination's schema is then made by the server's own migrations (as the server
// would on its first start); a destination with any row is refused. See internal/importsqlite for what the import guarantees.
//
// Text that PostgreSQL cannot store (a NUL byte or invalid UTF-8, which SQLite stored) is refused by default, and nothing is
// imported. --replace-invalid-text converts it instead, the way the server's store converts every text it writes: each NUL
// and each invalid UTF-8 sequence becomes U+FFFD, the replacement character. The report then counts the changed values per
// table and column (never the values), in a dry run too. Use it only when a plain dry run refused text values.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/Jaydee94/remedy/internal/importsqlite"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

const envURL = "REMEDY_DATABASE_URL"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is the command without the process around it: it returns the exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remedy-import-sqlite", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "the SQLite file to import: a copy of remedy.db, with its -wal next to it if it has one (required)")
	dryRun := fs.Bool("dry-run", false, "copy and read back everything inside the transaction, then roll it back: no row is written "+
		"(an empty database still gets the schema, as the server would make it)")
	replace := fs.Bool("replace-invalid-text", false, "store text that PostgreSQL refuses (a NUL byte, invalid UTF-8) with U+FFFD, the replacement "+
		"character, in place of each NUL and each invalid sequence, instead of refusing the import; the report counts the changed values "+
		"per table and column. Use it only when a plain dry run refused text values")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: %s=postgres://... remedy-import-sqlite --from <file> [--dry-run] [--replace-invalid-text]\n", envURL)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		// The arguments are not echoed: a database URL given by mistake would show its password.
		fmt.Fprintf(stderr, "remedy-import-sqlite: %d unexpected arguments (not shown; the database URL is read from %s, never from an argument)\n", fs.NArg(), envURL)
		return 2
	}
	if *from == "" {
		fmt.Fprintln(stderr, "remedy-import-sqlite: --from is required")
		fs.Usage()
		return 2
	}
	url := getenv(envURL)
	if url == "" {
		fmt.Fprintf(stderr, "remedy-import-sqlite: %s is not set: it names the PostgreSQL database to import into\n", envURL)
		return 2
	}

	// Before Open: its migration would create Remedy's tables in a database that holds something else.
	if err := store.CheckImportTarget(ctx, secret.NewValue(url)); err != nil {
		fmt.Fprintf(stderr, "remedy-import-sqlite: nothing was imported: %v\n", err) // its errors never contain the URL
		return 1
	}
	dst, err := store.Open(ctx, secret.NewValue(url), store.WithMaxConns(2))
	if err != nil {
		fmt.Fprintf(stderr, "remedy-import-sqlite: %v\n", err) // Open's errors never contain the URL
		return 1
	}
	defer dst.Close()

	rep, err := importsqlite.Run(ctx, *from, dst, importsqlite.Options{DryRun: *dryRun, ReplaceInvalidText: *replace})
	if err != nil {
		fmt.Fprintf(stderr, "remedy-import-sqlite: nothing was imported: %v\n", err)
		return 1
	}
	printReport(stdout, rep, *replace)
	return 0
}

func printReport(w io.Writer, rep importsqlite.Report, replace bool) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TABLE\tROWS")
	total := 0
	for _, t := range rep.Tables {
		fmt.Fprintf(tw, "%s\t%d\n", t.Name, t.Rows)
		total += t.Rows
	}
	fmt.Fprintf(tw, "total\t%d\n", total)
	_ = tw.Flush()
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQUENCE\tLAST ID")
	for _, s := range rep.Sequences {
		fmt.Fprintf(tw, "%s\t%d\n", s.Table, s.Last)
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
	if replace {
		if len(rep.Replaced) > 0 {
			tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "TABLE\tCOLUMN\tVALUES CHANGED")
			for _, r := range rep.Replaced {
				fmt.Fprintf(tw, "%s\t%s\t%d\n", r.Table, r.Column, r.Values)
			}
			_ = tw.Flush()
		}
		fmt.Fprintf(w, "%d values changed: replacement character (U+FFFD) in place of each NUL byte and each invalid UTF-8 sequence.\n\n",
			rep.ReplacedTotal())
	}
	if rep.DryRun {
		fmt.Fprintln(w, "Dry run: every row was copied and read back inside a transaction that was then rolled back. No row was written and the "+
			"sequences were not set (an empty database did get the schema, as the server would make it).")
		return
	}
	fmt.Fprintln(w, "Imported. Every table was read back from PostgreSQL: its row count and ids equal the file's, and every row equals "+
		"what was read from the file and converted. Each id sequence continues above its last id.")
}
