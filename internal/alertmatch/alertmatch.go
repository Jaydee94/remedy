// Package alertmatch decides whether an alert belongs to a host. A host keeps rules in the syntax of Alertmanager's matchers, one
// per line: label="value", label!="value", label=~"regex", label!~"regex". All rules of a host must hold; a host without rules never
// matches. The package compares labels and nothing else: no rule is ever executed or put into a command.
package alertmatch

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLines is the most rules a host may have, MaxLineLen the longest rule in characters.
	MaxLines   = 10
	MaxLineLen = 200
)

// Op is the operator of a rule.
type Op string

const (
	OpEq  Op = "="
	OpNe  Op = "!="
	OpRe  Op = "=~"
	OpNre Op = "!~"
)

// Matcher is one rule.
type Matcher struct {
	Label string
	Op    Op
	Value string
	re    *regexp.Regexp
}

// ParseError is a problem with one line, worded so that it can be shown as it is.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

var labelRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Parse reads the rules of a host. Empty lines are ignored; the empty text has no rules.
func Parse(text string) ([]Matcher, error) {
	var out []Matcher
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(out) == MaxLines {
			return nil, &ParseError{i + 1, fmt.Sprintf("at most %d rules", MaxLines)}
		}
		if utf8.RuneCountInString(line) > MaxLineLen {
			return nil, &ParseError{i + 1, fmt.Sprintf("a rule has at most %d characters", MaxLineLen)}
		}
		m, err := parseLine(line)
		if err != nil {
			return nil, &ParseError{i + 1, err.Error()}
		}
		out = append(out, m)
	}
	return out, nil
}

func isLabelChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func parseLine(s string) (Matcher, error) {
	i := 0
	for i < len(s) && isLabelChar(s[i]) {
		i++
	}
	label := s[:i]
	if !labelRE.MatchString(label) {
		return Matcher{}, errors.New(`a rule starts with a label name such as instance`)
	}
	rest := strings.TrimLeft(s[i:], " \t")
	var op Op
	switch {
	case strings.HasPrefix(rest, "=~"):
		op = OpRe
	case strings.HasPrefix(rest, "!~"):
		op = OpNre
	case strings.HasPrefix(rest, "!="):
		op = OpNe
	case strings.HasPrefix(rest, "="):
		op = OpEq
	default:
		return Matcher{}, errors.New(`after the label comes =, !=, =~ or !~`)
	}
	rest = strings.TrimLeft(rest[len(op):], " \t")
	value, tail, err := unquote(rest)
	if err != nil {
		return Matcher{}, err
	}
	if strings.TrimSpace(tail) != "" {
		return Matcher{}, errors.New("nothing may follow the closing quote")
	}
	m := Matcher{Label: label, Op: op, Value: value}
	if op == OpRe || op == OpNre {
		// The value must compile on its own: wrapped in a group it could hide an unbalanced bracket.
		if _, err := regexp.Compile(value); err != nil {
			return Matcher{}, errors.New("the regular expression does not compile")
		}
		re, err := regexp.Compile("^(?:" + value + ")$")
		if err != nil {
			return Matcher{}, errors.New("the regular expression does not compile")
		}
		m.re = re
	}
	return m, nil
}

// unquote reads a double-quoted value with the escapes \\, \", \n and \t, and returns what follows the closing quote.
func unquote(s string) (value, tail string, err error) {
	if !strings.HasPrefix(s, `"`) {
		return "", "", errors.New(`the value is written in double quotes, for example "ugreen-nas"`)
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), s[i+1:], nil
		case '\\':
			i++
			if i >= len(s) {
				return "", "", errors.New("the value ends in a backslash")
			}
			switch s[i] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", "", errors.New(`only \\, \", \n and \t may follow a backslash`)
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", errors.New("the value must end with a quote")
}

func quote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(v) + `"`
}

// String is the canonical line of the rule.
func (m Matcher) String() string { return m.Label + string(m.Op) + quote(m.Value) }

// Matches says whether every rule holds for the labels. A missing label counts as the empty string, as in Alertmanager. No rules
// never match: a host that has none is not named by any alert.
func Matches(rules []Matcher, labels map[string]string) bool {
	if len(rules) == 0 {
		return false
	}
	for _, m := range rules {
		v := labels[m.Label]
		var ok bool
		switch m.Op {
		case OpEq:
			ok = v == m.Value
		case OpNe:
			ok = v != m.Value
		case OpRe:
			ok = m.re.MatchString(v)
		case OpNre:
			ok = !m.re.MatchString(v)
		}
		if !ok {
			return false
		}
	}
	return true
}

// Canonical checks the text and writes it back with every rule in its canonical form, joined by newlines.
func Canonical(text string) (string, error) {
	ms, err := Parse(text)
	if err != nil {
		return "", err
	}
	lines := make([]string, len(ms))
	for i, m := range ms {
		lines[i] = m.String()
	}
	return strings.Join(lines, "\n"), nil
}
