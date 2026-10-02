// Package diagnosis defines what the responder agent must answer and validates the answer. The control
// plane never trusts the CLI to have checked it.
package diagnosis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Schema is passed to `claude --json-schema`. It is deliberately minimal: types, enums, required
// fields and no extra fields. The length limits are enforced by Parse only.
const Schema = `{"type":"object","additionalProperties":false,` +
	`"required":["summary","cause","confidence","category","affected_files","proposed_fix","fix_looks_automatable"],` +
	`"properties":{` +
	`"summary":{"type":"string","description":"One sentence: what failed."},` +
	`"cause":{"type":"string","description":"Why it failed, with the evidence from the log or the code."},` +
	`"confidence":{"type":"string","enum":["high","medium","low"]},` +
	`"category":{"type":"string","enum":["dependency_update","test_failure","build_error","configuration","infrastructure_or_flaky","unknown"]},` +
	`"affected_files":{"type":"array","items":{"type":"string"},"description":"Paths relative to the repository root."},` +
	`"proposed_fix":{"type":"string","description":"Concrete steps that would fix it."},` +
	`"fix_looks_automatable":{"type":"boolean","description":"True only for a small, mechanical change to repository files."}}}`

var (
	Confidences = []string{"high", "medium", "low"}
	Categories  = []string{"dependency_update", "test_failure", "build_error", "configuration", "infrastructure_or_flaky", "unknown"}
)

const (
	maxSummary = 500
	maxCause   = 4000
	maxFix     = 4000
	maxFiles   = 50
	maxPath    = 300
)

// ErrInvalid wraps every validation error.
var ErrInvalid = errors.New("invalid diagnosis")

type Diagnosis struct {
	Summary             string   `json:"summary"`
	Cause               string   `json:"cause"`
	Confidence          string   `json:"confidence"`
	Category            string   `json:"category"`
	AffectedFiles       []string `json:"affected_files"`
	ProposedFix         string   `json:"proposed_fix"`
	FixLooksAutomatable bool     `json:"fix_looks_automatable"`
}

var required = []string{"summary", "cause", "confidence", "category", "affected_files", "proposed_fix", "fix_looks_automatable"}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Parse validates raw strictly: a JSON object with exactly the schema's members, the right types, known
// enum values and sane lengths.
func Parse(raw []byte) (Diagnosis, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return Diagnosis{}, invalid("not a JSON object: %v", err)
	}
	for _, name := range required {
		if _, ok := members[name]; !ok {
			return Diagnosis{}, invalid("missing member %q", name)
		}
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Diagnosis
	if err := dec.Decode(&d); err != nil {
		return Diagnosis{}, invalid("%v", err)
	}

	if err := checkText("summary", d.Summary, maxSummary); err != nil {
		return Diagnosis{}, err
	}
	if err := checkText("cause", d.Cause, maxCause); err != nil {
		return Diagnosis{}, err
	}
	if err := checkText("proposed_fix", d.ProposedFix, maxFix); err != nil {
		return Diagnosis{}, err
	}
	if !slices.Contains(Confidences, d.Confidence) {
		return Diagnosis{}, invalid("confidence %q is not one of %v", d.Confidence, Confidences)
	}
	if !slices.Contains(Categories, d.Category) {
		return Diagnosis{}, invalid("category %q is not one of %v", d.Category, Categories)
	}
	if len(d.AffectedFiles) > maxFiles {
		return Diagnosis{}, invalid("%d affected files, at most %d", len(d.AffectedFiles), maxFiles)
	}
	for _, f := range d.AffectedFiles {
		if f == "" || utf8.RuneCountInString(f) > maxPath || hasControl(f) {
			return Diagnosis{}, invalid("an affected file name is empty, longer than %d characters or contains a control character", maxPath)
		}
	}
	return d, nil
}

// checkText accepts non-blank text of at most max characters. Newlines and tabs are fine.
func checkText(name, s string, max int) error {
	if strings.TrimSpace(s) == "" {
		return invalid("%s is empty", name)
	}
	if n := utf8.RuneCountInString(s); n > max {
		return invalid("%s has %d characters, at most %d", name, n, max)
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// JSON is the canonical form that is stored: exactly the seven members, and [] instead of null.
func (d Diagnosis) JSON() json.RawMessage {
	if d.AffectedFiles == nil {
		d.AffectedFiles = []string{}
	}
	b, _ := json.Marshal(d) // a struct of strings, a slice and a bool cannot fail to marshal
	return b
}
