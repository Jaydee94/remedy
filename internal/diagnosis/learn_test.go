package diagnosis_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/diagnosis"
)

func goodLearn() map[string]any {
	return map[string]any{
		"profile": map[string]any{
			"os": "Debian 12", "role": "NAS with Paperless and a media server",
			"services": []string{"docker", "sshd"}, "containers": []string{"remedy-paperless", "remedy-jellyfin"}, "notes": "Backups run at night.",
		},
		"skills": []any{
			map[string]any{
				"name": "paperless", "description": "Paperless keeps its data in /volume1/docker/paperless.",
				"body": "The consume folder is /volume1/docker/paperless/consume.\n\nA restart is safe.", "evidence": [][]string{{"docker", "ps", "-a"}, {"docker", "inspect", "remedy-paperless"}},
			},
		},
	}
}

// learnJSON marshals the good answer after mutate changed it.
func learnJSON(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := goodLearn()
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func profileOf(m map[string]any) map[string]any { return m["profile"].(map[string]any) }
func skillOf(m map[string]any) map[string]any   { return m["skills"].([]any)[0].(map[string]any) }

func TestAGoodLearningAnswerIsAccepted(t *testing.T) {
	l, err := diagnosis.ParseLearn(learnJSON(t, nil))
	if err != nil || l.Profile.OS != "Debian 12" || len(l.Skills) != 1 || l.Skills[0].Name != "paperless" || len(l.Skills[0].Evidence) != 2 {
		t.Fatalf("%+v, %v", l, err)
	}
	// An answer with no skill is valid: it stores only the profile.
	l, err = diagnosis.ParseLearn(learnJSON(t, func(m map[string]any) {
		m["skills"] = []any{}
		p := profileOf(m)
		p["services"], p["containers"], p["notes"] = []string{}, []string{}, ""
	}))
	if err != nil || len(l.Skills) != 0 {
		t.Fatalf("no skills, no services, empty notes: %+v, %v", l, err)
	}
}

func TestEveryMemberOfALearningAnswerIsRequiredAndNoOtherIsAllowed(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"no profile":            func(m map[string]any) { delete(m, "profile") },
		"no skills":             func(m map[string]any) { delete(m, "skills") },
		"an extra member":       func(m map[string]any) { m["extra"] = "x" },
		"no os":                 func(m map[string]any) { delete(profileOf(m), "os") },
		"no role":               func(m map[string]any) { delete(profileOf(m), "role") },
		"no services":           func(m map[string]any) { delete(profileOf(m), "services") },
		"no containers":         func(m map[string]any) { delete(profileOf(m), "containers") },
		"no notes":              func(m map[string]any) { delete(profileOf(m), "notes") },
		"an extra profile":      func(m map[string]any) { profileOf(m)["uptime"] = "3 days" },
		"no skill name":         func(m map[string]any) { delete(skillOf(m), "name") },
		"no skill description":  func(m map[string]any) { delete(skillOf(m), "description") },
		"no skill body":         func(m map[string]any) { delete(skillOf(m), "body") },
		"no skill evidence":     func(m map[string]any) { delete(skillOf(m), "evidence") },
		"an extra skill member": func(m map[string]any) { skillOf(m)["state"] = "active" },
		"null skills":           func(m map[string]any) { m["skills"] = nil },
		"null services":         func(m map[string]any) { profileOf(m)["services"] = nil },
		"null evidence":         func(m map[string]any) { skillOf(m)["evidence"] = nil },
		"skills is an object":   func(m map[string]any) { m["skills"] = map[string]any{} },
		"profile is a string":   func(m map[string]any) { m["profile"] = "Debian" },
		"os is a number":        func(m map[string]any) { profileOf(m)["os"] = 12 },
	}
	for name, mutate := range cases {
		if _, err := diagnosis.ParseLearn(learnJSON(t, mutate)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	for name, raw := range map[string]string{"not JSON": "no", "an array": "[]", "null": "null", "empty": "", "trailing data": string(learnJSON(t, nil)) + `{}`} {
		if _, err := diagnosis.ParseLearn([]byte(raw)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestTheLimitsOfALearningAnswer(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	list := func(n, size int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = long(size)
		}
		return out
	}
	skills := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"name": fmt.Sprintf("skill-%d", i), "description": "d", "body": "b", "evidence": [][]string{{"uname", "-a"}}}
		}
		return out
	}
	// Each case is the limit itself, which is accepted, and one past it, which is not.
	type limit struct {
		ok, over func(m map[string]any)
	}
	limits := map[string]limit{
		"os":                {func(m map[string]any) { profileOf(m)["os"] = long(200) }, func(m map[string]any) { profileOf(m)["os"] = long(201) }},
		"role":              {func(m map[string]any) { profileOf(m)["role"] = long(300) }, func(m map[string]any) { profileOf(m)["role"] = long(301) }},
		"notes":             {func(m map[string]any) { profileOf(m)["notes"] = long(2000) }, func(m map[string]any) { profileOf(m)["notes"] = long(2001) }},
		"services":          {func(m map[string]any) { profileOf(m)["services"] = list(20, 100) }, func(m map[string]any) { profileOf(m)["services"] = list(21, 1) }},
		"a service name":    {func(m map[string]any) { profileOf(m)["services"] = list(1, 100) }, func(m map[string]any) { profileOf(m)["services"] = list(1, 101) }},
		"containers":        {func(m map[string]any) { profileOf(m)["containers"] = list(20, 100) }, func(m map[string]any) { profileOf(m)["containers"] = list(21, 1) }},
		"a container name":  {func(m map[string]any) { profileOf(m)["containers"] = list(1, 100) }, func(m map[string]any) { profileOf(m)["containers"] = list(1, 101) }},
		"the number skills": {func(m map[string]any) { m["skills"] = skills(8) }, func(m map[string]any) { m["skills"] = skills(9) }},
		"cited commands": {
			func(m map[string]any) {
				e := make([][]string, 10)
				for i := range e {
					e[i] = []string{"uname"}
				}
				skillOf(m)["evidence"] = e
			},
			func(m map[string]any) {
				e := make([][]string, 11)
				for i := range e {
					e[i] = []string{"uname"}
				}
				skillOf(m)["evidence"] = e
			},
		},
		"words of a command": {
			func(m map[string]any) { skillOf(m)["evidence"] = [][]string{list(12, 1)} },
			func(m map[string]any) { skillOf(m)["evidence"] = [][]string{list(13, 1)} },
		},
		"the description": {func(m map[string]any) { skillOf(m)["description"] = long(200) }, func(m map[string]any) { skillOf(m)["description"] = long(201) }},
		"the body":        {func(m map[string]any) { skillOf(m)["body"] = long(8192) }, func(m map[string]any) { skillOf(m)["body"] = long(8193) }},
		"the name":        {func(m map[string]any) { skillOf(m)["name"] = long(40) }, func(m map[string]any) { skillOf(m)["name"] = long(41) }},
	}
	for name, l := range limits {
		if _, err := diagnosis.ParseLearn(learnJSON(t, l.ok)); err != nil {
			t.Errorf("%s at the limit: %v", name, err)
		}
		if _, err := diagnosis.ParseLearn(learnJSON(t, l.over)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s over the limit: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestLearningLimitsCountCharactersNotBytes(t *testing.T) {
	_, err := diagnosis.ParseLearn(learnJSON(t, func(m map[string]any) { profileOf(m)["os"] = strings.Repeat(string(rune(0xE9)), 200) }))
	if err != nil {
		t.Errorf("200 two-byte characters: %v", err)
	}
}

func TestTextThatIsBlankHasControlCharactersOrHidesThings(t *testing.T) {
	bidi, zeroWidth := string(rune(0x202E)), string(rune(0x200B))
	cases := map[string]func(m map[string]any){
		"a blank os":                          func(m map[string]any) { profileOf(m)["os"] = "  " },
		"a blank role":                        func(m map[string]any) { profileOf(m)["role"] = "" },
		"a blank service":                     func(m map[string]any) { profileOf(m)["services"] = []string{""} },
		"an escape in the notes":              func(m map[string]any) { profileOf(m)["notes"] = "a\x1b[31mb" },
		"a nul in the os":                     func(m map[string]any) { profileOf(m)["os"] = "Deb\x00ian" },
		"a carriage return":                   func(m map[string]any) { profileOf(m)["notes"] = "a\rb" },
		"a bidi override in the os":           func(m map[string]any) { profileOf(m)["os"] = "Debian " + bidi + "21" },
		"a zero-width space in notes":         func(m map[string]any) { profileOf(m)["notes"] = "a" + zeroWidth + "b" },
		"a bidi override in the body":         func(m map[string]any) { skillOf(m)["body"] = "safe " + bidi + " fed" },
		"a line break in the description":     func(m map[string]any) { skillOf(m)["description"] = "one\ntwo" },
		"a control character in a cited word": func(m map[string]any) { skillOf(m)["evidence"] = [][]string{{"echo", "a\x07b"}} },
		"a newline in a cited word":           func(m map[string]any) { skillOf(m)["evidence"] = [][]string{{"echo", "a\nb"}} },
		"a tab in a cited word":               func(m map[string]any) { skillOf(m)["evidence"] = [][]string{{"echo", "a\tb"}} },
		"an empty cited word":                 func(m map[string]any) { skillOf(m)["evidence"] = [][]string{{"echo", ""}} },
		"a bidi override in a cited word":     func(m map[string]any) { skillOf(m)["evidence"] = [][]string{{"echo", "a" + bidi}} },
	}
	for name, mutate := range cases {
		if _, err := diagnosis.ParseLearn(learnJSON(t, mutate)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// A newline and a tab are fine in the notes and in a body.
	if _, err := diagnosis.ParseLearn(learnJSON(t, func(m map[string]any) {
		profileOf(m)["notes"] = "line one\n\tline two"
		skillOf(m)["body"] = "line one\n\tline two"
	})); err != nil {
		t.Errorf("layout in the notes and the body: %v", err)
	}
}

func TestTheNamesOfProposedSkills(t *testing.T) {
	twice := func(m map[string]any) {
		second := map[string]any{}
		for k, v := range skillOf(m) {
			second[k] = v
		}
		m["skills"] = append(m["skills"].([]any), second)
	}
	cases := map[string]func(m map[string]any){
		"a duplicate name":           twice,
		"the reserved name profile":  func(m map[string]any) { skillOf(m)["name"] = "profile" },
		"an upper case name":         func(m map[string]any) { skillOf(m)["name"] = "Paperless" },
		"a name with a space":        func(m map[string]any) { skillOf(m)["name"] = "my skill" },
		"the name of the global tag": func(m map[string]any) { skillOf(m)["name"] = "(global)" },
		"an empty name":              func(m map[string]any) { skillOf(m)["name"] = "" },
		"an empty body":              func(m map[string]any) { skillOf(m)["body"] = " " },
		"an empty description":       func(m map[string]any) { skillOf(m)["description"] = "" },
		"no cited command":           func(m map[string]any) { skillOf(m)["evidence"] = [][]string{} },
		"an empty cited command":     func(m map[string]any) { skillOf(m)["evidence"] = [][]string{{}} },
	}
	for name, mutate := range cases {
		if _, err := diagnosis.ParseLearn(learnJSON(t, mutate)); !errors.Is(err, diagnosis.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestLearnJSONIsTheCanonicalForm(t *testing.T) {
	l, err := diagnosis.ParseLearn(learnJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	again, err := diagnosis.ParseLearn(l.JSON())
	if err != nil || again.Profile.OS != l.Profile.OS || len(again.Skills) != 1 {
		t.Fatalf("the canonical form does not parse again: %v", err)
	}
	empty := diagnosis.Learn{Profile: diagnosis.LearnProfile{OS: "o", Role: "r"}}
	for _, want := range []string{`"services":[]`, `"containers":[]`, `"skills":[]`} {
		if !strings.Contains(string(empty.JSON()), want) {
			t.Errorf("a nil list must become []: %s", empty.JSON())
		}
	}
	nilEvidence := diagnosis.Learn{Skills: []diagnosis.LearnSkill{{Name: "a"}}}
	if !strings.Contains(string(nilEvidence.JSON()), `"evidence":[]`) {
		t.Errorf("nil evidence must become []: %s", nilEvidence.JSON())
	}
}

// The schema that goes to the CLI and the structs that ParseLearn fills must describe the same members at every level.
func TestLearnSchemaMatchesTheStructs(t *testing.T) {
	var s struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Required   []string `json:"required"`
			Properties map[string]json.RawMessage
			Items      struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(diagnosis.LearnSchema), &s); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	keys := func(m map[string]json.RawMessage) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		return out
	}
	same := func(what string, a, b []string) {
		t.Helper()
		if len(a) != len(b) {
			t.Errorf("%s: %v vs %v", what, a, b)
			return
		}
		set := map[string]bool{}
		for _, x := range a {
			set[x] = true
		}
		for _, x := range b {
			if !set[x] {
				t.Errorf("%s: %v vs %v", what, a, b)
			}
		}
	}
	same("top level", s.Required, []string{"profile", "skills"})
	same("profile", s.Properties["profile"].Required, []string{"os", "role", "services", "containers", "notes"})
	same("skill", s.Properties["skills"].Items.Required, []string{"name", "description", "body", "evidence"})
	same("skill properties", keys(s.Properties["skills"].Items.Properties), s.Properties["skills"].Items.Required)
	// The schema and the struct tags: a member the schema names must be one the struct reads, and the good answer must satisfy both.
	if _, err := diagnosis.ParseLearn(learnJSON(t, nil)); err != nil {
		t.Fatal(err)
	}
}
