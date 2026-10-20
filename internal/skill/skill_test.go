package skill_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/skill"
)

func good() skill.Fields {
	return skill.Fields{Name: "paperless", Description: "Paperless keeps its data in /volume1/docker.", Body: "The consume folder is /volume1/docker/paperless/consume.\n\n- restart is safe"}
}

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var fe *skill.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want a *skill.FieldError", err)
	}
	return fe.Field
}

func TestAGoodSkillIsAccepted(t *testing.T) {
	got, err := good().Validate()
	if err != nil || got != good() {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestTheNameRule(t *testing.T) {
	for _, ok := range []string{"a", "0", "paperless", "node-exporter", "a1-b2-c3", strings.Repeat("a", 40)} {
		if !skill.ValidName(ok) {
			t.Errorf("%q should be a valid name", ok)
		}
	}
	if skill.ValidName("a\n") {
		t.Error("a name with a trailing line break must not be valid as it is")
	}
	for _, bad := range []string{"", "-a", "A", "Paperless", "a b", "a_b", "a.b", "a/b", "\u00e9", strings.Repeat("a", 41), "(global)"} {
		if skill.ValidName(bad) {
			t.Errorf("%q should not be a valid name", bad)
		}
		f := good()
		f.Name = bad
		if _, err := f.Validate(); err == nil || fieldOf(t, err) != "name" {
			t.Errorf("Validate with the name %q = %v", bad, err)
		}
	}
	f := good()
	f.Name = "  paperless  "
	if got, err := f.Validate(); err != nil || got.Name != "paperless" {
		t.Errorf("a name with spaces around it: %+v, %v", got, err)
	}
}

func TestTheDescriptionRules(t *testing.T) {
	f := good()
	f.Description = strings.Repeat("\u00e9", 200) // 200 characters, 400 bytes
	if _, err := f.Validate(); err != nil {
		t.Errorf("200 characters: %v", err)
	}
	for name, desc := range map[string]string{
		"empty":                 "",
		"blank":                 "   ",
		"201 characters":        strings.Repeat("x", 201),
		"a line break":          "one\ntwo",
		"a tab":                 "one\ttwo",
		"a control character":   "one\x00two",
		"a right-to-left mark":  "safe \u202e fed",
		"a zero-width space":    "a\u200bb",
		"a byte order mark":     "\ufeffa",
		"invalid UTF-8":         "a\xffb",
		"a next line character": "a\u0085b",
		"a line separator":      "a\u2028b",
	} {
		f := good()
		f.Description = desc
		if _, err := f.Validate(); err == nil || fieldOf(t, err) != "description" {
			t.Errorf("%s: Validate = %v", name, err)
		}
	}
}

func TestTheBodyRules(t *testing.T) {
	f := good()
	f.Body = strings.Repeat("x", 8192)
	if _, err := f.Validate(); err != nil {
		t.Errorf("8192 bytes: %v", err)
	}
	f.Body = strings.Repeat("x", 8193)
	if _, err := f.Validate(); err == nil || fieldOf(t, err) != "body" {
		t.Errorf("8193 bytes: %v", err)
	}
	for name, body := range map[string]string{
		"empty":                  "",
		"blank":                  " \n\t ",
		"a control character":    "a\x00b",
		"an escape":              "a\x1b[31mb",
		"a lone carriage return": "a\rb",
		"a bidi override":        "a\u202eb",
		"invalid UTF-8":          "a\xffb",
	} {
		f := good()
		f.Body = body
		if _, err := f.Validate(); err == nil || fieldOf(t, err) != "body" {
			t.Errorf("%s: Validate = %v", name, err)
		}
	}
}

func TestTheBodyKeepsItsLayoutAndMarkupAsText(t *testing.T) {
	f := good()
	f.Body = "\n\n# A heading\r\n\r\n```sh\r\n\tdocker ps\r\n```\r\n<script>alert(1)</script>\r\n"
	got, err := f.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if want := "# A heading\n\n```sh\n\tdocker ps\n```\n<script>alert(1)</script>"; got.Body != want {
		t.Errorf("body = %q, want %q", got.Body, want)
	}
}

func TestValidateIsIdempotent(t *testing.T) {
	once, err := good().Validate()
	if err != nil {
		t.Fatal(err)
	}
	twice, err := once.Validate()
	if err != nil || twice != once {
		t.Fatalf("%+v, %v", twice, err)
	}
}

func TestRedactedRemovesSecretsFromTheDescriptionAndTheBodyButNotTheName(t *testing.T) {
	f := skill.Fields{Name: "db", Description: "login: password=hunter2hunter2", Body: "export API_TOKEN=abcdef123456\nAKIAIOSFODNN7EXAMPLE"}
	got := skill.Redacted(f)
	for _, s := range []string{got.Description, got.Body} {
		if strings.Contains(s, "hunter2hunter2") || strings.Contains(s, "abcdef123456") || strings.Contains(s, "AKIAIOSFODNN7EXAMPLE") {
			t.Errorf("a secret survived: %q", s)
		}
	}
	if got.Name != "db" {
		t.Errorf("name = %q", got.Name)
	}
	if _, err := got.Validate(); err != nil {
		t.Errorf("a redacted skill is still valid: %v", err)
	}
}

func TestInvisibleCharactersAreRefusedInTheDescriptionAndTheBody(t *testing.T) {
	// Code points as numbers, so that no invisible character sits in this file.
	for _, c := range []struct {
		name string
		r    rune
	}{
		{"a tag character", 0xE0041},
		{"the last tag character", 0xE007F},
		{"an arabic letter mark", 0x061C},
		{"a soft hyphen", 0x00AD},
		{"an inhibit swap", 0x206A},
		{"a nominal digit shape", 0x206F},
		{"a private use character", 0xE000},
		{"a supplementary private use character", 0xF0000},
		{"a variation selector", 0xFE0F},
		{"a supplementary variation selector", 0xE0100},
		{"a word joiner", 0x2060},
		{"a left-to-right isolate", 0x2066},
		{"a pop directional isolate", 0x2069},
		{"a paragraph separator", 0x2029},
		{"a line separator", 0x2028},
		{"a zero-width joiner", 0x200D},
		{"a mongolian vowel separator", 0x180E},
		{"an interlinear annotation anchor", 0xFFF9},
		{"a combining grapheme joiner", 0x034F},
		{"a byte order mark", 0xFEFF},
		{"a zero-width space", 0x200B},
		{"a right-to-left override", 0x202E},
	} {
		text := "a" + string(c.r) + "b"
		d := good()
		d.Description = text
		if _, err := d.Validate(); err == nil || fieldOf(t, err) != "description" {
			t.Errorf("%s in the description: Validate = %v", c.name, err)
		}
		b := good()
		b.Body = text
		if _, err := b.Validate(); err == nil || fieldOf(t, err) != "body" {
			t.Errorf("%s in the body: Validate = %v", c.name, err)
		}
	}
}

func TestTheBodyLimitCountsBytes(t *testing.T) {
	f := good()
	f.Body = strings.Repeat("\u00e9", 4096) // 8192 bytes
	if _, err := f.Validate(); err != nil {
		t.Errorf("8192 bytes in 4096 characters: %v", err)
	}
	f.Body = strings.Repeat("\u00e9", 4097) // 8194 bytes
	if _, err := f.Validate(); err == nil || fieldOf(t, err) != "body" {
		t.Errorf("8194 bytes in 4097 characters: %v", err)
	}
}

func TestEvidenceRoundTripsAndNeverIsNil(t *testing.T) {
	in := []skill.Evidence{{Argv: []string{"docker", "ps", "-a"}, Ran: true}, {Argv: []string{"uname", "-a"}, Ran: false}}
	out := skill.DecodeEvidence(skill.EncodeEvidence(in))
	if len(out) != 2 || out[0].Argv[2] != "-a" || !out[0].Ran || out[1].Ran {
		t.Fatalf("round trip = %+v", out)
	}
	if got := skill.EncodeEvidence(nil); got != "[]" {
		t.Errorf("EncodeEvidence(nil) = %q", got)
	}
	for _, text := range []string{"", "[]", "null", "{", "not json"} {
		if got := skill.DecodeEvidence(text); got == nil || len(got) != 0 {
			t.Errorf("DecodeEvidence(%q) = %#v, want an empty non-nil list", text, got)
		}
	}
}
