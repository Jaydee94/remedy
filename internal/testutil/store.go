package testutil

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

// TestMaxConns is the pool of a test's Store: small, because many tests and packages share one server.
const TestMaxConns = 4

// Store opens a Store on a fresh Postgres schema of its own, dropped when the test ends. The Store is closed before the
// schema is dropped (its cleanup is registered after the schema's).
func Store(t testing.TB) *store.Store {
	t.Helper()
	s, _ := StoreDSN(t)
	return s
}

// StoreDSN is Store with the schema's DSN, for a test that must change a row the store has no method for, read the
// database past the store (RawDB) or open the same database a second time (StoreAt).
func StoreDSN(t testing.TB) (*store.Store, string) {
	t.Helper()
	dsn := pgtest.New(t)
	return StoreAt(t, dsn), dsn
}

// StoreAt opens a Store on the schema of dsn, which pgtest.New made. Called twice on one DSN it gives two Stores on the
// same data, which is how a test proves that something survives a restart. Its cleanup closes the Store.
func StoreAt(t testing.TB, dsn string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), secret.NewValue(dsn), store.WithMaxConns(TestMaxConns))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// RawDB is a connection to the schema of dsn past the store, closed when the test ends. It is for tests that seed or read
// a row the store has no method for. The pgx driver is registered by pgtest.
func RawDB(t testing.TB, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Dollars rewrites the ? placeholders of a test's statement to $1, $2, ... (the statements of tests have no ? in a
// literal).
func Dollars(stmt string) string {
	var b strings.Builder
	n := 0
	for _, c := range stmt {
		if c == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// SQL runs one statement on the schema of dsn through a connection of its own, which is closed again. The statement may
// use ? or $n placeholders.
func SQL(t testing.TB, dsn, stmt string, args ...any) {
	t.Helper()
	db := RawDB(t, dsn)
	defer db.Close()
	if _, err := db.Exec(Dollars(stmt), args...); err != nil {
		t.Fatal(err)
	}
}

// TSLayout is the layout of the store's TEXT timestamps: fixed width, so that string order is time order.
const TSLayout = "2006-01-02T15:04:05.000000000Z"

// TS formats a time the way the store writes timestamps.
func TS(tm time.Time) string { return tm.UTC().Format(TSLayout) }

// DumpSchema returns every cell of every row of every table of the schema of dsn as text, one cell per line ("table.column:
// value"), for a test that proves a secret is nowhere in the database (the Postgres counterpart of reading the SQLite
// file). Each cell is its raw value, scanned the way the driver hands it over: TEXT as it was written (a double quote or a
// backslash stays one character, which the text form of a whole row would double), bytea as its bytes, other types in
// their Go text form. Look for a string with DumpContains.
func DumpSchema(t testing.TB, dsn string) string {
	t.Helper()
	db := RawDB(t, dsn)
	rows, err := db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	var b strings.Builder
	for _, tbl := range tables {
		// The name comes from information_schema and is quoted, so it cannot be anything but a table of the schema.
		r, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(tbl, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := r.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range vals {
				var cell string
				switch x := v.(type) {
				case nil:
					continue
				case []byte:
					cell = string(x)
				case string:
					cell = x
				default:
					cell = fmt.Sprint(x)
				}
				b.WriteString(tbl + "." + cols[i] + ": " + cell + "\n")
			}
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
	}
	return b.String()
}

// DumpContains reports whether the dump of DumpSchema holds needle, in a TEXT or a bytea cell (or as the hex of one, for a
// cell that holds an encoding of the bytes).
func DumpContains(dump, needle string) bool {
	return strings.Contains(dump, needle) || strings.Contains(strings.ToLower(dump), hex.EncodeToString([]byte(needle)))
}
