package pgtest

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// currentSchema asks a connection made with dsn for its first schema in search_path.
func currentSchema(t *testing.T, dsn string) string {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var s string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&s); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	return s
}

func TestNewGivesEachCallItsOwnSchema(t *testing.T) {
	a, b := New(t), New(t)
	if a == b {
		t.Fatalf("two calls returned the same DSN")
	}
	sa, sb := currentSchema(t, a), currentSchema(t, b)
	if sa == sb {
		t.Fatalf("both DSNs use the schema %q", sa)
	}
	re := regexp.MustCompile(`^t_[0-9a-f]{16}$`)
	for _, s := range []string{sa, sb} {
		if !re.MatchString(s) {
			t.Fatalf("schema name %q does not look like t_<hex>", s)
		}
	}
}

func TestSchemasCannotSeeEachOthersTables(t *testing.T) {
	a, b := New(t), New(t)
	da, err := sql.Open("pgx", a)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := sql.Open("pgx", b)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := da.Exec(`CREATE TABLE only_in_a (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create in a: %v", err)
	}
	if _, err := da.Exec(`INSERT INTO only_in_a (id) VALUES (1)`); err != nil {
		t.Fatalf("insert in a: %v", err)
	}
	// The same unqualified name is free in b ...
	if _, err := db.Exec(`CREATE TABLE only_in_a (id INTEGER PRIMARY KEY, extra TEXT)`); err != nil {
		t.Fatalf("create the same name in b: %v", err)
	}
	// ... and is a different table there.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM only_in_a`).Scan(&n); err != nil {
		t.Fatalf("count in b: %v", err)
	}
	if n != 0 {
		t.Fatalf("b sees %d rows of a's table", n)
	}
	// A name that exists only in a does not resolve in b.
	if _, err := da.Exec(`CREATE TABLE a_only (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	var reg sql.NullString
	if err := db.QueryRow(`SELECT to_regclass('a_only')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.Valid {
		t.Fatalf("b resolves a_only to %q", reg.String)
	}
	if _, err := db.Exec(`SELECT * FROM a_only`); err == nil {
		t.Fatalf("b could read a table of a")
	}
}

func TestCleanupDropsTheSchemaAndItsTables(t *testing.T) {
	var schema string
	t.Run("inner", func(t *testing.T) {
		dsn := New(t)
		schema = currentSchema(t, dsn)
		d, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if _, err := d.Exec(`CREATE TABLE leftover (id INTEGER)`); err != nil {
			t.Fatal(err)
		}
	})
	if schema == "" {
		t.Fatal("the subtest did not run")
	}
	admin := Admin(t)
	var n int
	if err := admin.QueryRow(`SELECT count(*) FROM pg_namespace WHERE nspname = $1`, schema).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("schema %s is still there after the test ended", schema)
	}
}

func TestAdminIsNotScopedToATestSchema(t *testing.T) {
	scoped := New(t)
	admin := Admin(t)
	var adminSchema string
	if err := admin.QueryRow(`SELECT current_schema()`).Scan(&adminSchema); err != nil {
		t.Fatal(err)
	}
	if adminSchema == currentSchema(t, scoped) {
		t.Fatalf("Admin uses the test schema %q", adminSchema)
	}
	if strings.HasPrefix(adminSchema, "t_") {
		t.Fatalf("Admin is in a test schema: %q", adminSchema)
	}
}

func TestScopeKeepsTheRestOfTheURLAndWorksForKeywordForm(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://u:p@h:5432/d?sslmode=disable", "postgres://u:p@h:5432/d?search_path=t_x&sslmode=disable"},
		{"postgresql://u@h/d", "postgresql://u@h/d?search_path=t_x"},
		{"host=h user=u dbname=d", "host=h user=u dbname=d search_path=t_x"},
	}
	for _, c := range cases {
		got, err := scope(c.in, "t_x")
		if err != nil {
			t.Fatalf("scope(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("scope(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := scope("://nonsense", "t_x"); err == nil {
		t.Errorf("a broken URL was accepted")
	}
}

// stubTB records a Fatal and stops the test function the way t.Fatal does, without failing the real test.
type stubTB struct {
	testing.TB
	msg string
}

type fatalStop struct{}

func (s *stubTB) Helper()                   {}
func (s *stubTB) Cleanup(func())            {}
func (s *stubTB) Fatal(args ...any)         { s.msg = fmt.Sprint(args...); panic(fatalStop{}) }
func (s *stubTB) Fatalf(f string, a ...any) { s.msg = fmt.Sprintf(f, a...); panic(fatalStop{}) }

func fatalOf(fn func(testing.TB)) (msg string, fataled bool) {
	s := &stubTB{}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fatalStop); !ok {
				panic(r)
			}
			msg, fataled = s.msg, true
		}
	}()
	fn(s)
	return "", false
}

func TestUnsetVariableFailsWithInstructions(t *testing.T) {
	t.Setenv(EnvURL, "")
	for name, fn := range map[string]func(testing.TB){
		"New":   func(tb testing.TB) { New(tb) },
		"Admin": func(tb testing.TB) { Admin(tb) },
	} {
		msg, fataled := fatalOf(fn)
		if !fataled {
			t.Fatalf("%s did not fail with the variable unset", name)
		}
		for _, want := range []string{"REMEDY_TEST_DATABASE_URL", "make pg-up", ". dev/pg/env.sh"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: message %q lacks %q", name, msg, want)
			}
		}
	}
}

func TestUnreachableDatabaseFailsAndNeverPrintsTheURL(t *testing.T) {
	t.Setenv(EnvURL, "postgres://nobody:s3cretpass@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	msg, fataled := fatalOf(func(tb testing.TB) { New(tb) })
	if !fataled {
		t.Fatal("New did not fail for an unreachable database")
	}
	if strings.Contains(msg, "s3cretpass") {
		t.Fatalf("the failure message contains the password: %q", msg)
	}
}
