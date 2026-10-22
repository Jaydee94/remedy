// Package pgtext makes a Go string storable in a PostgreSQL text column. PostgreSQL refuses a text value with a NUL byte or
// with bytes that are not valid UTF-8 (SQLSTATE 22021); SQLite stored both. The store's database wrapper (internal/store, db.go)
// cleans every text argument with Clean, and the importer from SQLite (internal/importsqlite) does the same when it is told to
// with --replace-invalid-text, so the two convert in exactly the same way.
package pgtext

import (
	"strings"
	"unicode/utf8"
)

// Replacement is what takes the place of a NUL byte or an invalid UTF-8 sequence: U+FFFD REPLACEMENT CHARACTER.
const Replacement = "�"

// Valid reports whether s can be stored in a PostgreSQL text column as it is: valid UTF-8 without a NUL byte.
func Valid(s string) bool {
	return strings.IndexByte(s, 0) < 0 && utf8.ValidString(s)
}

// Clean returns s with every NUL byte and every invalid UTF-8 sequence replaced by U+FFFD. A run of adjacent invalid bytes is
// one sequence and becomes one U+FFFD (strings.ToValidUTF8); each NUL becomes one. A string that is Valid already is returned
// as it is, without a copy.
func Clean(s string) string {
	if Valid(s) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, Replacement), "\x00", Replacement)
}
