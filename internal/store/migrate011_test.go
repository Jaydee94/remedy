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

// A database as 010 left it, with a run in it, must open under 011: the run keeps every column and gets host_count 0, and
// run_hosts works.
func TestMigration011AddsRunHostsAndKeepsTheRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v010.db")
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
		if name >= "011" {
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
	if _, err := raw.Exec(`INSERT INTO runs (id, provider, prompt, status, mcp, cluster, created_at)
		VALUES ('run-1', 'claude', 'hello', 'succeeded', 1, 1, '2026-10-04T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 010 database: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	got, err := s.GetRun(ctx, "run-1")
	if err != nil || got.Prompt != "hello" || !got.MCP || !got.Cluster || got.HostCount != 0 {
		t.Fatalf("run after migrating = %+v, %v", got, err)
	}
	h, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", HostIDs: []int64{h.ID}})
	if err != nil || created.HostCount != 1 {
		t.Fatalf("CreateRunWith after migrating = %+v, %v", created, err)
	}
	if hosts, err := s.RunHosts(ctx, created.ID); err != nil || len(hosts) != 1 || hosts[0].Host.Name != "nas" {
		t.Fatalf("RunHosts = %+v, %v", hosts, err)
	}
}
