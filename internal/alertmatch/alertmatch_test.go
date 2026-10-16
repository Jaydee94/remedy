package alertmatch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/alertmatch"
)

func rules(t *testing.T, text string) []alertmatch.Matcher {
	t.Helper()
	m, err := alertmatch.Parse(text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return m
}

func TestParseAcceptsTheAlertmanagerSyntax(t *testing.T) {
	for _, line := range []string{
		`instance="ugreen-nas"`,
		`instance=~"ugreen-nas(:[0-9]+)?"`,
		`job!="node"`,
		`job!~"cadvisor|kube.*"`,
		`  instance   =   "x"  `,
		`a_b1="\"quoted\" and \\ and \n and \t"`,
		`_x=""`,
	} {
		if _, err := alertmatch.Parse(line); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

func TestParseGivesNothingForEmptyText(t *testing.T) {
	for _, text := range []string{"", "   ", "\n\n", " \r\n \r\n"} {
		m, err := alertmatch.Parse(text)
		if err != nil || len(m) != 0 {
			t.Errorf("%q: %v, %v", text, m, err)
		}
	}
}

func TestParseRefusesWhatIsNotARule(t *testing.T) {
	long := `a="` + strings.Repeat("x", alertmatch.MaxLineLen) + `"`
	eleven := strings.TrimSpace(strings.Repeat("a=\"b\"\n", alertmatch.MaxLines+1))
	for name, text := range map[string]string{
		"unquoted value":                        `instance=ugreen-nas`,
		"label starts with a digit":             `1abc="x"`,
		"no label":                              `="x"`,
		"no operator":                           `instance "x"`,
		"double equals":                         `instance=="x"`,
		"text after the quote":                  `a="x" junk`,
		"unknown escape":                        `a="\q"`,
		"unterminated":                          `a="x`,
		"regex does not compile":                `a=~"("`,
		"regex that compiles only when wrapped": `a=~"a)(b"`,
		"label with a dash":                     `a-b="x"`,
		"line too long":                         long,
		"eleven rules":                          eleven,
	} {
		_, err := alertmatch.Parse(text)
		var pe *alertmatch.ParseError
		if err == nil || !errors.As(err, &pe) {
			t.Errorf("%s: %q was accepted or gave %T", name, text, err)
		}
	}
	if _, err := alertmatch.Parse(strings.TrimSpace(strings.Repeat("a=\"b\"\n", alertmatch.MaxLines))); err != nil {
		t.Errorf("ten rules must be fine: %v", err)
	}
}

func TestAParseErrorNamesTheLine(t *testing.T) {
	_, err := alertmatch.Parse("a=\"b\"\n\nc=d")
	if err == nil || !strings.HasPrefix(err.Error(), "line 3: ") {
		t.Fatalf("error = %v, want one that starts with 'line 3: '", err)
	}
}

func TestMatches(t *testing.T) {
	labels := map[string]string{"instance": "ugreen-nas:9100", "job": "node", "severity": "critical"}
	cases := []struct {
		name, text string
		want       bool
	}{
		{"equal", `job="node"`, true},
		{"equal fails", `job="cadvisor"`, false},
		{"not equal", `job!="cadvisor"`, true},
		{"not equal fails", `job!="node"`, false},
		{"regex is anchored: a part does not match", `instance=~"ugreen-nas"`, false},
		{"regex matches the whole value", `instance=~"ugreen-nas(:[0-9]+)?"`, true},
		{"regex alternative", `job=~"a|node"`, true},
		{"negated regex", `job!~"cad.*"`, true},
		{"negated regex fails", `job!~"no.*"`, false},
		{"a missing label is the empty string: equal", `missing=""`, true},
		{"a missing label is the empty string: not equal fails", `missing!=""`, false},
		{"a missing label does not equal a value", `missing="x"`, false},
		{"all lines must hold", "job=\"node\"\nseverity=\"critical\"", true},
		{"one failing line fails the host", "job=\"node\"\nseverity=\"warning\"", false},
		{"no rules never match", ``, false},
	}
	for _, c := range cases {
		if got := alertmatch.Matches(rules(t, c.text), labels); got != c.want {
			t.Errorf("%s: Matches(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
	if alertmatch.Matches(rules(t, `a="b"`), nil) {
		t.Error("a nil label map matched a rule for a non-empty value")
	}
}

func TestTheHomeServerCase(t *testing.T) {
	rs := rules(t, `instance=~"ugreen-nas(:[0-9]+)?"`)
	if !alertmatch.Matches(rs, map[string]string{"instance": "ugreen-nas"}) || !alertmatch.Matches(rs, map[string]string{"instance": "ugreen-nas:9100"}) {
		t.Error("the NAS must match with and without a port")
	}
	if alertmatch.Matches(rs, map[string]string{"instance": "192.168.178.118:9100"}) || alertmatch.Matches(rs, map[string]string{"instance": "my-ugreen-nas"}) {
		t.Error("another instance matched")
	}
}

func TestCanonicalWritesEveryRuleTheSameWay(t *testing.T) {
	got, err := alertmatch.Canonical("a  =  \"x\"\n\n  b!~\"y.*\"  \r\nc=\"q\\\"uote\"")
	want := "a=\"x\"\nb!~\"y.*\"\nc=\"q\\\"uote\""
	if err != nil || got != want {
		t.Fatalf("Canonical = %q, %v, want %q", got, err, want)
	}
	again, err := alertmatch.Canonical(got)
	if err != nil || again != got {
		t.Fatalf("Canonical is not idempotent: %q, %v", again, err)
	}
	if _, err := alertmatch.Canonical("nonsense"); err == nil {
		t.Error("Canonical accepted nonsense")
	}
	if s, err := alertmatch.Canonical(" \n "); err != nil || s != "" {
		t.Errorf("empty = %q, %v", s, err)
	}
}

func TestAMatcherPrintsAsItsCanonicalLine(t *testing.T) {
	m := rules(t, `instance =~ "a\tb"`)
	if len(m) != 1 || m[0].String() != `instance=~"a\tb"` {
		t.Fatalf("String = %v", m)
	}
}
