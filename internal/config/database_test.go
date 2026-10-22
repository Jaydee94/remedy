package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

const testDatabaseURL = "postgres://remedy:s3cr3t-pw@db.example:5433/remedydb?sslmode=require"

func TestServerFromEnvDatabaseDefaults(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL.Reveal() != testDatabaseURL {
		t.Fatal("DatabaseURL is not the value of REMEDY_DATABASE_URL")
	}
	if c.DatabaseMaxConns != 10 {
		t.Fatalf("DatabaseMaxConns = %d, want 10", c.DatabaseMaxConns)
	}
}

func TestServerFromEnvRequiresTheDatabaseURL(t *testing.T) {
	_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": ""}))
	if err == nil || !strings.Contains(err.Error(), "REMEDY_DATABASE_URL") {
		t.Fatalf("err = %v, want one that names REMEDY_DATABASE_URL", err)
	}
}

func TestServerFromEnvMalformedDatabaseURLIsNotEchoed(t *testing.T) {
	for _, bad := range []string{
		"postgres://remedy:s3cr3t-pw@db.example:notaport/x",
		"postgres://remedy:s3cr3t-pw@db.example/x?sslmode=bogus",
		"host=db.example password=s3cr3t-pw port=abc",
		"postgres://remedy:s3cr3t-pw@[::1/x",
		"s3cr3t-pw",
	} {
		_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": bad}))
		if err == nil {
			t.Errorf("%q: expected an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "REMEDY_DATABASE_URL") {
			t.Errorf("%q: error %q does not name the variable", bad, err)
		}
		if strings.Contains(err.Error(), "s3cr3t-pw") || strings.Contains(err.Error(), "db.example") {
			t.Errorf("%q: error %q leaks the URL", bad, err)
		}
	}
}

func TestServerFromEnvDatabaseMaxConns(t *testing.T) {
	for in, want := range map[string]int{"2": 2, "25": 25, "100": 100} {
		c, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_MAX_CONNS": in}))
		if err != nil || c.DatabaseMaxConns != want {
			t.Errorf("%s: DatabaseMaxConns = %d, err = %v", in, c.DatabaseMaxConns, err)
		}
	}
	for _, bad := range []string{"1", "0", "-3", "101", "ten", "10.5"} {
		_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_MAX_CONNS": bad}))
		if err == nil || !strings.Contains(err.Error(), "REMEDY_DATABASE_MAX_CONNS") {
			t.Errorf("%q: err = %v, want one that names REMEDY_DATABASE_MAX_CONNS", bad, err)
		}
	}
}

func TestServerFromEnvRefusesTheOldDatabaseVariable(t *testing.T) {
	_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DB": "/data/remedy.db"}))
	if err == nil {
		t.Fatal("REMEDY_DB must be refused, not ignored")
	}
	for _, want := range []string{"REMEDY_DB is gone", "PostgreSQL (REMEDY_DATABASE_URL)", "not migrated", "docs/runbook/postgres.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "/data/remedy.db") {
		t.Errorf("error %q echoes the value", err)
	}
}

func TestDatabaseURLNeverAppearsWhenTheConfigIsPrinted(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, c)
		if strings.Contains(out, "s3cr3t-pw") {
			t.Errorf("%s of the config shows the password", verb)
		}
	}
}

func TestDatabaseTargetShowsOnlyHostPortAndName(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.DatabaseTarget(); got != "db.example:5433/remedydb" {
		t.Fatalf("DatabaseTarget = %q", got)
	}
	c, err = config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": "postgres://u:pw@pg/remedy"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.DatabaseTarget(); got != "pg:5432/remedy" {
		t.Fatalf("DatabaseTarget = %q, want the default port filled in", got)
	}
	if got := (config.Server{}).DatabaseTarget(); strings.Contains(got, "pw") {
		t.Fatalf("zero config target = %q", got)
	}
}

func TestServerFromEnvRefusesABlankDatabaseURL(t *testing.T) {
	for _, blank := range []string{" ", "\t", " \n "} {
		_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": blank}))
		if err == nil || !strings.Contains(err.Error(), "REMEDY_DATABASE_URL must be set") {
			t.Errorf("%q: err = %v, want the one for an unset variable", blank, err)
		}
	}
}

// Without a database name pgx falls back to the PG* environment and the user's name, so the target is not what was meant.
func TestServerFromEnvRefusesADatabaseURLWithoutADatabaseName(t *testing.T) {
	for _, raw := range []string{
		"postgres://remedy:s3cr3t-pw@db.example:5432",
		"postgres://remedy:s3cr3t-pw@db.example:5432/",
		"postgres://remedy:s3cr3t-pw@db.example:5432/?sslmode=require",
		"host=db.example user=remedy password=s3cr3t-pw",
	} {
		_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": raw}))
		if err == nil || !strings.Contains(err.Error(), "database name") {
			t.Errorf("%q: err = %v, want one about the database name", raw, err)
			continue
		}
		if strings.Contains(err.Error(), "s3cr3t-pw") || strings.Contains(err.Error(), "db.example") {
			t.Errorf("%q: error %q leaks the URL", raw, err)
		}
	}
}

func TestTheParseErrorNamesWhatToCheck(t *testing.T) {
	_, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": "postgres://remedy:s3cr3t-pw@db.example/x?sslmode=bogus"}))
	if err == nil || !strings.Contains(err.Error(), "check sslmode, sslrootcert and the port") {
		t.Fatalf("err = %v", err)
	}
}

func TestDatabaseTargetForms(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"IPv6", "postgres://remedy:s3cr3t-pw@[fd00::1]:5433/remedy", "[fd00::1]:5433/remedy"},
		{"IPv6 without a port", "postgres://remedy:s3cr3t-pw@[::1]/remedy", "[::1]:5432/remedy"},
		{"a unix socket", "host=/var/run/postgresql user=remedy password=s3cr3t-pw dbname=remedy", "/var/run/postgresql:5432/remedy"},
		{"a percent-encoded password", "postgres://remedy:s3cr3t-pw%40%2F%3Ax@db.example:5432/remedy", "db.example:5432/remedy"},
		{"a password in the query", "postgres://remedy@db.example/remedy?password=s3cr3t-pw&sslmode=require", "db.example:5432/remedy"},
		{"key=value", "host=db.example port=6432 user=remedy password=s3cr3t-pw dbname=remedy sslmode=verify-full", "db.example:6432/remedy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_DATABASE_URL": tc.raw}))
			if err != nil {
				t.Fatal(err)
			}
			if got := c.DatabaseTarget(); got != tc.want {
				t.Errorf("DatabaseTarget = %q, want %q", got, tc.want)
			}
			if strings.Contains(c.DatabaseTarget(), "s3cr3t") {
				t.Errorf("the target %q shows the password", c.DatabaseTarget())
			}
			if c.DatabaseURL.Reveal() != tc.raw {
				t.Error("DatabaseURL is not the value as given")
			}
		})
	}
}
