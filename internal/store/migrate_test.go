package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// A database created before the GitHub tables existed must migrate on the next start without losing
// data, and reopening a migrated database must not apply a migration twice.
func TestOpenMigratesAnExistingDatabaseAndKeepsItsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	ctx := context.Background()

	// Build a database that only knows migration 001, with one run in it.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	init001 := `
		CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version) VALUES ('001_init.sql');
		CREATE TABLE runs (
		  id TEXT PRIMARY KEY, provider TEXT NOT NULL, prompt TEXT NOT NULL,
		  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
		  exit_code INTEGER, result TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		  cost_usd REAL NOT NULL DEFAULT 0, created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT);
		CREATE TABLE run_events (
		  run_id TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE, seq INTEGER NOT NULL,
		  kind TEXT NOT NULL, payload TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY (run_id, seq));
		INSERT INTO runs (id, provider, prompt, status, created_at)
		  VALUES ('old-run', 'claude', 'from before', 'queued', '2026-10-02T10:00:00.000000000Z');`
	if _, err := raw.Exec(init001); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 001-only database: %v", err)
	}
	if got, err := s.GetRun(ctx, "old-run"); err != nil || got.Prompt != "from before" {
		t.Fatalf("existing run = %+v, %v", got, err)
	}
	if err := s.SaveConnection(ctx, connection("octo")); err != nil {
		t.Fatalf("the GitHub tables were not created: %v", err)
	}
	_ = s.Close()

	again, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopening a migrated database: %v", err)
	}
	defer again.Close()
	if got, err := again.GetConnection(ctx); err != nil || got.Login != "octo" {
		t.Fatalf("connection after reopening = %+v, %v", got, err)
	}
}
