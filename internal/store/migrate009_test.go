package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// databaseAt008 builds a database the way version 008 left it, with incidents (with and without a stored diagnosis) and
// a row in every table that points at them: it applies the migration files below 009 from disk, records them, and fills the
// tables with raw SQL.
func databaseAt008(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v008.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)

	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files: %v", err)
	}
	sort.Strings(files)
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		if name >= "009" {
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

	const (
		at   = "2026-10-04T12:00:00.000000000Z"
		d1   = "2026-10-04T12:05:00.000000000Z" // the start of the only diagnosis of incident 1
		d2   = "2026-10-05T08:30:00.000000000Z" // the start of the LAST diagnosis of incident 2, which may have failed
		d3   = "2026-10-05T09:00:00.000000000Z" // a resolved incident
		fail = "2026-10-05T10:00:00.000000000Z" // incident 4 had a diagnosis started and never got one
	)
	seed := []string{
		`INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, checked_at) VALUES (1, x'00', '…abcd', 'octo', 'ok', '` + at + `')`,
		`INSERT INTO repos (id, connection_id, full_name, default_branch, created_at) VALUES (1, 1, 'octo/hello', 'main', '` + at + `')`,
		`INSERT INTO runs (id, provider, prompt, status, created_at, role) VALUES ('run-1', 'claude', 'diagnose', 'succeeded', '` + at + `', 'responder')`,
		// Incident 1: diagnosed once; the start of that run is the best value for diagnosed_at.
		`INSERT INTO incidents (id, repo_id, ref, check_name, key, state, conclusion, head_sha, first_seen, last_seen, diagnoses, last_diagnosis_at, diagnosis, run_id, diagnosed_sha)
		 VALUES (1, 1, 'pr:7', 'go', '1' || char(31) || 'pr:7' || char(31) || 'go', 'diagnosed', 'failure', 'aaa', '` + at + `', '` + at + `', 1, '` + d1 + `', '{"summary":"s"}', 'run-1', 'aaa')`,
		// Incident 2: a diagnosis and a newer last_diagnosis_at, as left by a diagnosis that was run again. The old data does not
		// say when the stored diagnosis was written; last_diagnosis_at is the best it has.
		`INSERT INTO incidents (id, repo_id, ref, check_name, key, state, conclusion, head_sha, first_seen, last_seen, diagnoses, last_diagnosis_at, diagnosis, diagnosed_sha)
		 VALUES (2, 1, 'pr:8', 'go', '1' || char(31) || 'pr:8' || char(31) || 'go', 'diagnosed', 'failure', 'bbb', '` + at + `', '` + at + `', 2, '` + d2 + `', '{"summary":"old"}', 'bbb')`,
		// Incident 3: resolved, with a diagnosis.
		`INSERT INTO incidents (id, repo_id, ref, check_name, key, state, conclusion, head_sha, first_seen, last_seen, resolved_at, resolved_reason, diagnoses, last_diagnosis_at, diagnosis, diagnosed_sha)
		 VALUES (3, 1, 'pr:9', 'go', '1' || char(31) || 'pr:9' || char(31) || 'go', 'resolved', 'failure', 'ccc', '` + at + `', '` + at + `', '` + at + `', 'green', 1, '` + d3 + `', '{"summary":"r"}', 'ccc')`,
		// Incident 4: no diagnosis, but a diagnosis was started once (and failed): stays NULL.
		`INSERT INTO incidents (id, repo_id, ref, check_name, key, state, conclusion, head_sha, first_seen, last_seen, diagnoses, last_diagnosis_at)
		 VALUES (4, 1, 'pr:10', 'go', '1' || char(31) || 'pr:10' || char(31) || 'go', 'open', 'failure', 'ddd', '` + at + `', '` + at + `', 1, '` + fail + `')`,
		// Incident 5: never diagnosed.
		`INSERT INTO incidents (id, repo_id, ref, check_name, key, state, conclusion, head_sha, first_seen, last_seen)
		 VALUES (5, 1, 'pr:11', 'go', '1' || char(31) || 'pr:11' || char(31) || 'go', 'open', 'failure', 'eee', '` + at + `', '` + at + `')`,
		// Incident 6: an alert, which has no repository.
		`INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen)
		 VALUES (6, 'alertmanager', 'KubePodCrashLooping/abc', 'KubePodCrashLooping', 'open', 'firing', '` + at + `', '` + at + `')`,
		`INSERT INTO activity (at, kind, repo_id, incident_id, summary) VALUES ('` + at + `', 'incident_opened', 1, 1, 'opened one')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (1, 'run-1', 'first note', '` + at + `')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (2, NULL, 'second note', '` + at + `')`,
		`INSERT INTO tool_calls (run_id, incident_id, tool_use_id, tool, kind, status, created_at) VALUES ('run-1', 1, 'toolu_1', 'incident_get', 'read', 'succeeded', '` + at + `')`,
		`UPDATE runs SET incident_id = 1 WHERE id = 'run-1'`,
	}
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	return path
}

func TestMigration009BackfillsDiagnosedAtAndKeepsEverything(t *testing.T) {
	path := databaseAt008(t)
	ctx := context.Background()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 008 database: %v", err)
	}
	defer s.Close()

	list, err := s.ListIncidents(ctx, store.IncidentFilter{State: "all"})
	if err != nil || len(list) != 6 {
		t.Fatalf("ListIncidents = %d incidents, %v, want 6", len(list), err)
	}
	byID := map[int64]store.Incident{}
	for _, in := range list {
		byID[in.ID] = in
	}

	// diagnosed_at is last_diagnosis_at where there is a diagnosis (also on the resolved incident), NULL elsewhere: a
	// diagnosis that was started and never stored does not count.
	for id, want := range map[int64]string{1: "2026-10-04T12:05:00Z", 2: "2026-10-05T08:30:00Z", 3: "2026-10-05T09:00:00Z"} {
		in := byID[id]
		w, _ := time.Parse(time.RFC3339, want)
		if in.DiagnosedAt == nil || !in.DiagnosedAt.Equal(w) || in.LastDiagnosisAt == nil || !in.DiagnosedAt.Equal(*in.LastDiagnosisAt) {
			t.Errorf("incident %d: DiagnosedAt = %v, LastDiagnosisAt = %v, want both %s", id, in.DiagnosedAt, in.LastDiagnosisAt, want)
		}
	}
	for _, id := range []int64{4, 5, 6} {
		if in := byID[id]; in.DiagnosedAt != nil {
			t.Errorf("incident %d has no diagnosis but DiagnosedAt = %v", id, in.DiagnosedAt)
		}
	}
	if byID[4].LastDiagnosisAt == nil {
		t.Errorf("incident 4 lost its last_diagnosis_at")
	}

	// Nothing that pointed at an incident was lost.
	if notes, err := s.ListNotes(ctx, 1); err != nil || len(notes) != 1 {
		t.Errorf("notes of incident 1 = %d, %v, want 1", len(notes), err)
	}
	if notes, err := s.ListNotes(ctx, 2); err != nil || len(notes) != 1 {
		t.Errorf("notes of incident 2 = %d, %v, want 1", len(notes), err)
	}
	if log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: 1}); err != nil || len(log) != 1 || log[0].Summary != "opened one" {
		t.Errorf("activity of incident 1 = %+v, %v", log, err)
	}
	if calls, err := s.ListToolCalls(ctx, "run-1"); err != nil || len(calls) != 1 || calls[0].IncidentID != 1 {
		t.Errorf("tool calls of run-1 = %+v, %v", calls, err)
	}
	if r, err := s.GetRun(ctx, "run-1"); err != nil || r.IncidentID == nil || *r.IncidentID != 1 {
		t.Errorf("run-1 = %+v, %v, want incident 1", r, err)
	}
	if byID[1].RunID != "run-1" {
		t.Errorf("incident 1 lost its run: %+v", byID[1])
	}
}

func TestMigration009MigratesADatabaseWithNoIncidents(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if list, err := s.ListIncidents(context.Background(), store.IncidentFilter{State: "all"}); err != nil || len(list) != 0 {
		t.Errorf("ListIncidents = %+v, %v, want none", list, err)
	}
}
