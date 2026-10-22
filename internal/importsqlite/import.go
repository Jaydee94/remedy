// Package importsqlite carries the data of a SQLite database at schema 018 into an empty PostgreSQL database whose schema the
// store made (docs/specs/2026-10-22-postgres-only-design.md, decisions 5, 6 and 9). It is a one-time tool, kept for one release.
//
// What it guarantees:
//
//   - The source is opened read-only (mode=ro, query_only) and read in one read transaction, so what is copied is one
//     consistent snapshot. Neither the file nor its -wal is written (a -shm may be created or updated next to them: it is
//     SQLite's index of the -wal, not data). A file that another process has open (a server that still runs on it) is
//     refused with ErrSourceBusy.
//   - The source must be at exactly schema 018: its schema_migrations names equal SourceMigrations, in both directions.
//   - The destination must be empty (every table but schema_migrations); it is locked for the whole run.
//   - Everything is written in one destination transaction: a failure anywhere rolls back every row.
//   - Values are copied as they are: ids unchanged (OVERRIDING SYSTEM VALUE), text as text, sealed bytes as bytes, numbers as
//     numbers. A value whose kind does not fit its destination column is refused, never converted by guessing.
//   - Text that PostgreSQL cannot store (a NUL byte, bytes that are not valid UTF-8; SQLite stored both) is refused by default.
//     Only with Options.ReplaceInvalidText (--replace-invalid-text) is it converted, exactly as the server's store converts
//     every text it writes (internal/pgtext: each NUL and each invalid UTF-8 sequence becomes U+FFFD), and the report counts
//     the changed values per table and column (Report.Replaced; counts only, never the values). Bytes (BYTEA columns, the
//     sealed values among them) are never converted. Two keys that the conversion would make equal are refused.
//   - Tables are copied parents first, in an order derived from the destination's foreign keys. The two cycles of the schema
//     (runs.incident_id and incidents.run_id; skills.replaces_id, which points at skills) are broken by writing
//     runs.incident_id and skills.replaces_id in a second pass; the foreign keys stay as they are.
//   - The source's tables and columns must be exactly the destination's: an extra or missing table or column is refused,
//     so nothing is dropped silently.
//   - After the copy every table is read back from PostgreSQL: its row count and its set of keys equal the file's, and every
//     row equals the row as it was read from the file and converted (a digest of every value, a replaced text in its
//     converted form on both sides). This catches a value lost or
//     changed on the way into PostgreSQL; it does not check the conversion itself, which the tests pin by dumping the file
//     and the database independently and comparing them. Then every identity sequence is set to the higher of the highest id and SQLite's sqlite_sequence high-water mark, so
//     the id of a deleted row is never given again.
package importsqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite" // registers the "sqlite" driver, for the source only

	"github.com/Jaydee94/remedy/internal/pgtext"
	"github.com/Jaydee94/remedy/internal/store"
)

// SourceMigrations are the names of the SQLite migrations that make schema 018, the only schema the importer reads. They are
// the files of testdata/sqlite-migrations (a test keeps the two equal).
var SourceMigrations = []string{
	"001_init.sql", "002_github.sql", "003_incidents.sql", "004_run_extension.sql", "005_responder.sql", "006_gatekeeper.sql",
	"007_cluster.sql", "008_incident_sources.sql", "009_diagnosed_at.sql", "010_hosts.sql", "011_run_hosts.sql",
	"012_settings.sql", "013_alert_hosts.sql", "014_tool_call_expiry.sql", "015_skills.sql", "016_learner_role.sql",
	"017_alertmanager_poll_interval.sql", "018_incident_reopen.sql",
}

// deferred are the columns written in the second pass, one per cycle of the foreign keys: the rows go in with them NULL, and
// an UPDATE sets them once every row of the table they point at is in.
var deferred = map[string]string{
	"runs":   "incident_id", // runs.incident_id -> incidents, incidents.run_id -> runs
	"skills": "replaces_id", // skills.replaces_id -> skills
}

// ErrDestinationNotEmpty means the destination holds a row in some table: the importer writes only into an empty database.
var ErrDestinationNotEmpty = errors.New("the destination database is not empty: the importer writes only into a database that has no rows (one the server created and never used)")

// ErrSourceBusy means another process has the SQLite file open, most likely a server that still runs on it.
var ErrSourceBusy = errors.New("another process has the SQLite file open (a server that still runs on it?): scale the server to zero, copy the file with its -wal, and import the copy")

// ErrSchemaVersion means the SQLite file is not at schema 018.
type ErrSchemaVersion struct {
	Have    string   // the highest migration the file records, or "none"
	Want    string   // the last of SourceMigrations
	Missing []string // migrations of SourceMigrations the file does not record
	Unknown []string // migrations the file records and SourceMigrations does not have
}

func (e *ErrSchemaVersion) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the SQLite file is at schema %s, and this importer reads exactly schema %s", e.Have, e.Want)
	if len(e.Missing) > 0 {
		fmt.Fprintf(&b, " (missing: %s)", strings.Join(e.Missing, ", "))
	}
	if len(e.Unknown) > 0 {
		fmt.Fprintf(&b, " (not known to this importer: %s)", strings.Join(e.Unknown, ", "))
	}
	b.WriteString(": upgrade the server through the last release that runs on SQLite first and let it start once, so that it " +
		"migrates the file, then copy the file again; a file with migrations this importer does not know was written by a newer " +
		"build and needs that build's importer")
	return b.String()
}

// Options change what Run does.
type Options struct {
	// DryRun does everything in the destination transaction, the comparison included, and then rolls it back; the
	// sequences are not touched.
	DryRun bool
	// ReplaceInvalidText converts text that PostgreSQL cannot store (a NUL byte, invalid UTF-8) instead of refusing it: each
	// NUL and each invalid sequence becomes U+FFFD (pgtext.Clean), and Report.Replaced counts the changed values.
	ReplaceInvalidText bool
}

// TableReport is one table's part of the report.
type TableReport struct {
	Name string
	Rows int
}

// SequenceReport says what an identity sequence was set to: the next id given is Last+1.
type SequenceReport struct {
	Table string
	Last  int64
}

// ReplacedReport counts the values of one column whose text was converted (Options.ReplaceInvalidText).
type ReplacedReport struct {
	Table, Column string
	Values        int
}

// Report says what was copied (or, for a dry run, what would have been), in copy order.
type Report struct {
	Tables    []TableReport
	Sequences []SequenceReport
	// Replaced lists, in copy order and column order, every column with at least one converted text value. It is empty
	// without Options.ReplaceInvalidText.
	Replaced []ReplacedReport
	DryRun   bool
}

// ReplacedTotal is the number of text values that were converted, over every table and column.
func (r Report) ReplacedTotal() int {
	n := 0
	for _, x := range r.Replaced {
		n += x.Values
	}
	return n
}

// errDryRun ends the destination transaction of a dry run with a rollback.
var errDryRun = errors.New("dry run")

// Run copies the SQLite file src into dst. See the package comment for what it guarantees.
func Run(ctx context.Context, src string, dst *store.Store, opts Options) (Report, error) {
	// SQLite keeps the -wal and -shm next to the file a symlink points at, so the check and the open use the target.
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return Report{}, fmt.Errorf("the SQLite file: %w", err)
	}
	st, err := os.Stat(src)
	if err != nil {
		return Report{}, fmt.Errorf("the SQLite file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return Report{}, fmt.Errorf("the SQLite file %s is not a regular file", src)
	}
	if err := checkNoOtherProcess(src); err != nil {
		return Report{}, err
	}
	sdb, err := openSource(src)
	if err != nil {
		return Report{}, err
	}
	defer sdb.Close()
	stx, err := sdb.BeginTx(ctx, nil) // one read transaction: one snapshot of the file
	if err != nil {
		return Report{}, fmt.Errorf("read the SQLite file: %w", err)
	}
	defer func() { _ = stx.Rollback() }()

	if err := checkSchema(ctx, stx); err != nil {
		return Report{}, err
	}
	highWater, err := readSequences(ctx, stx)
	if err != nil {
		return Report{}, err
	}

	var rep Report
	err = dst.ImportTx(ctx, func(tx *sql.Tx) error {
		rep = Report{DryRun: opts.DryRun} // a fresh report for this attempt
		if err := copyAll(ctx, stx, tx, highWater, opts, &rep); err != nil {
			return err
		}
		if opts.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return rep, nil
	}
	if err != nil {
		return Report{}, err
	}
	return rep, nil
}

// openSource opens the SQLite file read-only: mode=ro opens the file without write access, query_only refuses every write
// statement on top of it.
func openSource(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// In a file: URI the path ends at ? or #, and % starts an escape.
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(abs))
	db, err := sql.Open("sqlite", "file:"+esc+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("open the SQLite file: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open the SQLite file: %w", err)
	}
	return db, nil
}

// checkSchema refuses a source whose schema_migrations is not exactly SourceMigrations.
func checkSchema(ctx context.Context, stx *sql.Tx) error {
	want := SourceMigrations[len(SourceMigrations)-1]
	var n int
	if err := stx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&n); err != nil {
		return fmt.Errorf("read the SQLite file: %w", err)
	}
	var have []string
	if n > 0 {
		rows, err := stx.QueryContext(ctx, `SELECT version FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("read schema_migrations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return fmt.Errorf("read schema_migrations: %w", err)
			}
			have = append(have, v)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read schema_migrations: %w", err)
		}
	}
	sort.Strings(have)
	e := &ErrSchemaVersion{Have: "none", Want: want}
	if len(have) > 0 {
		e.Have = have[len(have)-1]
	}
	for _, m := range SourceMigrations {
		if !slices.Contains(have, m) {
			e.Missing = append(e.Missing, m)
		}
	}
	for _, m := range have {
		if !slices.Contains(SourceMigrations, m) {
			e.Unknown = append(e.Unknown, m)
		}
	}
	if len(e.Missing) > 0 || len(e.Unknown) > 0 {
		return e
	}
	return nil
}

// readSequences returns SQLite's AUTOINCREMENT high-water marks by table: the highest id each table ever gave.
func readSequences(ctx context.Context, stx *sql.Tx) (map[string]int64, error) {
	out := map[string]int64{}
	var n int
	if err := stx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'`).Scan(&n); err != nil {
		return nil, fmt.Errorf("read sqlite_sequence: %w", err)
	}
	if n == 0 {
		return out, nil
	}
	rows, err := stx.QueryContext(ctx, `SELECT name, seq FROM sqlite_sequence`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite_sequence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var seq int64
		if err := rows.Scan(&name, &seq); err != nil {
			return nil, fmt.Errorf("read sqlite_sequence: %w", err)
		}
		out[name] = seq
	}
	return out, rows.Err()
}

// column is one destination column.
type column struct {
	name, typ string
	identity  bool
}

// table is one destination table with what the copy needs of it.
type table struct {
	name     string
	cols     []column
	pk       []string // primary key columns, in key order
	parents  []string // tables its foreign keys point at, except through its deferred column
	identity string   // the identity column, or ""
}

// identRE is what a table or column name must look like before it goes into a statement.
var identRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func quote(name string) string { return `"` + name + `"` }

// readCatalog describes the destination's tables (all but schema_migrations) from PostgreSQL's catalog.
func readCatalog(ctx context.Context, tx *sql.Tx) (map[string]*table, error) {
	tables := map[string]*table{}
	rows, err := tx.QueryContext(ctx, `SELECT c.table_name, c.column_name, c.data_type, c.is_identity
		FROM information_schema.columns c JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE c.table_schema = current_schema() AND c.table_name <> 'schema_migrations'
		ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tn, cn, typ, ident string
		if err := rows.Scan(&tn, &cn, &typ, &ident); err != nil {
			rows.Close()
			return nil, err
		}
		if !identRE.MatchString(tn) || !identRE.MatchString(cn) {
			rows.Close()
			return nil, fmt.Errorf("the destination has a table or column name the importer does not quote: %q.%q", tn, cn)
		}
		t := tables[tn]
		if t == nil {
			t = &table{name: tn}
			tables[tn] = t
		}
		c := column{name: cn, typ: typ, identity: ident == "YES"}
		t.cols = append(t.cols, c)
		if c.identity {
			t.identity = cn
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Primary keys, in key order.
	rows, err = tx.QueryContext(ctx, `SELECT c.relname, a.attname
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		CROSS JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
		WHERE con.contype = 'p' AND c.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
		ORDER BY c.relname, k.ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tn, cn string
		if err := rows.Scan(&tn, &cn); err != nil {
			rows.Close()
			return nil, err
		}
		if t := tables[tn]; t != nil {
			t.pk = append(t.pk, cn)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Foreign keys.
	rows, err = tx.QueryContext(ctx, `SELECT c.relname, a.attname, p.relname, cardinality(con.conkey)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_class p ON p.oid = con.confrelid
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = con.conkey[1]
		WHERE con.contype = 'f' AND c.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
		ORDER BY c.relname, a.attname`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var child, col, parent string
		var n int
		if err := rows.Scan(&child, &col, &parent, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if n != 1 {
			rows.Close()
			return nil, fmt.Errorf("the destination has a foreign key of %d columns on %s; the importer knows only single-column ones", n, child)
		}
		t := tables[child]
		if t == nil {
			continue
		}
		if deferred[child] == col {
			continue // written in the second pass
		}
		if !slices.Contains(t.parents, parent) {
			t.parents = append(t.parents, parent)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for tn, col := range deferred {
		t := tables[tn]
		if t == nil || len(t.pk) != 1 || !slices.ContainsFunc(t.cols, func(c column) bool { return c.name == col }) {
			return nil, fmt.Errorf("the destination has no %s.%s with a single-column key: the importer's second pass does not fit the schema", tn, col)
		}
	}
	for _, t := range tables {
		if len(t.pk) == 0 {
			return nil, fmt.Errorf("the destination table %s has no primary key; the importer compares rows by key", t.name)
		}
	}
	return tables, nil
}

// copyOrder sorts the tables so that every table comes after the tables its foreign keys point at (deferred columns aside),
// by name among the tables that are free at the same step. A cycle that is left is an error.
func copyOrder(tables map[string]*table) ([]*table, error) {
	done := map[string]bool{}
	var order []*table
	for len(order) < len(tables) {
		var ready []string
		for name, t := range tables {
			if done[name] {
				continue
			}
			free := true
			for _, p := range t.parents {
				if p != name && !done[p] {
					free = false
					break
				}
				if p == name {
					return nil, fmt.Errorf("the destination table %s points at itself through a column the importer does not defer", name)
				}
			}
			if free {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			var left []string
			for name := range tables {
				if !done[name] {
					left = append(left, name)
				}
			}
			sort.Strings(left)
			return nil, fmt.Errorf("the foreign keys of the destination tables %s form a cycle the importer does not break", strings.Join(left, ", "))
		}
		sort.Strings(ready)
		for _, name := range ready {
			done[name] = true
			order = append(order, tables[name])
		}
	}
	return order, nil
}

// sqliteOwn are SQLite's internal tables, by exact name: they hold no data of Remedy and are not copied.
var sqliteOwn = map[string]bool{
	"sqlite_sequence": true, "sqlite_stat1": true, "sqlite_stat2": true, "sqlite_stat3": true, "sqlite_stat4": true,
}

// sourceTables lists the SQLite file's tables (SQLite's own and schema_migrations aside) with their columns.
func sourceTables(ctx context.Context, stx *sql.Tx) (map[string][]string, error) {
	rows, err := stx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, fmt.Errorf("read the SQLite tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		if n == "schema_migrations" || sqliteOwn[n] {
			continue
		}
		if strings.HasPrefix(strings.ToLower(n), "sqlite_") {
			rows.Close()
			return nil, fmt.Errorf("the SQLite file has a table %q that looks like one of SQLite's own but is not one the importer knows", n)
		}
		names = append(names, n) // any other table is compared with the destination's by checkShapes
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, n := range names {
		crows, err := stx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, n)
		if err != nil {
			return nil, fmt.Errorf("read the columns of %s: %w", n, err)
		}
		var cols []string
		for crows.Next() {
			var c string
			if err := crows.Scan(&c); err != nil {
				crows.Close()
				return nil, err
			}
			cols = append(cols, c)
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return nil, err
		}
		sort.Strings(cols)
		out[n] = cols
	}
	return out, nil
}

// checkShapes refuses a source whose tables or columns are not the destination's.
func checkShapes(src map[string][]string, dst map[string]*table) error {
	var srcNames, dstNames []string
	for n := range src {
		srcNames = append(srcNames, n)
	}
	for n := range dst {
		dstNames = append(dstNames, n)
	}
	sort.Strings(srcNames)
	sort.Strings(dstNames)
	if !slices.Equal(srcNames, dstNames) {
		return fmt.Errorf("the SQLite tables (%s) are not the destination's (%s)", strings.Join(srcNames, ", "), strings.Join(dstNames, ", "))
	}
	for _, n := range dstNames {
		var cols []string
		for _, c := range dst[n].cols {
			cols = append(cols, c.name)
		}
		sort.Strings(cols)
		if !slices.Equal(cols, src[n]) {
			return fmt.Errorf("the columns of %s differ: the SQLite file has %s, the destination %s", n, strings.Join(src[n], ", "), strings.Join(cols, ", "))
		}
	}
	return nil
}

// copyAll is the whole destination transaction: checks, copy, second pass, comparison, sequences.
func copyAll(ctx context.Context, stx, tx *sql.Tx, highWater map[string]int64, opts Options, rep *Report) error {
	// A server that still uses the destination would wait on the locks; do not wait for ever.
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '10s'`); err != nil {
		return err
	}
	tables, err := readCatalog(ctx, tx)
	if err != nil {
		return fmt.Errorf("read the destination's schema: %w", err)
	}
	srcShapes, err := sourceTables(ctx, stx)
	if err != nil {
		return err
	}
	if err := checkShapes(srcShapes, tables); err != nil {
		return err
	}
	order, err := copyOrder(tables)
	if err != nil {
		return err
	}

	// Lock every table, so that nothing writes into the destination while it is checked and filled, then check it is empty.
	var names []string
	for _, t := range order {
		names = append(names, quote(t.name))
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE `+strings.Join(names, ", ")+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock the destination tables: %w", err)
	}
	sorted := slices.Clone(order)
	slices.SortFunc(sorted, func(a, b *table) int { return strings.Compare(a.name, b.name) })
	for _, t := range sorted {
		var has bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+quote(t.name)+`)`).Scan(&has); err != nil {
			return fmt.Errorf("check the destination table %s: %w", t.name, err)
		}
		if has {
			return fmt.Errorf("%w (table %s has rows)", ErrDestinationNotEmpty, t.name)
		}
	}

	digests := map[string]map[string][32]byte{}
	type later struct {
		key any
		val any
	}
	seconds := map[string][]later{}
	maxID := map[string]int64{}
	for _, t := range order {
		c := newCopier(tx, t)
		dcol := deferred[t.name]
		seen := map[string][32]byte{}
		replaced := make([]int, len(t.cols))
		err := readRows(ctx, stx, t, opts.ReplaceInvalidText, replaced, func(vals []any) error {
			key := encodeKey(t, vals)
			if _, dup := seen[key]; dup {
				return fmt.Errorf("copy %s: the SQLite file has two rows with key %s", t.name, describeKey(t, vals))
			}
			seen[key] = digest(vals)
			ins := vals
			if dcol != "" {
				i := colIndex(t, dcol)
				if vals[i] != nil {
					seconds[t.name] = append(seconds[t.name], later{key: vals[colIndex(t, t.pk[0])], val: vals[i]})
					ins = slices.Clone(vals)
					ins[i] = nil
				}
			}
			if t.identity != "" {
				id, ok := vals[colIndex(t, t.identity)].(int64)
				if !ok {
					return fmt.Errorf("copy %s: an id that is not an integer", t.name)
				}
				maxID[t.name] = max(maxID[t.name], id)
			}
			return c.add(ctx, ins)
		})
		if err != nil {
			return err
		}
		if err := c.flush(ctx); err != nil {
			return err
		}
		digests[t.name] = seen
		rep.Tables = append(rep.Tables, TableReport{Name: t.name, Rows: len(seen)})
		for i, n := range replaced {
			if n > 0 {
				rep.Replaced = append(rep.Replaced, ReplacedReport{Table: t.name, Column: t.cols[i].name, Values: n})
			}
		}
	}

	// The second pass: the deferred columns, now that every row they point at is in.
	for _, t := range order {
		dcol := deferred[t.name]
		if dcol == "" || len(seconds[t.name]) == 0 {
			continue
		}
		q := `UPDATE ` + quote(t.name) + ` SET ` + quote(dcol) + ` = $1 WHERE ` + quote(t.pk[0]) + ` = $2`
		stmt, err := tx.PrepareContext(ctx, q)
		if err != nil {
			return fmt.Errorf("second pass of %s: %w", t.name, err)
		}
		for _, l := range seconds[t.name] {
			res, err := stmt.ExecContext(ctx, l.val, l.key)
			if err != nil {
				_ = stmt.Close()
				return fmt.Errorf("second pass of %s.%s: %w", t.name, dcol, err)
			}
			if n, err := res.RowsAffected(); err != nil || n != 1 {
				_ = stmt.Close()
				return fmt.Errorf("second pass of %s.%s: %d rows changed, want 1 (%v)", t.name, dcol, n, err)
			}
		}
		_ = stmt.Close()
	}

	// Read every table back and compare it with the source, row by row.
	for _, t := range order {
		if err := verify(ctx, tx, t, digests[t.name]); err != nil {
			return err
		}
	}

	// The sequences: the higher of the highest id and the high-water mark. setval is not undone by a rollback, so it is the
	// last step, and a dry run leaves it out.
	for _, t := range order {
		if t.identity == "" {
			continue
		}
		last := max(maxID[t.name], highWater[t.name])
		if last <= 0 {
			continue // an empty table that never gave an id: the sequence starts at 1 as it is
		}
		rep.Sequences = append(rep.Sequences, SequenceReport{Table: t.name, Last: last})
		if opts.DryRun {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence($1, $2), $3, true)`, quote(t.name), t.identity, last); err != nil {
			return fmt.Errorf("set the id sequence of %s: %w", t.name, err)
		}
	}
	return nil
}

func colIndex(t *table, name string) int {
	return slices.IndexFunc(t.cols, func(c column) bool { return c.name == name })
}

// readRows reads every row of t from the source in key order, converts each value for its destination column and calls fn.
// With replace, text PostgreSQL cannot store is converted (pgtext.Clean) and replaced[i] counts the values of column i that
// changed; without it such text is refused.
func readRows(ctx context.Context, stx *sql.Tx, t *table, replace bool, replaced []int, fn func([]any) error) error {
	var cols, keys []string
	for _, c := range t.cols {
		cols = append(cols, quote(c.name))
	}
	for _, k := range t.pk {
		keys = append(keys, quote(k))
	}
	rows, err := stx.QueryContext(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+quote(t.name)+` ORDER BY `+strings.Join(keys, ", "))
	if err != nil {
		return fmt.Errorf("read %s from the SQLite file: %w", t.name, err)
	}
	defer rows.Close()
	for rows.Next() {
		raw := make([]any, len(t.cols))
		ptrs := make([]any, len(t.cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("read %s from the SQLite file: %w", t.name, err)
		}
		vals := make([]any, len(raw))
		var convErr error
		for i, c := range t.cols {
			v, changed, err := convertValue(raw[i], c.typ, replace)
			if err != nil && convErr == nil {
				convErr = fmt.Errorf("copy %s.%s: %w", t.name, c.name, err)
			}
			if changed {
				replaced[i]++
			}
			vals[i] = v
		}
		if convErr != nil {
			// Name the row by its key (ids and names, never other values: they may be secrets).
			return fmt.Errorf("%w (row %s)", convErr, describeKey(t, raw))
		}
		if err := fn(vals); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s from the SQLite file: %w", t.name, err)
	}
	return nil
}

// convert is convertValue without the replacement of invalid text: such text is refused.
func convert(v any, typ string) (any, error) {
	v, _, err := convertValue(v, typ, false)
	return v, err
}

// errInvalidTextHint is how a refusal of text PostgreSQL cannot store ends: what the operator may do about it.
const errInvalidTextHint = "; run again with --replace-invalid-text to store it with U+FFFD in place of the bad bytes (the report counts the changed values)"

// convertValue checks a SQLite value against the type of its destination column and returns it in the form the driver writes
// exactly. It never guesses: a value of another kind is refused. Text with a NUL byte or invalid UTF-8 is refused too, unless
// replace is set: then it is converted with pgtext.Clean and changed reports it.
func convertValue(v any, typ string, replace bool) (_ any, changed bool, _ error) {
	if v == nil {
		return nil, false, nil
	}
	if typ == "text" {
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case []byte: // text the SQLite server stored as a BLOB (a []byte argument): the same bytes as text
			s = string(x)
		default:
			return nil, false, fmt.Errorf("%s value in a text column", kind(v))
		}
		if pgtext.Valid(s) {
			return s, false, nil
		}
		if replace {
			return pgtext.Clean(s), true, nil
		}
		if !utf8.ValidString(s) {
			return nil, false, errors.New("text that is not valid UTF-8" + errInvalidTextHint)
		}
		return nil, false, errors.New("text with a NUL character, which PostgreSQL cannot store" + errInvalidTextHint)
	}
	out, err := convertOther(v, typ)
	return out, false, err
}

// convertOther converts a non-NULL value for a column that is not text.
func convertOther(v any, typ string) (any, error) {
	switch typ {
	case "integer", "bigint":
		x, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("%s value in an integer column", kind(v))
		}
		if typ == "integer" && (x < math.MinInt32 || x > math.MaxInt32) {
			return nil, errors.New("an integer out of the range of a PostgreSQL integer")
		}
		return x, nil
	case "double precision":
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			// Every integer of magnitude up to 2^53 is a double exactly; a larger one is refused even when it would fit,
			// which keeps the check free of the platform-dependent float-to-int conversion near 2^63.
			if x > 1<<53 || x < -(1<<53) {
				return nil, errors.New("an integer a double cannot hold exactly")
			}
			return float64(x), nil
		default:
			return nil, fmt.Errorf("%s value in a double precision column", kind(v))
		}
	case "bytea":
		switch x := v.(type) {
		case []byte:
			return slices.Clone(x), nil
		case string: // bytes the SQLite server stored as text: the same bytes
			return []byte(x), nil
		default:
			return nil, fmt.Errorf("%s value in a bytea column", kind(v))
		}
	default:
		return nil, fmt.Errorf("a destination column of type %s, which the importer does not know", typ)
	}
}

func kind(v any) string {
	switch v.(type) {
	case int64:
		return "an integer"
	case float64:
		return "a real"
	case string:
		return "a text"
	case []byte:
		return "a blob"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

// encode appends a value in a form that tells the kinds apart, the same for a value read from SQLite and from PostgreSQL.
func encode(b []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(b, 'n')
	case int64:
		b = append(b, 'i')
		return binary.BigEndian.AppendUint64(b, uint64(x))
	case float64:
		b = append(b, 'f')
		return binary.BigEndian.AppendUint64(b, math.Float64bits(x))
	case string:
		b = append(b, 's')
		b = binary.BigEndian.AppendUint64(b, uint64(len(x)))
		return append(b, x...)
	case []byte:
		b = append(b, 'b')
		b = binary.BigEndian.AppendUint64(b, uint64(len(x)))
		return append(b, x...)
	default:
		return append(b, fmt.Sprintf("?%T:%v", v, v)...) // never equal to a known kind: the comparison fails
	}
}

func encodeKey(t *table, vals []any) string {
	var b []byte
	for _, k := range t.pk {
		b = encode(b, vals[colIndex(t, k)])
	}
	return string(b)
}

func digest(vals []any) [32]byte {
	var b []byte
	for _, v := range vals {
		b = encode(b, v)
	}
	return sha256.Sum256(b)
}

// describeKey names a row by its primary key, for an error.
func describeKey(t *table, vals []any) string {
	var parts []string
	for _, k := range t.pk {
		v := vals[colIndex(t, k)]
		switch x := v.(type) {
		case []byte:
			parts = append(parts, fmt.Sprintf("%s=%q", k, string(x)))
		case string:
			parts = append(parts, fmt.Sprintf("%s=%q", k, x))
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", k, x))
		}
	}
	return strings.Join(parts, " ")
}

// copier inserts the rows of one table in batches.
type copier struct {
	tx      *sql.Tx
	t       *table
	maxRows int
	args    []any
	n       int
}

func newCopier(tx *sql.Tx, t *table) *copier {
	// PostgreSQL takes at most 65535 parameters per statement.
	return &copier{tx: tx, t: t, maxRows: max(1, min(500, 60000/len(t.cols)))}
}

func (c *copier) add(ctx context.Context, vals []any) error {
	c.args = append(c.args, vals...)
	c.n++
	if c.n >= c.maxRows {
		return c.flush(ctx)
	}
	return nil
}

func (c *copier) flush(ctx context.Context) error {
	if c.n == 0 {
		return nil
	}
	var cols []string
	for _, col := range c.t.cols {
		cols = append(cols, quote(col.name))
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO ` + quote(c.t.name) + ` (` + strings.Join(cols, ", ") + `)`)
	if c.t.identity != "" {
		b.WriteString(` OVERRIDING SYSTEM VALUE`)
	}
	b.WriteString(` VALUES `)
	p := 1
	for r := 0; r < c.n; r++ {
		if r > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for i := range c.t.cols {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", p)
			p++
		}
		b.WriteByte(')')
	}
	if _, err := c.tx.ExecContext(ctx, b.String(), c.args...); err != nil {
		return fmt.Errorf("copy %s: %w", c.t.name, err)
	}
	c.args, c.n = c.args[:0], 0
	return nil
}

// verify reads t back from the destination and compares it with the digests of the source rows: the same keys, and every
// row with the same values.
func verify(ctx context.Context, tx *sql.Tx, t *table, want map[string][32]byte) error {
	var cols []string
	for _, c := range t.cols {
		cols = append(cols, quote(c.name))
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+quote(t.name))
	if err != nil {
		return fmt.Errorf("read %s back: %w", t.name, err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		vals := make([]any, len(t.cols))
		ptrs := make([]any, len(t.cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("read %s back: %w", t.name, err)
		}
		n++
		d, ok := want[encodeKey(t, vals)]
		if !ok {
			return fmt.Errorf("verify %s: the destination has a row the SQLite file has not (%s)", t.name, describeKey(t, vals))
		}
		if d != digest(vals) {
			return fmt.Errorf("verify %s: the row %s differs from the SQLite file", t.name, describeKey(t, vals))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s back: %w", t.name, err)
	}
	if n != len(want) {
		return fmt.Errorf("verify %s: %d rows in the destination, %d in the SQLite file", t.name, n, len(want))
	}
	return nil
}
