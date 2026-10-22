package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

// openTest opens a Store on a fresh schema with a small pool. The Store's cleanup is registered after pgtest.New, so it
// runs first (cleanups are last-in first-out) and the schema drop never waits on an open pool.
func openTest(t *testing.T) *Store {
	t.Helper()
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(3))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRebindNumbersPlaceholders(t *testing.T) {
	cases := []struct{ in, want string }{
		{`SELECT 1 WHERE a = ? AND b IN (?, ?)`, `SELECT 1 WHERE a = $1 AND b IN ($2, $3)`},
		{`SELECT 1`, `SELECT 1`},
		{`SELECT 'it''s' WHERE a = ?`, `SELECT 'it''s' WHERE a = $1`},
		{`SELECT x::text FROM "t" WHERE a = ?`, `SELECT x::text FROM "t" WHERE a = $1`},
		{`DO $$ BEGIN PERFORM 1; END $$`, `DO $$ BEGIN PERFORM 1; END $$`},
		{`SELECT $tag$ a $tag$, ?`, `SELECT $tag$ a $tag$, $1`},
		{`SELECT ? -- note` + "\n" + `, ?`, `SELECT $1 -- note` + "\n" + `, $2`},
	}
	for _, c := range cases {
		if got := rebind(c.in); got != c.want {
			t.Errorf("rebind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRebindRefusesAQuestionMarkInsideQuotesOrComments(t *testing.T) {
	for _, q := range []string{
		`SELECT 'what?' WHERE a = ?`,
		`SELECT "odd?name" FROM t`,
		`SELECT 1 -- why?` + "\n",
		`SELECT 1 /* why? */`,
		`SELECT $$ why? $$`,
		`SELECT ?, 'unterminated`,
		`SELECT ? /* unterminated`,
	} {
		if _, err := rebindErr(q); err == nil {
			t.Errorf("rebindErr(%q) accepted the statement", q)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("rebind(%q) did not panic", q)
				}
			}()
			rebind(q)
		}()
	}
}

func TestRefusedStatementFailsAtFirstUse(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	bad := `SELECT 'what?' WHERE 1 = ?`
	if _, err := s.db.ExecContext(ctx, bad, 1); err == nil {
		t.Error("ExecContext ran a refused statement")
	}
	if _, err := s.db.QueryContext(ctx, bad, 1); err == nil {
		t.Error("QueryContext ran a refused statement")
	}
	err := s.inTx(ctx, func(tx *txn) error {
		_, err := tx.ExecContext(ctx, bad, 1)
		return err
	})
	if err == nil {
		t.Error("a transaction ran a refused statement")
	}
}

func TestWrapperRewritesPlaceholdersOnPoolAndTransaction(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE kv (k TEXT PRIMARY KEY, v INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?), (?, ?)`, "a", 1, "b", 2); err != nil {
		t.Fatalf("pool insert: %v", err)
	}
	err := s.inTx(ctx, func(tx *txn) error {
		if _, err := tx.ExecContext(ctx, `UPDATE kv SET v = v + ? WHERE k = ?`, 10, "a"); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT k FROM kv WHERE v > ? ORDER BY k`, 0)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			n++
		}
		_ = rows.Close()
		if n != 2 {
			return fmt.Errorf("saw %d rows", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, "a").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 11 {
		t.Fatalf("v = %d, want 11", v)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE r (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := s.inTx(ctx, func(tx *txn) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO r (id) VALUES (?)`, 1); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("inTx = %v, want boom", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM r`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows survived a rolled-back transaction", n)
	}
}

func TestUniqueViolationIsRecognisedWithItsConstraint(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE u (name TEXT, CONSTRAINT u_name_key UNIQUE (name))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO u (name) VALUES (?)`, "x"); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO u (name) VALUES (?)`, "x")
	if !isUnique(err) {
		t.Fatalf("isUnique(%v) = false", err)
	}
	if got := uniqueConstraint(err); got != "u_name_key" {
		t.Fatalf("uniqueConstraint = %q, want u_name_key", got)
	}
	if isSerialization(err) {
		t.Fatal("a unique violation counts as a serialization failure")
	}
	wrapped := fmt.Errorf("insert: %w", err)
	if !isUnique(wrapped) || uniqueConstraint(wrapped) != "u_name_key" {
		t.Fatal("a wrapped unique violation is not recognised")
	}

	_, other := s.db.ExecContext(ctx, `INSERT INTO u (name, nope) VALUES (?, ?)`, "y", 1)
	if other == nil || isUnique(other) || uniqueConstraint(other) != "" {
		t.Fatalf("another error counts as a unique violation: %v", other)
	}
	if isUnique(nil) || isSerialization(nil) || uniqueConstraint(nil) != "" {
		t.Fatal("nil counts as an error")
	}
}

func TestInTxIsoRetriesSerializationFailures(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	serial := &pgconn.PgError{Code: "40001", Message: "could not serialize access"}

	calls := 0
	err := s.inTxIso(ctx, sql.LevelSerializable, func(tx *txn) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("step: %w", serial)
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("inTxIso = %v after %d calls, want nil after 3", err, calls)
	}

	calls = 0
	err = s.inTxIso(ctx, sql.LevelSerializable, func(tx *txn) error {
		calls++
		return serial
	})
	if !isSerialization(err) {
		t.Fatalf("inTxIso = %v, want the serialization failure", err)
	}
	if calls != 1+maxSerializationRetries {
		t.Fatalf("fn ran %d times, want %d", calls, 1+maxSerializationRetries)
	}

	calls = 0
	other := errors.New("other")
	if err := s.inTxIso(ctx, sql.LevelSerializable, func(tx *txn) error { calls++; return other }); !errors.Is(err, other) || calls != 1 {
		t.Fatalf("another error was retried: %v after %d calls", err, calls)
	}
}

// withPassword returns the test server's URL with another user and password.
func withPassword(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("the test URL does not parse")
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

func TestOpenWithAWrongPasswordFailsWithoutShowingIt(t *testing.T) {
	dsn := pgtest.New(t)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("the test URL does not parse")
	}
	const wrong = "Wr0ng-p4ssw0rd-never-shown"
	bad := withPassword(t, dsn, u.User.Username(), wrong)
	start := time.Now()
	s, err := Open(context.Background(), secret.NewValue(bad), WithMaxConns(2))
	if err == nil {
		_ = s.Close()
		t.Fatal("Open accepted a wrong password")
	}
	if strings.Contains(err.Error(), wrong) || strings.Contains(err.Error(), bad) {
		t.Fatalf("the error shows the password: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("a refused login was retried for %v", time.Since(start))
	}
}

func TestOpenRetriesAClosedPortForABoundedTimeThenFails(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	const pw = "cl0sed-port-secret"
	dsn := "postgres://remedy:" + pw + "@127.0.0.1:" + strconv.Itoa(port) + "/remedy?sslmode=disable"

	start := time.Now()
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(2),
		withConnectWait(1500*time.Millisecond, 100*time.Millisecond, 300*time.Millisecond))
	elapsed := time.Since(start)
	if err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded against a closed port")
	}
	if elapsed < 1400*time.Millisecond {
		t.Fatalf("Open gave up after %v, before the wait was over", elapsed)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("Open waited %v, longer than the bound", elapsed)
	}
	if strings.Contains(err.Error(), pw) {
		t.Fatalf("the error shows the password: %v", err)
	}
}

func TestOpenRefusesAnUnparsableURLWithoutShowingIt(t *testing.T) {
	const bad = "postgres://u:s3cret-in-bad-url@[::1/oops"
	_, err := Open(context.Background(), secret.NewValue(bad))
	if err == nil {
		t.Fatal("Open accepted a broken URL")
	}
	if strings.Contains(err.Error(), "s3cret-in-bad-url") {
		t.Fatalf("the error shows the password: %v", err)
	}
}

func TestOpenAcceptsAPasswordWithURLSpecialCharacters(t *testing.T) {
	dsn := pgtest.New(t)
	admin := pgtest.Admin(t)
	role := "t_role_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	const pw = `p@ss/w%rd?x#1`
	if _, err := admin.Exec(`CREATE ROLE ` + role + ` LOGIN PASSWORD '` + strings.ReplaceAll(pw, "'", "''") + `'`); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() {
		// DROP OWNED removes what the role made in the test schema (schema_migrations) and its grants.
		if _, err := admin.Exec(`DROP OWNED BY ` + role); err != nil {
			t.Errorf("drop owned: %v", err)
		}
		if _, err := admin.Exec(`DROP ROLE ` + role); err != nil {
			t.Errorf("drop role: %v", err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("the test URL does not parse")
	}
	schema := u.Query().Get("search_path")
	if _, err := admin.Exec(`GRANT USAGE, CREATE ON SCHEMA ` + schema + ` TO ` + role); err != nil {
		t.Fatalf("grant: %v", err)
	}

	s, err := Open(context.Background(), secret.NewValue(withPassword(t, dsn, role, pw)), WithMaxConns(1))
	if err != nil {
		t.Fatalf("Open with a percent-encoded password: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var who string
	if err := s.db.QueryRowContext(context.Background(), `SELECT current_user`).Scan(&who); err != nil {
		t.Fatal(err)
	}
	if who != role {
		t.Fatalf("connected as %q, want %q", who, role)
	}
}

func TestWithMaxConnsSetsThePoolSize(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if got := s.db.db.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("MaxOpenConnections = %d, want 4", got)
	}
	if _, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(0)); err == nil {
		t.Fatal("Open accepted a pool of 0 connections")
	}
}

func TestDefaultPoolSize(t *testing.T) {
	o := defaultOptions()
	if o.maxConns != DefaultMaxConns || DefaultMaxConns != 10 {
		t.Fatalf("default pool size = %d, want 10", o.maxConns)
	}
}

func TestOpenCreatesTheMigrationTable(t *testing.T) {
	s := openTest(t)
	var reg sql.NullString
	if err := s.db.QueryRowContext(context.Background(), `SELECT to_regclass('schema_migrations')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if !reg.Valid {
		t.Fatal("schema_migrations is missing after Open")
	}
}

// quotedQuestionMarks returns the string literals of the Go files in dir (tests excluded) that hold a question mark the
// placeholder rewrite would refuse: inside quotes or a comment of the SQL.
func quotedQuestionMarks(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.Contains(s, "?") {
				return true
			}
			if _, err := rebindErr(s); err != nil {
				bad = append(bad, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	return bad
}

func TestNoStoreSQLHasAQuotedQuestionMark(t *testing.T) {
	if bad := quotedQuestionMarks(t, "."); len(bad) > 0 {
		t.Fatalf("SQL strings with a question mark inside quotes or a comment: %v", bad)
	}
}

func TestTheSourceScanFindsAQuotedQuestionMark(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nconst q = `SELECT 'why?' FROM t WHERE a = ?`\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if bad := quotedQuestionMarks(t, dir); len(bad) != 1 {
		t.Fatalf("the scan found %v, want the one planted literal", bad)
	}
}

func TestIncidentStatementTextIsConstant(t *testing.T) {
	q1, a1 := incidentSelect(` WHERE i.id = ?`, int64(7))
	time.Sleep(time.Millisecond)
	q2, _ := incidentSelect(` WHERE i.id = ?`, int64(7))
	if q1 != q2 {
		t.Fatalf("the incident statement text changes between calls:\n%s\n%s", q1, q2)
	}
	if len(a1) != 2 || a1[1] != int64(7) {
		t.Fatalf("args = %v, want the cutoff first, then the caller's", a1)
	}
	cut, ok := a1[0].(string)
	if !ok {
		t.Fatalf("the cutoff is %T, want a timestamp string", a1[0])
	}
	ts, err := parseTS(cut)
	if err != nil {
		t.Fatalf("the cutoff %q is not a timestamp: %v", cut, err)
	}
	if d := time.Since(ts) - 24*time.Hour; d < 0 || d > time.Minute {
		t.Fatalf("the cutoff is %v off 24 hours ago", d)
	}
	// The cutoff binds to the first placeholder, which must be the one in the column list.
	if first, from := strings.Index(q1, "?"), strings.Index(q1, incidentFrom); first < 0 || first > from {
		t.Fatal("the first placeholder is not in the column list")
	}
	if n := strings.Count(q1, "?"); n != 2 {
		t.Fatalf("the statement has %d placeholders, want 2", n)
	}
}

func TestRebindCacheIsBounded(t *testing.T) {
	for i := 0; i < maxRebound+200; i++ {
		q := fmt.Sprintf(`SELECT %d WHERE a = ? AND b = ?`, i)
		want := fmt.Sprintf(`SELECT %d WHERE a = $1 AND b = $2`, i)
		if got := rebind(q); got != want {
			t.Fatalf("rebind(%q) = %q", q, got)
		}
		if got := rebind(q); got != want { // a second time, from the cache or past the cap
			t.Fatalf("rebind(%q) again = %q", q, got)
		}
	}
	if n := reboundLen(); n > maxRebound {
		t.Fatalf("the cache holds %d entries, cap %d", n, maxRebound)
	}
	q := `SELECT 'past the cap' WHERE x = ?`
	if got := rebind(q); got != `SELECT 'past the cap' WHERE x = $1` {
		t.Fatalf("rebind past the cap = %q", got)
	}
	if _, err := rebindErr(`SELECT 'past the cap?' WHERE x = ?`); err == nil {
		t.Fatal("past the cap a quoted question mark is accepted")
	}
	if n := reboundLen(); n > maxRebound {
		t.Fatalf("the cache grew to %d", n)
	}
}

func TestPanicInTransactionRollsBack(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE p (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("recovered %v, want the re-panicked boom", r)
			}
		}()
		_ = s.inTx(ctx, func(tx *txn) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO p (id) VALUES (?)`, 1); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	// With a pool of one, a transaction left open would hold the only connection and block this query.
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var n int
	if err := s.db.QueryRowContext(qctx, `SELECT count(*) FROM p`).Scan(&n); err != nil {
		t.Fatalf("the connection is not usable after the panic: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d rows of the panicked transaction are visible", n)
	}
}

// runtime.Goexit (what t.Fatal calls) inside a transaction must roll it back too: it runs the defers but recover sees
// nothing.
func TestGoexitInTransactionRollsBack(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE g (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.inTx(ctx, func(tx *txn) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO g (id) VALUES (?)`, 1); err != nil {
				return err
			}
			runtime.Goexit()
			return nil
		})
		t.Error("inTx returned after a Goexit")
	}()
	<-done
	// With a pool of one, a transaction left open would hold the only connection and block this query.
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var n int
	if err := s.db.QueryRowContext(qctx, `SELECT count(*) FROM g`).Scan(&n); err != nil {
		t.Fatalf("the connection is not usable after the Goexit: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d rows of the abandoned transaction are visible", n)
	}
}

func TestInTxIsoReturnsTheContextErrorWhenCancelledDuringThePause(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	serial := &pgconn.PgError{Code: "40001"}
	calls := 0
	err := s.inTxIso(ctx, sql.LevelSerializable, func(tx *txn) error {
		calls++
		cancel()
		return serial
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("inTxIso = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times after the cancel, want 1", calls)
	}
}

func TestScrubbedErrorKeepsTheChain(t *testing.T) {
	dsn := pgtest.New(t)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("the test URL does not parse")
	}
	const wrong = "Wr0ng-p4ss-for-the-chain"
	bad := withPassword(t, dsn, u.User.Username(), wrong)
	_, err = Open(context.Background(), secret.NewValue(bad), WithMaxConns(1))
	if err == nil {
		t.Fatal("Open accepted a wrong password")
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "28P01" {
		t.Fatalf("errors.As found no PgError 28P01 in %v", err)
	}
	if strings.Contains(err.Error(), wrong) || strings.Contains(err.Error(), bad) {
		t.Fatalf("the error shows the password: %v", err)
	}
	se := scrub(errors.New("dial "+bad+" as "+wrong), bad, wrong)
	if got := fmt.Sprintf("%v|%s|%+v|%v", se, se, se, fmt.Errorf("w: %w", se)); strings.Contains(got, wrong) {
		t.Fatalf("a scrubbed error prints the secret: %s", got)
	}
}

// withoutPassword returns the test server's URL with its user and no password, and that password.
func withoutPassword(t *testing.T, dsn string) (string, string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("the test URL does not parse")
	}
	pw, ok := u.User.Password()
	if !ok || pw == "" {
		t.Fatal("the test URL has no password to move into PGPASSWORD")
	}
	u.User = url.User(u.User.Username())
	return u.String(), pw
}

// The chart's own PostgreSQL gives the control plane a URL without a password and the password as PGPASSWORD
// (deploy/chart/templates/server-deployment.yaml): pgx must take it from the environment. A pgx that stopped doing so
// fails this test, not the cluster.
func TestOpenTakesThePasswordFromPGPASSWORDWhenTheURLHasNone(t *testing.T) {
	bare, pw := withoutPassword(t, pgtest.New(t))
	t.Setenv("PGPASSWORD", pw)
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "none")) // no ~/.pgpass may stand in for it
	s, err := Open(context.Background(), secret.NewValue(bare), WithMaxConns(1))
	if err != nil {
		t.Fatalf("Open with the password in PGPASSWORD: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var one int
	if err := s.db.QueryRowContext(context.Background(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 = %d, %v", one, err)
	}
	// Without PGPASSWORD the same URL is refused: the password really came from the environment.
	t.Setenv("PGPASSWORD", "")
	if s2, err := Open(context.Background(), secret.NewValue(bare), WithMaxConns(1)); err == nil {
		_ = s2.Close()
		t.Fatal("the URL without a password connected without PGPASSWORD too: the test proves nothing")
	}
}

func TestOpenWithAWrongPGPASSWORDFailsFastWithoutShowingIt(t *testing.T) {
	bare, _ := withoutPassword(t, pgtest.New(t))
	const wrong = "Wr0ng-PGPASSWORD-never-shown"
	t.Setenv("PGPASSWORD", wrong)
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "none"))
	start := time.Now()
	s, err := Open(context.Background(), secret.NewValue(bare), WithMaxConns(1))
	if err == nil {
		_ = s.Close()
		t.Fatal("Open accepted a wrong PGPASSWORD")
	}
	if strings.Contains(err.Error(), wrong) || strings.Contains(fmt.Sprintf("%+v", err), wrong) {
		t.Fatalf("the error shows the password: %v", err)
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "28P01" {
		t.Errorf("want the server's refusal of the password (28P01), got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("a refused login was retried for %v", time.Since(start))
	}
}
