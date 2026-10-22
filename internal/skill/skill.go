// Package skill holds the rules for a skill: what a name, a description and a body may be, and the limits. It is the one place that
// says so; the store, the API, the prompt and the tool use it. A skill is Markdown text that is shown as text and never as markup. It
// informs an agent's reasoning and nothing else: it can never widen what host_read accepts, remove an approval or change a limit.
package skill

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/redact"
)

const (
	// MaxNameLen is the longest name.
	MaxNameLen = 40
	// MaxDescriptionRunes is the longest description, in characters.
	MaxDescriptionRunes = 200
	// MaxBodyBytes is the longest body.
	MaxBodyBytes = 8 << 10
	// MaxPerScope is how many skills one scope (one host, or global) may have. Proposals do not count until they are confirmed.
	MaxPerScope = 30
	// MaxProposalsPerRun is how many skills one learning run may propose, besides the profile (ProfileName).
	MaxProposalsPerRun = 8
	// MaxPendingPerHost is how many proposals may wait for a review for one host, across learning runs.
	MaxPendingPerHost = 20
	// MaxListed is how many skills the prompt lists.
	MaxListed = 40
	// ProfileName is the name of the skill a learning run proposes for what a host is.
	ProfileName = "profile"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidName says whether s is a skill name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// CutDescription shortens a description that is longer than MaxDescriptionRunes. It ends at a word (a text without a space in its second
// half is cut at a character), drops a separator left at the cut and ends with "…", so the maintainer sees that the line is not the whole
// sentence. The result has at most MaxDescriptionRunes characters; a description that fits is returned as it is.
func CutDescription(s string) string {
	runes := []rune(s)
	if len(runes) <= MaxDescriptionRunes {
		return s
	}
	cut := runes[:MaxDescriptionRunes-1]
	if !unicode.IsSpace(runes[MaxDescriptionRunes-1]) {
		for i := len(cut) - 1; i >= len(cut)/2; i-- {
			if unicode.IsSpace(cut[i]) {
				cut = cut[:i]
				break
			}
		}
	}
	return strings.TrimRightFunc(string(cut), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",;:-(", r) }) + "…"
}

// FieldError is a problem with one field, worded so that it can be shown to the maintainer as it is.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Fields are the texts of a skill.
type Fields struct{ Name, Description, Body string }

// hasInvisible reports characters that hide, reorder or smuggle text: every format character (category Cf: zero-width characters,
// bidirectional marks, the soft hyphen, the byte order mark, the tag characters U+E0000 to U+E007F), the line and paragraph
// separators, private use characters, variation selectors and the combining grapheme joiner. A maintainer who reads a skill as text
// must be able to trust what he sees, and so must the agent that reads it.
func hasInvisible(s string) bool {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r), unicode.In(r, unicode.Zl, unicode.Zp, unicode.Co):
			return true
		case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF, r == 0x034F:
			return true
		}
	}
	return false
}

// HasInvisible says whether s has a character that hides or reorders text. The learning answer is checked with it too.
func HasInvisible(s string) bool { return hasInvisible(s) }

// HasControl reports control characters. With layout set, the newline and the tab are allowed. The learning answer is checked with
// it too.
func HasControl(s string, layout bool) bool {
	for _, r := range s {
		if !unicode.IsControl(r) {
			continue
		}
		if layout && (r == '\n' || r == '\t') {
			continue
		}
		return true
	}
	return false
}

// Validate checks the three texts and returns them normalised: the name and the description trimmed, the body with \r\n made \n
// and trimmed. The error is a *FieldError.
func (f Fields) Validate() (Fields, error) {
	f.Name = strings.TrimSpace(f.Name)
	f.Description = strings.TrimSpace(f.Description)
	if !nameRE.MatchString(f.Name) {
		return f, &FieldError{"name", "The name may contain lower case letters, digits and dashes, must start with a letter or a digit and has 1 to 40 characters."}
	}
	switch {
	case f.Description == "":
		return f, &FieldError{"description", "The description must not be empty."}
	case utf8.RuneCountInString(f.Description) > MaxDescriptionRunes:
		return f, &FieldError{"description", "The description has at most 200 characters."}
	case !utf8.ValidString(f.Description) || HasControl(f.Description, false) || hasInvisible(f.Description):
		return f, &FieldError{"description", "The description is one line of plain text: no line breaks, control characters or invisible characters."}
	}
	body := strings.TrimSpace(strings.ReplaceAll(f.Body, "\r\n", "\n"))
	switch {
	case body == "":
		return f, &FieldError{"body", "The body must not be empty."}
	case len(body) > MaxBodyBytes:
		return f, &FieldError{"body", "The body has at most 8 KB."}
	case !utf8.ValidString(body) || HasControl(body, true) || hasInvisible(body):
		return f, &FieldError{"body", "The body is plain text: line breaks and tabs are fine, other control characters and invisible characters are not."}
	}
	f.Body = body
	return f, nil
}

// Redacted returns f with secrets removed from the description and the body. A learning run saw host output; what it proposes is
// redacted before it is stored. Callers validate first and validate again (or cut) after redacting: a redaction marker can make a
// text longer.
func Redacted(f Fields) Fields {
	f.Description = redact.Redact(f.Description)
	f.Body = redact.Redact(f.Body)
	return f
}

// Evidence is one command that a proposal cites as the source of a fact, and whether the learning run really ran it.
type Evidence struct {
	Argv []string `json:"argv"`
	Ran  bool     `json:"ran"`
}

// EncodeEvidence is the stored form: a JSON array, [] for none.
func EncodeEvidence(e []Evidence) string {
	if e == nil {
		return "[]"
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// DecodeEvidence reads the stored form. It never returns nil; a text that is not a JSON array gives an empty list.
func DecodeEvidence(s string) []Evidence {
	var e []Evidence
	if err := json.Unmarshal([]byte(s), &e); err != nil || e == nil {
		return []Evidence{}
	}
	return e
}
