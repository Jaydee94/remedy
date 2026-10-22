package pgtext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanReplacesNULAndInvalidUTF8(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "hello", "hello"},
		{"multi-byte", "Grüße ✓ 日本 😀", "Grüße ✓ 日本 😀"},
		{"nul", "a\x00b", "a�b"},
		{"two nuls", "\x00\x00", "��"},
		{"invalid byte", "a\xffb", "a�b"},
		{"a run of invalid bytes is one replacement", "a\xff\xfeb", "a�b"},
		{"cut multi-byte", "a\xe2\x9cb", "a�b"},
		{"surrogate half", "a\xed\xa0\x80b", "a�b"},
		{"nul and invalid", "\x00\xff", "��"},
		{"a replacement character stays", "a�b", "a�b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Clean(c.in)
			if got != c.want {
				t.Fatalf("Clean(%q) = %q, want %q", c.in, got, c.want)
			}
			if !Valid(got) {
				t.Fatalf("Clean(%q) = %q is not valid", c.in, got)
			}
			if Valid(c.in) != (c.in == c.want) {
				t.Fatalf("Valid(%q) = %v", c.in, Valid(c.in))
			}
		})
	}
}

func TestCleanOfAVeryLongText(t *testing.T) {
	clean := strings.Repeat("ab✓", 1<<18)
	if got := Clean(clean); got != clean {
		t.Fatal("a long clean text changed")
	}
	dirty := clean + "\x00" + clean + "\xff"
	got := Clean(dirty)
	if want := clean + "�" + clean + "�"; got != want {
		t.Fatalf("long text: len %d, want %d", len(got), len(want))
	}
	if !utf8.ValidString(got) || strings.IndexByte(got, 0) >= 0 {
		t.Fatal("the cleaned long text is not valid")
	}
}

// A text that is clean already is returned as it is: no copy.
func TestCleanDoesNotAllocateForACleanText(t *testing.T) {
	s := strings.Repeat("clean text ✓ ", 100)
	if n := testing.AllocsPerRun(100, func() { _ = Clean(s) }); n != 0 {
		t.Fatalf("Clean of a clean text allocates %v times", n)
	}
}
