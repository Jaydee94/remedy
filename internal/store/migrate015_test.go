package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

// A database as 014 left it, with a host, its login, a run bound to it, an incident and what points at them, must open under 015: every
// row stays, the table of skills starts empty, a skill can be bound to the host and deleting the host takes only its skills.
func TestMigration015AddsSkillsAndKeepsEveryRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v014.db")
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
		if name >= "015" {
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
		`INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen) VALUES (1, 'alertmanager', 'HighLoad/abc', 'HighLoad on nas', 'open', 'firing', '` + at + `', '` + at + `')`,
		`INSERT INTO runs (id, provider, prompt, status, created_at, role) VALUES ('run-1', 'claude', 'diagnose', 'succeeded', '` + at + `', 'responder')`,
		`INSERT INTO run_hosts (run_id, host_id) VALUES ('run-1', 1)`,
		`INSERT INTO activity (at, kind, incident_id, run_id, summary) VALUES ('` + at + `', 'incident_opened', 1, 'run-1', 'opened one')`,
	}
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 014 database: %v", err)
	}
	ctx := context.Background()

	if list, err := s.ListSkills(ctx, store.SkillFilter{}); err != nil || len(list) != 0 {
		t.Fatalf("skills after migrating = %v, %v", list, err)
	}
	if e, err := s.GetHost(ctx, 1); err != nil || e.Host.Name != "nas" || e.Credential == nil {
		t.Fatalf("host after migrating = %+v, %v", e, err)
	}
	if _, err := s.CreateSkill(ctx, 1, skill.Fields{Name: "paperless", Description: "d", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSkill(ctx, 0, skill.Fields{Name: "lab-notes", Description: "d", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHost(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	check, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM skills`: 1,
		`SELECT COUNT(*) FROM skills WHERE host_id IS NULL AND name = 'lab-notes'`:                                   1,
		`SELECT COUNT(*) FROM runs WHERE id = 'run-1'`:                                                               1,
		`SELECT COUNT(*) FROM incidents WHERE id = 1`:                                                                1,
		`SELECT COUNT(*) FROM activity WHERE run_id = 'run-1'`:                                                       1,
		`SELECT COUNT(*) FROM pragma_foreign_key_list('skills') WHERE "table" = 'hosts' AND on_delete = 'CASCADE'`:   1,
		`SELECT COUNT(*) FROM pragma_foreign_key_list('skills') WHERE "table" = 'runs' AND on_delete = 'SET NULL'`:   1,
		`SELECT COUNT(*) FROM pragma_foreign_key_list('skills') WHERE "table" = 'skills' AND on_delete = 'SET NULL'`: 1,
	} {
		var n int
		if err := check.QueryRow(query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v, want %d", query, n, err, want)
		}
	}
}

// The unique index is per scope and leaves proposals out; the checks refuse an unknown origin or state.
func TestTheSkillsTableKeepsNamesUniquePerScopeAndLeavesProposalsOut(t *testing.T) {
	s, path := openStoreAt(t)
	host := hostNamed(t, s, "nas")
	_ = s.Close()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const at = "2026-10-04T12:00:00.000000000Z"
	insert := func(hostID any, name, state string) error {
		_, err := db.Exec(`INSERT INTO skills (host_id, name, description, body, origin, state, created_at, updated_at) VALUES (?, ?, 'd', 'b', 'written', ?, ?, ?)`,
			hostID, name, state, at, at)
		return err
	}
	if err := insert(nil, "a", "active"); err != nil {
		t.Fatal(err)
	}
	if err := insert(nil, "a", "off"); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("a second global skill with the same name: %v", err)
	}
	if err := insert(host.ID, "a", "active"); err != nil {
		t.Errorf("the same name for a host: %v", err)
	}
	if err := insert(host.ID, "a", "active"); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("a second skill with the same name for the host: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := insert(nil, "a", "proposed"); err != nil {
			t.Errorf("proposal %d with the name of an active skill: %v", i, err)
		}
	}
	if err := insert(nil, "b", "sleeping"); err == nil {
		t.Error("an unknown state was accepted")
	}
	if _, err := db.Exec(`INSERT INTO skills (name, description, body, origin, state, created_at, updated_at) VALUES ('c', 'd', 'b', 'robot', 'active', ?, ?)`, at, at); err == nil {
		t.Error("an unknown origin was accepted")
	}
}
