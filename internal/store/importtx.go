package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/Jaydee94/remedy/internal/secret"
)

// ImportTx runs fn in one transaction of the pool: it commits when fn returns nil and rolls back when fn returns an error or
// panics (the panic goes on).
//
// It exists for one caller only: the one-time importer from SQLite (internal/importsqlite, cmd/remedy-import-sqlite;
// docs/specs/2026-10-22-postgres-only-design.md, decision 9), the one package besides the store that issues SQL, because it
// copies every table of the schema as it is. Nothing else may use it: the server's code goes through the store's methods.
// fn gets the driver's own transaction, so its statements use PostgreSQL's $1, $2 placeholders and not the store's ?; the
// placeholder rewrite of db.go does not apply, and neither does its cleaning of text arguments (the importer has its own
// rules for text). It goes when the importer goes.
func (s *Store) ImportTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ErrImportTargetNotDedicated is in the chain of CheckImportTarget's refusal: the schema holds tables that are not Remedy's.
var ErrImportTargetNotDedicated = errors.New("the destination is not a dedicated database for Remedy")

// CheckImportTarget is for the importer only (cmd/remedy-import-sqlite, before it calls Open): it refuses a destination that
// is not a dedicated database for Remedy, before Open's migration would create Remedy's tables in it. It opens a short
// connection of its own and only reads the catalog: it creates and changes nothing.
//
// The current schema of the connection (the first schema of its search_path that exists) is accepted when it holds no table
// at all, when it holds only schema_migrations (a first start whose baseline failed), or when it holds only Remedy's own
// tables (those the store's migrations create) with schema_migrations recording the baseline migration. Anything else is refused with an error that names the count and the names of the other tables (never
// a row) and ErrImportTargetNotDedicated in its chain. Tables, views, materialized views and foreign tables count; sequences
// and indexes do not. No error contains the URL.
func CheckImportTarget(ctx context.Context, url secret.Value) error {
	raw := url.Reveal()
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		return errURL
	}
	db := sql.OpenDB(stdlib.GetConnector(*cfg))
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := ping(ctx, db, defaultOptions()); err != nil {
		return fmt.Errorf("store: cannot reach the database: %w", scrub(err, raw, cfg.Password))
	}
	if err := checkImportTarget(ctx, db); err != nil {
		if errors.Is(err, ErrImportTargetNotDedicated) {
			return err
		}
		return fmt.Errorf("store: check the destination: %w", scrub(err, raw, cfg.Password))
	}
	return nil
}

func checkImportTarget(ctx context.Context, db *sql.DB) error {
	var schema sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	if !schema.Valid {
		return errNoSchema
	}
	rows, err := db.QueryContext(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm', 'f') ORDER BY c.relname`, schema.String)
	if err != nil {
		return err
	}
	var all []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		all = append(all, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}
	own, err := remedyTables()
	if err != nil {
		return err
	}
	var other []string
	hasMigrations := false
	for _, n := range all {
		switch {
		case n == "schema_migrations":
			hasMigrations = true
		case !own[n]:
			other = append(other, n)
		}
	}
	if len(other) > 0 {
		word := "tables"
		if len(other) == 1 {
			word = "table"
		}
		return fmt.Errorf("%w: its schema %s has %d %s that are not Remedy's (%s): use a dedicated empty database",
			ErrImportTargetNotDedicated, schema.String, len(other), word, strings.Join(other, ", "))
	}
	baseline := false
	if hasMigrations {
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, baselineMigration).Scan(&baseline); err != nil {
			return err
		}
	}
	// schema_migrations alone is what a first start leaves when its baseline failed (the table is made before the migration's
	// transaction): Open migrates it like an empty schema.
	if !baseline && !(hasMigrations && len(all) == 1) {
		return fmt.Errorf("%w: its schema %s has tables with Remedy's names but no record of Remedy's migration %s: use a dedicated empty database",
			ErrImportTargetNotDedicated, schema.String, baselineMigration)
	}
	return nil
}

// baselineMigration is the first migration of the store; a schema Remedy made records it in schema_migrations.
const baselineMigration = "001_baseline.sql"

// remedyTables are the tables the store's migrations create, read from the migrations themselves: every line that starts
// with CREATE TABLE (IF NOT EXISTS) names one.
func remedyTables() (map[string]bool, error) {
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := map[string]bool{}
	for _, f := range files {
		body, err := fs.ReadFile(migrationsFS, f)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(body), "\n") {
			f := strings.Fields(strings.ToLower(line))
			if len(f) < 3 || f[0] != "create" || f[1] != "table" {
				continue
			}
			name := f[2]
			if len(f) >= 6 && f[2] == "if" && f[3] == "not" && f[4] == "exists" {
				name = f[5]
			}
			out[strings.Trim(name, `"(`)] = true
		}
	}
	return out, nil
}
