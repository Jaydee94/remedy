package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// tablesAtRuns are the tables with a foreign key to runs, and what the key does when a run goes away. The list is derived from the
// schema of migrations 001 to 015 (grep REFERENCES runs); TestMigration016KnowsEveryTableThatPointsAtRuns fails when a table is missing.
var tablesAtRuns = map[string]string{
	"run_events":     "CASCADE",
	"run_tokens":     "CASCADE",
	"tool_calls":     "CASCADE",
	"run_hosts":      "CASCADE",
	"incidents":      "SET NULL",
	"incident_notes": "SET NULL",
	"activity":       "SET NULL",
	"skills":         "SET NULL",
}

// buildV015 builds a database the way 015 left it: it applies the migration files below 016 from disk, records them, and fills every
// table that points at runs (and the run itself, in every column) with raw SQL.
func buildV015(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v015.db")
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
		if name >= "016" {
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
		`INSERT INTO host_credentials (host_id, kind, ciphertext, hint, public_key, updated_at) VALUES (1, 'key', x'0102', 'SHA256:abc', 'ssh-ed25519 AAAA', '` + at + `')`,
		`INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen) VALUES (1, 'alertmanager', 'HighLoad/abc', 'HighLoad on nas', 'diagnosed', 'firing', '` + at + `', '` + at + `')`,
		// run-1 has a value in every column that is not the default.
		`INSERT INTO runs (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at, role, incident_id, output,
		   failure_reason, head_sha, automatic, mcp, cancel_requested, last_heartbeat_at, cluster, host_count)
		 VALUES ('run-1', 'claude', 'diagnose it', 'failed', 3, 'it broke', 'sess-1', 0.25, '` + at + `', '` + at + `', '` + at + `', 'responder', 1, '{"summary":"s"}',
		   'timeout', 'abc1234', 1, 1, 1, '` + at + `', 1, 1)`,
		`INSERT INTO runs (id, provider, prompt, status, created_at, role, mcp, host_count) VALUES ('run-2', 'claude', 'look at the nas', 'running', '` + at + `', 'adhoc', 1, 1)`,
		`UPDATE incidents SET run_id = 'run-1' WHERE id = 1`,
		`INSERT INTO run_events (run_id, seq, kind, payload, created_at) VALUES ('run-1', 1, 'system', '{"a":1}', '` + at + `')`,
		`INSERT INTO run_events (run_id, seq, kind, payload, created_at) VALUES ('run-1', 2, 'result', '{"b":2}', '` + at + `')`,
		`INSERT INTO run_events (run_id, seq, kind, payload, created_at) VALUES ('run-2', 1, 'system', '{}', '` + at + `')`,
		`INSERT INTO run_tokens (run_id, token_hash, created_at, revoked_at) VALUES ('run-1', x'aa01', '` + at + `', '` + at + `')`,
		`INSERT INTO run_tokens (run_id, token_hash, created_at) VALUES ('run-2', x'aa02', '` + at + `')`,
		`INSERT INTO tool_calls (id, run_id, incident_id, tool_use_id, tool, kind, arguments, status, created_at) VALUES (1, 'run-1', 1, 'toolu_1', 'incident_get', 'read', '{"id":1}', 'succeeded', '` + at + `')`,
		`INSERT INTO tool_calls (id, run_id, tool_use_id, tool, kind, arguments, status, decision, created_at, expires_at)
		 VALUES (2, 'run-2', 'toolu_2', 'host_change', 'mutating', '{"host":"nas"}', 'waiting', 'pending', '` + at + `', '` + at + `')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (1, 'run-1', 'a note', '` + at + `')`,
		`INSERT INTO activity (at, kind, incident_id, run_id, summary) VALUES ('` + at + `', 'diagnosis_started', 1, 'run-1', 'started')`,
		`INSERT INTO run_hosts (run_id, host_id) VALUES ('run-1', 1)`,
		`INSERT INTO run_hosts (run_id, host_id) VALUES ('run-2', 1)`,
		`INSERT INTO skills (host_id, name, description, body, origin, state, run_id, created_at, updated_at) VALUES (1, 'paperless', 'd', 'b', 'proposed', 'proposed', 'run-1', '` + at + `', '` + at + `')`,
		`INSERT INTO skills (host_id, name, description, body, origin, state, run_id, created_at, updated_at) VALUES (1, 'gone-with-run-2', 'd', 'b', 'proposed', 'proposed', 'run-2', '` + at + `', '` + at + `')`,
	}
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	return path
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestMigration016KeepsEveryRunAndEverythingThatPointsAtIt(t *testing.T) {
	path := buildV015(t)

	before, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{"runs": countRows(t, before, "runs")}
	for table := range tablesAtRuns {
		counts[table] = countRows(t, before, table)
		if counts[table] == 0 {
			t.Fatalf("the seed has no row in %s: the test would prove nothing for it", table)
		}
	}
	_ = before.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 015 database: %v", err)
	}
	ctx := context.Background()

	// The run keeps every column.
	r1, err := s.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Provider != "claude" || r1.Prompt != "diagnose it" || r1.Status != run.Failed || r1.ExitCode == nil || *r1.ExitCode != 3 || r1.Result != "it broke" ||
		r1.SessionID != "sess-1" || r1.CostUSD != 0.25 || r1.StartedAt == nil || r1.FinishedAt == nil || r1.Role != run.RoleResponder || r1.IncidentID == nil ||
		*r1.IncidentID != 1 || string(r1.Output) != `{"summary":"s"}` || r1.FailureReason != "timeout" || r1.HeadSHA != "abc1234" || !r1.Automatic ||
		!r1.MCP || !r1.CancelRequested || !r1.Cluster || r1.HostCount != 1 {
		t.Errorf("run-1 changed: %+v", r1)
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

	// Every table that points at runs has every row it had, and the links still point.
	for table, want := range counts {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s has %d rows after the migration, had %d", table, got, want)
		}
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM incidents WHERE id = 1 AND run_id = 'run-1'`:                  1,
		`SELECT COUNT(*) FROM incident_notes WHERE run_id = 'run-1'`:                        1,
		`SELECT COUNT(*) FROM activity WHERE run_id = 'run-1'`:                              1,
		`SELECT COUNT(*) FROM skills WHERE run_id = 'run-1'`:                                1,
		`SELECT COUNT(*) FROM run_events WHERE run_id = 'run-1'`:                            2,
		`SELECT COUNT(*) FROM run_tokens WHERE run_id = 'run-1' AND revoked_at IS NOT NULL`: 1,
		`SELECT COUNT(*) FROM tool_calls WHERE run_id = 'run-2' AND expires_at IS NOT NULL`: 1,
		`SELECT COUNT(*) FROM run_hosts WHERE host_id = 1`:                                  2,
		`SELECT COUNT(*) FROM runs WHERE id = 'run-2' AND last_heartbeat_at IS NULL`:        1,
		`SELECT COUNT(*) FROM runs WHERE id = 'run-1' AND last_heartbeat_at IS NOT NULL`:    1,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`:                                     0,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'runs_new'`:     0,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'runs'
		   AND name IN ('runs_status_created', 'runs_incident', 'runs_responder_created')`: 3,
	} {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v, want %d", query, n, err, want)
		}
	}

	// The foreign keys of the children still name runs, with the action they had.
	for table, action := range tablesAtRuns {
		var n int
		q := `SELECT COUNT(*) FROM pragma_foreign_key_list('` + table + `') WHERE "table" = 'runs' AND on_delete = '` + action + `'`
		if err := db.QueryRow(q).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s: %s = %d, %v, want 1", table, q, n, err)
		}
	}

	// The new role is accepted, an unknown one is not, and the foreign keys work: deleting run-2 takes its events, its token, its call and
	// its host binding with it and only clears the link of the skill it proposed.
	const at = "2026-10-04T12:00:00.000000000Z"
	if _, err := db.Exec(`INSERT INTO runs (id, provider, prompt, status, created_at, role, mcp, host_count) VALUES ('run-3', 'claude', 'learn', 'queued', ?, 'learner', 1, 1)`, at); err != nil {
		t.Errorf("a learner run is refused: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO runs (id, provider, prompt, status, created_at, role) VALUES ('run-4', 'claude', 'x', 'queued', ?, 'robot')`, at); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("an unknown role was accepted: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM runs WHERE id = 'run-2'`); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM run_events WHERE run_id = 'run-2'`:                        0,
		`SELECT COUNT(*) FROM run_tokens WHERE run_id = 'run-2'`:                        0,
		`SELECT COUNT(*) FROM tool_calls WHERE run_id = 'run-2'`:                        0,
		`SELECT COUNT(*) FROM run_hosts WHERE run_id = 'run-2'`:                         0,
		`SELECT COUNT(*) FROM skills WHERE name = 'gone-with-run-2' AND run_id IS NULL`: 1,
		`SELECT COUNT(*) FROM runs WHERE id = 'run-1'`:                                  1,
	} {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v, want %d", query, n, err, want)
		}
	}
}

// A table that points at runs and is not in tablesAtRuns is a table the test above does not look after: a later migration that adds one
// must add it to the list (and to the seed).
func TestMigration016KnowsEveryTableThatPointsAtRuns(t *testing.T) {
	s, path := openStoreAt(t)
	_ = s.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT m.name FROM sqlite_master m, pragma_foreign_key_list(m.name) f WHERE m.type = 'table' AND f."table" = 'runs'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		found[name] = true
		if _, ok := tablesAtRuns[name]; !ok {
			t.Errorf("the table %s points at runs and is not in tablesAtRuns", name)
		}
	}
	for name := range tablesAtRuns {
		if !found[name] {
			t.Errorf("tablesAtRuns lists %s, which does not point at runs", name)
		}
	}
}
