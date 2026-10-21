package diagnosis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/skill"
)

// LearnSchema is passed to `claude --json-schema` for a learning run. Like Schema it is deliberately minimal; the length limits, the
// rules of a skill and the evidence rules are enforced by ParseLearn only.
const LearnSchema = `{"type":"object","additionalProperties":false,"required":["profile","skills"],"properties":{` +
	`"profile":{"type":"object","additionalProperties":false,"required":["os","role","services","containers","notes"],"properties":{` +
	`"os":{"type":"string","description":"The operating system and its version, in one line."},` +
	`"role":{"type":"string","description":"What this machine is for, in one line."},` +
	`"services":{"type":"array","items":{"type":"string"},"description":"The services that run on it (systemd units, daemons), by name."},` +
	`"containers":{"type":"array","items":{"type":"string"},"description":"The containers that run on it, by name."},` +
	`"notes":{"type":"string","description":"Anything else a later diagnosis should know about the machine; empty if nothing."}}},` +
	`"skills":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["name","description","body","evidence"],"properties":{` +
	`"name":{"type":"string","description":"Lower case letters, digits and dashes, at most 40 characters."},` +
	`"description":{"type":"string","description":"One line, at most 200 characters: what the note is about."},` +
	`"body":{"type":"string","description":"The note: plain text, at most 8 KB."},` +
	`"evidence":{"type":"array","items":{"type":"array","items":{"type":"string"}},"description":"The commands you ran with host_read that showed this fact, each as its list of words."}}}}}}`

// The limits of a learning answer (spec section 7).
const (
	maxLearnOS        = 200
	maxLearnRole      = 300
	maxLearnList      = 20
	maxLearnListItem  = 100
	maxLearnNotes     = 2000
	maxLearnEvidence  = 10
	maxLearnArgvWords = 12
	// maxLearnWordRunes is not in the spec: it is defence in depth, so that 10 commands of 12 words cannot carry an unbounded text.
	maxLearnWordRunes = 400
	reservedSkillName = skill.ProfileName
)

// LearnProfile is what the learner found out about the host as a whole. Remedy writes it into one Markdown note with fixed headings.
type LearnProfile struct {
	OS         string   `json:"os"`
	Role       string   `json:"role"`
	Services   []string `json:"services"`
	Containers []string `json:"containers"`
	Notes      string   `json:"notes"`
}

// LearnSkill is one proposed skill and the commands that showed its facts.
type LearnSkill struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Body        string     `json:"body"`
	Evidence    [][]string `json:"evidence"`
}

// Learn is the answer of a learning run.
type Learn struct {
	Profile LearnProfile `json:"profile"`
	Skills  []LearnSkill `json:"skills"`
}

// learnShape is the form of one value of a learning answer: an object with exactly these members, a list of elem, or (both nil) a
// string.
type learnShape struct {
	members []learnMember
	elem    *learnShape
}

type learnMember struct {
	name  string
	shape *learnShape
}

var (
	learnString = &learnShape{}
	learnList   = &learnShape{elem: learnString}
	learnAnswer = &learnShape{members: []learnMember{
		{"profile", &learnShape{members: []learnMember{{"os", learnString}, {"role", learnString}, {"services", learnList}, {"containers", learnList}, {"notes", learnString}}}},
		{"skills", &learnShape{elem: &learnShape{members: []learnMember{{"name", learnString}, {"description", learnString}, {"body", learnString}, {"evidence", &learnShape{elem: learnList}}}}}},
	}}
	// errLearnShape is the one text for every problem of form. It never repeats what the agent wrote: an error ends up in the run's
	// record and in logs.
	errLearnShape = invalid("the answer is not an object of the schema: a member is missing, unknown, repeated, null or of the wrong type")
)

// walkLearn reads one value from dec and checks it against shape token by token. Go's decoder matches member names regardless of case
// and lets the last of two equal members win; this walk refuses both, and refuses null anywhere.
func walkLearn(dec *json.Decoder, shape *learnShape) error {
	tok, err := dec.Token()
	if err != nil {
		return errLearnShape
	}
	switch {
	case shape.members != nil:
		if tok != json.Delim('{') {
			return errLearnShape
		}
		seen := map[string]bool{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return errLearnShape
			}
			key, _ := tok.(string)
			var member *learnShape
			for _, m := range shape.members {
				if m.name == key {
					member = m.shape
				}
			}
			if member == nil || seen[key] {
				return errLearnShape
			}
			seen[key] = true
			if err := walkLearn(dec, member); err != nil {
				return err
			}
		}
		if len(seen) != len(shape.members) {
			return errLearnShape
		}
	case shape.elem != nil:
		if tok != json.Delim('[') {
			return errLearnShape
		}
		for dec.More() {
			if err := walkLearn(dec, shape.elem); err != nil {
				return err
			}
		}
	default:
		if _, ok := tok.(string); !ok {
			return errLearnShape
		}
		return nil
	}
	if _, err := dec.Token(); err != nil { // the closing delimiter
		return errLearnShape
	}
	return nil
}

// checkLearnText accepts text of at most max characters that is valid UTF-8, has no control character (with layout, the newline and the
// tab are fine) and no invisible or bidirectional character. blank says whether it may be empty.
func checkLearnText(name, s string, max int, blank, layout bool) error {
	switch {
	case !utf8.ValidString(s):
		return invalid("%s is not valid UTF-8", name)
	case !blank && strings.TrimSpace(s) == "":
		return invalid("%s is empty", name)
	case utf8.RuneCountInString(s) > max:
		return invalid("%s has %d characters, at most %d", name, utf8.RuneCountInString(s), max)
	case skill.HasControl(s, layout):
		if layout {
			return invalid("%s contains a control character other than a line break or a tab", name)
		}
		return invalid("%s must be one line: it contains a line break, a tab or another control character", name)
	case skill.HasInvisible(s):
		return invalid("%s contains an invisible or bidirectional character", name)
	}
	return nil
}

// ParseLearn validates raw strictly: exactly the members of the schema (each once, with its exact name, never null), the right types,
// the limits of the spec, the rules of a skill (internal/skill) for each proposed skill, evidence of 1 to 10 commands of at most 12 words
// for each, and a name that is used once and is not "profile" (the note that Remedy writes from the profile has that name). It returns
// the skills normalised by skill.Fields.Validate. No error text repeats what the agent wrote.
func ParseLearn(raw []byte) (Learn, error) {
	walk := json.NewDecoder(bytes.NewReader(raw))
	if err := walkLearn(walk, learnAnswer); err != nil {
		return Learn{}, err
	}
	if _, err := walk.Token(); err != io.EOF {
		return Learn{}, invalid("data after the answer")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var l Learn
	if err := dec.Decode(&l); err != nil {
		return Learn{}, errLearnShape
	}

	p := l.Profile
	if p.Services == nil || p.Containers == nil {
		return Learn{}, invalid("profile.services and profile.containers must be lists")
	}
	for _, c := range []struct {
		name, s string
		max     int
		blank   bool
		layout  bool
	}{{"profile.os", p.OS, maxLearnOS, false, false}, {"profile.role", p.Role, maxLearnRole, false, false}, {"profile.notes", p.Notes, maxLearnNotes, true, true}} {
		// os, role and the entries of the lists are one line each, so that none can fake a heading of the profile note; the notes may
		// have layout.
		if err := checkLearnText(c.name, c.s, c.max, c.blank, c.layout); err != nil {
			return Learn{}, err
		}
	}
	for _, list := range []struct {
		name  string
		items []string
	}{{"profile.services", p.Services}, {"profile.containers", p.Containers}} {
		if len(list.items) > maxLearnList {
			return Learn{}, invalid("%s has %d entries, at most %d", list.name, len(list.items), maxLearnList)
		}
		for _, s := range list.items {
			if err := checkLearnText(list.name+" entry", s, maxLearnListItem, false, false); err != nil {
				return Learn{}, err
			}
		}
	}

	if len(l.Skills) > skill.MaxProposalsPerRun {
		return Learn{}, invalid("%d skills, at most %d", len(l.Skills), skill.MaxProposalsPerRun)
	}
	seen := map[string]bool{}
	for i, k := range l.Skills {
		where := fmt.Sprintf("skill %d", i+1)
		fields, err := skill.Fields{Name: k.Name, Description: k.Description, Body: k.Body}.Validate()
		if err != nil {
			var fe *skill.FieldError
			if errors.As(err, &fe) { // the messages of a FieldError are fixed texts
				return Learn{}, invalid("%s: %s", where, fe.Msg)
			}
			return Learn{}, invalid("%s: the name, the description or the body is not valid", where)
		}
		if fields.Name == reservedSkillName {
			return Learn{}, invalid("%s: the name %q is reserved for the note that Remedy writes from the profile", where, reservedSkillName)
		}
		if seen[fields.Name] {
			return Learn{}, invalid("%s: its name is used by an earlier skill", where)
		}
		seen[fields.Name] = true
		// What is returned and stored is the normalised text: trimmed, and the body with \r\n made \n.
		l.Skills[i].Name, l.Skills[i].Description, l.Skills[i].Body = fields.Name, fields.Description, fields.Body
		if k.Evidence == nil {
			return Learn{}, invalid("%s: evidence must be a list", where)
		}
		if len(k.Evidence) < 1 || len(k.Evidence) > maxLearnEvidence {
			return Learn{}, invalid("%s: %d cited commands, between 1 and %d are needed", where, len(k.Evidence), maxLearnEvidence)
		}
		for _, argv := range k.Evidence {
			if len(argv) < 1 || len(argv) > maxLearnArgvWords {
				return Learn{}, invalid("%s: a cited command has %d words, between 1 and %d are needed", where, len(argv), maxLearnArgvWords)
			}
			for _, w := range argv {
				if err := checkLearnText(where+" cited word", w, maxLearnWordRunes, false, false); err != nil {
					return Learn{}, err
				}
			}
		}
	}
	return l, nil
}

// JSON is the canonical form that is stored as the run's output: the members of the schema, and [] instead of null.
func (l Learn) JSON() json.RawMessage {
	if l.Profile.Services == nil {
		l.Profile.Services = []string{}
	}
	if l.Profile.Containers == nil {
		l.Profile.Containers = []string{}
	}
	if l.Skills == nil {
		l.Skills = []LearnSkill{}
	}
	for i := range l.Skills {
		if l.Skills[i].Evidence == nil {
			l.Skills[i].Evidence = [][]string{}
		}
	}
	b, _ := json.Marshal(l) // strings, lists and nothing else cannot fail to marshal
	return b
}
