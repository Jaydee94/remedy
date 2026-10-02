package diagnosis_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/diagnosis"
)

const valid = `{
  "summary": "npm ci fails because the lock file is out of date",
  "cause": "package.json asks for typescript 7.0.2 but package-lock.json still pins 6.0.3.",
  "confidence": "high",
  "category": "dependency_update",
  "affected_files": ["web/package.json", "web/package-lock.json"],
  "proposed_fix": "Run npm install in web/ and commit the updated lock file.",
  "fix_looks_automatable": true
}`

func TestParseAcceptsAValidDiagnosis(t *testing.T) {
	d, err := diagnosis.Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if d.Summary == "" || d.Confidence != "high" || d.Category != "dependency_update" || !d.FixLooksAutomatable ||
		!slices.Equal(d.AffectedFiles, []string{"web/package.json", "web/package-lock.json"}) {
		t.Fatalf("diagnosis = %+v", d)
	}
}

func TestParseAcceptsNoAffectedFiles(t *testing.T) {
	raw := strings.Replace(valid, `["web/package.json", "web/package-lock.json"]`, `[]`, 1)
	if _, err := diagnosis.Parse([]byte(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejectsInvalidAnswers(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(valid, old, new, 1) }
	noBool := replace(",\n  \"fix_looks_automatable\": true", "")
	cases := map[string]string{
		"not JSON":               `not json`,
		"empty":                  ``,
		"null":                   `null`,
		"an array":               `[]`,
		"a string":               `"hello"`,
		"missing summary":        replace(`"summary": "npm ci fails because the lock file is out of date",`, ``),
		"missing the boolean":    noBool,
		"unknown field":          replace(`"confidence": "high",`, `"confidence": "high", "extra": 1,`),
		"bad confidence":         replace(`"high"`, `"certain"`),
		"bad category":           replace(`"dependency_update"`, `"other"`),
		"confidence is a number": replace(`"high"`, `3`),
		"automatable is text":    replace(`true`, `"true"`),
		"summary is a number":    replace(`"npm ci fails because the lock file is out of date"`, `42`),
		"files is not a list":    replace(`["web/package.json", "web/package-lock.json"]`, `"web/package.json"`),
		"a file is not text":     replace(`["web/package.json", "web/package-lock.json"]`, `["web/package.json", 7]`),
		"blank summary":          replace(`"npm ci fails because the lock file is out of date"`, `"   "`),
		"blank cause":            replace(`"package.json asks for typescript 7.0.2 but package-lock.json still pins 6.0.3."`, `""`),
		"blank proposed fix":     replace(`"Run npm install in web/ and commit the updated lock file."`, `""`),
		"summary too long":       replace(`"npm ci fails because the lock file is out of date"`, `"`+strings.Repeat("x", 501)+`"`),
		"cause too long":         replace(`"package.json asks for typescript 7.0.2 but package-lock.json still pins 6.0.3."`, `"`+strings.Repeat("x", 4001)+`"`),
		"too many files":         replace(`["web/package.json", "web/package-lock.json"]`, `[`+strings.TrimSuffix(strings.Repeat(`"a",`, 51), ",")+`]`),
		"empty file name":        replace(`["web/package.json", "web/package-lock.json"]`, `[""]`),
		"file name too long":     replace(`["web/package.json", "web/package-lock.json"]`, `["`+strings.Repeat("a", 301)+`"]`),
	}
	cases["control character in a file name"] = replace(`"web/package.json"`, `"web/pack\u0000age.json"`)
	cases["trailing data"] = valid + ` {}`

	for name, raw := range cases {
		_, err := diagnosis.Parse([]byte(raw))
		if !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestParseCountsRunesNotBytes(t *testing.T) {
	ok := strings.Replace(valid, `"npm ci fails because the lock file is out of date"`, `"`+strings.Repeat("é", 500)+`"`, 1)
	if _, err := diagnosis.Parse([]byte(ok)); err != nil {
		t.Fatalf("500 two-byte characters must fit: %v", err)
	}
	bad := strings.Replace(valid, `"npm ci fails because the lock file is out of date"`, `"`+strings.Repeat("é", 501)+`"`, 1)
	if _, err := diagnosis.Parse([]byte(bad)); !errors.Is(err, diagnosis.ErrInvalid) {
		t.Fatalf("501 characters must not fit: %v", err)
	}
}

func TestJSONIsTheCanonicalForm(t *testing.T) {
	d, _ := diagnosis.Parse([]byte(valid))
	again, err := diagnosis.Parse(d.JSON())
	if err != nil || !reflect.DeepEqual(again, d) {
		t.Fatalf("round trip: %+v, %v", again, err)
	}
	var m map[string]any
	if err := json.Unmarshal(d.JSON(), &m); err != nil || len(m) != 7 {
		t.Fatalf("canonical JSON has %d members: %v", len(m), err)
	}
	empty := diagnosis.Diagnosis{Summary: "s", Cause: "c", Confidence: "low", Category: "unknown", ProposedFix: "f"}
	if !strings.Contains(string(empty.JSON()), `"affected_files":[]`) {
		t.Fatalf("a nil file list must become []: %s", empty.JSON())
	}
}

// The schema that goes to the CLI and the struct that Parse fills must describe the same thing.
func TestSchemaMatchesTheStruct(t *testing.T) {
	var s struct {
		Type                 string   `json:"type"`
		AdditionalProperties bool     `json:"additionalProperties"`
		Required             []string `json:"required"`
		Properties           map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(diagnosis.Schema), &s); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	if s.Type != "object" || s.AdditionalProperties {
		t.Fatalf("schema type = %q, additionalProperties = %v", s.Type, s.AdditionalProperties)
	}

	var tags []string
	typ := reflect.TypeOf(diagnosis.Diagnosis{})
	for i := 0; i < typ.NumField(); i++ {
		tags = append(tags, typ.Field(i).Tag.Get("json"))
	}
	slices.Sort(tags)

	required := slices.Clone(s.Required)
	slices.Sort(required)
	if !slices.Equal(required, tags) {
		t.Errorf("required = %v, struct fields = %v", required, tags)
	}
	var props []string
	for name := range s.Properties {
		props = append(props, name)
	}
	slices.Sort(props)
	if !slices.Equal(props, tags) {
		t.Errorf("properties = %v, struct fields = %v", props, tags)
	}
	if !slices.Equal(s.Properties["confidence"].Enum, diagnosis.Confidences) {
		t.Errorf("confidence enum = %v, want %v", s.Properties["confidence"].Enum, diagnosis.Confidences)
	}
	if !slices.Equal(s.Properties["category"].Enum, diagnosis.Categories) {
		t.Errorf("category enum = %v, want %v", s.Properties["category"].Enum, diagnosis.Categories)
	}
	if strings.ContainsAny(diagnosis.Schema, "\n\t") {
		t.Error("the schema must be a single line: it is one command line argument")
	}
}
