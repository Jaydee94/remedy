// Package loginrelay holds the rules of the runner login from the web UI: the
// holder for the sign-in address and the code, their validators, the
// vocabulary of states, reasons and refusals, and the strict wire decoders.
// It is stdlib only and holds no state.
package loginrelay

import (
	"fmt"
	"log/slog"
)

const stars = "***"

// Material holds a sign-in address or a code. It answers "***" to every fmt
// verb, to JSON and to slog, so a careless log line or an encoder cannot leak
// it. Only Bytes returns the content. Copies share one backing array, so Zero
// clears every copy.
//
// The content sits behind a func value on purpose: fmt cannot call methods on
// an unexported struct field and prints it by reflection, which would show a
// byte slice (or a pointer to it) under every verb. A func value prints as an
// address, never as its closure's content.
//
// Go cannot promise that no other copy exists (the HTTP stack, encoding/json
// and strings are outside its control): Zero shortens the life of the
// long-lived ones.
type Material struct{ get func() []byte }

// NewMaterial copies s into a new holder.
func NewMaterial(s string) Material {
	b := []byte(s)
	return Material{get: func() []byte { return b }}
}

// Bytes returns the content itself, not a copy.
func (m Material) Bytes() []byte {
	if m.get == nil {
		return nil
	}
	return m.get()
}

// Len is the length of the content in bytes.
func (m Material) Len() int { return len(m.Bytes()) }

// Zero overwrites the shared backing array with zero bytes.
func (m Material) Zero() { clear(m.Bytes()) }

func (Material) String() string   { return stars }
func (Material) GoString() string { return stars }

// MarshalJSON always writes the string "***".
func (Material) MarshalJSON() ([]byte, error) { return []byte(`"` + stars + `"`), nil }

// LogValue keeps the content out of slog.
func (Material) LogValue() slog.Value { return slog.StringValue(stars) }

// Format writes the stars for every verb: without it %d and %x would print
// the byte slice.
func (Material) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(stars)) }
