package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"testing"
)

type namedText string

func TestCleanArgsCleansTextAndLeavesBytes(t *testing.T) {
	dirty := "a\x00b\xff"
	want := "a�b�"
	ptr := dirty
	var nilPtr *string
	raw := []byte{0, 0xff, 'x'}
	rawJSON := json.RawMessage("{\"a\":\"\xff\"}")
	in := []any{
		dirty, &ptr, nilPtr, sql.NullString{String: dirty, Valid: true}, sql.NullString{String: dirty}, namedText(dirty),
		raw, rawJSON, int64(3), 4, 1.5, nil, true,
	}
	out := cleanArgs(in)

	if out[0] != want {
		t.Errorf("string: %q", out[0])
	}
	if p, ok := out[1].(*string); !ok || *p != want {
		t.Errorf("*string: %#v", out[1])
	}
	if ptr != dirty {
		t.Errorf("the caller's string behind the pointer changed: %q", ptr)
	}
	if p, ok := out[2].(*string); !ok || p != nil {
		t.Errorf("nil *string: %#v", out[2])
	}
	if ns := out[3].(sql.NullString); !ns.Valid || ns.String != want {
		t.Errorf("valid NullString: %#v", ns)
	}
	if ns := out[4].(sql.NullString); ns.Valid || ns.String != dirty {
		t.Errorf("a NULL NullString was changed: %#v", ns)
	}
	if nt, ok := out[5].(namedText); !ok || nt != namedText(want) {
		t.Errorf("named string type: %#v", out[5])
	}
	if b := out[6].([]byte); !bytes.Equal(b, []byte{0, 0xff, 'x'}) {
		t.Errorf("[]byte changed: %x", b)
	}
	if j := out[7].(json.RawMessage); string(j) != "{\"a\":\"\xff\"}" {
		t.Errorf("json.RawMessage changed: %q", j)
	}
	for i, v := range []any{int64(3), 4, 1.5, nil, true} {
		if out[8+i] != v {
			t.Errorf("arg %d changed: %#v", 8+i, out[8+i])
		}
	}
	if in[0] != dirty {
		t.Error("the caller's argument slice was changed")
	}
}

func TestCleanArgsReturnsCleanArgumentsAsTheyAre(t *testing.T) {
	s := "clean ✓"
	args := []any{"a", "Grüße", &s, sql.NullString{String: "x", Valid: true}, namedText("ok"), []byte{0}, int64(1), nil}
	out := cleanArgs(args)
	if &out[0] != &args[0] {
		t.Fatal("clean arguments were copied")
	}
	if n := testing.AllocsPerRun(100, func() { _ = cleanArgs(args) }); n != 0 {
		t.Fatalf("cleanArgs of clean arguments allocates %v times", n)
	}
}
