package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// A database as 012 left it, with a host, an Alertmanager incident and everything that points at them, must open under 013:
// the host and the incident keep every field, the new columns start empty, and deleting the host leaves the incident, its
// notes, its activity and its run alone.
func TestMigration013AddsTheRulesAndTheHostOfAnIncidentAndKeepsEveryRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v012.db")
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
		if name >= "013" {
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
		 VALUES (1, 'nas', '192.168.178.118', 22, 'remedy', 'the NAS', 'remedy-*', '["node_exporter.service"]', 1, '` + at + `', '` + at + `')`,
		`INSERT INTO host_credentials (host_id, kind, ciphertext, hint, public_key, updated_at)
		 VALUES (1, 'key', x'0102', 'SHA256:abc', 'ssh-ed25519 AAAA', '` + at + `')`,
		`INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen)
		 VALUES (1, 'alertmanager', 'HighLoad/abc', 'HighLoad on nas', 'open', 'firing', '` + at + `', '` + at + `')`,
		`INSERT INTO runs (id, provider, prompt, status, created_at, role) VALUES ('run-1', 'claude', 'diagnose', 'succeeded', '` + at + `', 'responder')`,
		`UPDATE runs SET incident_id = 1 WHERE id = 'run-1'`,
		`INSERT INTO run_hosts (run_id, host_id) VALUES ('run-1', 1)`,
		`INSERT INTO activity (at, kind, incident_id, run_id, summary) VALUES ('` + at + `', 'incident_opened', 1, 'run-1', 'opened one')`,
		`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (1, 'run-1', 'first note', '` + at + `')`,
	}
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 012 database: %v", err)
	}
	ctx := context.Background()

	// 1. The host keeps every field and has no rules.
	e, err := s.GetHost(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	h := e.Host
	if h.Name != "nas" || h.Address != "192.168.178.118" || h.Port != 22 || h.Account != "remedy" || h.Notes != "the NAS" ||
		h.DockerPattern != "remedy-*" || len(h.SystemdUnits) != 1 || h.SystemdUnits[0] != "node_exporter.service" || !h.Enabled || h.Matchers != "" {
		t.Errorf("host after migrating = %+v", h)
	}
	if e.Credential == nil || e.Credential.Kind != "key" || e.Credential.Hint != "SHA256:abc" {
		t.Errorf("credential after migrating = %+v", e.Credential)
	}

	// 2. The incident keeps its fields and notes and has no host.
	in, err := s.GetIncident(ctx, 1)
	if err != nil || in.Title != "HighLoad on nas" || in.State != store.IncOpen || in.Source != store.SourceAlertmanager ||
		in.HostID != 0 || in.HostName != "" {
		t.Fatalf("incident after migrating = %+v, %v", in, err)
	}
	if notes, err := s.ListNotes(ctx, 1); err != nil || len(notes) != 1 {
		t.Errorf("notes after migrating = %d, %v", len(notes), err)
	}

	// 3. The rules can be written and read back.
	f := h.HostFields
	f.Matchers = `instance="x"`
	if _, _, err := s.UpdateHost(ctx, h.ID, f); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetHost(ctx, h.ID); err != nil || got.Host.Matchers != `instance="x"` {
		t.Errorf("host after the update = %+v, %v", got.Host, err)
	}

	// 4. An incident can be given a host.
	if err := s.TouchIncidentSignal(ctx, in.ID, h.ID, json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	in, err = s.GetIncident(ctx, in.ID)
	if err != nil || in.HostID != h.ID || in.HostName != "nas" || string(in.Details) != `{"a":1}` {
		t.Fatalf("incident after the touch = %+v, %v", in, err)
	}

	// 5. Deleting the host leaves the incident without one, and everything that pointed at it or at the incident stays.
	if err := s.DeleteHost(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	in, err = s.GetIncident(ctx, in.ID)
	if err != nil || in.HostID != 0 || in.HostName != "" || in.Title != "HighLoad on nas" {
		t.Fatalf("incident after the host was deleted = %+v, %v", in, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	check, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM incidents WHERE id = 1 AND host_id IS NULL`:                                              1,
		`SELECT COUNT(*) FROM incident_notes WHERE incident_id = 1`:                                                    1,
		`SELECT COUNT(*) FROM activity WHERE incident_id = 1`:                                                          1,
		`SELECT COUNT(*) FROM runs WHERE id = 'run-1' AND incident_id = 1`:                                             1,
		`SELECT COUNT(*) FROM run_hosts WHERE run_id = 'run-1'`:                                                        0,
		`SELECT COUNT(*) FROM host_credentials`:                                                                        0,
		`SELECT COUNT(*) FROM hosts`:                                                                                   0,
		`SELECT COUNT(*) FROM pragma_foreign_key_list('incidents') WHERE "table" = 'hosts' AND on_delete = 'SET NULL'`: 1,
	} {
		var n int
		if err := check.QueryRow(query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v, want %d", query, n, err, want)
		}
	}
}
