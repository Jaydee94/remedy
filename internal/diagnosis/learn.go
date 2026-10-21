package diagnosis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
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

var (
	learnRequired   = []string{"profile", "skills"}
	profileRequired = []string{"os", "role", "services", "containers", "notes"}
	skillRequired   = []string{"name", "description", "body", "evidence"}
)

// layoutControl reports a control character other than the newline and the tab.
func layoutControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0
}

// checkLearnText accepts text of at most max characters that is valid UTF-8, has no control character but the newline and the tab and no
// invisible or bidirectional character. blank says whether it may be empty.
func checkLearnText(name, s string, max int, blank bool) error {
	switch {
	case !utf8.ValidString(s):
		return invalid("%s is not valid UTF-8", name)
	case !blank && strings.TrimSpace(s) == "":
		return invalid("%s is empty", name)
	case utf8.RuneCountInString(s) > max:
		return invalid("%s has %d characters, at most %d", name, utf8.RuneCountInString(s), max)
	case layoutControl(s):
		return invalid("%s contains a control character", name)
	case skill.HasInvisible(s):
		return invalid("%s contains an invisible or bidirectional character", name)
	}
	return nil
}

func requireMembers(raw []byte, what string, names []string) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return invalid("%s is not a JSON object", what)
	}
	for _, name := range names {
		if _, ok := members[name]; !ok {
			return invalid("%s has no member %q", what, name)
		}
	}
	return nil
}

// ParseLearn validates raw strictly: exactly the members of the schema, the right types, the limits of the spec, the rules of a skill
// (internal/skill) for each proposed skill, evidence of 1 to 10 commands of at most 12 words for each, and a name that is used once and
// is not "profile" (the note that Remedy writes from the profile has that name).
func ParseLearn(raw []byte) (Learn, error) {
	if err := requireMembers(raw, "the answer", learnRequired); err != nil {
		return Learn{}, err
	}
	var members map[string]json.RawMessage
	_ = json.Unmarshal(raw, &members)
	if err := requireMembers(members["profile"], "profile", profileRequired); err != nil {
		return Learn{}, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(members["skills"], &items); err != nil || items == nil {
		return Learn{}, invalid("skills is not a list")
	}
	for i, item := range items {
		if err := requireMembers(item, fmt.Sprintf("skill %d", i+1), skillRequired); err != nil {
			return Learn{}, err
		}
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var l Learn
	if err := dec.Decode(&l); err != nil {
		return Learn{}, invalid("%v", err)
	}
	if dec.More() {
		return Learn{}, invalid("data after the answer")
	}

	p := l.Profile
	if p.Services == nil || p.Containers == nil {
		return Learn{}, invalid("profile.services and profile.containers must be lists")
	}
	for _, c := range []struct {
		name, s string
		max     int
		blank   bool
	}{{"profile.os", p.OS, maxLearnOS, false}, {"profile.role", p.Role, maxLearnRole, false}, {"profile.notes", p.Notes, maxLearnNotes, true}} {
		if err := checkLearnText(c.name, c.s, c.max, c.blank); err != nil {
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
			if err := checkLearnText(list.name+" entry", s, maxLearnListItem, false); err != nil {
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
			if errors.As(err, &fe) {
				return Learn{}, invalid("%s: %s", where, fe.Msg)
			}
			return Learn{}, invalid("%s: %v", where, err)
		}
		if fields.Name == reservedSkillName {
			return Learn{}, invalid("%s: the name %q is reserved for the note that Remedy writes from the profile", where, reservedSkillName)
		}
		if seen[fields.Name] {
			return Learn{}, invalid("%s: the name %q is used twice", where, fields.Name)
		}
		seen[fields.Name] = true
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
				if err := checkLearnText(where+" cited word", w, maxLearnWordRunes, false); err != nil {
					return Learn{}, err
				}
				if strings.ContainsAny(w, "\n\t") {
					return Learn{}, invalid("%s: a cited word contains a line break or a tab", where)
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
