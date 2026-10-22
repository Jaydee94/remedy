package testutil

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

// A plaintext with a double quote or a backslash must be found in a TEXT and in a bytea column: the text form of a whole
// row doubles both, so a dump of row texts missed it. Each column kind gets a schema of its own, so that a find in one
// cannot stand in for a miss in the other.
func TestDumpSchemaFindsQuotesAndBackslashes(t *testing.T) {
	needles := []string{`pass"word`, `back\slash`, `plain-needle`}
	for _, kind := range []string{"TEXT", "BYTEA"} {
		t.Run(kind, func(t *testing.T) {
			dsn := pgtest.New(t)
			SQL(t, dsn, `CREATE TABLE probe (id INTEGER PRIMARY KEY, v `+kind+`)`)
			for i, n := range needles {
				var v any = "x" + n + "y"
				if kind == "BYTEA" {
					v = []byte("x" + n + "y")
				}
				SQL(t, dsn, `INSERT INTO probe (id, v) VALUES (?, ?)`, i, v)
			}
			dump := DumpSchema(t, dsn)
			for _, n := range needles {
				if !DumpContains(dump, n) {
					t.Errorf("the dump misses %q:\n%s", n, dump)
				}
			}
			if DumpContains(dump, "absent-needle") {
				t.Error("the dump holds a string nobody wrote")
			}
		})
	}
}

// Each column is checked on its own: a needle must not be found across the border of two cells.
func TestDumpSchemaDoesNotJoinCells(t *testing.T) {
	dsn := pgtest.New(t)
	SQL(t, dsn, `CREATE TABLE probe (a TEXT, b TEXT)`)
	SQL(t, dsn, `INSERT INTO probe (a, b) VALUES (?, ?)`, "sec", "ret")
	if DumpContains(DumpSchema(t, dsn), "secret") {
		t.Error("the dump joined two cells into one string")
	}
}
