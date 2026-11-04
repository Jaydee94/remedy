package store

import (
	"context"
	"testing"
)

// Migration 004 widens the CHECK of incidents.source and adds a table. The schema is put back to the state after 003 (the table
// dropped, the old CHECK, the row of schema_migrations removed), filled with a row in every table, and migrated again: every row
// must survive, the old sources must still be accepted, and a webhook incident must be accepted now.
func TestMigration004KeepsEveryRowAndWidensTheCheck(t *testing.T) {
	s, db := schemaTest(t)
	ctx := context.Background()
	for _, q := range []string{
		`DROP TABLE webhook_endpoints`,
		`ALTER TABLE incidents DROP CONSTRAINT incidents_source_check`,
		`ALTER TABLE incidents ADD CONSTRAINT incidents_source_check CHECK (source IN ('github', 'alertmanager', 'argocd'))`,
		`DELETE FROM schema_migrations WHERE version = '004_webhook_endpoints.sql'`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO incidents (source, key, state, conclusion, first_seen, last_seen) VALUES ('webhook', 'k', 'open', 'firing', '`+schemaAt+`', '`+schemaAt+`')`); err == nil {
		t.Fatal("the test setup is wrong: the old CHECK accepts a webhook incident")
	}
	seedGraph(t, db)
	count := func(table string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := map[string]int{}
	for _, table := range []string{"incidents", "runs", "activity", "tool_calls", "incident_notes", "run_events", "hosts"} {
		before[table] = count(table)
		if before[table] == 0 {
			t.Fatalf("seedGraph left %s empty", table)
		}
	}
	if err := s.migrateFS(ctx, migrationsFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for table, n := range before {
		if got := count(table); got != n {
			t.Errorf("%s has %d rows after the migration, want %d", table, got, n)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO incidents (source, key, state, conclusion, first_seen, last_seen) VALUES ('webhook', 'k', 'open', 'firing', '`+schemaAt+`', '`+schemaAt+`')`); err != nil {
		t.Errorf("a webhook incident after the migration: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO incidents (source, key, state, conclusion, first_seen, last_seen) VALUES ('other', 'k', 'open', 'firing', '`+schemaAt+`', '`+schemaAt+`')`); err == nil {
		t.Error("an unknown source is accepted")
	}
}
