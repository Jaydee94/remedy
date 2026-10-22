package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
	"unicode"

	_ "modernc.org/sqlite" // this test builds SQLite schema 018 in memory; the server itself no longer links SQLite

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
	_ "modernc.org/sqlite" // the driver of the in-memory SQLite that builds schema 018
)

// The schema tests check the PostgreSQL baseline (migrations/001_baseline.sql) against what the 18 SQLite migrations left
// (schema 018) and the migration runner against two starts at once. The SQLite files live on as the importer's fixture in
// internal/importsqlite/testdata/sqlite-migrations; TestSchemaMatchesSQLiteSchema018 builds schema 018 from them in memory.

const sqliteMigrationsDir = "../importsqlite/testdata/sqlite-migrations"

// schemaTest opens a Store on a fresh schema and returns it with its pool for raw SQL (PostgreSQL placeholders: $1, ...).
func schemaTest(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	s := openTest(t)
	return s, s.db.db
}

// tables are the tables of SQLite schema 018 (migrations 001, 002, 003, 006, 010, 011, 012, 015; 008 and 016 rebuilt incidents
// and runs under their old names; sqlite_sequence is SQLite's own) plus the runner's schema_migrations.
var schemaTables = []string{
	"activity", "github_connections", "host_credentials", "hosts", "incident_notes", "incidents", "repos", "run_events",
	"run_hosts", "run_tokens", "runs", "schema_migrations", "settings", "skills", "tool_calls",
}

// fkRule is one foreign key of SQLite schema 018, as the CREATE TABLE and ALTER TABLE statements of the old files wrote it.
type fkRule struct{ child, column, parent, onDelete string }

// schemaForeignKeys is every foreign key of the old files with its ON DELETE action (CASCADE or SET NULL; none had another).
var schemaForeignKeys = []fkRule{
	{"activity", "incident_id", "incidents", "SET NULL"},        // 003
	{"activity", "repo_id", "repos", "SET NULL"},                // 003
	{"activity", "run_id", "runs", "SET NULL"},                  // 003
	{"host_credentials", "host_id", "hosts", "CASCADE"},         // 010
	{"incident_notes", "incident_id", "incidents", "CASCADE"},   // 006
	{"incident_notes", "run_id", "runs", "SET NULL"},            // 006
	{"incidents", "host_id", "hosts", "SET NULL"},               // 013
	{"incidents", "repo_id", "repos", "CASCADE"},                // 003, 008
	{"incidents", "run_id", "runs", "SET NULL"},                 // 003, 008
	{"repos", "connection_id", "github_connections", "CASCADE"}, // 002
	{"run_events", "run_id", "runs", "CASCADE"},                 // 001
	{"run_hosts", "host_id", "hosts", "CASCADE"},                // 011
	{"run_hosts", "run_id", "runs", "CASCADE"},                  // 011
	{"run_tokens", "run_id", "runs", "CASCADE"},                 // 006
	{"runs", "incident_id", "incidents", "SET NULL"},            // 004, 016
	{"skills", "host_id", "hosts", "CASCADE"},                   // 015
	{"skills", "replaces_id", "skills", "SET NULL"},             // 015
	{"skills", "run_id", "runs", "SET NULL"},                    // 015
	{"tool_calls", "incident_id", "incidents", "SET NULL"},      // 006
	{"tool_calls", "run_id", "runs", "CASCADE"},                 // 006
}

// identityTables have an id of INTEGER PRIMARY KEY AUTOINCREMENT in SQLite, an identity column here.
var identityTables = []string{"activity", "hosts", "incident_notes", "incidents", "repos", "skills", "tool_calls"}

func TestSchemaHasEveryTableOfSQLiteSchema018(t *testing.T) {
	_, raw := schemaTest(t)
	got := queryStrings(t, raw, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() ORDER BY table_name`)
	if strings.Join(got, ",") != strings.Join(schemaTables, ",") {
		t.Fatalf("tables = %v\nwant     %v", got, schemaTables)
	}
}

func TestSchemaRecordsTheBaseline(t *testing.T) {
	_, raw := schemaTest(t)
	got := queryStrings(t, raw, `SELECT version FROM schema_migrations ORDER BY version`)
	if strings.Join(got, ",") != "001_baseline.sql" {
		t.Fatalf("schema_migrations = %v, want [001_baseline.sql]", got)
	}
}

// --- the comparison with SQLite schema 018 ---

type colInfo struct {
	name, typ, dflt string
	notNull         bool
}

type fkInfo struct{ column, parent, parentColumn, onDelete string }

type idxInfo struct {
	name            string
	unique, partial bool
	cols, pred      string // normalized by normSQL
}

type tableInfo struct {
	cols     []colInfo
	fks      []fkInfo
	idx      []idxInfo
	checks   int
	inSets   map[string]string // column -> the sorted values of its CHECK (column IN (...)), joined by |
	identity bool
}

// sqliteSchema018 applies the old migration files in name order to an empty in-memory SQLite and describes the result.
func sqliteSchema018(t *testing.T) map[string]*tableInfo {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sqliteMigrationsDir, "*.sql"))
	if err != nil || len(files) != 18 {
		t.Fatalf("the SQLite migration fixture: %d files, %v; want 18", len(files), err)
	}
	sort.Strings(files)
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
	}
	out := map[string]*tableInfo{}
	type tbl struct{ name, sql string }
	var tbls []tbl
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name <> 'sqlite_sequence' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var x tbl
		if err := rows.Scan(&x.name, &x.sql); err != nil {
			t.Fatal(err)
		}
		tbls = append(tbls, x)
	}
	rows.Close()
	for _, x := range tbls {
		ti := &tableInfo{identity: strings.Contains(x.sql, "AUTOINCREMENT"), checks: strings.Count(x.sql, "CHECK ("), inSets: map[string]string{}}
		for _, m := range sqliteInCheck.FindAllStringSubmatch(x.sql, -1) {
			ti.inSets[m[1]] = quotedSet(m[2])
		}
		out[x.name] = ti
		r, err := db.Query(`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?) ORDER BY cid`, x.name)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var c colInfo
			var nn, pk int
			var d sql.NullString
			if err := r.Scan(&c.name, &c.typ, &nn, &d, &pk); err != nil {
				t.Fatal(err)
			}
			c.notNull, c.dflt = nn == 1 || pk > 0, d.String
			ti.cols = append(ti.cols, c)
		}
		r.Close()
		r, err = db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list(?)`, x.name)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var f fkInfo
			if err := r.Scan(&f.column, &f.parent, &f.parentColumn, &f.onDelete); err != nil {
				t.Fatal(err)
			}
			ti.fks = append(ti.fks, f)
		}
		r.Close()
		r, err = db.Query(`SELECT l.name, l."unique", l.partial, m.sql FROM pragma_index_list(?) l
			JOIN sqlite_master m ON m.name = l.name AND m.type = 'index' AND m.sql IS NOT NULL`, x.name)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var i idxInfo
			var u, p int
			var def string
			if err := r.Scan(&i.name, &u, &p, &def); err != nil {
				t.Fatal(err)
			}
			i.unique, i.partial = u == 1, p == 1
			i.cols, i.pred = splitIndexDef(t, def)
			ti.idx = append(ti.idx, i)
		}
		r.Close()
	}
	return out
}

// postgresSchema describes the tables of the current schema the way sqliteSchema018 describes SQLite's.
func postgresSchema(t *testing.T, db *sql.DB) map[string]*tableInfo {
	t.Helper()
	out := map[string]*tableInfo{}
	rows, err := db.Query(`SELECT table_name, column_name, data_type, is_nullable = 'NO', COALESCE(column_default, ''), is_identity = 'YES'
		FROM information_schema.columns WHERE table_schema = current_schema() ORDER BY table_name, ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table string
		var c colInfo
		var identity bool
		if err := rows.Scan(&table, &c.name, &c.typ, &c.notNull, &c.dflt, &identity); err != nil {
			t.Fatal(err)
		}
		if out[table] == nil {
			out[table] = &tableInfo{inSets: map[string]string{}}
		}
		out[table].cols = append(out[table].cols, c)
		out[table].identity = out[table].identity || identity
	}
	rows.Close()
	rows, err = db.Query(`SELECT c.conrelid::regclass::text, a.attname, c.confrelid::regclass::text, pa.attname, c.confdeltype
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		JOIN pg_attribute pa ON pa.attrelid = c.confrelid AND pa.attnum = c.confkey[1]
		WHERE c.contype = 'f' AND c.connamespace = current_schema()::regnamespace AND cardinality(c.conkey) = 1`)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{"a": "NO ACTION", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}
	for rows.Next() {
		var table, action string
		var f fkInfo
		if err := rows.Scan(&table, &f.column, &f.parent, &f.parentColumn, &action); err != nil {
			t.Fatal(err)
		}
		f.onDelete = actions[action]
		out[table].fks = append(out[table].fks, f)
	}
	rows.Close()
	rows, err = db.Query(`SELECT conrelid::regclass::text, pg_get_constraintdef(oid) FROM pg_constraint
		WHERE contype = 'c' AND connamespace = current_schema()::regnamespace`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table, def string
		if err := rows.Scan(&table, &def); err != nil {
			t.Fatal(err)
		}
		out[table].checks++
		if m := pgInCheck.FindStringSubmatch(def); m != nil {
			out[table].inSets[m[1]] = quotedSet(m[2])
		}
	}
	rows.Close()
	// Indexes that are not behind a constraint (primary keys and UNIQUE column constraints are SQLite's autoindexes).
	rows, err = db.Query(`SELECT t.relname, i.relname, x.indisunique, x.indpred IS NOT NULL, pg_get_indexdef(x.indexrelid)
		FROM pg_index x JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_class t ON t.oid = x.indrelid
		WHERE t.relnamespace = current_schema()::regnamespace
		  AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = x.indexrelid)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table, def string
		var i idxInfo
		if err := rows.Scan(&table, &i.name, &i.unique, &i.partial, &def); err != nil {
			t.Fatal(err)
		}
		i.cols, i.pred = splitIndexDef(t, def)
		out[table].idx = append(out[table].idx, i)
	}
	rows.Close()
	return out
}

// nocaseIndexes replace SQLite's UNIQUE COLLATE NOCASE columns (spec decision 4).
var nocaseIndexes = map[string]idxInfo{
	"repos": {name: "repos_full_name_lower", unique: true, cols: "lower full_name"},
	"hosts": {name: "hosts_name_lower", unique: true, cols: "lower name"},
}

var (
	// sqliteInCheck finds CHECK (column IN ('a', 'b')) in a CREATE TABLE of SQLite; pgInCheck the same CHECK as
	// pg_get_constraintdef prints it: CHECK ((column = ANY (ARRAY['a'::text, 'b'::text]))).
	sqliteInCheck = regexp.MustCompile(`CHECK \((\w+) IN \(((?:'[^']*'(?:, )?)*)\)\)`)
	pgInCheck     = regexp.MustCompile(`^CHECK \(\((\w+) = ANY \(ARRAY\[(.*)\]\)\)\)$`)
	quoted        = regexp.MustCompile(`'([^']*)'`)
	casts         = regexp.MustCompile(`::[a-z_]+( precision| varying)?`)
	spaces        = regexp.MustCompile(`\s+`)
)

// quotedSet returns the quoted values in s, sorted and joined by |.
func quotedSet(s string) string {
	var vals []string
	for _, m := range quoted.FindAllStringSubmatch(s, -1) {
		vals = append(vals, m[1])
	}
	sort.Strings(vals)
	return strings.Join(vals, "|")
}

// normSQL makes the text of an index's columns or predicate comparable between SQLite's CREATE INDEX and
// pg_get_indexdef: lower case outside string literals (a predicate on 'Open' and one on 'open' differ), no casts, no
// parentheses, one space between words and none before a comma.
func normSQL(s string) string {
	s = casts.ReplaceAllString(lowerOutsideQuotes(s), "")
	s = strings.NewReplacer("(", " ", ")", " ").Replace(s)
	s = spaces.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.ReplaceAll(s, " ,", ","))
}

// lowerOutsideQuotes lower-cases s except inside single-quoted literals (a doubled quote inside one reads as two adjacent
// literals, which toggling on every quote handles the same way).
func lowerOutsideQuotes(s string) string {
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		if r == '\'' {
			inQuote = !inQuote
		}
		if !inQuote {
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestNormSQLKeepsTheCaseOfLiterals(t *testing.T) {
	for in, want := range map[string]string{
		`WHERE (State = 'Open'::text)`:           `where state = 'Open'`,
		`WHERE state IN ('A', 'b')`:              `where state in 'A', 'b'`,
		`WHERE note = 'it''s Mixed' AND X = 'Y'`: `where note = 'it''s Mixed' and x = 'Y'`,
		`(Lower(Name))`:                          `lower name`,
	} {
		if got := normSQL(in); got != want {
			t.Errorf("normSQL(%q) = %q, want %q", in, got, want)
		}
	}
}

// splitIndexDef returns the normalized column list and predicate of a CREATE INDEX statement of either database:
// the parenthesized group after ON table (and USING method), and what follows WHERE.
func splitIndexDef(t *testing.T, def string) (cols, pred string) {
	t.Helper()
	on := strings.Index(def, " ON ")
	open := strings.IndexByte(def[max(on, 0):], '(')
	if on < 0 || open < 0 {
		t.Fatalf("not an index definition: %s", def)
	}
	open += on
	depth, end := 0, -1
	for i := open; i < len(def) && end < 0; i++ {
		switch def[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		t.Fatalf("unbalanced index definition: %s", def)
	}
	rest := strings.TrimSpace(def[end+1:])
	if w, ok := strings.CutPrefix(rest, "WHERE "); ok {
		pred = normSQL(w)
	} else if rest != "" {
		t.Fatalf("unexpected text after the columns of %s", def)
	}
	return normSQL(def[open+1 : end]), pred
}

// pgType is the PostgreSQL type a SQLite column of schema 018 must have (spec decisions 3 and 5): ids and the columns that
// point at them are bigint, REAL is double precision (SQLite's REAL has eight bytes), BLOB is bytea.
func pgType(sqliteType string, isID bool) string {
	switch sqliteType {
	case "TEXT":
		return "text"
	case "REAL":
		return "double precision"
	case "BLOB":
		return "bytea"
	case "INTEGER":
		if isID {
			return "bigint"
		}
		return "integer"
	}
	return "unknown SQLite type " + sqliteType
}

func TestSchemaMatchesSQLiteSchema018(t *testing.T) {
	_, raw := schemaTest(t)
	old, pg := sqliteSchema018(t), postgresSchema(t, raw)
	delete(pg, "schema_migrations") // the runner's, not a migration's

	var oldNames, pgNames []string
	for n := range old {
		oldNames = append(oldNames, n)
	}
	for n := range pg {
		pgNames = append(pgNames, n)
	}
	sort.Strings(oldNames)
	sort.Strings(pgNames)
	if strings.Join(oldNames, ",") != strings.Join(pgNames, ",") {
		t.Fatalf("tables: SQLite %v, PostgreSQL %v", oldNames, pgNames)
	}
	var want []string
	for _, n := range schemaTables {
		if n != "schema_migrations" {
			want = append(want, n)
		}
	}
	if strings.Join(oldNames, ",") != strings.Join(want, ",") {
		t.Errorf("schemaTables %v does not list the tables of the SQLite files %v", want, oldNames)
	}

	// Every CHECK (column IN (...)) of the old files is found, so the set comparison below cannot pass by finding none on
	// either side: runs 2, github_connections 1, incidents 3, tool_calls 3, hosts 1, host_credentials 1, skills 2.
	inChecks := 0
	for _, n := range oldNames {
		inChecks += len(old[n].inSets)
	}
	if inChecks != 13 {
		t.Errorf("found %d CHECK (... IN ...) constraints in the SQLite files, want 13", inChecks)
	}

	var oldFKs []string
	for _, name := range oldNames {
		o, p := old[name], pg[name]
		t.Run(name, func(t *testing.T) {
			idCols := map[string]bool{}
			for _, f := range o.fks {
				if f.parentColumn == "id" && f.parent != "runs" {
					idCols[f.column] = true
				}
			}
			for _, c := range o.cols {
				if c.name == "id" && c.typ == "INTEGER" {
					idCols[c.name] = true
				}
			}
			if len(o.cols) != len(p.cols) {
				t.Fatalf("columns: SQLite %d, PostgreSQL %d (%v vs %v)", len(o.cols), len(p.cols), o.cols, p.cols)
			}
			for i, oc := range o.cols {
				pc := p.cols[i]
				if pc.name != oc.name {
					t.Errorf("column %d: %s, want %s (the order of the SQLite end state)", i+1, pc.name, oc.name)
					continue
				}
				if wantType := pgType(oc.typ, idCols[oc.name]); pc.typ != wantType {
					t.Errorf("%s: type %s, want %s (SQLite %s)", oc.name, pc.typ, wantType, oc.typ)
				}
				if pc.notNull != oc.notNull {
					t.Errorf("%s: NOT NULL %v, want %v", oc.name, pc.notNull, oc.notNull)
				}
				if got := strings.TrimSuffix(pc.dflt, "::text"); got != oc.dflt {
					t.Errorf("%s: default %q, want %q", oc.name, pc.dflt, oc.dflt)
				}
			}
			if p.identity != o.identity {
				t.Errorf("identity column: %v, want %v (AUTOINCREMENT in SQLite)", p.identity, o.identity)
			}
			if p.checks != o.checks {
				t.Errorf("CHECK constraints: %d, want %d", p.checks, o.checks)
			}
			if fmt.Sprint(p.inSets) != fmt.Sprint(o.inSets) {
				t.Errorf("the values of the CHECK (... IN ...) constraints:\n PostgreSQL %v\n SQLite     %v", p.inSets, o.inSets)
			}
			fkKey := func(f fkInfo) string { return f.column + "->" + f.parent + "." + f.parentColumn + " " + f.onDelete }
			var of, pf []string
			for _, f := range o.fks {
				of = append(of, fkKey(f))
				oldFKs = append(oldFKs, name+"."+f.column+"->"+f.parent+" "+f.onDelete)
			}
			for _, f := range p.fks {
				pf = append(pf, fkKey(f))
			}
			sort.Strings(of)
			sort.Strings(pf)
			if strings.Join(of, "; ") != strings.Join(pf, "; ") {
				t.Errorf("foreign keys:\n PostgreSQL %v\n SQLite     %v", pf, of)
			}
			idxKey := func(i idxInfo) string {
				return fmt.Sprintf("%s unique=%v partial=%v cols=(%s) where=(%s)", i.name, i.unique, i.partial, i.cols, i.pred)
			}
			var oi, pi []string
			for _, i := range o.idx {
				oi = append(oi, idxKey(i))
			}
			if extra, ok := nocaseIndexes[name]; ok {
				oi = append(oi, idxKey(extra))
			}
			for _, i := range p.idx {
				pi = append(pi, idxKey(i))
			}
			sort.Strings(oi)
			sort.Strings(pi)
			if strings.Join(oi, "; ") != strings.Join(pi, "; ") {
				t.Errorf("indexes:\n PostgreSQL %v\n want       %v", pi, oi)
			}
		})
	}

	// The hand-written table of foreign keys, which the deletion test below walks, is the SQLite one.
	var listed []string
	for _, f := range schemaForeignKeys {
		listed = append(listed, f.child+"."+f.column+"->"+f.parent+" "+f.onDelete)
	}
	sort.Strings(oldFKs)
	sort.Strings(listed)
	if strings.Join(oldFKs, "; ") != strings.Join(listed, "; ") {
		t.Errorf("schemaForeignKeys:\n listed %v\n SQLite %v", listed, oldFKs)
	}
	var autoinc []string
	for _, n := range oldNames {
		if old[n].identity {
			autoinc = append(autoinc, n)
		}
	}
	if strings.Join(autoinc, ",") != strings.Join(identityTables, ",") {
		t.Errorf("identityTables %v, the AUTOINCREMENT tables of the SQLite files %v", identityTables, autoinc)
	}
}

// --- rows for the behaviour tests ---

const schemaAt = "2026-10-22T12:00:00.000000000Z"

// graph fills one row into every table, all linked, with fixed ids: connection 1, repo 1, host 1, run r1, incident 1 (of repo 1,
// run r1, host 1; runs.incident_id points back at it), activity 1, tool call 1, note 1, skill 1 (host 1, run r1) and skill 2
// (global, replaces skill 1), and the rows that hang off them.
var graph = []string{
	`INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, checked_at) VALUES (1, '\x00'::bytea, '…abcd', 'me', 'ok', '` + schemaAt + `')`,
	`INSERT INTO repos (id, connection_id, full_name, default_branch, created_at) VALUES (1, 1, 'me/repo', 'main', '` + schemaAt + `')`,
	`INSERT INTO hosts (id, name, address, created_at, updated_at) VALUES (1, 'nas', '10.0.0.2', '` + schemaAt + `', '` + schemaAt + `')`,
	`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES ('r1', 'claude', 'p', 'running', '` + schemaAt + `')`,
	`INSERT INTO incidents (id, source, key, repo_id, ref, check_name, state, conclusion, first_seen, last_seen, run_id, host_id)
		VALUES (1, 'github', 'k', 1, 'pr:1', 'go', 'open', 'failure', '` + schemaAt + `', '` + schemaAt + `', 'r1', 1)`,
	`UPDATE runs SET incident_id = 1 WHERE id = 'r1'`,
	`INSERT INTO run_events (run_id, seq, kind, payload, created_at) VALUES ('r1', 1, 'stdout', '{}', '` + schemaAt + `')`,
	`INSERT INTO activity (id, at, kind, repo_id, incident_id, run_id, summary) VALUES (1, '` + schemaAt + `', 'incident_opened', 1, 1, 'r1', 's')`,
	`INSERT INTO run_tokens (run_id, token_hash, created_at) VALUES ('r1', '\x01'::bytea, '` + schemaAt + `')`,
	`INSERT INTO tool_calls (id, run_id, incident_id, tool_use_id, tool, kind, status, created_at) VALUES (1, 'r1', 1, 'u1', 'incident_get', 'read', 'running', '` + schemaAt + `')`,
	`INSERT INTO incident_notes (id, incident_id, run_id, note, created_at) VALUES (1, 1, 'r1', 'n', '` + schemaAt + `')`,
	`INSERT INTO host_credentials (host_id, kind, ciphertext, hint, updated_at) VALUES (1, 'key', '\x02'::bytea, '…wxyz', '` + schemaAt + `')`,
	`INSERT INTO run_hosts (run_id, host_id) VALUES ('r1', 1)`,
	`INSERT INTO settings (key, value, updated_at) VALUES ('poll_interval', '"1m"', '` + schemaAt + `')`,
	`INSERT INTO skills (id, host_id, name, description, body, origin, state, run_id, created_at, updated_at)
		VALUES (1, 1, 'restart', 'd', 'b', 'proposed', 'active', 'r1', '` + schemaAt + `', '` + schemaAt + `')`,
	`INSERT INTO skills (id, name, description, body, origin, state, replaces_id, created_at, updated_at)
		VALUES (2, 'restart', 'd', 'b', 'written', 'active', 1, '` + schemaAt + `', '` + schemaAt + `')`,
}

func seedGraph(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range graph {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// The ids were given explicitly, as the importer gives them: move each sequence past them, as the importer does.
	for _, table := range identityTables {
		if _, err := db.Exec(`SELECT setval(pg_get_serial_sequence($1, 'id'), (SELECT max(id) FROM `+table+`))`, table); err != nil {
			t.Fatalf("setval %s: %v", table, err)
		}
	}
}

func schemaCount(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// Deleting a parent row cascades or nulls each child exactly as the SQLite foreign keys did.
func TestSchemaForeignKeysDeleteAsBefore(t *testing.T) {
	parents := map[string]string{
		"github_connections": "1", "repos": "1", "hosts": "1", "runs": "r1", "incidents": "1", "skills": "1",
	}
	for parent, id := range parents {
		t.Run(parent, func(t *testing.T) {
			_, db := schemaTest(t)
			seedGraph(t, db)
			type before struct{ linked, total int }
			seen := map[fkRule]before{}
			for _, f := range schemaForeignKeys {
				if f.parent != parent {
					continue
				}
				b := before{
					linked: schemaCount(t, db, `SELECT count(*) FROM `+f.child+` WHERE `+f.column+`::text = $1`, id),
					total:  schemaCount(t, db, `SELECT count(*) FROM `+f.child),
				}
				if b.linked == 0 {
					t.Fatalf("the graph has no %s row that points at %s %s", f.child, parent, id)
				}
				seen[f] = b
			}
			if len(seen) == 0 {
				t.Fatalf("no foreign key points at %s", parent)
			}
			if _, err := db.Exec(`DELETE FROM `+parent+` WHERE id::text = $1`, id); err != nil {
				t.Fatalf("delete %s %s: %v", parent, id, err)
			}
			for f, b := range seen {
				if n := schemaCount(t, db, `SELECT count(*) FROM `+f.child+` WHERE `+f.column+`::text = $1`, id); n != 0 {
					t.Errorf("%s.%s: %d rows still point at the deleted %s", f.child, f.column, n, parent)
				}
				want := b.total
				if f.onDelete == "CASCADE" {
					want -= b.linked
				}
				if f.child == parent {
					want-- // the deleted row itself
				}
				if n := schemaCount(t, db, `SELECT count(*) FROM `+f.child); n != want {
					t.Errorf("%s (%s): %d rows after the delete, want %d", f.child, f.onDelete, n, want)
				}
			}
		})
	}
}

// tryExec runs q and returns the SQLSTATE and constraint of its error ("" when it succeeded). It runs in a transaction that is
// rolled back, so that the cases do not see each other.
func tryExec(t *testing.T, db *sql.DB, q string, args ...any) (code, constraint string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(q, args...); err != nil {
		c, pe := pgCode(err)
		if pe == nil {
			t.Fatalf("%s: %v", q, err)
		}
		return c, pe.ConstraintName
	}
	return "", ""
}

// Each CHECK of the old files refuses a bad row, and the row it allows is accepted.
func TestSchemaChecksRefuseWhatTheSQLiteChecksRefused(t *testing.T) {
	_, db := schemaTest(t)
	seedGraph(t, db)
	run := func(status, role string) string {
		return `INSERT INTO runs (id, provider, prompt, status, role, created_at) VALUES ('r2', 'claude', 'p', '` + status + `', '` + role + `', '` + schemaAt + `')`
	}
	conn := func(status string) string {
		return `INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, checked_at) VALUES (2, '\x00'::bytea, 'h', 'l', '` + status + `', '` + schemaAt + `')`
	}
	incident := func(source, repo, state, severity string) string {
		return `INSERT INTO incidents (source, key, repo_id, state, severity, conclusion, first_seen, last_seen)
			VALUES ('` + source + `', 'k2', ` + repo + `, '` + state + `', '` + severity + `', 'c', '` + schemaAt + `', '` + schemaAt + `')`
	}
	call := func(kind, status, decision string) string {
		return `INSERT INTO tool_calls (run_id, tool_use_id, tool, kind, status, decision, created_at)
			VALUES ('r1', 'u2', 't', '` + kind + `', '` + status + `', '` + decision + `', '` + schemaAt + `')`
	}
	host := func(port, status string) string {
		return `INSERT INTO hosts (name, address, port, status, created_at, updated_at) VALUES ('h2', 'a', ` + port + `, '` + status + `', '` + schemaAt + `', '` + schemaAt + `')`
	}
	cred := func(kind string) string {
		return `INSERT INTO host_credentials (host_id, kind, ciphertext, hint, updated_at) VALUES (1, '` + kind + `', '\x00'::bytea, 'h', '` + schemaAt + `')`
	}
	skill := func(origin, state string) string {
		return `INSERT INTO skills (name, description, body, origin, state, created_at, updated_at)
			VALUES ('s2', 'd', 'b', '` + origin + `', '` + state + `', '` + schemaAt + `', '` + schemaAt + `')`
	}
	// Each allowed value of every CHECK (host_credentials: host 1 already has one, so its good rows go to a fresh host below).
	var good []string
	for _, s := range []string{"queued", "running", "succeeded", "failed"} {
		good = append(good, run(s, "adhoc"))
	}
	for _, r := range []string{"adhoc", "responder", "learner"} {
		good = append(good, run("queued", r))
	}
	for _, s := range []string{"ok", "error", "undecryptable"} {
		good = append(good, conn(s))
	}
	for _, s := range []string{"open", "diagnosing", "diagnosed", "resolved", "ignored"} {
		good = append(good, incident("github", "1", s, "none"))
	}
	for _, src := range []string{"alertmanager", "argocd"} {
		for _, sev := range []string{"critical", "warning", "info", "none"} {
			good = append(good, incident(src, "NULL", "open", sev))
		}
	}
	for _, k := range []string{"read", "mutating"} {
		good = append(good, call(k, "running", ""))
	}
	for _, s := range []string{"running", "waiting", "succeeded", "failed", "denied", "abandoned"} {
		good = append(good, call("mutating", s, "pending"))
	}
	for _, d := range []string{"", "pending", "approved", "denied", "abandoned"} {
		good = append(good, call("mutating", "waiting", d))
	}
	for _, p := range []string{"1", "22", "65535"} {
		good = append(good, host(p, "new"))
	}
	for _, s := range []string{"new", "untrusted", "ok", "error", "key_changed", "undecryptable"} {
		good = append(good, host("22", s))
	}
	for _, o := range []string{"written", "proposed"} {
		for _, s := range []string{"proposed", "active", "off"} {
			good = append(good, skill(o, s))
		}
	}
	for _, q := range good {
		if code, c := tryExec(t, db, q); code != "" {
			t.Errorf("refused (%s, %s): %s", code, c, q)
		}
	}
	if _, err := db.Exec(`DELETE FROM host_credentials`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"key", "password"} {
		if code, c := tryExec(t, db, cred(k)); code != "" {
			t.Errorf("refused (%s, %s): %s", code, c, cred(k))
		}
	}

	bad := map[string]string{
		"runs.status":                    run("paused", "adhoc"),
		"runs.role":                      run("queued", "fixer"),
		"github_connections.status":      conn("broken"),
		"incidents.state":                incident("github", "1", "closed", "none"),
		"incidents.source":               incident("loki", "NULL", "open", "none"),
		"incidents.severity":             incident("alertmanager", "NULL", "open", "urgent"),
		"incidents: GitHub without repo": incident("github", "NULL", "open", "none"),
		"incidents: alert with a repo":   incident("alertmanager", "1", "open", "none"),
		"incidents: Argo CD with a repo": incident("argocd", "1", "open", "none"),
		"tool_calls.kind":                call("write", "running", ""),
		"tool_calls.status":              call("read", "done", ""),
		"tool_calls.decision":            call("mutating", "waiting", "maybe"),
		"hosts.port 0":                   host("0", "new"),
		"hosts.port 65536":               host("65536", "new"),
		"hosts.status":                   host("22", "lost"),
		"host_credentials.kind":          cred("token"),
		"skills.origin":                  skill("robot", "active"),
		"skills.state":                   skill("written", "sleeping"),
	}
	for name, q := range bad {
		if code, _ := tryExec(t, db, q); code != "23514" {
			t.Errorf("%s: SQLSTATE %q, want 23514 (check violation)", name, code)
		}
	}
}

func TestSchemaActiveKeyIsUniqueWhileUnresolved(t *testing.T) {
	_, db := schemaTest(t)
	ins := func(id int, state string) (string, string) {
		return tryExecKeep(t, db, `INSERT INTO incidents (id, source, key, state, conclusion, first_seen, last_seen)
			VALUES ($1, 'alertmanager', 'same', $2, 'firing', $3, $3)`, id, state, schemaAt)
	}
	for i, state := range []string{"resolved", "resolved", "open"} {
		if code, c := ins(i+1, state); code != "" {
			t.Fatalf("row %d (%s): %s %s", i+1, state, code, c)
		}
	}
	for _, state := range []string{"open", "diagnosing", "diagnosed", "ignored"} {
		if code, c := ins(10, state); code != "23505" || c != "incidents_active_key" {
			t.Errorf("a second unresolved %s row: %q %q, want 23505 incidents_active_key", state, code, c)
		}
	}
	// Another source with the same key is another incident.
	if code, c := tryExecKeep(t, db, `INSERT INTO incidents (id, source, key, state, conclusion, first_seen, last_seen)
		VALUES (20, 'argocd', 'same', 'open', 'degraded', $1, $1)`, schemaAt); code != "" {
		t.Errorf("the same key of another source: %s %s", code, c)
	}
	// Resolving the open one frees the key again.
	if _, err := db.Exec(`UPDATE incidents SET state = 'resolved' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	if code, c := ins(11, "open"); code != "" {
		t.Errorf("reopening after the resolve: %s %s", code, c)
	}
}

// tryExecKeep is tryExec without the rollback: a successful statement stays.
func tryExecKeep(t *testing.T, db *sql.DB, q string, args ...any) (code, constraint string) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		c, pe := pgCode(err)
		if pe == nil {
			t.Fatalf("%s: %v", q, err)
		}
		return c, pe.ConstraintName
	}
	return "", ""
}

func TestSchemaNamesAreUniqueIgnoringCase(t *testing.T) {
	_, db := schemaTest(t)
	seedGraph(t, db) // me/repo and nas
	for _, c := range []struct{ q, index string }{
		{`INSERT INTO repos (connection_id, full_name, default_branch, created_at) VALUES (1, 'ME/Repo', 'main', '` + schemaAt + `')`, "repos_full_name_lower"},
		{`INSERT INTO repos (connection_id, full_name, default_branch, created_at) VALUES (1, 'me/repo', 'main', '` + schemaAt + `')`, "repos_full_name_lower"},
		{`INSERT INTO hosts (name, address, created_at, updated_at) VALUES ('NAS', 'a', '` + schemaAt + `', '` + schemaAt + `')`, "hosts_name_lower"},
		{`INSERT INTO hosts (name, address, created_at, updated_at) VALUES ('nas', 'a', '` + schemaAt + `', '` + schemaAt + `')`, "hosts_name_lower"},
	} {
		if code, idx := tryExec(t, db, c.q); code != "23505" || idx != c.index {
			t.Errorf("%s: %q %q, want 23505 %s", c.q, code, idx, c.index)
		}
	}
	for _, q := range []string{
		`INSERT INTO repos (connection_id, full_name, default_branch, created_at) VALUES (1, 'me/repo2', 'main', '` + schemaAt + `')`,
		`INSERT INTO hosts (name, address, created_at, updated_at) VALUES ('nas2', 'a', '` + schemaAt + `', '` + schemaAt + `')`,
	} {
		if code, c := tryExec(t, db, q); code != "" {
			t.Errorf("%s: %s %s", q, code, c)
		}
	}
}

// The other UNIQUE and PRIMARY KEY constraints of the old files: a token hash, a tool call of a run, an event number, a host
// binding, a setting, one credential per host.
func TestSchemaOtherUniquesHold(t *testing.T) {
	_, db := schemaTest(t)
	seedGraph(t, db)
	if _, err := db.Exec(`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES ('r2', 'c', 'p', 'queued', '` + schemaAt + `')`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO run_tokens (run_id, token_hash, created_at) VALUES ('r2', '\x01'::bytea, '` + schemaAt + `')`,
		`INSERT INTO run_tokens (run_id, token_hash, created_at) VALUES ('r1', '\x09'::bytea, '` + schemaAt + `')`,
		`INSERT INTO tool_calls (run_id, tool_use_id, tool, kind, status, created_at) VALUES ('r1', 'u1', 't', 'read', 'running', '` + schemaAt + `')`,
		`INSERT INTO run_events (run_id, seq, kind, payload, created_at) VALUES ('r1', 1, 'stdout', '{}', '` + schemaAt + `')`,
		`INSERT INTO run_hosts (run_id, host_id) VALUES ('r1', 1)`,
		`INSERT INTO settings (key, value, updated_at) VALUES ('poll_interval', '"2m"', '` + schemaAt + `')`,
		`INSERT INTO host_credentials (host_id, kind, ciphertext, hint, updated_at) VALUES (1, 'password', '\x00'::bytea, 'h', '` + schemaAt + `')`,
		`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES ('r1', 'c', 'p', 'queued', '` + schemaAt + `')`,
	} {
		if code, _ := tryExec(t, db, q); code != "23505" {
			t.Errorf("%s: SQLSTATE %q, want 23505", q, code)
		}
	}
	// The same tool use id in another run is another call.
	if code, c := tryExec(t, db, `INSERT INTO tool_calls (run_id, tool_use_id, tool, kind, status, created_at) VALUES ('r2', 'u1', 't', 'read', 'running', '`+schemaAt+`')`); code != "" {
		t.Errorf("the tool use id of another run: %s %s", code, c)
	}
}

// The unique index of skills is per scope and leaves proposals out (as TestTheSkillsTableKeepsNamesUniquePerScopeAndLeavesProposalsOut
// proved for SQLite).
func TestSchemaSkillNamesAreUniquePerScopeAndLeaveProposalsOut(t *testing.T) {
	_, db := schemaTest(t)
	seedGraph(t, db) // skill 1 "restart" of host 1, skill 2 "restart" global, both active
	insert := func(hostID any, name, state string) (string, string) {
		return tryExecKeep(t, db, `INSERT INTO skills (host_id, name, description, body, origin, state, created_at, updated_at)
			VALUES ($1, $2, 'd', 'b', 'written', $3, $4, $4)`, hostID, name, state, schemaAt)
	}
	if code, c := insert(nil, "restart", "off"); code != "23505" || c != "skills_scope_name" {
		t.Errorf("a second global skill of a name: %q %q", code, c)
	}
	if code, c := insert(int64(1), "restart", "active"); code != "23505" || c != "skills_scope_name" {
		t.Errorf("a second skill of a name for the host: %q %q", code, c)
	}
	for i := 0; i < 2; i++ {
		if code, c := insert(nil, "restart", "proposed"); code != "" {
			t.Errorf("proposal %d with the name of an active skill: %s %s", i, code, c)
		}
	}
	if code, c := insert(int64(1), "other", "active"); code != "" {
		t.Errorf("another name: %s %s", code, c)
	}
}

// An identity column takes an explicit id (the importer copies ids) and, once its sequence is set to the highest one, gives the
// next id above it.
func TestSchemaIdentityColumnsTakeExplicitIdsAndContinueAboveThem(t *testing.T) {
	_, db := schemaTest(t)
	seedGraph(t, db)
	// One insert per table without an id; the explicit-id variant inserts id 41 first.
	rows := map[string]string{
		"activity":       `(at, kind, summary) VALUES ('` + schemaAt + `', 'k', 's')`,
		"hosts":          `(name, address, created_at, updated_at) VALUES ('h%d', 'a', '` + schemaAt + `', '` + schemaAt + `')`,
		"incident_notes": `(incident_id, note, created_at) VALUES (1, 'n', '` + schemaAt + `')`,
		"incidents":      `(source, key, state, conclusion, first_seen, last_seen) VALUES ('argocd', 'k%d', 'open', 'c', '` + schemaAt + `', '` + schemaAt + `')`,
		"repos":          `(connection_id, full_name, default_branch, created_at) VALUES (1, 'me/r%d', 'main', '` + schemaAt + `')`,
		"skills":         `(name, description, body, origin, state, created_at, updated_at) VALUES ('s%d', 'd', 'b', 'written', 'active', '` + schemaAt + `', '` + schemaAt + `')`,
		"tool_calls":     `(run_id, tool_use_id, tool, kind, status, created_at) VALUES ('r1', 'u%d', 't', 'read', 'running', '` + schemaAt + `')`,
	}
	if len(rows) != len(identityTables) {
		t.Fatalf("rows lists %d tables, identityTables %d", len(rows), len(identityTables))
	}
	for _, table := range identityTables {
		values := rows[table]
		withID := func(n int) string {
			v := values
			if strings.Contains(v, "%d") {
				v = fmt.Sprintf(v, n)
			}
			cols, rest, _ := strings.Cut(v, ") VALUES (")
			return `INSERT INTO ` + table + ` (id, ` + cols[1:] + `) VALUES (41, ` + rest
		}
		without := func(n int) string {
			v := values
			if strings.Contains(v, "%d") {
				v = fmt.Sprintf(v, n)
			}
			return `INSERT INTO ` + table + ` ` + v + ` RETURNING id`
		}
		if _, err := db.Exec(withID(41)); err != nil {
			t.Fatalf("%s with id 41: %v", table, err)
		}
		if _, err := db.Exec(`SELECT setval(pg_get_serial_sequence($1, 'id'), (SELECT max(id) FROM `+table+`))`, table); err != nil {
			t.Fatalf("%s setval: %v", table, err)
		}
		var id int64
		if err := db.QueryRow(without(42)).Scan(&id); err != nil {
			t.Fatalf("%s without an id: %v", table, err)
		}
		if id != 42 {
			t.Errorf("%s: the next id is %d, want 42", table, id)
		}
	}
}

// --- the runner ---

func TestSchemaMigrateTwiceAppliesNothing(t *testing.T) {
	s, db := schemaTest(t)
	seedGraph(t, db)
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := queryStrings(t, db, `SELECT version FROM schema_migrations ORDER BY version`); strings.Join(got, ",") != "001_baseline.sql" {
		t.Errorf("schema_migrations = %v", got)
	}
	if n := schemaCount(t, db, `SELECT count(*) FROM incidents`); n != 1 {
		t.Errorf("incidents after the second migrate: %d, want 1", n)
	}
}

func TestSchemaOpensAtOnceApplyEachMigrationOnce(t *testing.T) {
	dsn := pgtest.New(t)
	const n = 4
	stores := make([]*Store, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stores[i], errs[i] = Open(context.Background(), secret.NewValue(dsn), WithMaxConns(2))
		}()
	}
	close(start)
	wg.Wait()
	t.Cleanup(func() {
		for _, s := range stores {
			if s != nil {
				_ = s.Close()
			}
		}
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("Open %d: %v", i, err)
		}
	}
	if stores[0] == nil {
		t.FailNow()
	}
	got := queryStrings(t, stores[0].db.db, `SELECT version FROM schema_migrations ORDER BY version`)
	if strings.Join(got, ",") != "001_baseline.sql" {
		t.Errorf("schema_migrations = %v", got)
	}
}

// schemaOf returns the schema the connections of db migrate (the first of their search_path).
func schemaOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

// lockIsFree reports whether another session can take the migration lock of schema at once. The lock is per schema and each
// test has a schema of its own, so no other test takes it. The probe runs on one pinned session, and its unlock must say
// that it released the lock it took.
func lockIsFree(t *testing.T, schema string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgtest.Admin(t).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, hashtext($2))`, migrateLockClass, schema).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		return false
	}
	var released bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, hashtext($2))`, migrateLockClass, schema).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("the probe's unlock released nothing")
	}
	return true
}

// holdLock takes the migration lock of schema on a pinned session of its own and returns the release. The release runs at
// most once, and also when the test ends, so that no failure or panic leaves the lock held or the session locked in a pool.
func holdLock(t *testing.T, schema string) (release func()) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgtest.Admin(t).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, hashtext($2))`, migrateLockClass, schema); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			var released bool
			err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, hashtext($2))`, migrateLockClass, schema).Scan(&released)
			_ = conn.Close()
			if err != nil || !released {
				t.Errorf("releasing the held lock: %v, released %v", err, released)
			}
		})
	}
	t.Cleanup(release)
	return release
}

func TestSchemaFailedMigrationRollsBackAndReleasesTheLock(t *testing.T) {
	s, db := schemaTest(t)
	fsys := fstest.MapFS{
		"migrations/001_baseline.sql": {Data: []byte(`SELECT 1`)}, // recorded already: skipped
		"migrations/002_good.sql":     {Data: []byte(`CREATE TABLE good (id INTEGER)`)},
		"migrations/003_bad.sql":      {Data: []byte(`CREATE TABLE half (id INTEGER); SELECT * FROM no_such_table`)},
		"migrations/004_later.sql":    {Data: []byte(`CREATE TABLE later (id INTEGER)`)},
	}
	err := s.migrateFS(context.Background(), fsys)
	if err == nil || !strings.Contains(err.Error(), "003_bad.sql") {
		t.Fatalf("migrate = %v, want an error naming 003_bad.sql", err)
	}
	if got := queryStrings(t, db, `SELECT version FROM schema_migrations ORDER BY version`); strings.Join(got, ",") != "001_baseline.sql,002_good.sql" {
		t.Errorf("schema_migrations = %v", got)
	}
	for table, want := range map[string]bool{"good": true, "half": false, "later": false} {
		var reg sql.NullString
		if err := db.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		if reg.Valid != want {
			t.Errorf("table %s exists = %v, want %v", table, reg.Valid, want)
		}
	}
	if !lockIsFree(t, schemaOf(t, db)) {
		t.Error("the migration lock is still held after the failed migration")
	}
}

func TestSchemaMigrateWaitsForTheLockAndGivesUpWithTheContext(t *testing.T) {
	s, db := schemaTest(t)
	schema := schemaOf(t, db)
	release := holdLock(t, schema)
	defer release()
	fsys := fstest.MapFS{"migrations/002_x.sql": {Data: []byte(`CREATE TABLE x (id INTEGER)`)}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := s.migrateFS(ctx, fsys)
	release()
	if err == nil {
		t.Fatal("migrate ran while another session held the lock")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "cancel") {
		t.Errorf("migrate = %v, want the context's end", err)
	}
	var reg sql.NullString
	if err := db.QueryRow(`SELECT to_regclass('x')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.Valid {
		t.Error("the migration ran although the lock was not taken")
	}
	if !lockIsFree(t, schema) {
		t.Error("the migration lock is held after migrate gave up")
	}
	// With the lock free, the same store migrates.
	if err := s.migrateFS(context.Background(), fsys); err != nil {
		t.Fatalf("migrate after the lock came free: %v", err)
	}
}

// The lock belongs to one schema: a server (or test) that migrates schema A does not hold up one that migrates schema B of the
// same database, while two on the same schema still wait for each other (TestSchemaOpensAtOnceApplyEachMigrationOnce and the
// test above).
func TestSchemaLocksOfTwoSchemasDoNotWaitForEachOther(t *testing.T) {
	dsnA, dsnB := pgtest.New(t), pgtest.New(t)
	rawA, err := sql.Open("pgx", dsnA)
	if err != nil {
		t.Fatal(err)
	}
	defer rawA.Close()
	release := holdLock(t, schemaOf(t, rawA))
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := Open(ctx, secret.NewValue(dsnB), WithMaxConns(2))
	if err != nil {
		t.Fatalf("Open of schema B while schema A's lock is held: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if got := queryStrings(t, b.db.db, `SELECT version FROM schema_migrations`); strings.Join(got, ",") != "001_baseline.sql" {
		t.Errorf("schema B: schema_migrations = %v", got)
	}

	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	a, err := Open(short, secret.NewValue(dsnA), WithMaxConns(2))
	if err == nil {
		_ = a.Close()
		t.Fatal("Open of schema A migrated while its lock was held")
	}
	// It waited for the lock until its context ended, rather than failing for another reason.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open of schema A = %v, want it to end with its context's deadline", err)
	}
}

// pg_advisory_lock is STRICT: hashtext(NULL) would make it return without a lock. A connection whose search_path names no
// schema that exists has current_schema() NULL, and migrate refuses it before it locks anything.
func TestSchemaMigrateRefusesAConnectionWithoutASchema(t *testing.T) {
	dsn := pgtest.New(t)
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// One connection: the session setting stays on it.
	if _, err := s.db.db.Exec(`SET search_path TO no_such_schema_for_remedy`); err != nil {
		t.Fatal(err)
	}
	err = s.migrateFS(context.Background(), fstest.MapFS{"migrations/002_x.sql": {Data: []byte(`CREATE TABLE x (id INTEGER)`)}})
	if !errors.Is(err, errNoSchema) {
		t.Fatalf("migrate = %v, want errNoSchema", err)
	}
}

// The baseline is PostgreSQL SQL and says nothing SQLite-specific.
func TestSchemaBaselineHasNoSQLiteIsms(t *testing.T) {
	body, err := os.ReadFile("migrations/001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The comments may name what was translated; the statements may not.
	body = regexp.MustCompile(`--[^\n]*`).ReplaceAll(body, nil)
	re := regexp.MustCompile(`(?i)autoincrement|sqlite_sequence|pragma|strftime|char\(31\)|collate\s+nocase|\bblob\b|foreign-keys-off`)
	if m := re.FindAllString(string(body), -1); len(m) > 0 {
		t.Errorf("SQLite-isms in the baseline: %v", m)
	}
	files, err := filepath.Glob("migrations/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f) != "001_baseline.sql" {
			t.Errorf("%s: only PostgreSQL migrations belong in migrations/ (the SQLite ones are the importer's testdata)", f)
		}
	}
}

func queryStrings(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
