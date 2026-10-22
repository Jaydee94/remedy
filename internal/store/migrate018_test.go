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

// buildV017 builds a database the way the migrations below 018 leave it: it applies every migration file with a number below 018 (found
// by glob, so a migration 017 that lands later is part of it), records them, and fills every table that points at incidents.
func buildV017(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v017.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		if name >= "018" {
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
	seed := []string{
		`INSERT INTO hosts (id, name, address, port, account, notes, docker_pattern, systemd_units, enabled, created_at, updated_at)
		 VALUES (1, 'nas', '192.168.178.118', 22, 'remedy', 'the NAS', 'remedy-*', '[]', 1, '` + at + `', '` + at + `')`,
		`INSERT INTO incidents (id, source, key, title, severity, state, conclusion, occurrences, first_seen, last_seen, host_id, diagnosis, diagnosed_at)
		 VALUES (1, 'alertmanager', 'HighLoad/abc', 'HighLoad on nas', 'warning', 'diagnosed', 'firing', 3, '` + at + `', '` + at + `', 1, '{"summary":"s"}', '` + at + `')`,
		`INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen, resolved_at, resolved_reason)
		 VALUES (2, 'alertmanager', 'Disk/def', 'Disk full', 'resolved', 'firing', '` + at + `', '` + at + `', '` + at + `', 'cleared')`,
		`INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen) VALUES (3, 'alertmanager', 'Noise/xyz', 'Noise', 'ignored', 'firing', '` + at + `', '` + at + `')`,
		`INSERT INTO runs (id, provider, prompt, status, created_at, role, incident_id) VALUES ('run-1', 'claude', 'diagnose', 'succeeded', '` + at + `', 'responder', 1)`,
		`UPDATE incidents SET run_id = 'run-1' WHERE id = 1`,
		`INSERT INTO activity (at, kind, incident_id, summary) VALUES ('` + at + `', 'incident_opened', 1, 'opened')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (1, 'run-1', 'a note', '` + at + `')`,
		`INSERT INTO tool_calls (id, run_id, incident_id, tool_use_id, tool, kind, arguments, status, created_at) VALUES (1, 'run-1', 1, 'toolu_1', 'incident_get', 'read', '{}', 'succeeded', '` + at + `')`,
	}
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	return path
}

func TestMigration018KeepsEveryRowAndStartsTheFlagAtZero(t *testing.T) {
	path := buildV017(t)

	before, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, table := range []string{"incidents", "runs", "activity", "incident_notes", "tool_calls", "hosts"} {
		counts[table] = countRows(t, before, table)
		if counts[table] == 0 {
			t.Fatalf("the seed has no row in %s", table)
		}
	}
	_ = before.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 017 database: %v", err)
	}
	ctx := context.Background()
	for id, state := range map[int64]store.IncidentState{1: store.IncDiagnosed, 2: store.IncResolved, 3: store.IncIgnored} {
		got, err := s.GetIncident(ctx, id)
		if err != nil || got.State != state {
			t.Errorf("incident %d = %+v, %v, want state %s", id, got, err, state)
		}
	}
	if got, _ := s.GetIncident(ctx, 1); got.Occurrences != 3 || string(got.Diagnosis) != `{"summary":"s"}` || got.HostName != "nas" || got.RunID != "run-1" {
		t.Errorf("incident 1 changed: %+v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for table, want := range counts {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s has %d rows after the migration, had %d", table, got, want)
		}
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM incidents WHERE resolved_while_ignored = 0`:        3,
		`SELECT COUNT(*) FROM incident_notes WHERE incident_id = 1`:              1,
		`SELECT COUNT(*) FROM tool_calls WHERE incident_id = 1`:                  1,
		`SELECT COUNT(*) FROM activity WHERE incident_id = 1`:                    1,
		`SELECT COUNT(*) FROM runs WHERE incident_id = 1`:                        1,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`:                          0,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'incidents_active_key'`: 1,
	} {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v, want %d", query, n, err, want)
		}
	}
}
