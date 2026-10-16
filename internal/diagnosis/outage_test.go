package diagnosis_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/diagnosis"
)

func goodOutage() map[string]any {
	return map[string]any{
		"summary": "The pod web keeps crashing.", "cause": "It runs out of memory: the limit is 128Mi and the heap grows past it.",
		"confidence": "high", "category": "resource_pressure", "affected": []string{"deployment/demo/web"},
		"proposed_action": "Raise the memory limit of deployment demo/web to 256Mi.", "evidence": "cluster_describe showed OOMKilled and a limit of 128Mi.",
	}
}

func outageJSON(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	m := goodOutage()
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAGoodOutageIsAccepted(t *testing.T) {
	o, err := diagnosis.ParseOutage(outageJSON(t, nil))
	if err != nil || o.Category != "resource_pressure" || len(o.Affected) != 1 || o.ProposedAction == "" {
		t.Fatalf("%+v, %v", o, err)
	}
	if _, err := diagnosis.ParseOutage(outageJSON(t, func(m map[string]any) { m["proposed_action"] = "none"; m["affected"] = []string{} })); err != nil {
		t.Errorf("proposed_action none with no affected objects: %v", err)
	}
}

func TestEveryMemberIsRequiredAndNoOtherIsAllowed(t *testing.T) {
	for _, name := range []string{"summary", "cause", "confidence", "category", "affected", "proposed_action", "evidence"} {
		raw := outageJSON(t, func(m map[string]any) { delete(m, name) })
		if _, err := diagnosis.ParseOutage(raw); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	raw := outageJSON(t, func(m map[string]any) { m["extra"] = "x" })
	if _, err := diagnosis.ParseOutage(raw); !errors.Is(err, diagnosis.ErrInvalid) {
		t.Errorf("an extra member: %v", err)
	}
}

func TestValuesOutsideTheRulesAreRefused(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	many := make([]string, 31)
	for i := range many {
		many[i] = "a"
	}
	cases := map[string]func(map[string]any){
		"bad confidence":       func(m map[string]any) { m["confidence"] = "certain" },
		"bad category":         func(m map[string]any) { m["category"] = "dependency_update" }, // GitHub's, not an outage's
		"empty summary":        func(m map[string]any) { m["summary"] = "  " },
		"summary too long":     func(m map[string]any) { m["summary"] = long(501) },
		"cause too long":       func(m map[string]any) { m["cause"] = long(4001) },
		"evidence too long":    func(m map[string]any) { m["evidence"] = long(4001) },
		"empty evidence":       func(m map[string]any) { m["evidence"] = "" },
		"empty action":         func(m map[string]any) { m["proposed_action"] = "" },
		"action too long":      func(m map[string]any) { m["proposed_action"] = long(2001) },
		"too many affected":    func(m map[string]any) { m["affected"] = many },
		"an empty affected":    func(m map[string]any) { m["affected"] = []string{""} },
		"a long affected":      func(m map[string]any) { m["affected"] = []string{long(201)} },
		"a control character":  func(m map[string]any) { m["affected"] = []string{"a\nb"} },
		"affected is a string": func(m map[string]any) { m["affected"] = "deployment/demo/web" },
		"summary is a number":  func(m map[string]any) { m["summary"] = 5 },
	}
	for name, mutate := range cases {
		if _, err := diagnosis.ParseOutage(outageJSON(t, mutate)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	for _, raw := range []string{``, `null`, `[]`, `"x"`, `{`} {
		if _, err := diagnosis.ParseOutage([]byte(raw)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%q: error = %v", raw, err)
		}
	}
}

func TestTheLimitsAreInclusive(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	max := make([]string, 30)
	for i := range max {
		max[i] = long(200)
	}
	raw := outageJSON(t, func(m map[string]any) {
		m["summary"], m["cause"], m["evidence"], m["proposed_action"], m["affected"] = long(500), long(4000), long(4000), long(2000), max
	})
	if _, err := diagnosis.ParseOutage(raw); err != nil {
		t.Fatalf("the exact limits: %v", err)
	}
}

func TestTheTwoShapesAreNotInterchangeable(t *testing.T) {
	github := []byte(`{"summary":"s","cause":"c","confidence":"high","category":"unknown","affected_files":[],"proposed_fix":"f","fix_looks_automatable":false}`)
	if _, err := diagnosis.ParseOutage(github); !errors.Is(err, diagnosis.ErrInvalid) {
		t.Errorf("a GitHub answer parsed as an outage: %v", err)
	}
	if _, err := diagnosis.Parse(outageJSON(t, nil)); !errors.Is(err, diagnosis.ErrInvalid) {
		t.Errorf("an outage answer parsed as a GitHub diagnosis: %v", err)
	}
}

func TestTheStoredFormHasAnEmptyArrayAndTheSevenMembers(t *testing.T) {
	o, _ := diagnosis.ParseOutage(outageJSON(t, func(m map[string]any) { m["affected"] = []string{} }))
	o.Affected = nil
	var members map[string]json.RawMessage
	if err := json.Unmarshal(o.JSON(), &members); err != nil || len(members) != 7 || string(members["affected"]) != "[]" {
		t.Fatalf("%s, %v", o.JSON(), err)
	}
}

func TestTheSchemaIsValidJSONWithTheSameMembersAndCategories(t *testing.T) {
	var s struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(diagnosis.OutageSchema), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Required) != 7 || len(s.Properties) != 7 {
		t.Fatalf("schema members: %v / %d", s.Required, len(s.Properties))
	}
	if got := strings.Join(s.Properties["category"].Enum, ","); got != strings.Join(diagnosis.OutageCategories, ",") {
		t.Errorf("the schema's categories %q differ from OutageCategories", got)
	}
}
