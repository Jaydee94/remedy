package importsqlite

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for the tests' own look at the destination

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

const migrationsDir = "testdata/sqlite-migrations"

// Secrets of the fixture. They are test values, sealed with testKey like the server seals them.
const (
	hostPlain     = "-----BEGIN OPENSSH PRIVATE KEY-----\nfixture\n-----END OPENSSH PRIVATE KEY-----\n"
	githubPlain   = "ghp_fixtureTokenThatIsNotReal0000"
	alertmgrPlain = "am-token-fixture-1234"
)

func testKey(t *testing.T) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// ts is a timestamp in the store's fixed-width layout (nine fractional digits).
func ts(sec int) string { return fmt.Sprintf("2026-10-04T12:00:%02d.000000000Z", sec) }

// openSQLite opens a writable SQLite file in WAL mode, as the SQLite server opened it.
func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

// migrationFiles are the names of the copied SQLite migrations.
func migrationFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no SQLite migrations in %s: %v", migrationsDir, err)
	}
	sort.Strings(files)
	return files
}

// migrateSQLite applies the 18 files the way the SQLite server did: each in a transaction that records it in
// schema_migrations. Foreign keys are off while they run (the old runner switched them off for 008 and 016; on an empty file
// it makes no difference).
func migrateSQLite(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `PRAGMA foreign_keys = OFF`)
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`)
	for _, f := range migrationFiles(t) {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, filepath.Base(f)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, db, `PRAGMA foreign_keys = ON`)
}

func mustExec(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(q), err)
	}
}

// fillSQLite puts at least one row in every table of schema 018, with the cases the importer must carry over exactly.
func fillSQLite(t *testing.T, db *sql.DB, key secret.Key) {
	t.Helper()
	ghSealed, err := key.Seal([]byte(githubPlain), store.ConnectionAAD())
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
		VALUES (1, ?, '…0000', 'jaydee94', 'ok', '', ?)`, ghSealed, ts(1))
	mustExec(t, db, `INSERT INTO repos (id, connection_id, full_name, default_branch, enabled, last_polled_at, last_error, created_at)
		VALUES (1, 1, 'Jaydee94/home', 'main', 1, ?, '', ?)`, ts(2), ts(2))

	// Hosts 1 and 2; 2 is deleted, so the high-water mark (2) is above the highest id (1).
	for _, id := range []int{1, 2} {
		mustExec(t, db, `INSERT INTO hosts (id, name, address, port, account, notes, docker_pattern, systemd_units, enabled, pinned_key,
			pinned_at, status, status_detail, report, checked_at, created_at, updated_at, matchers)
			VALUES (?, ?, '192.0.2.10', 2222, 'remedy', 'Notizen: Grüße', '^remedy-', '["nginx.service"]', 1, 'ssh-ed25519 AAAAfixture',
			?, 'ok', '', 'report', ?, ?, ?, 'instance="nas"')`, id, fmt.Sprintf("nas-%d", id), ts(3), ts(3), ts(3), ts(4))
	}
	mustExec(t, db, `DELETE FROM hosts WHERE id = 2`)
	hostSealed, err := key.Seal([]byte(hostPlain), store.HostCredentialAAD(1))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO host_credentials (host_id, kind, ciphertext, hint, public_key, updated_at)
		VALUES (1, 'key', ?, 'SHA256:fixture', 'ssh-ed25519 AAAApublic remedy', ?)`, hostSealed, ts(5))

	// Runs. r-adhoc has events; r-resp diagnoses incident 1, which points back at it (the cycle runs <-> incidents).
	mustExec(t, db, `INSERT INTO runs (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at,
		finished_at, role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested, last_heartbeat_at, cluster, host_count)
		VALUES ('r-adhoc', 'claude', 'Wie geht es dem Cluster?', 'succeeded', 0, 'gut', 'sess-1', 0.30000000000000004, ?, ?, ?,
		'adhoc', NULL, NULL, '', '', 0, 1, 0, ?, 1, 1)`, ts(6), ts(6), ts(7), ts(7))
	mustExec(t, db, `INSERT INTO runs (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, role, head_sha,
		automatic, mcp) VALUES ('r-resp', 'claude', 'diagnose', 'failed', NULL, '', '', 1e-7, ?, 'responder', 'abc123', 1, 1)`, ts(8))
	mustExec(t, db, `INSERT INTO runs (id, provider, prompt, status, created_at, role) VALUES ('r-learn', 'claude', 'learn', 'queued', ?, 'learner')`, ts(9))

	// Incidents 1 to 5. 3 is deleted (a gap) and 5, the last, too (high-water mark 5 above the highest id 4).
	ghKey := "1\x1frefs/heads/main\x1fci"
	mustExec(t, db, `INSERT INTO incidents (id, source, key, title, severity, auto_diagnose, details, repo_id, ref, ref_url, check_name, state,
		conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, diagnosis, run_id, diagnosed_sha,
		diagnosed_at) VALUES (1, 'github', ?, 'ci', 'none', 1, ?, 1, 'refs/heads/main', 'https://example.test/ref', 'ci', 'diagnosed',
		'failure', 'abc123', 'https://example.test/check', 3, 1, ?, ?, ?, '{"summary":"x"}', NULL, 'abc123', ?)`,
		ghKey, []byte(`{"blob":"stored as a BLOB in a TEXT column"}`), ts(10), ts(11), ts(11), ts(11))
	mustExec(t, db, `UPDATE runs SET incident_id = 1 WHERE id = 'r-resp'`)
	mustExec(t, db, `UPDATE incidents SET run_id = 'r-resp' WHERE id = 1`)
	mustExec(t, db, `INSERT INTO incidents (id, source, key, title, severity, state, conclusion, first_seen, last_seen, resolved_at,
		resolved_reason, host_id, resolved_while_ignored) VALUES (2, 'alertmanager', 'fp-2', 'DiskFull', 'warning', 'resolved', 'firing', ?, ?, ?,
		'alert resolved', 1, 1)`, ts(12), ts(12), ts(13))
	mustExec(t, db, `INSERT INTO incidents (id, source, key, state, conclusion, first_seen, last_seen) VALUES (3, 'argocd', 'gone', 'open', 'x', ?, ?)`, ts(14), ts(14))
	mustExec(t, db, `INSERT INTO incidents (id, source, key, title, state, conclusion, first_seen, last_seen) VALUES (4, 'alertmanager', 'fp-4', 'Ignored', 'ignored', 'firing', ?, ?)`, ts(15), ts(15))
	mustExec(t, db, `INSERT INTO incidents (id, source, key, state, conclusion, first_seen, last_seen) VALUES (5, 'argocd', 'last', 'open', 'x', ?, ?)`, ts(16), ts(16))
	mustExec(t, db, `DELETE FROM incidents WHERE id IN (3, 5)`)

	mustExec(t, db, `INSERT INTO run_events (run_id, seq, kind, payload, created_at) VALUES ('r-adhoc', 1, 'stdout', '{"type":"system"}', ?),
		('r-adhoc', 2, 'stderr', 'Zeile mit Umlaut ä', ?)`, ts(17), ts(17))
	hash := sha256.Sum256([]byte("run token"))
	mustExec(t, db, `INSERT INTO run_tokens (run_id, token_hash, created_at, revoked_at) VALUES ('r-resp', ?, ?, ?)`, hash[:], ts(18), ts(19))
	mustExec(t, db, `INSERT INTO tool_calls (id, run_id, incident_id, tool_use_id, tool, kind, arguments, status, result, error, decision,
		decision_reason, decided_at, created_at, finished_at, expires_at) VALUES (1, 'r-resp', 1, 'toolu_1', 'cluster_restart', 'mutating',
		'{"namespace":"demo"}', 'succeeded', 'restarted', '', 'approved', 'ok', ?, ?, ?, ?)`, ts(20), ts(20), ts(21), ts(22))
	mustExec(t, db, `INSERT INTO incident_notes (id, incident_id, run_id, note, created_at) VALUES (1, 1, 'r-resp', 'a note', ?)`, ts(23))
	mustExec(t, db, `INSERT INTO run_hosts (run_id, host_id) VALUES ('r-adhoc', 1)`)

	amSealed, err := key.Seal([]byte(alertmgrPlain), "settings/alertmanager_token")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO settings (key, value, updated_at) VALUES ('run_timeout', '"10m"', ?), ('alertmanager_token', ?, ?)`,
		ts(24), fmt.Sprintf(`{"sealed":%q,"hint":"…1234"}`, base64.StdEncoding.EncodeToString(amSealed)), ts(24))

	// Skills: 2 replaces 1 (a lower id), 3 replaces 4 (a higher id: only a second pass can write it).
	mustExec(t, db, `INSERT INTO skills (id, host_id, name, description, body, origin, state, run_id, evidence, replaces_id, created_at, updated_at,
		confirmed_at) VALUES (1, NULL, 'restart', 'Restart nginx', 'body 1', 'written', 'active', NULL, '[]', NULL, ?, ?, ?)`, ts(25), ts(25), ts(25))
	mustExec(t, db, `INSERT INTO skills (id, host_id, name, description, body, origin, state, run_id, evidence, replaces_id, created_at, updated_at)
		VALUES (2, NULL, 'restart', 'Restart nginx better', 'body 2', 'proposed', 'proposed', 'r-learn', '[]', 1, ?, ?)`, ts(26), ts(26))
	mustExec(t, db, `INSERT INTO skills (id, host_id, name, description, body, origin, state, run_id, evidence, replaces_id, created_at, updated_at)
		VALUES (3, 1, 'disk', 'Disk', 'body 3', 'proposed', 'proposed', 'r-learn', '[]', NULL, ?, ?)`, ts(27), ts(27))
	mustExec(t, db, `INSERT INTO skills (id, host_id, name, description, body, origin, state, evidence, created_at, updated_at)
		VALUES (4, 1, 'disk', 'Disk', 'body 4', 'written', 'off', '[]', ?, ?)`, ts(28), ts(28))
	mustExec(t, db, `UPDATE skills SET replaces_id = 4 WHERE id = 3`)

	// Activity in order, nine-digit timestamps, links to every kind of target.
	mustExec(t, db, `INSERT INTO activity (at, kind, repo_id, incident_id, run_id, summary, data) VALUES
		(?, 'incident_opened', 1, 1, NULL, 'Incident opened', '{}'),
		(?, 'diagnosis_started', 1, 1, 'r-resp', 'Diagnosis started', '{"automatic":true}'),
		(?, 'setting_changed', NULL, NULL, NULL, 'Setting alertmanager_token changed', '{"key":"alertmanager_token"}')`,
		"2026-10-04T12:01:00.123456789Z", "2026-10-04T12:01:00.123456790Z", "2026-10-04T12:01:01.000000001Z")
}

// buildSource writes a SQLite file at schema 018 with a row in every table, checkpointed into the main file, and returns its
// path.
func buildSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remedy.db")
	db := openSQLite(t, path)
	migrateSQLite(t, db)
	fillSQLite(t, db, testKey(t))
	assertEveryTableHasRows(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// sqliteTables lists the tables the 18 migrations made, derived from the file, not from a list in the test.
func sqliteTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func assertEveryTableHasRows(t *testing.T, db *sql.DB) {
	t.Helper()
	tables := sqliteTables(t, db)
	if len(tables) != 14 {
		t.Fatalf("schema 018 has %d tables, want 14: %v", len(tables), tables)
	}
	for _, tb := range tables {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + tb).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatalf("the fixture has no row in %s", tb)
		}
	}
}

// destination opens an empty store on a fresh schema and a raw connection to the same schema for the test's own checks.
func destination(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	dsn := pgtest.New(t)
	s, err := store.Open(context.Background(), secret.NewValue(dsn), store.WithMaxConns(2))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	return s, raw
}

// dump returns every row of table as text lines in key order, the same form for SQLite and PostgreSQL: a TEXT value stored
// as a BLOB in SQLite and the same text in PostgreSQL read the same, a number keeps its kind, bytes are shown as hex.
func dump(t *testing.T, db *sql.DB, table string, cols []string, orderBy string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT ` + strings.Join(cols, ", ") + ` FROM ` + table + ` ORDER BY ` + orderBy)
	if err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for i, v := range vals {
			fmt.Fprintf(&b, "%s=", cols[i])
			switch x := v.(type) {
			case nil:
				b.WriteString("NULL")
			case int64:
				fmt.Fprintf(&b, "int:%d", x)
			case float64:
				fmt.Fprintf(&b, "real:%x", math.Float64bits(x))
			case string:
				fmt.Fprintf(&b, "str:%q", x)
			case []byte:
				if byteCols[table+"."+cols[i]] {
					fmt.Fprintf(&b, "bytes:%s", hex.EncodeToString(x))
				} else {
					fmt.Fprintf(&b, "str:%q", string(x))
				}
			default:
				t.Fatalf("%s.%s: unexpected type %T", table, cols[i], v)
			}
			b.WriteString(" ")
		}
		out = append(out, b.String())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// byteCols are the BYTEA columns: their values are compared as bytes.
var byteCols = map[string]bool{
	"github_connections.token_ciphertext": true, "host_credentials.ciphertext": true, "run_tokens.token_hash": true,
}

// orderOf is the key order of each table for dump: its primary key.
var orderOf = map[string]string{
	"activity": "id", "github_connections": "id", "host_credentials": "host_id", "hosts": "id", "incident_notes": "id",
	"incidents": "id", "repos": "id", "run_events": "run_id, seq", "run_hosts": "run_id, host_id", "run_tokens": "run_id",
	"runs": "id", "settings": "key", "skills": "id", "tool_calls": "id",
}

// sqliteColumns returns the column names of a SQLite table.
func sqliteColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func openSourceForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// counts returns the row count of every application table of the destination.
func counts(t *testing.T, raw *sql.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for tb := range orderOf {
		var n int
		if err := raw.QueryRow(`SELECT count(*) FROM ` + tb).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[tb] = n
	}
	return out
}

func assertEmpty(t *testing.T, raw *sql.DB) {
	t.Helper()
	for tb, n := range counts(t, raw) {
		if n != 0 {
			t.Errorf("destination table %s has %d rows, want 0", tb, n)
		}
	}
}

func TestSourceMigrationsAreTheTestdataFiles(t *testing.T) {
	var names []string
	for _, f := range migrationFiles(t) {
		names = append(names, filepath.Base(f))
	}
	if strings.Join(names, ",") != strings.Join(SourceMigrations, ",") {
		t.Fatalf("SourceMigrations = %v\nthe testdata has %v", SourceMigrations, names)
	}
}

func TestImportCopiesEveryTableExactly(t *testing.T) {
	src := buildSource(t)
	dst, raw := destination(t)

	rep, err := Run(context.Background(), src, dst, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.DryRun {
		t.Fatal("report says dry run")
	}

	sdb := openSourceForTest(t, src)
	seen := map[string]int{}
	for _, tr := range rep.Tables {
		seen[tr.Name] = tr.Rows
	}
	for _, tb := range sqliteTables(t, sdb) {
		cols := sqliteColumns(t, sdb, tb)
		want := dump(t, sdb, tb, cols, orderOf[tb])
		got := dump(t, raw, tb, cols, orderOf[tb])
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("table %s differs\n got: %v\nwant: %v", tb, got, want)
		}
		if n, ok := seen[tb]; !ok || n != len(want) {
			t.Errorf("report for %s: rows %d (listed %v), want %d", tb, n, ok, len(want))
		}
	}
	if len(rep.Tables) != 14 {
		t.Errorf("report lists %d tables, want 14", len(rep.Tables))
	}
}

func TestImportCopiesParentsFirst(t *testing.T) {
	src := buildSource(t)
	dst, _ := destination(t)
	rep, err := Run(context.Background(), src, dst, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, tr := range rep.Tables {
		pos[tr.Name] = i
	}
	before := [][2]string{
		{"github_connections", "repos"}, {"repos", "incidents"}, {"hosts", "incidents"}, {"runs", "incidents"},
		{"incidents", "activity"}, {"runs", "run_events"}, {"hosts", "host_credentials"}, {"incidents", "tool_calls"},
		{"hosts", "skills"}, {"runs", "skills"}, {"incidents", "incident_notes"}, {"hosts", "run_hosts"},
	}
	for _, p := range before {
		if pos[p[0]] >= pos[p[1]] {
			t.Errorf("%s is copied at %d, after %s at %d", p[0], pos[p[0]], p[1], pos[p[1]])
		}
	}
}

func TestImportedSecretsOpenWithTheSameKey(t *testing.T) {
	src := buildSource(t)
	dst, raw := destination(t)
	if _, err := Run(context.Background(), src, dst, Options{}); err != nil {
		t.Fatal(err)
	}
	key := testKey(t)

	h, err := dst.GetHost(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if h.Credential == nil {
		t.Fatal("host 1 has no credential after the import")
	}
	plain, err := key.Open(h.Credential.Ciphertext, store.HostCredentialAAD(h.Host.ID))
	if err != nil || string(plain) != hostPlain {
		t.Fatalf("host credential: %v (plaintext matches: %v)", err, string(plain) == hostPlain)
	}

	var gh []byte
	if err := raw.QueryRow(`SELECT token_ciphertext FROM github_connections WHERE id = 1`).Scan(&gh); err != nil {
		t.Fatal(err)
	}
	if p, err := key.Open(gh, store.ConnectionAAD()); err != nil || string(p) != githubPlain {
		t.Fatalf("GitHub token: %v", err)
	}
}

func TestImportContinuesIdsAboveTheHighWaterMark(t *testing.T) {
	src := buildSource(t)
	dst, raw := destination(t)
	rep, err := Run(context.Background(), src, dst, Options{})
	if err != nil {
		t.Fatal(err)
	}
	next := func(q string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := raw.QueryRow(q, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return id
	}
	// incidents: highest id 4, last given 5 (deleted).
	if id := next(`INSERT INTO incidents (source, key, state, conclusion, first_seen, last_seen) VALUES ('argocd', 'new', 'open', 'x', $1, $1) RETURNING id`, ts(40)); id != 6 {
		t.Errorf("next incident id = %d, want 6", id)
	}
	// hosts: highest id 1, last given 2 (deleted).
	if id := next(`INSERT INTO hosts (name, address, created_at, updated_at) VALUES ('new', 'x', $1, $1) RETURNING id`, ts(40)); id != 3 {
		t.Errorf("next host id = %d, want 3", id)
	}
	// activity: 3 rows, none deleted.
	if id := next(`INSERT INTO activity (at, kind, summary) VALUES ($1, 'k', 's') RETURNING id`, ts(40)); id != 4 {
		t.Errorf("next activity id = %d, want 4", id)
	}
	if id := next(`INSERT INTO skills (name, description, body, origin, state, created_at, updated_at) VALUES ('n', 'd', 'b', 'written', 'active', $1, $1) RETURNING id`, ts(40)); id != 5 {
		t.Errorf("next skill id = %d, want 5", id)
	}
	want := map[string]int64{"incidents": 5, "hosts": 2, "activity": 3, "skills": 4, "tool_calls": 1, "incident_notes": 1, "repos": 1}
	got := map[string]int64{}
	for _, sq := range rep.Sequences {
		got[sq.Table] = sq.Last
	}
	for tb, w := range want {
		if got[tb] != w {
			t.Errorf("report: sequence of %s set to %d, want %d", tb, got[tb], w)
		}
	}
}

func TestImportRefusesANonEmptyDestination(t *testing.T) {
	src := buildSource(t)
	dst, raw := destination(t)
	if _, err := Run(context.Background(), src, dst, Options{}); err != nil {
		t.Fatal(err)
	}
	before := counts(t, raw)
	beforeRows := dump(t, raw, "incidents", []string{"id", "state", "run_id"}, "id")

	_, err := Run(context.Background(), src, dst, Options{})
	if !errors.Is(err, ErrDestinationNotEmpty) {
		t.Fatalf("second Run: %v, want ErrDestinationNotEmpty", err)
	}
	if !strings.Contains(err.Error(), "activity") {
		t.Errorf("error %q does not name the first non-empty table (activity)", err)
	}
	after := counts(t, raw)
	for tb, n := range before {
		if after[tb] != n {
			t.Errorf("%s: %d rows after the refused import, %d before", tb, after[tb], n)
		}
	}
	if strings.Join(dump(t, raw, "incidents", []string{"id", "state", "run_id"}, "id"), "\n") != strings.Join(beforeRows, "\n") {
		t.Error("incidents changed")
	}
}

func TestImportRefusesOneRowInAnyTable(t *testing.T) {
	src := buildSource(t)
	dst, raw := destination(t)
	if _, err := raw.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('x', '1', $1)`, ts(1)); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), src, dst, Options{})
	if !errors.Is(err, ErrDestinationNotEmpty) || !strings.Contains(err.Error(), "settings") {
		t.Fatalf("Run: %v, want ErrDestinationNotEmpty naming settings", err)
	}
	if n := counts(t, raw)["incidents"]; n != 0 {
		t.Fatalf("incidents has %d rows after a refused import", n)
	}
}

// withMigrations rewrites the schema_migrations rows of a copy of the source.
func withMigrations(t *testing.T, src string, stmt string) string {
	t.Helper()
	cp := filepath.Join(t.TempDir(), "copy.db")
	copyFile(t, src, cp)
	db := openSQLite(t, cp)
	mustExec(t, db, stmt)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return cp
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportRefusesAnotherSchemaVersion(t *testing.T) {
	src := buildSource(t)
	cases := []struct {
		name, stmt, have, missing, unknown string
	}{
		{"missing 018", `DELETE FROM schema_migrations WHERE version = '018_incident_reopen.sql'`, "017_alertmanager_poll_interval.sql", "018_incident_reopen.sql", ""},
		{"missing 005", `DELETE FROM schema_migrations WHERE version = '005_responder.sql'`, "018_incident_reopen.sql", "005_responder.sql", ""},
		{"extra 019", `INSERT INTO schema_migrations (version) VALUES ('019_future.sql')`, "019_future.sql", "", "019_future.sql"},
		{"no migrations", `DELETE FROM schema_migrations`, "none", "001_init.sql", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cp := withMigrations(t, src, c.stmt)
			dst, raw := destination(t)
			_, err := Run(context.Background(), cp, dst, Options{})
			var sv *ErrSchemaVersion
			if !errors.As(err, &sv) {
				t.Fatalf("Run: %v, want *ErrSchemaVersion", err)
			}
			if sv.Have != c.have || sv.Want != "018_incident_reopen.sql" {
				t.Errorf("Have %q Want %q, want Have %q Want 018_incident_reopen.sql", sv.Have, sv.Want, c.have)
			}
			msg := err.Error()
			for _, s := range []string{c.have, sv.Want, c.missing, c.unknown, "upgrade"} {
				if s != "" && !strings.Contains(msg, s) {
					t.Errorf("message %q does not contain %q", msg, s)
				}
			}
			assertEmpty(t, raw)
		})
	}
}

func TestImportRefusesAFileWithoutSchemaMigrations(t *testing.T) {
	src := withMigrations(t, buildSource(t), `DROP TABLE schema_migrations`)
	dst, raw := destination(t)
	_, err := Run(context.Background(), src, dst, Options{})
	var sv *ErrSchemaVersion
	if !errors.As(err, &sv) || sv.Have != "none" {
		t.Fatalf("Run: %v, want *ErrSchemaVersion with Have none", err)
	}
	assertEmpty(t, raw)
}

func TestImportRefusesAMissingSource(t *testing.T) {
	dst, _ := destination(t)
	missing := filepath.Join(t.TempDir(), "nope.db")
	if _, err := Run(context.Background(), missing, dst, Options{}); err == nil {
		t.Fatal("Run on a missing file succeeded")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Run created the missing source: %v", err)
	}
}

func TestImportRollsBackEverythingOnAFailureMidway(t *testing.T) {
	src := buildSource(t)
	// A value SQLite took with its checks switched off and PostgreSQL's CHECK refuses. skills is among the last tables, so
	// most tables are already copied when it fails.
	cp := withMigrations(t, src, `PRAGMA ignore_check_constraints = ON; UPDATE skills SET state = 'bogus' WHERE id = 4`)
	dst, raw := destination(t)
	_, err := Run(context.Background(), cp, dst, Options{})
	if err == nil {
		t.Fatal("Run succeeded with a row the destination refuses")
	}
	if !strings.Contains(err.Error(), "skills") {
		t.Errorf("error %q does not name the table", err)
	}
	assertEmpty(t, raw)
	// The sequences were not moved: an insert takes id 1.
	var id int64
	if err := raw.QueryRow(`INSERT INTO incidents (source, key, state, conclusion, first_seen, last_seen) VALUES ('argocd', 'k', 'open', 'x', $1, $1) RETURNING id`, ts(1)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("first incident id after a failed import = %d, want 1", id)
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	src := buildSource(t)
	dst, raw := destination(t)
	rep, err := Run(context.Background(), src, dst, Options{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !rep.DryRun {
		t.Error("report does not say dry run")
	}
	want := map[string]int{"incidents": 3, "hosts": 1, "runs": 3, "skills": 4, "activity": 3, "run_events": 2, "settings": 2}
	got := map[string]int{}
	for _, tr := range rep.Tables {
		got[tr.Name] = tr.Rows
	}
	for tb, n := range want {
		if got[tb] != n {
			t.Errorf("dry run reports %d rows for %s, want %d", got[tb], tb, n)
		}
	}
	assertEmpty(t, raw)
	var id int64
	if err := raw.QueryRow(`INSERT INTO activity (at, kind, summary) VALUES ($1, 'k', 's') RETURNING id`, ts(1)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("a dry run moved the activity sequence: next id %d", id)
	}
	if _, err := raw.Exec(`DELETE FROM activity`); err != nil {
		t.Fatal(err)
	}
	// The real import still works after it (and still finds an empty destination).
	if _, err := Run(context.Background(), src, dst, Options{}); err != nil {
		t.Fatalf("import after a dry run: %v", err)
	}
}

func sum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// walCopy builds a source whose data is still in its -wal (never checkpointed) and copies the file with its -wal while no
// connection is open on the copy, the way the runbook says: the server is scaled to zero, then the file and its -wal are
// copied.
func walCopy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "remedy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	migrateSQLite(t, db)
	fillSQLite(t, db, testKey(t))
	// While the writer is still open the -wal holds everything; copy both files now.
	cpDir := t.TempDir()
	cp := filepath.Join(cpDir, "remedy.db")
	copyFile(t, path, cp)
	copyFile(t, path+"-wal", cp+"-wal")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(cp + "-wal")
	if err != nil || st.Size() == 0 {
		t.Fatalf("the copy has no -wal with content: %v", err)
	}
	return cp
}

func TestImportReadsAnUnflushedWALAndChangesNoFile(t *testing.T) {
	cp := walCopy(t)
	dbSum, walSum := sum(t, cp), sum(t, cp+"-wal")

	dst, raw := destination(t)
	rep, err := Run(context.Background(), cp, dst, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := counts(t, raw)["activity"]; n != 3 {
		t.Fatalf("activity has %d rows: the -wal was not read (report %v)", n, rep.Tables)
	}
	if sum(t, cp) != dbSum {
		t.Error("the import changed the database file")
	}
	if _, err := os.Stat(cp + "-wal"); err != nil {
		t.Fatalf("the -wal is gone after the import: %v", err)
	}
	if sum(t, cp+"-wal") != walSum {
		t.Error("the import changed the -wal")
	}
}

func TestImportDoesNotChangeACheckpointedFile(t *testing.T) {
	src := buildSource(t)
	before := sum(t, src)
	dst, _ := destination(t)
	if _, err := Run(context.Background(), src, dst, Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), src, dst, Options{}); err != nil {
		t.Fatal(err)
	}
	if sum(t, src) != before {
		t.Error("the import changed the database file")
	}
}

func TestOpenSourceIsReadOnly(t *testing.T) {
	src := buildSource(t)
	db, err := openSource(src)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Even with query_only switched off again, the file is opened read-only.
	if _, err := db.Exec(`PRAGMA query_only = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE intruder (a)`); err == nil {
		t.Fatal("a write through the importer's connection succeeded")
	}
}

// helperEnv makes the test binary act as a second process that keeps a SQLite file open, as a running server does.
const helperEnv = "REMEDY_IMPORTSQLITE_HELPER_DB"

func TestMain(m *testing.M) {
	if path := os.Getenv(helperEnv); path != "" {
		holdOpen(path)
		return
	}
	os.Exit(m.Run())
}

// holdOpen opens path like the server (WAL), writes a row, prints "ready" and waits until stdin closes.
func holdOpen(path string) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(2)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('live', '1', '` + ts(50) + `')`); err != nil {
		fmt.Println("error:", err)
		os.Exit(2)
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = db.Close()
}

func TestImportRefusesAFileAnotherProcessHasOpen(t *testing.T) {
	src := buildSource(t)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"="+src)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper: %q %v", line, err)
	}

	dst, raw := destination(t)
	_, err = Run(context.Background(), src, dst, Options{})
	if !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("Run while another process has the file open: %v, want ErrSourceBusy", err)
	}
	assertEmpty(t, raw)

	// Once the other process is gone, the same file imports.
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	if _, err := Run(context.Background(), src, dst, Options{}); err != nil {
		t.Fatalf("Run after the other process closed the file: %v", err)
	}
	if n := counts(t, raw)["settings"]; n != 3 {
		t.Errorf("settings has %d rows, want 3 (with the helper's row)", n)
	}
}

// startHolder starts a second process that keeps path open as a running server does, and stops it when the test ends. The
// returned function stops it earlier.
func startHolder(t *testing.T, path string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := false
	stop := func() {
		if done {
			return
		}
		done = true
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("helper: %v", err)
		}
	}
	t.Cleanup(stop)
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper: %q %v", line, err)
	}
	return stop
}

func TestImportRefusesASymlinkToAFileAnotherProcessHasOpen(t *testing.T) {
	src := buildSource(t)
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	stop := startHolder(t, src)
	dst, raw := destination(t)
	if _, err := Run(context.Background(), link, dst, Options{}); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("Run on a symlink to a file another process has open: %v, want ErrSourceBusy", err)
	}
	assertEmpty(t, raw)
	stop()
	if _, err := Run(context.Background(), link, dst, Options{}); err != nil {
		t.Fatalf("Run on the symlink once the other process is gone: %v", err)
	}
}

func TestImportRefusesASourceOfAnotherShape(t *testing.T) {
	src := buildSource(t)
	cases := []struct {
		name, stmt string
		want       []string
	}{
		{"extra column", `ALTER TABLE hosts ADD COLUMN extra TEXT NOT NULL DEFAULT 'kept nowhere'`, []string{"hosts", "extra"}},
		{"extra table", `CREATE TABLE notes_of_mine (id INTEGER PRIMARY KEY, body TEXT); INSERT INTO notes_of_mine VALUES (1, 'x')`, []string{"notes_of_mine"}},
		{"table named like SQLite's own", `CREATE TABLE sqlitefoo (a TEXT); INSERT INTO sqlitefoo VALUES ('x')`, []string{"sqlitefoo"}},
		{"missing column", `ALTER TABLE incidents DROP COLUMN ref_url`, []string{"incidents", "ref_url"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cp := withMigrations(t, src, c.stmt)
			dst, raw := destination(t)
			_, err := Run(context.Background(), cp, dst, Options{})
			if err == nil {
				t.Fatal("Run succeeded on a source of another shape")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not name %q", err, w)
				}
			}
			assertEmpty(t, raw)
		})
	}
}

func TestImportedDataReadsBackThroughTheStore(t *testing.T) {
	src := buildSource(t)
	dst, _ := destination(t)
	if _, err := Run(context.Background(), src, dst, Options{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	inc, err := dst.GetIncident(ctx, 1)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if inc.Key != "1\x1frefs/heads/main\x1fci" || inc.RunID != "r-resp" || inc.RepoID != 1 || string(inc.Details) != `{"blob":"stored as a BLOB in a TEXT column"}` {
		t.Errorf("incident 1 = key %q run %q repo %d details %s", inc.Key, inc.RunID, inc.RepoID, inc.Details)
	}
	inc2, err := dst.GetIncident(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if inc2.HostID != 1 || string(inc2.State) != "resolved" {
		t.Errorf("incident 2 = host %d state %s", inc2.HostID, inc2.State)
	}
	if _, err := dst.GetIncident(ctx, 3); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetIncident(3) = %v, want ErrNotFound (it was deleted)", err)
	}

	r, err := dst.GetRun(ctx, "r-resp")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if r.IncidentID == nil || *r.IncidentID != 1 || r.CostUSD != 1e-7 {
		t.Errorf("run r-resp = incident %v cost %v", r.IncidentID, r.CostUSD)
	}
	a, err := dst.GetRun(ctx, "r-adhoc")
	if err != nil {
		t.Fatal(err)
	}
	if a.CostUSD != 0.30000000000000004 || a.Prompt != "Wie geht es dem Cluster?" {
		t.Errorf("run r-adhoc = cost %v prompt %q", a.CostUSD, a.Prompt)
	}

	hosts, err := dst.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Host.ID != 1 || hosts[0].Host.Name != "nas-1" {
		t.Errorf("hosts = %+v", hosts)
	}

	settings, err := dst.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if len(settings) != 2 {
		t.Fatalf("settings = %+v", settings)
	}

	sk, err := dst.GetSkill(ctx, 3)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if sk.ReplacesID != 4 || sk.HostID != 1 {
		t.Errorf("skill 3 = replaces %d host %d", sk.ReplacesID, sk.HostID)
	}
}

func TestVerifyFindsAChangedAMissingAndAnExtraRow(t *testing.T) {
	dst, _ := destination(t)
	errStop := errors.New("stop")
	err := dst.ImportTx(context.Background(), func(tx *sql.Tx) error {
		ctx := context.Background()
		tables, err := readCatalog(ctx, tx)
		if err != nil {
			return err
		}
		tb := tables["settings"]
		row := func(key, value string) []any {
			vals := make([]any, len(tb.cols))
			for i, c := range tb.cols {
				switch c.name {
				case "key":
					vals[i] = key
				case "value":
					vals[i] = value
				case "updated_at":
					vals[i] = ts(1)
				}
			}
			return vals
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('a', '1', $1)`, ts(1)); err != nil {
			return err
		}
		same := map[string][32]byte{encodeKey(tb, row("a", "1")): digest(row("a", "1"))}
		if err := verify(ctx, tx, tb, same); err != nil {
			t.Errorf("verify of an equal table: %v", err)
		}
		changed := map[string][32]byte{encodeKey(tb, row("a", "1")): digest(row("a", "2"))}
		if err := verify(ctx, tx, tb, changed); err == nil || !strings.Contains(err.Error(), "differs") {
			t.Errorf("verify of a changed row: %v", err)
		}
		missing := map[string][32]byte{encodeKey(tb, row("a", "1")): digest(row("a", "1")), encodeKey(tb, row("b", "1")): digest(row("b", "1"))}
		if err := verify(ctx, tx, tb, missing); err == nil || !strings.Contains(err.Error(), "1 rows in the destination, 2 in the SQLite file") {
			t.Errorf("verify of a missing row: %v", err)
		}
		if err := verify(ctx, tx, tb, map[string][32]byte{}); err == nil || !strings.Contains(err.Error(), "has a row the SQLite file has not") {
			t.Errorf("verify of an extra row: %v", err)
		}
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatal(err)
	}
}

func TestConvertRefusesWhatDoesNotFit(t *testing.T) {
	bad := []struct {
		v   any
		typ string
	}{
		{int64(1), "text"}, {1.5, "text"}, {"x", "integer"}, {1.5, "bigint"}, {int64(math.MaxInt32 + 1), "integer"},
		{"1.5", "double precision"}, {int64(1)<<60 + 1, "double precision"}, {int64(1), "bytea"}, {"\xff", "text"},
		{"a\x00b", "text"}, {"x", "jsonb"},
	}
	for _, c := range bad {
		if v, err := convert(c.v, c.typ); err == nil {
			t.Errorf("convert(%#v, %s) = %#v, want an error", c.v, c.typ, v)
		}
	}
	good := []struct {
		v    any
		typ  string
		want any
	}{
		{"x", "text", "x"}, {[]byte("x"), "text", "x"}, {int64(7), "integer", int64(7)}, {int64(1) << 40, "bigint", int64(1) << 40},
		{0.25, "double precision", 0.25}, {int64(3), "double precision", 3.0}, {nil, "text", nil},
		{int64(1) << 53, "double precision", float64(1 << 53)}, {-(int64(1) << 53), "double precision", -float64(1 << 53)},
	}
	for _, c := range good {
		v, err := convert(c.v, c.typ)
		if err != nil || v != c.want {
			t.Errorf("convert(%#v, %s) = %#v, %v; want %#v", c.v, c.typ, v, err, c.want)
		}
	}
	if _, err := convert(int64(1)<<53+1, "double precision"); err == nil {
		t.Error("2^53+1 converted to a double")
	}
	if _, err := convert(int64(1), "text"); err == nil || err.Error() != "an integer value in a text column" {
		t.Errorf("message = %v", err)
	}
	if v, err := convert("ab", "bytea"); err != nil || !bytes.Equal(v.([]byte), []byte("ab")) {
		t.Errorf("convert text to bytea = %#v, %v", v, err)
	}
}
