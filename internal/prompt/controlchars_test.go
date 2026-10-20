package prompt_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/prompt"
)

// Characters that are invisible or reorder the text must not reach a prompt: a person reviewing it cannot see them, and a model may read
// them. Code points are numeric on purpose, so that the test file itself carries none of them.
var droppedRunes = []struct {
	name string
	r    rune
}{
	{"NUL", 0x00}, {"ESC", 0x1b}, {"DEL", 0x7f},
	{"C1 first", 0x80}, {"C1 NEL", 0x85}, {"C1 CSI", 0x9b}, {"C1 last", 0x9f},
	{"soft hyphen", 0xad},
	{"arabic letter mark", 0x061c},
	{"zero width space", 0x200b}, {"zero width non-joiner", 0x200c}, {"zero width joiner", 0x200d},
	{"left-to-right mark", 0x200e}, {"right-to-left mark", 0x200f},
	{"line separator", 0x2028}, {"paragraph separator", 0x2029},
	{"LRE", 0x202a}, {"RLE", 0x202b}, {"PDF", 0x202c}, {"LRO", 0x202d}, {"RLO", 0x202e},
	{"word joiner", 0x2060},
	{"LRI", 0x2066}, {"RLI", 0x2067}, {"FSI", 0x2068}, {"PDI", 0x2069},
	{"BOM", 0xfeff},
	{"tag begin", 0xe0001}, {"tag A", 0xe0041}, {"tag cancel", 0xe007f},
}

var keptRunes = []struct {
	name string
	r    rune
}{
	{"letter", 'a'}, {"accented letter", 0xe9}, {"umlaut", 0xfc}, {"em dash", 0x2014}, {"emoji", 0x1f600},
	{"cjk", 0x4e2d}, {"variation selector 16", 0xfe0f},
}

func TestInvisibleAndReorderingCharactersAreDroppedInEveryBlock(t *testing.T) {
	for _, c := range droppedRunes {
		bad := string(c.r)
		in := input()
		in.Title = "ti" + bad + "tle"
		in.Labels = map[string]string{"k" + bad + "ey": "va" + bad + "lue"}
		in.Annotations = map[string]string{"su" + bad + "mmary": "line one\nli" + bad + "ne two"}
		in.HostName = "n" + bad + "as"
		in.Skills = []prompt.Skill{{Name: "na" + bad + "me", Host: "ho" + bad + "st", Description: "de" + bad + "scription"}}
		text := prompt.Outage(in, d)
		if strings.ContainsRune(text, c.r) {
			t.Errorf("%s (U+%04X) reached the outage prompt", c.name, c.r)
		}
		for _, want := range []string{"title", "value", "line two", "name", "description"} {
			if !strings.Contains(strings.ReplaceAll(text, " ", ""), strings.ReplaceAll(want, " ", "")) {
				t.Errorf("%s (U+%04X): %q was lost with the character", c.name, c.r, want)
			}
		}
		section := prompt.SkillsSection([]prompt.Skill{{Name: "x" + bad + "y", Description: "a" + bad + "b"}}, d)
		if strings.ContainsRune(section, c.r) {
			t.Errorf("%s (U+%04X) reached the skills section", c.name, c.r)
		}
	}
}

func TestLegitimateTextSurvivesTheControlFilter(t *testing.T) {
	for _, c := range keptRunes {
		in := input()
		in.Title = "t" + string(c.r) + "t"
		in.Annotations = map[string]string{"summary": "s" + string(c.r) + "s"}
		in.Skills = []prompt.Skill{{Name: "n", Description: "d" + string(c.r) + "d"}}
		text := prompt.Outage(in, d)
		for _, want := range []string{"t" + string(c.r) + "t", "s" + string(c.r) + "s", "d" + string(c.r) + "d"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s (U+%04X): %q was dropped", c.name, c.r, want)
			}
		}
	}
}

func TestNewlineAndTabSurviveInAnAnnotation(t *testing.T) {
	in := input()
	in.Annotations = map[string]string{"description": "first\n\tsecond"}
	if text := prompt.Outage(in, d); !strings.Contains(text, "first\n\tsecond") {
		t.Errorf("a line break and a tab of an annotation were dropped:\n%s", text)
	}
}
