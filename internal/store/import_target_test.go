package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

// rawOpen is a connection to the DSN's schema past the store.
func rawOpen(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func rawExec(t *testing.T, dsn, q string) {
	t.Helper()
	db := rawOpen(t, dsn)
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func relations(t *testing.T, dsn string) []string {
	t.Helper()
	db := rawOpen(t, dsn)
	rows, err := db.QueryContext(context.Background(),
		`SELECT relname FROM pg_class WHERE relnamespace = current_schema()::regnamespace AND relkind IN ('r','p','v','m','f') ORDER BY relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestCheckImportTargetAcceptsAnEmptySchema(t *testing.T) {
	dsn := pgtest.New(t)
	if err := CheckImportTarget(context.Background(), secret.NewValue(dsn)); err != nil {
		t.Fatalf("empty schema: %v", err)
	}
	if got := relations(t, dsn); len(got) != 0 {
		t.Fatalf("the check created %v", got)
	}
}

func TestCheckImportTargetAcceptsASchemaRemedyMigrated(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := CheckImportTarget(context.Background(), secret.NewValue(dsn)); err != nil {
		t.Fatalf("a schema Remedy migrated: %v", err)
	}
}

func TestCheckImportTargetRefusesASchemaWithAnotherTable(t *testing.T) {
	dsn := pgtest.New(t)
	rawExec(t, dsn, `CREATE TABLE customers (id int PRIMARY KEY)`)
	err := CheckImportTarget(context.Background(), secret.NewValue(dsn))
	if !errors.Is(err, ErrImportTargetNotDedicated) {
		t.Fatalf("err = %v, want ErrImportTargetNotDedicated", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "1 table") || !strings.Contains(msg, "customers") ||
		!strings.Contains(msg, "use a dedicated empty database") {
		t.Errorf("message = %q", msg)
	}
	if got := relations(t, dsn); strings.Join(got, ",") != "customers" {
		t.Fatalf("the check changed the schema: %v", got)
	}
}

func TestCheckImportTargetRefusesRemedysSchemaWithAnExtraTable(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	rawExec(t, dsn, `CREATE TABLE invoices (id int)`)
	rawExec(t, dsn, `CREATE VIEW invoice_view AS SELECT id FROM invoices`)
	err = CheckImportTarget(context.Background(), secret.NewValue(dsn))
	if !errors.Is(err, ErrImportTargetNotDedicated) || !strings.Contains(err.Error(), "2 tables") {
		t.Fatalf("err = %v", err)
	}
}

// Tables with Remedy's names but no record of its baseline migration were not made by Remedy.
func TestCheckImportTargetRefusesRemedysNamesWithoutTheMigrationRecord(t *testing.T) {
	dsn := pgtest.New(t)
	rawExec(t, dsn, `CREATE TABLE settings (key text)`)
	if err := CheckImportTarget(context.Background(), secret.NewValue(dsn)); !errors.Is(err, ErrImportTargetNotDedicated) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckImportTargetAcceptsAnEmptyMigrationTableAlone(t *testing.T) {
	dsn := pgtest.New(t)
	rawExec(t, dsn, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`)
	if err := CheckImportTarget(context.Background(), secret.NewValue(dsn)); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// The list of Remedy's tables comes from the migrations; it must be the set a migrated schema holds.
func TestRemedyTablesAreTheMigratedOnes(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	own, err := remedyTables()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range own {
		names = append(names, n)
	}
	sort.Strings(names)
	got := relations(t, dsn)
	want := append(names, "schema_migrations")
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("migrated schema %v\nremedyTables + schema_migrations %v", got, want)
	}
}

func TestCheckImportTargetNeverShowsTheURL(t *testing.T) {
	err := CheckImportTarget(context.Background(), secret.NewValue("postgres://user:s3cretPassw0rd@[::1/db"))
	if err == nil || strings.Contains(err.Error(), "s3cretPassw0rd") {
		t.Fatalf("err = %v", err)
	}
}
