package store_test

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// Tokens that are already sealed in a database depend on this exact value.
func TestConnectionAADIsStable(t *testing.T) {
	if got := store.ConnectionAAD(); got != "github_connection:1" {
		t.Fatalf("ConnectionAAD() = %q; changing it makes every stored token undecryptable", got)
	}
}
