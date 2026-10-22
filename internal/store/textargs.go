package store

import (
	"database/sql"
	"reflect"

	"github.com/Jaydee94/remedy/internal/pgtext"
)

// cleanArgs returns the arguments of a statement with each text argument it covers made storable in a PostgreSQL text column
// (pgtext.Clean: each NUL byte and each invalid UTF-8 sequence becomes U+FFFD). See the db interface for why.
//
// What is covered is what the driver sends as one text value: a string, a *string, a valid sql.NullString and a value of any
// other type whose kind is string (HostStatus and the like; pgx encodes those by their kind). Everything else is passed on as
// it is: above all []byte and every other byte slice (json.RawMessage among them), because sealed values and hashes reach
// their BYTEA columns as []byte and a byte slice is never altered; and arrays and every other slice type, such as the
// []string of an "= ANY(?)" (incidents.go, flushActivity), whose elements are NOT cleaned. A future caller that passes a
// slice of text must clean its elements itself. The store passes JSON as string(...), so it is cleaned as text; a
// JSON text that is valid stays valid, since only bytes inside its strings can be invalid UTF-8 and a raw NUL is not JSON.
//
// When every argument is clean already, args itself is returned: no copy, no allocation. Otherwise the result is a new
// slice; the caller's slice and the strings behind its pointers are never changed.
func cleanArgs(args []any) []any {
	for i, a := range args {
		c, changed := cleanArg(a)
		if !changed {
			continue
		}
		out := make([]any, len(args))
		copy(out, args)
		out[i] = c
		for j := i + 1; j < len(args); j++ {
			if c, changed := cleanArg(args[j]); changed {
				out[j] = c
			}
		}
		return out
	}
	return args
}

// cleanArg cleans one argument and reports whether it had to change.
func cleanArg(a any) (any, bool) {
	switch v := a.(type) {
	case nil, []byte, int64, int, bool, float64:
		return a, false
	case string:
		if pgtext.Valid(v) {
			return a, false
		}
		return pgtext.Clean(v), true
	case *string:
		if v == nil || pgtext.Valid(*v) {
			return a, false
		}
		c := pgtext.Clean(*v)
		return &c, true
	case sql.NullString:
		if !v.Valid || pgtext.Valid(v.String) {
			return a, false
		}
		v.String = pgtext.Clean(v.String)
		return v, true
	}
	rv := reflect.ValueOf(a)
	if rv.Kind() != reflect.String {
		return a, false
	}
	s := rv.String()
	if pgtext.Valid(s) {
		return a, false
	}
	return reflect.ValueOf(pgtext.Clean(s)).Convert(rv.Type()).Interface(), true
}
