package run_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestNewIDIs32HexCharsAndUnique(t *testing.T) {
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := run.NewID()
		if !hex32.MatchString(id) {
			t.Fatalf("NewID() = %q, want 32 lowercase hex chars", id)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = true
	}
}

func TestJSONStringEscapesIntoValidJSON(t *testing.T) {
	in := "line1\n\"quoted\" \\ back\x00nul"
	raw := run.JSONString(in)

	var back string
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("JSONString produced invalid JSON %s: %v", raw, err)
	}
	if back != in {
		t.Fatalf("round trip = %q, want %q", back, in)
	}
}

func TestStatusTerminal(t *testing.T) {
	cases := map[run.Status]bool{
		run.Queued: false, run.Running: false, run.Succeeded: true, run.Failed: true,
	}
	for status, want := range cases {
		if got := status.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", status, got, want)
		}
	}
}
