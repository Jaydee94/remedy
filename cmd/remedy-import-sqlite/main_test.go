package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunRefusesBadArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no from", nil, map[string]string{envURL: "postgres://u:p@h/db"}, "--from is required"},
		{"no url", []string{"--from", "x.db"}, nil, envURL + " is not set"},
		{"positional url", []string{"--from", "x.db", "postgres://u:hunter2@h/db"}, nil, "unexpected arguments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(context.Background(), c.args, env(c.env), &out, &errb); code != 2 {
				t.Fatalf("exit %d, want 2 (stderr %q)", code, errb.String())
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Errorf("stderr %q does not contain %q", errb.String(), c.want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout %q, want nothing", out.String())
			}
			if strings.Contains(errb.String(), "hunter2") {
				t.Errorf("stderr shows the password: %q", errb.String())
			}
		})
	}
}

// emptySource makes a SQLite file at schema 018 with no rows, from the importer's testdata.
func emptySource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remedy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../../internal/importsqlite/testdata/sqlite-migrations/*.sql")
	if err != nil || len(files) != 18 {
		t.Fatalf("migrations: %v %v", files, err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, filepath.Base(f)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('run_timeout', '"10m"', '2026-10-04T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunImportsAndReports(t *testing.T) {
	src := emptySource(t)
	e := env(map[string]string{envURL: pgtest.New(t)})

	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--from", src, "--dry-run"}, e, &out, &errb); code != 0 {
		t.Fatalf("dry run: exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Dry run") || !regexp.MustCompile(`(?m)^settings\s+1$`).MatchString(out.String()) {
		t.Errorf("dry run report:\n%s", out.String())
	}

	out.Reset()
	if code := run(context.Background(), []string{"--from", src}, e, &out, &errb); code != 0 {
		t.Fatalf("import: exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Imported.") || !regexp.MustCompile(`(?m)^total\s+1$`).MatchString(out.String()) {
		t.Errorf("report:\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run(context.Background(), []string{"--from", src}, e, &out, &errb); code != 1 {
		t.Fatalf("second import: exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not empty") || !strings.Contains(errb.String(), "settings") {
		t.Errorf("second import stderr %q", errb.String())
	}
}

func TestRunNeverPrintsTheURL(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x.db")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A URL that cannot be parsed and one whose server does not answer: neither error may show the password.
	for _, url := range []string{"postgres://user:s3cretPassw0rd@[::1/db", "postgres://user:s3cretPassw0rd@127.0.0.1:1/db?connect_timeout=1"} {
		var out, errb bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the reachable-later wait ends at once
		code := run(ctx, []string{"--from", src}, env(map[string]string{envURL: url}), &out, &errb)
		if code != 1 {
			t.Fatalf("exit %d, want 1 (stderr %q)", code, errb.String())
		}
		if strings.Contains(errb.String()+out.String(), "s3cretPassw0rd") {
			t.Errorf("the output shows the password: %q", errb.String())
		}
	}
}

// dirtySource is emptySource with two more settings whose values hold a NUL byte and an invalid UTF-8 byte.
func dirtySource(t *testing.T) string {
	t.Helper()
	path := emptySource(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES
		('diagnose_cooldown', 'a' || char(0) || CAST(x'ff' AS TEXT), '2026-10-04T12:00:00.000000000Z'),
		('poll_interval', 'b' || char(0), '2026-10-04T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunRefusesInvalidTextWithoutTheFlag(t *testing.T) {
	src := dirtySource(t)
	e := env(map[string]string{envURL: pgtest.New(t)})
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--from", src}, e, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "nothing was imported") || !strings.Contains(errb.String(), "--replace-invalid-text") {
		t.Errorf("stderr %q", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout %q, want nothing", out.String())
	}
}

func TestRunReplacesInvalidTextWithTheFlagAndSaysSo(t *testing.T) {
	src := dirtySource(t)
	e := env(map[string]string{envURL: pgtest.New(t)})
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--from", src, "--dry-run", "--replace-invalid-text"}, e, &out, &errb); code != 0 {
		t.Fatalf("dry run: exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "2 values changed: replacement character") ||
		!regexp.MustCompile(`(?m)^settings\s+value\s+2$`).MatchString(out.String()) || !strings.Contains(out.String(), "Dry run") {
		t.Errorf("dry run report:\n%s", out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"--from", src, "--replace-invalid-text"}, e, &out, &errb); code != 0 {
		t.Fatalf("import: exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Imported.") || !strings.Contains(out.String(), "2 values changed: replacement character") {
		t.Errorf("report:\n%s", out.String())
	}
}

func TestRunWithTheFlagAndCleanTextSaysNothingChanged(t *testing.T) {
	src := emptySource(t)
	e := env(map[string]string{envURL: pgtest.New(t)})
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--from", src, "--replace-invalid-text"}, e, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "0 values changed") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A destination that holds other tables is refused before the migration, so Remedy's tables are never created in it.
func TestRunRefusesADatabaseWithOtherTablesAndCreatesNothing(t *testing.T) {
	dsn := pgtest.New(t)
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE customers (id int PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--from", emptySource(t)}, env(map[string]string{envURL: dsn}), &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "use a dedicated empty database") || !strings.Contains(errb.String(), "1 table") {
		t.Errorf("stderr %q", errb.String())
	}
	var n int
	if err := raw.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace = current_schema()::regnamespace AND relkind = 'r'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the schema has %d tables after the refusal, want 1 (customers)", n)
	}
}

func TestRunRefusesABadValueOfTheFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--from", "x.db", "--replace-invalid-text=maybe"}, env(map[string]string{envURL: "postgres://u:p@h/db"}), &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %q)", code, errb.String())
	}
}

func TestUsageNamesTheFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, env(nil), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "--replace-invalid-text") || !strings.Contains(errb.String(), "U+FFFD") {
		t.Errorf("usage:\n%s", errb.String())
	}
}
