package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// A database as 009 left it, with a run and an incident, must open under 010 with the two host tables added and every old row unchanged.
func TestMigration010AddsTheHostTablesAndKeepsTheData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v009.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("migrations/*.sql")
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		if name >= "010" {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	const at = "2026-10-04T12:00:00.000000000Z"
	for _, q := range []string{
		`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES ('run-1', 'claude', 'hello', 'succeeded', '` + at + `')`,
		`INSERT INTO incidents (id, source, key, title, ref, check_name, state, conclusion, head_sha, first_seen, last_seen)
		 VALUES (1, 'alertmanager', 'HighLoad/abc', 'High load', '', '', 'open', 'firing', '', '` + at + `', '` + at + `')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 009 database: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if r, err := s.GetRun(ctx, "run-1"); err != nil || r.Prompt != "hello" {
		t.Fatalf("run after migrating = %+v, %v", r, err)
	}
	if _, err := s.CreateHost(ctx, hostFields("nas")); err != nil {
		t.Fatalf("the host tables were not created: %v", err)
	}
}
