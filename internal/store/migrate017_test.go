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

// buildV016 builds a database the way 016 left it (the migration files below 017, applied from disk) and runs the seed on it.
func buildV016(t *testing.T, seed ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v016.db")
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
		if name >= "017" {
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
	for _, q := range seed {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	return path
}

func settingsAfterMigrating(t *testing.T, path string) map[string]string {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open on a 016 database: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	rows, err := s.ListSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Key] = r.Value
	}
	return got
}

const at017 = "2026-10-04T12:00:00.000000000Z"

// The interval used to pace both sources. A maintainer who raised it to one hour because GitHub is not connected keeps that
// interval for Alertmanager until he changes it, so nothing changes under him.
func TestMigration017CopiesAnExistingPollIntervalToTheAlertmanagerInterval(t *testing.T) {
	got := settingsAfterMigrating(t, buildV016(t,
		`INSERT INTO settings (key, value, updated_at) VALUES ('poll_interval', '"1h"', '`+at017+`')`,
		`INSERT INTO settings (key, value, updated_at) VALUES ('run_timeout', '"20m"', '`+at017+`')`,
	))
	if got["alertmanager_poll_interval"] != `"1h"` || got["poll_interval"] != `"1h"` || got["run_timeout"] != `"20m"` || len(got) != 3 {
		t.Errorf("settings after 017 = %v", got)
	}
}

func TestMigration017AddsNothingWhenThePollIntervalWasNeverSet(t *testing.T) {
	got := settingsAfterMigrating(t, buildV016(t,
		`INSERT INTO settings (key, value, updated_at) VALUES ('run_timeout', '"20m"', '`+at017+`')`,
	))
	if _, ok := got["alertmanager_poll_interval"]; ok || len(got) != 1 {
		t.Errorf("settings after 017 = %v", got)
	}
}

func TestMigration017KeepsAnAlertmanagerIntervalThatIsAlreadySet(t *testing.T) {
	got := settingsAfterMigrating(t, buildV016(t,
		`INSERT INTO settings (key, value, updated_at) VALUES ('poll_interval', '"1h"', '`+at017+`')`,
		`INSERT INTO settings (key, value, updated_at) VALUES ('alertmanager_poll_interval', '"30s"', '`+at017+`')`,
	))
	if got["alertmanager_poll_interval"] != `"30s"` || got["poll_interval"] != `"1h"` || len(got) != 2 {
		t.Errorf("settings after 017 = %v", got)
	}
}
