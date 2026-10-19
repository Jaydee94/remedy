package diagnosis

import (
	"bytes"
	"encoding/json"
	"slices"
	"unicode/utf8"
)

// OutageSchema is passed to `claude --json-schema` for the diagnosis of an alert. Like Schema it is deliberately minimal; the length limits
// are enforced by ParseOutage only.
const OutageSchema = `{"type":"object","additionalProperties":false,` +
	`"required":["summary","cause","confidence","category","affected","proposed_action","evidence"],` +
	`"properties":{` +
	`"summary":{"type":"string","description":"One sentence: what is wrong."},` +
	`"cause":{"type":"string","description":"Why it is wrong, with the evidence you read."},` +
	`"confidence":{"type":"string","enum":["high","medium","low"]},` +
	`"category":{"type":"string","enum":["workload_crash","image_pull","resource_pressure","configuration","dependency_outage","host_problem","unknown"]},` +
	`"affected":{"type":"array","items":{"type":"string"},"description":"What is affected, for example deployment/namespace/name, pod/namespace/name or host/service."},` +
	`"proposed_action":{"type":"string","description":"The one action that would fix it and why, or none."},` +
	`"evidence":{"type":"string","description":"What you read to reach this: tool names and what they showed."}}}`

// OutageCategories are the categories of an outage answer.
var OutageCategories = []string{"workload_crash", "image_pull", "resource_pressure", "configuration", "dependency_outage", "host_problem", "unknown"}

const (
	maxAction   = 2000
	maxEvidence = 4000
	maxAffected = 30
	maxObject   = 200
)

// Outage is the answer of the responder agent for an alert.
type Outage struct {
	Summary        string   `json:"summary"`
	Cause          string   `json:"cause"`
	Confidence     string   `json:"confidence"`
	Category       string   `json:"category"`
	Affected       []string `json:"affected"`
	ProposedAction string   `json:"proposed_action"`
	Evidence       string   `json:"evidence"`
}

var outageRequired = []string{"summary", "cause", "confidence", "category", "affected", "proposed_action", "evidence"}

// ParseOutage validates raw strictly: a JSON object with exactly the schema's members, the right types, known enum values and sane lengths.
func ParseOutage(raw []byte) (Outage, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return Outage{}, invalid("not a JSON object")
	}
	for _, name := range outageRequired {
		if _, ok := members[name]; !ok {
			return Outage{}, invalid("missing member %q", name)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var o Outage
	if err := dec.Decode(&o); err != nil {
		return Outage{}, invalid("%v", err)
	}
	for _, c := range []struct {
		name, s string
		max     int
	}{{"summary", o.Summary, maxSummary}, {"cause", o.Cause, maxCause}, {"proposed_action", o.ProposedAction, maxAction}, {"evidence", o.Evidence, maxEvidence}} {
		if err := checkText(c.name, c.s, c.max); err != nil {
			return Outage{}, err
		}
	}
	if !slices.Contains(Confidences, o.Confidence) {
		return Outage{}, invalid("confidence %q is not one of %v", o.Confidence, Confidences)
	}
	if !slices.Contains(OutageCategories, o.Category) {
		return Outage{}, invalid("category %q is not one of %v", o.Category, OutageCategories)
	}
	if len(o.Affected) > maxAffected {
		return Outage{}, invalid("%d affected entries, at most %d", len(o.Affected), maxAffected)
	}
	for _, a := range o.Affected {
		if a == "" || utf8.RuneCountInString(a) > maxObject || hasControl(a) {
			return Outage{}, invalid("an affected entry is empty, longer than %d characters or contains a control character", maxObject)
		}
	}
	return o, nil
}

// JSON is the canonical form that is stored: exactly the seven members, and [] instead of null.
func (o Outage) JSON() json.RawMessage {
	if o.Affected == nil {
		o.Affected = []string{}
	}
	b, _ := json.Marshal(o)
	return b
}
