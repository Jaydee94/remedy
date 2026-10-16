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

// A database as 013 left it, with a run and a tool call, must open under 014: the call keeps every field and has no deadline.
func TestMigration014AddsTheColumnAndKeepsEveryCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v013.db")
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
		if name >= "014" {
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
		`INSERT INTO runs (id, provider, prompt, status, created_at, role, mcp) VALUES ('run-1', 'claude', 'q', 'running', '` + at + `', 'adhoc', 1)`,
		`INSERT INTO tool_calls (id, run_id, tool_use_id, tool, kind, arguments, status, decision, created_at)
		 VALUES (1, 'run-1', 'u1', 'cluster_rollout_restart', 'mutating', '{"a":1}', 'waiting', 'pending', '` + at + `')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 013 database: %v", err)
	}
	defer s.Close()
	c, err := s.GetToolCall(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.RunID != "run-1" || c.ToolUseID != "u1" || c.Tool != "cluster_rollout_restart" || c.Kind != store.CallKindMutating ||
		string(c.Arguments) != `{"a":1}` || c.Status != store.CallWaiting || c.Decision != store.DecisionPending || c.ExpiresAt != nil {
		t.Errorf("call after migrating = %+v", c)
	}
	// A call that has no deadline never expires.
	if ok, err := s.ExpireCall(context.Background(), 1, c.CreatedAt.AddDate(1, 0, 0)); ok || err != nil {
		t.Errorf("ExpireCall of a call from before the migration = %v, %v", ok, err)
	}
}
