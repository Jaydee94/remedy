# Alertmanager source (cycle 4a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Alerts of an Alertmanager become incidents within one poll interval, mapped to a host by rules kept at the host; the Setup page configures the source (address, sealed token, maximum age of an alert for automatic diagnosis) and shows its state. Nothing diagnoses these incidents yet (cycle 4b).

**Architecture:** A read-only client (`internal/alertmanager`, transport rules of `internal/kube`) feeds a new runner in `internal/signals` with its own loop, which maps each alert to a host with Alertmanager-style matchers (`internal/alertmatch`) and applies it to the incident engine (`Observe`). The settings registry gets a text kind and the first secret kind (sealed with the master key). Migration 013 adds `hosts.matchers` and `incidents.host_id`.

**Tech Stack:** Go (stdlib, `modernc.org/sqlite`, `regexp` RE2), React 19 + TypeScript with `node --test` for pure logic.

**Spec:** `docs/specs/2026-10-16-alertmanager-4-design.md` (sections 1 to 6, 9, 10 and 11 items 2 to 7; sections 7 and 8, the responder and the approval expiry, are plan 4b). Parents: the settings spec `docs/specs/2026-10-14-settings-3-design.md`, the signals spec `docs/specs/2026-10-05-phase-2d-signals-design.md`.

## Global Constraints

- Everything in the repo is English: code, comments, docs, commit messages, UI copy. Go is stdlib-first; **no new dependency**.
- Run `gofmt -w` on every Go file you write before you commit (`make check` fails on an unformatted file).
- A new file name must be free before you create it (`command ls <path>` first).
- `Store.inTx` bodies use only the `tx`.
- Timestamps in SQLite have nine fractional digits (`formatTS`/`parseTS`); every state change writes its `activity` entry in the same transaction.
- The Alertmanager client is read-only: every exported method of `Client` is a `Get*` or `List*` (a test enforces it), the transport refuses every method but `GET` and `HEAD` before it is sent, no redirects, no proxy; the answer is limited to 8 MB; an answer that is not a JSON array is an error, never a partial list.
- **A fetch that failed, timed out, was cut off or was not a complete array resolves nothing.** Only a complete, successful fetch resolves the incidents of the source that are absent from it.
- At most **50 new incidents per source and cycle**.
- Alert text (labels, annotations, titles) is untrusted: before it is stored it is bounded (at most 30 labels with keys of at most 64 and values of at most 300 characters; at most 20 annotations with values of at most 1000; a title of one line of at most 200 characters) and redacted with `redact.Redact`. It is shown as text only.
- **The Alertmanager token never appears** in a response of the API, an activity entry, a log line, an error or the database in plaintext. It is sealed with `secret.Key.Seal` and the additional data `settings/alertmanager_token`; the snapshot of the reader keeps only the sealed bytes and a hint (`…` plus its last four characters, or `…` alone for a token shorter than 12 characters).
- The Alertmanager address has no userinfo, no query and no fragment (so the Setup page may show it), at most 2048 characters, scheme `http` or `https`, a trailing slash removed.
- Host rules: Alertmanager matcher syntax, one per line, `label op "value"` with `=`, `!=`, `=~`, `!~`; the value is quoted; a regex is RE2 and anchored to the whole value; a missing label counts as the empty string; **all lines of a host must hold**; a host with no lines never matches; at most 10 lines of at most 200 characters.
- Automatic diagnosis is decided **once, when an incident opens**: severity `critical` or `warning` **and** `startsAt` at most `alertmanager_auto_max_age` (default 1 hour, at least 1 minute) ago.
- UI: a button that starts a request keeps the keyboard focus (`aria-disabled` plus a guard, not `disabled`); `web/tsconfig.app.json` has `erasableSyntaxOnly` and `verbatimModuleSyntax` (no enums, `import type`); modules in `web/src/*.ts` import relatively with `.ts` extensions.
- Commit messages end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Pull request title: `feat: Alertmanager source and host rules (cycle 4a)`.

## Review Focus

1. **Alert text as an attack surface:** control characters, newlines, huge values, many labels, a value that looks like a secret must come out bounded, one-line (titles) and redacted (Task 7 tests).
2. **A partial or failed answer must never resolve incidents** (not-an-array, truncated, 500, unreachable, size limit) (Tasks 5 and 7 tests).
3. **The token on every surface:** API, activity, logs, errors, database file, a wrong master key (Task 3 walk).
4. **Host rules at the edges:** a missing label, `!=""`, anchoring, an invalid regex that only compiles when wrapped, ties, a deleted host, a rule edited between cycles (Tasks 1, 4, 7 tests).
5. **Storm and idempotency:** 60 alerts at once, the same cycle twice, a source switched off in the middle (Task 7 tests).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/alertmatch/alertmatch.go` (new) | `Parse`, `Matches`, `Canonical`, `Matcher` |
| `internal/settings/value.go`, `registry.go`, `settings.go` | text and secret kinds, three new keys, `WithKey`, `Secret`, `Text` |
| `internal/store/migrations/013_alert_hosts.sql` (new), `internal/store/hosts.go`, `internal/store/incidents.go` | `hosts.matchers`, `incidents.host_id`, `TouchIncidentSignal`, `ListUnresolvedBySource` |
| `internal/sshhost/validate.go`, `internal/server/hosts.go` | the `matchers` field of a host |
| `internal/alertmanager/client.go` (new), `internal/alertmanager/alertmanagertest/` (new) | the client and the fake |
| `internal/incident/incident.go` | `ObserveSignal`, `HostID` on an observation, `ResolveAbsent` |
| `internal/signals/` (new) | `Signal`, `Source`, the Alertmanager source, `Runner`, host mapping |
| `internal/config/config.go`, `internal/app/app.go`, `cmd/remedy-server/main.go`, `internal/server/signals.go` (new), `internal/server/incidents.go` | wiring and `GET /api/signals` |
| `web/src/api.ts`, `settings.ts`, `signals.ts` (new), `incidents.ts`, `components/setup/SettingsSection.tsx`, `components/hosts/HostForm.tsx`, `IncidentThreadPage.tsx` | the Setup block, the host rules field, the host on an incident |

---

### Task 1: The host rules (`internal/alertmatch`)

**Files:**
- Create: `internal/alertmatch/alertmatch.go`, `internal/alertmatch/alertmatch_test.go`
- Modify: `docs/specs/2026-10-16-alertmanager-4-design.md` (two small corrections, Step 6)

**Interfaces:**
- Produces (used by Tasks 4 and 7):
  - `const MaxLines = 10`, `const MaxLineLen = 200`.
  - `type Op string` with `OpEq`, `OpNe`, `OpRe`, `OpNre`.
  - `type Matcher struct { Label string; Op Op; Value string; ... }` with `func (m Matcher) String() string` (the canonical line).
  - `type ParseError struct { Line int; Msg string }` (implements `error`; text `line N: msg`).
  - `func Parse(text string) ([]Matcher, error)`: the empty text gives `nil, nil`.
  - `func Matches(rules []Matcher, labels map[string]string) bool`: false for no rules.
  - `func Canonical(text string) (string, error)`: the lines in canonical form, joined by `\n`.

- [ ] **Step 1: Check the name is free**

Run: `command ls internal/alertmatch`
Expected: `ls` reports that the directory does not exist.

- [ ] **Step 2: Write the failing tests**

Create `internal/alertmatch/alertmatch_test.go`:

```go
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
		"unquoted value":         `instance=ugreen-nas`,
		"label starts with a digit": `1abc="x"`,
		"no label":               `="x"`,
		"no operator":            `instance "x"`,
		"double equals":          `instance=="x"`,
		"text after the quote":   `a="x" junk`,
		"unknown escape":         `a="\q"`,
		"unterminated":           `a="x`,
		"regex does not compile": `a=~"("`,
		"regex that compiles only when wrapped": `a=~"a)(b"`,
		"label with a dash":      `a-b="x"`,
		"line too long":          long,
		"eleven rules":           eleven,
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
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/alertmatch`
Expected: FAIL, the package does not compile (`undefined: alertmatch.Parse`).

- [ ] **Step 4: Write `internal/alertmatch/alertmatch.go`**

```go
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
```

- [ ] **Step 5: Run the tests and format**

Run: `gofmt -w internal/alertmatch && go test ./internal/alertmatch && go vet ./internal/alertmatch`
Expected: PASS. (The test file's hand-aligned map literal is reformatted by `gofmt -w`.)

- [ ] **Step 6: Two corrections of the spec**

In `docs/specs/2026-10-16-alertmanager-4-design.md`:
1. Section 4 (settings table) and section 12 (order of work): the key `auto_approval_timeout` belongs to **plan 4b** (it has no consumer before the expiry exists). In the table add to its row "(registered in plan 4b, with its consumer)"; in section 12 item 1 write "the three keys `alertmanager_url`, `alertmanager_token` and `alertmanager_auto_max_age`" instead of "the four keys", and add `auto_approval_timeout` to item 2.
2. Section 9, the incident routes: the fields `hostId`, `hostName` and `alsoMatches` stay as written; add that `alsoMatches` is read from `details.alsoMatches`.

- [ ] **Step 7: Commit**

```bash
git add internal/alertmatch docs/specs/2026-10-16-alertmanager-4-design.md
git commit -m "feat: the rules that map an alert to a host

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: A text kind and two Alertmanager settings

**Files:**
- Modify: `internal/settings/value.go`, `internal/settings/registry.go`, `internal/settings/settings.go`, `internal/settings/registry_test.go`, `internal/settings/settings_test.go`, `web/src/api.ts`, `web/src/settings.ts`, `web/src/settings.test.ts`
- Modify (tests that count the registry): every test that asserts the number of settings (for example `internal/server/settings_test.go`: `len(page.Settings) != 6`); change the number and say why in a comment.

**Interfaces:**
- Consumes: Task 1 of cycle 3 (`Spec`, `Layer`, `Value`, `Settings`).
- Produces:
  - `KindText Kind = "text"`; `Value.Str string` (the field of a text value; the method `Value.Text(Kind)` stays).
  - Keys `AlertmanagerURL = "alertmanager_url"`, `AlertmanagerAutoMaxAge = "alertmanager_auto_max_age"`.
  - `Layer.Text(key) string`, `(*Settings).Text(key) string`.
  - A spec may have an empty `Env`: then `EnvLayer` and `EnvValue` skip it.
  - Web: `Setting.kind` may be `'text'`.

- [ ] **Step 1: Write the failing tests**

In `internal/settings/registry_test.go`: in `TestEveryDefaultPassesItsOwnBoundsAndSurvivesTheJSONRoundTrip` remove `s.Env == "" ||` from the condition that checks the fields are not empty (some settings have no environment variable), and append:

```go
func TestTheAlertmanagerAddressIsCheckedAndWrittenWithoutATrailingSlash(t *testing.T) {
	s := spec(t, settings.AlertmanagerURL)
	good := map[string]string{
		"":                               "",
		"http://alertmanager:9093":       "http://alertmanager:9093",
		"https://am.example/":            "https://am.example",
		"http://10.0.0.5:9093/base/path/": "http://10.0.0.5:9093/base/path",
		"  http://x  ":                   "http://x",
	}
	for in, want := range good {
		v, err := s.ParseText(in)
		if err != nil || v.Str != want {
			t.Errorf("%q: %q, %v, want %q", in, v.Str, err, want)
		}
	}
	for _, in := range []string{
		"ftp://x", "alertmanager:9093", "http://", "http://user@host", "http://user:pw@host", "http://h?x=1", "http://h#frag",
		"http://h space", "http://h\n", "javascript:alert(1)", "http://" + strings.Repeat("a", 2100),
	} {
		if _, err := s.ParseText(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func TestTheTextJSONFormIsAString(t *testing.T) {
	s := spec(t, settings.AlertmanagerURL)
	v, err := s.ParseJSON(json.RawMessage(`"http://a:9093/"`))
	if err != nil || v.Str != "http://a:9093" {
		t.Fatalf("%+v, %v", v, err)
	}
	for _, raw := range []string{`null`, `5`, `["a"]`, ``, `{}`} {
		if _, err := s.ParseJSON(json.RawMessage(raw)); err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}
	if got := string((settings.Value{Str: "http://a"}).JSON(settings.KindText)); got != `"http://a"` {
		t.Errorf("JSON = %s", got)
	}
	if got := (settings.Value{}).Text(settings.KindText); got != "none" {
		t.Errorf("empty text = %q", got)
	}
}

func TestTheMaximumAgeIsADurationOfAtLeastAMinute(t *testing.T) {
	s := spec(t, settings.AlertmanagerAutoMaxAge)
	if _, err := s.ParseText("1h"); err != nil {
		t.Error(err)
	}
	if _, err := s.ParseText("59s"); err == nil {
		t.Error("59s was accepted")
	}
	if s.Default.Dur != time.Hour || s.Env != "" {
		t.Errorf("default = %v, env = %q", s.Default.Dur, s.Env)
	}
}

func TestEnvironmentVariablesOfTheTextKind(t *testing.T) {
	l, err := settings.EnvLayer(env(map[string]string{"REMEDY_ALERTMANAGER_URL": "http://am:9093/"}))
	if err != nil || l.Text(settings.AlertmanagerURL) != "http://am:9093" || len(l) != 1 {
		t.Fatalf("%v, %v", l, err)
	}
	_, err = settings.EnvLayer(env(map[string]string{"REMEDY_ALERTMANAGER_URL": "http://u:p@am"}))
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_ALERTMANAGER_URL must be an http or https address") {
		t.Fatalf("error = %v", err)
	}
	if _, set, err := settings.EnvValue(settings.AlertmanagerAutoMaxAge, env(nil)); err != nil || set {
		t.Errorf("a setting without a variable: set = %v, err = %v", set, err)
	}
}
```

In `internal/settings/settings_test.go` append:

```go
func TestATextSettingIsSetAndReadBack(t *testing.T) {
	ctx := context.Background()
	s, st := newReader(t, nil)
	if s.Text(settings.AlertmanagerURL) != "" {
		t.Fatal("the default address is not empty")
	}
	e, err := s.Set(ctx, settings.AlertmanagerURL, json.RawMessage(`"http://am:9093/"`))
	if err != nil || e.Source != settings.SourceSet || s.Text(settings.AlertmanagerURL) != "http://am:9093" {
		t.Fatalf("%+v, %v", e, err)
	}
	if got := activityTexts(t, st); len(got) != 1 || got[0] != "Setting alertmanager_url changed from none (default) to http://am:9093 (set here)" {
		t.Fatalf("activity = %q", got)
	}
	if _, err := s.Set(ctx, settings.AlertmanagerURL, json.RawMessage(`"http://u:p@am"`)); err == nil {
		t.Error("an address with a user name was accepted")
	}
}
```

Run: `go test ./internal/settings`
Expected: FAIL to compile (`settings.AlertmanagerURL` undefined).

- [ ] **Step 2: `value.go`**

Add the kind and the field, and teach `JSON` and `Text` about it:

```go
	KindText     Kind = "text"
```
```go
type Value struct {
	Dur  time.Duration
	Num  int
	List []string
	Str  string // a text value
}
```
In `JSON`, add before the `default:` case:
```go
	case KindText:
		b, _ = json.Marshal(v.Str)
```
In `Text`, add after the `KindNumber` case:
```go
	case KindText:
		if v.Str == "" {
			return "none"
		}
		return v.Str
```

- [ ] **Step 3: `registry.go`**

1. Constants: `AlertmanagerURL = "alertmanager_url"`, `AlertmanagerAutoMaxAge = "alertmanager_auto_max_age"`.
2. `Spec` gets `normalize func(Value) Value`.
3. Two entries at the end of `specs` (after `RunTimeout`):

```go
	{
		Key: AlertmanagerURL, Group: "Alertmanager", Label: "Alertmanager address", Env: "REMEDY_ALERTMANAGER_URL",
		Kind: KindText, Rule: "An http or https address without a user name, for example http://alertmanager:9093. Empty: I do not ask Alertmanager.",
		Effect:    "Takes effect at the next check.",
		problem:   "must be an http or https address without a user name, a query or a fragment, for example http://alertmanager:9093",
		check:     func(v Value) bool { return validAlertmanagerURL(v.Str) },
		normalize: func(v Value) Value { v.Str = strings.TrimRight(v.Str, "/"); return v },
	},
	{
		Key: AlertmanagerAutoMaxAge, Group: "Alertmanager", Label: "Oldest alert I diagnose on my own",
		Kind: KindDuration, Default: Value{Dur: time.Hour}, Rule: "At least 1m, for example 1h. An older alert is shown, and a click diagnoses it.",
		Effect:  "Takes effect when an incident opens.",
		problem: "must be a duration of at least 1m, for example 1h",
		check:   func(v Value) bool { return v.Dur >= time.Minute },
	},
```
4. The validator (new function, same file; add `net/url` to the imports):

```go
// validAlertmanagerURL says whether s is empty or an http or https address that carries no credential, query or fragment.
func validAlertmanagerURL(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 2048 || strings.ContainsAny(s, " \t\r\n\x00") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return u.User == nil && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery
}
```
5. `ParseText`: add the case before `default:` and apply `normalize` after the check:

```go
	case KindText:
		v.Str = strings.TrimSpace(text)
```
and at the end of `ParseText`, before `return v, nil`:
```go
	if s.normalize != nil {
		v = s.normalize(v)
	}
```
6. `ParseJSON`: add before the list code:

```go
	case KindText:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || isNull {
			return Value{}, fmt.Errorf("%s (write it as a string)", s.problem)
		}
		return s.ParseText(text)
```
7. `Layer` gets `func (l Layer) Text(key string) string { return l.value(key).Str }`.
8. `EnvLayer` and `EnvValue` skip a spec whose `Env` is empty (`if s.Env == "" { continue }` in the loop; `return Value{}, false, nil` in `EnvValue`).

- [ ] **Step 4: `settings.go`**

Add: `// Text is the value of a text setting.` `func (s *Settings) Text(key string) string { return s.must(key).Value.Str }`.

- [ ] **Step 5: Update the tests that count the settings**

Run: `go test ./internal/settings ./internal/server ./internal/app ./internal/config`
Fix every test that asserts the number of settings or the order of the first ones (the registry grew from 6 to 8). Keep the assertion, change the number, add a comment such as `// 6 of cycle 3 and 2 of the Alertmanager source`.
Expected after the fix: PASS.

- [ ] **Step 6: The web side of the text kind**

`web/src/api.ts`: `Setting.kind` becomes `'duration' | 'number' | 'list' | 'text'`.
`web/src/settings.ts`: `valueText` returns `'none'` for an empty string value of kind `text`; `inputText` returns the string; `parseInput` for kind `text` returns `{ ok: true, value: t }` (trimmed; the server has the last word). In `web/src/settings.test.ts` add cases for the three functions (an empty text shows as `none`, the input shows the string, `parseInput(text, '  http://x  ')` is `{ ok: true, value: 'http://x' }`, and an empty input is `{ ok: true, value: '' }`).

Run: `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
Expected: PASS.

- [ ] **Step 7: Format and commit**

Run: `gofmt -w internal/settings && go vet ./... && go test ./internal/settings ./internal/server ./internal/app ./internal/config`
Expected: PASS.

```bash
git add internal/settings internal/server internal/app internal/config web
git commit -m "feat: a text kind of setting and the first Alertmanager settings

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: A secret kind of setting (the Alertmanager token)

**Files:**
- Modify: `internal/settings/value.go`, `internal/settings/registry.go`, `internal/settings/settings.go`, `internal/settings/registry_test.go`, `internal/server/settings.go`, `web/src/api.ts`
- Create: `internal/settings/secret_test.go`, `internal/server/settings_secret_test.go`

**Interfaces:**
- Consumes: Task 2 (`KindText`, `Value.Str`).
- Produces:
  - `KindSecret Kind = "secret"`, key `AlertmanagerToken = "alertmanager_token"`.
  - `type Option func(*Settings)`, `func WithKey(k secret.Key) Option`, and `New(ctx, st, env, log, opts ...Option)` (existing callers keep compiling).
  - `Entry.Hint string`; a secret entry has a zero `Value`, `Source` `set here` while a token is stored, `Problem` when it cannot be opened.
  - `func (s *Settings) Secret(key string) (secret.Value, error)`; `var ErrNotSet`, `var ErrCannotOpen`, `var ErrNoKey`.
  - API: `settingView.Hint` (`json:"hint,omitempty"`); a secret has `value: null`.
  - Web: `Setting.kind` may be `'secret'`, `Setting.hint?: string`.

- [ ] **Step 1: Write the failing tests**

Create `internal/settings/secret_test.go` (package `settings_test`; it uses `newReader`-like helpers; because `newReader` has no key, write a keyed one):

```go
package settings_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

const plainToken = "tok-0123456789abcdef-SECRET"

func key(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newKeyed(t *testing.T, k secret.Key, logTo io.Writer) (*settings.Settings, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(logTo, nil)), settings.WithKey(k)), st, path
}

func TestASecretIsSealedAndOpenedOnDemand(t *testing.T) {
	var logs bytes.Buffer
	s, st, path := newKeyed(t, key(t, 5), &logs)
	ctx := context.Background()

	if _, err := s.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrNotSet) {
		t.Fatalf("before: %v", err)
	}
	e, err := s.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"`+plainToken+`"`))
	if err != nil || e.Source != settings.SourceSet || e.Hint != "…CRET" {
		t.Fatalf("entry = %+v, %v", e, err)
	}
	got, err := s.Secret(settings.AlertmanagerToken)
	if err != nil || got.Reveal() != plainToken {
		t.Fatalf("Secret = %q, %v", got.Reveal(), err)
	}
	if got.String() != "***" {
		t.Error("the secret value prints its content")
	}

	// A token never reaches the activity, the logs, the entries or the database file.
	log, _ := st.ListActivity(ctx, store.ActivityQuery{})
	var everything strings.Builder
	for _, a := range log {
		everything.WriteString(a.Summary + string(a.Data))
	}
	for _, en := range s.Entries() {
		b, _ := json.Marshal(en.Value.JSON(en.Spec.Kind))
		everything.WriteString(string(b) + en.Hint + en.Problem)
	}
	everything.WriteString(logs.String())
	if strings.Contains(everything.String(), plainToken) || strings.Contains(everything.String(), "0123456789abcdef") {
		t.Fatalf("the token is in the activity, the entries or the logs:\n%s", everything.String())
	}
	for _, f := range []string{path, path + "-wal"} {
		if raw, err := os.ReadFile(f); err == nil && bytes.Contains(raw, []byte(plainToken)) {
			t.Fatalf("the token is in %s in plaintext", f)
		}
	}
	if len(log) != 1 || log[0].Summary != "Setting alertmanager_token changed" || string(log[0].Data) != `{"key":"alertmanager_token"}` {
		t.Fatalf("activity = %+v", log)
	}
}

func TestAShortTokenShowsNoCharactersOfItself(t *testing.T) {
	s, _, _ := newKeyed(t, key(t, 5), io.Discard)
	e, err := s.Set(context.Background(), settings.AlertmanagerToken, json.RawMessage(`"short-token"`))
	if err != nil || e.Hint != "…" {
		t.Fatalf("hint = %q, %v", e.Hint, err)
	}
}

func TestASecretIsRemovedWithoutAValueInTheActivity(t *testing.T) {
	s, st, _ := newKeyed(t, key(t, 5), io.Discard)
	ctx := context.Background()
	_, _ = s.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"`+plainToken+`"`))
	e, err := s.Delete(ctx, settings.AlertmanagerToken)
	if err != nil || e.Source != settings.SourceDefault || e.Hint != "" {
		t.Fatalf("entry = %+v, %v", e, err)
	}
	if _, err := s.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrNotSet) {
		t.Fatalf("after removal: %v", err)
	}
	got := activityTexts(t, st) // newest first
	if len(got) != 2 || got[0] != "Setting alertmanager_token removed" {
		t.Fatalf("activity = %q", got)
	}
}

func TestASecretThatTheMasterKeyDoesNotOpenIsAProblem(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := settings.New(ctx, st, nil, quiet, settings.WithKey(key(t, 5)))
	if _, err := first.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"`+plainToken+`"`)); err != nil {
		t.Fatal(err)
	}
	other := settings.New(ctx, st, nil, quiet, settings.WithKey(key(t, 6)))
	e, _ := other.Entry(settings.AlertmanagerToken)
	if e.Problem == "" || e.Source != settings.SourceSet || strings.Contains(e.Problem, plainToken) {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := other.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrCannotOpen) {
		t.Fatalf("Secret with the wrong key: %v", err)
	}
	// A reader without any key cannot open it either, and cannot store one.
	none := settings.New(ctx, st, nil, quiet)
	if _, err := none.Secret(settings.AlertmanagerToken); !errors.Is(err, settings.ErrCannotOpen) {
		t.Fatalf("Secret without a key: %v", err)
	}
	if _, err := none.Set(ctx, settings.AlertmanagerToken, json.RawMessage(`"x"`)); !errors.Is(err, settings.ErrNoKey) {
		t.Fatalf("Set without a key: %v", err)
	}
}

func TestASecretValueIsCheckedAndNeverEchoed(t *testing.T) {
	s, _, _ := newKeyed(t, key(t, 5), io.Discard)
	for _, raw := range []string{`""`, `null`, `5`, `"leak-me\nplease"`, `"leak-me\u0001please"`, `"` + strings.Repeat("leak-me", 700) + `"`} {
		_, err := s.Set(context.Background(), settings.AlertmanagerToken, json.RawMessage(raw))
		var invalid *settings.InvalidError
		if !errors.As(err, &invalid) {
			t.Errorf("%.30s: error = %v", raw, err)
			continue
		}
		if strings.Contains(invalid.Message, "leak-me") {
			t.Errorf("the message echoes the value: %q", invalid.Message)
		}
	}
}

func TestAStoredSecretThatIsNotTheExpectedShapeIsAProblem(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.SetSetting(ctx, settings.AlertmanagerToken, `"just a string"`, store.NewActivity{Kind: store.KindSettingChanged, Summary: "x"})
	s := settings.New(ctx, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), settings.WithKey(key(t, 5)))
	e, _ := s.Entry(settings.AlertmanagerToken)
	if e.Problem == "" || e.Source != settings.SourceDefault {
		t.Fatalf("entry = %+v", e)
	}
}
```

(`activityTexts` and `newReader` exist in `settings_test.go` of the same package.) In `registry_test.go`, `TestEveryDefaultPassesItsOwnBoundsAndSurvivesTheJSONRoundTrip`: skip the round trip for `s.Kind == settings.KindSecret` (keep the check that Label, Group, Rule and Effect are not empty).

Create `internal/server/settings_secret_test.go` (package `server_test`, uses `newSettingsEnv` of `claimtimeout_test.go`; give it a key through the `tune` argument by building a keyed reader: extend `newSettingsEnv` is not needed, build the env with a keyed `settings.New` in the test):

```go
package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestTheTokenIsWrittenOnceAndNeverComesBack(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	k, _ := secret.ParseKey("BQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU=")
	set := settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), settings.WithKey(k))
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	c := e.adminClient(t, true)

	const token = "tok-0123456789abcdef-SECRET"
	resp := e.do(t, c, http.MethodPut, "/api/settings/alertmanager_token", `{"value":"`+token+`"}`, true)
	body := string(mustReadAll(t, resp))
	if resp.StatusCode != http.StatusOK || strings.Contains(body, token) || !strings.Contains(body, `"hint":"…CRET"`) || !strings.Contains(body, `"value":null`) || !strings.Contains(body, `"kind":"secret"`) {
		t.Fatalf("PUT = %d %s", resp.StatusCode, body)
	}
	for _, path := range []string{"/api/settings", "/api/activity"} {
		got := string(mustReadAll(t, e.do(t, c, http.MethodGet, path, "", false)))
		if strings.Contains(got, token) || strings.Contains(got, "0123456789abcdef") {
			t.Errorf("%s shows the token: %s", path, got)
		}
	}
	bad := e.do(t, c, http.MethodPut, "/api/settings/alertmanager_token", `{"value":"bad\nvalue-with-secret-part"}`, true)
	if msg := string(mustReadAll(t, bad)); bad.StatusCode != http.StatusBadRequest || strings.Contains(msg, "secret-part") {
		t.Errorf("a refused token: %d %s", bad.StatusCode, msg)
	}
	if got := e.do(t, c, http.MethodDelete, "/api/settings/alertmanager_token", "", true).StatusCode; got != http.StatusNoContent {
		t.Errorf("DELETE = %d", got)
	}
}
```

Run: `go test ./internal/settings ./internal/server -run 'Secret|Token'`
Expected: FAIL to compile (`settings.AlertmanagerToken` undefined).

- [ ] **Step 2: `value.go` and `registry.go`**

`value.go`: add `KindSecret Kind = "secret"` to the kinds. In `JSON` add `case KindSecret: b = []byte("null")`; in `Text` add `case KindSecret: return "***"`.

`registry.go`:
1. Constant `AlertmanagerToken = "alertmanager_token"`.
2. Entry in `specs` (between the URL and the maximum age, so the page shows address, token, age):

```go
	{
		Key: AlertmanagerToken, Group: "Alertmanager", Label: "Alertmanager token",
		Kind: KindSecret, Rule: "Written once and never shown again. Empty: I send no token.",
		Effect:  "Takes effect at the next request.",
		problem: "must be 1 to 4096 characters without a line break or a control character",
		check:   func(v Value) bool { return validSecretText(v.Str) },
	},
```
3. `validSecretText(s string) bool`: length 1 to 4096 bytes, no rune below `0x20` or equal to `0x7f`.
4. `ParseText`: `case KindSecret: v.Str = strings.TrimSpace(text)`. `ParseJSON`: same as the text case (a string, `null` refused) with the hint "(write it as a string)".
5. `EnvLayer`/`EnvValue` need no change (empty `Env`).

- [ ] **Step 3: `settings.go`**

1. Fields on `Settings`: `key secret.Key` and `haveKey bool`; `type Option func(*Settings)`; `func WithKey(k secret.Key) Option { return func(s *Settings) { s.key, s.haveKey = k, true } }`; `New(ctx, st, env, log, opts ...Option)` applies the options before the first `reload`.
2. `Entry` gets `Hint string`; `snapshot` gets `sealed map[string][]byte` (made in `build`).
3. Errors:

```go
var (
	// ErrNotSet means a secret has no value. ErrCannotOpen means it has one that cannot be opened (a changed master key, a
	// damaged value, or a reader without a key). ErrNoKey means a secret cannot be stored because the reader has no master key.
	ErrNotSet     = errors.New("settings: the secret is not set")
	ErrCannotOpen = errors.New("settings: the stored secret cannot be opened")
	ErrNoKey      = errors.New("settings: no master key to seal a secret with")
)
```
4. In `build`, branch for `sp.Kind == KindSecret` **before** the generic parse:

```go
		if sp.Kind == KindSecret {
			e := Entry{Spec: sp, Source: SourceDefault, Fallback: SourceDefault}
			if raw, ok := stored[sp.Key]; ok {
				s.buildSecret(&e, snap, raw)
				delete(stored, sp.Key)
			}
			snap.byKey[sp.Key] = e
			continue
		}
```
and

```go
// storedSecret is the stored form of a secret: the sealed token and a hint that shows its last characters.
type storedSecret struct {
	Sealed string `json:"sealed"`
	Hint   string `json:"hint"`
}

func aadFor(key string) string { return "settings/" + key }

func (s *Settings) buildSecret(e *Entry, snap *snapshot, raw string) {
	var st storedSecret
	sealed, err := base64.StdEncoding.DecodeString("")
	if json.Unmarshal([]byte(raw), &st) == nil {
		sealed, err = base64.StdEncoding.DecodeString(st.Sealed)
	} else {
		err = errors.New("not the expected shape")
	}
	if err != nil || len(sealed) == 0 {
		e.Problem = "The stored token cannot be used. Enter it again."
		s.log.Warn("ignoring a stored secret that is no longer valid", "key", e.Spec.Key)
		return
	}
	snap.sealed[e.Spec.Key] = sealed
	e.Source, e.Hint = SourceSet, st.Hint
	if !s.haveKey {
		e.Problem = "No master key is available, so the stored token cannot be opened."
		return
	}
	if _, err := s.key.Open(sealed, aadFor(e.Spec.Key)); err != nil {
		e.Problem = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
		s.log.Warn("a stored secret cannot be opened", "key", e.Spec.Key)
	}
}
```
(Tidy the first lines of `buildSecret`: declare `var sealed []byte` and `var err error` instead of the placeholder decode.)
5. `Secret`:

```go
// Secret opens a secret setting. It never touches the database; the sealed bytes are in the snapshot.
func (s *Settings) Secret(key string) (secret.Value, error) {
	if e := s.must(key); e.Spec.Kind != KindSecret {
		panic("settings: " + key + " is not a secret")
	}
	sealed, ok := s.snap.Load().sealed[key]
	if !ok {
		return secret.Value{}, ErrNotSet
	}
	if !s.haveKey {
		return secret.Value{}, ErrCannotOpen
	}
	plain, err := s.key.Open(sealed, aadFor(key))
	if err != nil {
		return secret.Value{}, ErrCannotOpen
	}
	return secret.NewValue(string(plain)), nil
}
```
6. `Set`: after the `InvalidError` check and the lock, branch `if sp.Kind == KindSecret { return s.setSecret(ctx, sp, v.Str) }`:

```go
func (s *Settings) setSecret(ctx context.Context, sp Spec, plain string) (Entry, error) {
	if !s.haveKey {
		return Entry{}, ErrNoKey
	}
	sealed, err := s.key.Seal([]byte(plain), aadFor(sp.Key))
	if err != nil {
		return Entry{}, err
	}
	b, _ := json.Marshal(storedSecret{Sealed: base64.StdEncoding.EncodeToString(sealed), Hint: hintOf(plain)})
	if err := s.store.SetSetting(ctx, sp.Key, string(b), secretActivity(sp, "changed")); err != nil {
		return Entry{}, err
	}
	if err := s.reloadCommitted(ctx); err != nil {
		return Entry{}, err
	}
	return s.must(sp.Key), nil
}

// secretActivity names the key and never a value.
func secretActivity(sp Spec, verb string) store.NewActivity {
	data, _ := json.Marshal(map[string]string{"key": sp.Key})
	return store.NewActivity{Kind: store.KindSettingChanged, Summary: "Setting " + sp.Key + " " + verb, Data: data}
}

// hintOf shows the last four characters of a long token and nothing of a short one.
func hintOf(plain string) string {
	r := []rune(plain)
	if len(r) < 12 {
		return "…"
	}
	return "…" + string(r[len(r)-4:])
}
```
`Set` must hold `s.mu` for the secret path as well (the `Lock` comes before the branch).
7. `Delete`: for a secret use `secretActivity(sp, "removed")` as the activity (instead of `s.activity(...)`); the rest is unchanged.
8. Imports: `encoding/base64`, `secret`.

- [ ] **Step 4: The API view**

`internal/server/settings.go`: `settingView` gets `Hint string json:"hint,omitempty"`; `settingViewOf` sets `Hint: e.Hint`. A secret's `Value` is already `null` (`Value.JSON(KindSecret)`).

- [ ] **Step 5: The web types**

`web/src/api.ts`: `Setting.kind` adds `'secret'`; `Setting` gets `hint?: string`; `value` may be `null` for a secret: `value: SettingValue | null` (fix the compile errors this causes in `settings.ts` by treating `null` as "none"/"" in `valueText` and `inputText`, and in `unchanged`). Add a test to `settings.test.ts`.

- [ ] **Step 6: Run everything**

Run: `gofmt -w internal/settings internal/server && go vet ./... && go test ./internal/settings ./internal/server -run 'Secret|Token|Setting|Limits|Refused|Bootstrap|Claim|Capab' && go test -race ./internal/settings && npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
Expected: PASS. Update every test that counts the settings (now 9: the registry has 6 + URL + token + maximum age).

- [ ] **Step 7: Commit**

```bash
git add internal/settings internal/server web
git commit -m "feat: the first secret setting, sealed with the master key

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Migration 013, the host rules field and the host of an incident

**Files:**
- Create: `internal/store/migrations/013_alert_hosts.sql`, `internal/store/migrate013_test.go`
- Modify: `internal/store/hosts.go`, `internal/store/incidents.go`, `internal/store/hosts_test.go`, `internal/store/incidents_test.go` (or the test file that covers `OpenIncident`; check with `command ls internal/store/*incident*_test.go`), `internal/sshhost/validate.go`, `internal/sshhost/validate_test.go`, `internal/server/hosts.go`, `internal/server/hosts_test.go`, `web/src/api.ts`

**Interfaces:**
- Consumes: Task 1 (`alertmatch.Canonical`).
- Produces:
  - Migration 013: `hosts.matchers TEXT NOT NULL DEFAULT ''`, `incidents.host_id INTEGER REFERENCES hosts (id) ON DELETE SET NULL`.
  - `store.HostFields.Matchers string`; `store.Incident.HostID int64` and `HostName string` (zero when no host); `store.NewIncident.HostID int64`.
  - `func (s *Store) TouchIncidentSignal(ctx context.Context, id, hostID int64, details json.RawMessage) error`: sets `last_seen`, `host_id` (NULL for 0) and `details` of an incident that is not resolved; `ErrNotFound` otherwise.
  - `func (s *Store) ListUnresolvedBySource(ctx context.Context, source string) ([]Incident, error)`: every incident of the source whose state is not `resolved` (ignored ones included), oldest first.
  - `sshhost.Fields.Matchers string` (validated and canonicalised in `Validate`, `FieldError` field `matchers`).
  - API: hosts carry `matchers` (read and `PATCH`/`POST`); an invalid rule is a 400 with the parse message.

- [ ] **Step 1: Check the names are free**

Run: `command ls internal/store/migrations/013_alert_hosts.sql internal/store/migrate013_test.go`
Expected: both reported missing.

- [ ] **Step 2: The migration**

`internal/store/migrations/013_alert_hosts.sql`:

```sql
-- The rules that map an alert to a host, and the host of an incident. matchers is Alertmanager matcher syntax, one rule per
-- line, canonical (checked by the server, never executed); empty means the host is never named by an alert. host_id is set by
-- the Alertmanager source when an alert's labels match a host; deleting the host leaves the incident without one.
ALTER TABLE hosts ADD COLUMN matchers TEXT NOT NULL DEFAULT '';
ALTER TABLE incidents ADD COLUMN host_id INTEGER REFERENCES hosts (id) ON DELETE SET NULL;
CREATE INDEX incidents_host ON incidents (host_id);
```

- [ ] **Step 3: Write the failing tests**

`internal/store/migrate013_test.go` (package `store_test`; model: `migrate011_test.go`): build a database from the migration files below `013`, insert a host with a credential (`host_credentials` columns: see `010_hosts.sql`), an `alertmanager` incident row (columns of `008_incident_sources.sql`: `source`, `key`, `title`, `state`, `conclusion`, `first_seen`, `last_seen`, with `severity`, `auto_diagnose`, `details` defaulting), an activity row, a note and a run bound to the host (`run_hosts`) and to the incident. Open the file with `store.Open` and assert:

```go
func TestMigration013AddsTheRulesAndTheHostOfAnIncidentAndKeepsEveryRow(t *testing.T) {
	// ... build the 012 database as TestMigration011... does (files with a name below "013"), insert the rows above, close it.
	s, err := store.Open(path)
	// 1. the host keeps every field and has Matchers == ""
	// 2. the incident keeps its title, state and notes and has HostID == 0 and HostName == ""
	// 3. s.UpdateHost(ctx, h.ID, fields with Matchers: `instance="x"`) stores it and GetHost returns it
	// 4. s.TouchIncidentSignal(ctx, in.ID, h.ID, json.RawMessage(`{"a":1}`)) sets HostID and HostName "nas", and details {"a":1}
	// 5. s.DeleteHost(ctx, h.ID): the incident still exists with HostID == 0; its notes, activity and run are intact (counted with SQL)
}
```
Write the SQL inserts and assertions fully (use `hostFields("nas")` from `hosts_test.go`, which sets `Matchers` through the new field once it exists).

Add to the store tests (`hosts_test.go`): a host created with `Matchers: "a=\"b\""` reads back with it, `UpdateHost` changes it; and (incident tests): 

```go
func TestAnIncidentOfAnotherSourceCanBelongToAHost(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	in, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceAlertmanager, Key: "A/1", Title: "t", Conclusion: "firing", HostID: h.ID},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil || in.HostID != h.ID || in.HostName != "nas" {
		t.Fatalf("%+v, %v", in, err)
	}
	if err := s.TouchIncidentSignal(ctx, in.ID, 0, json.RawMessage(`{"labels":{"a":"b"}}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.HostID != 0 || got.HostName != "" || string(got.Details) != `{"labels":{"a":"b"}}` || !got.LastSeen.After(in.LastSeen) && !got.LastSeen.Equal(in.LastSeen) {
		t.Fatalf("after the touch: %+v", got)
	}
	if err := s.ResolveIncident(ctx, in.ID, "cleared", store.NewActivity{Kind: store.KindIncidentResolved, Summary: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchIncidentSignal(ctx, in.ID, h.ID, json.RawMessage(`{}`)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("touching a resolved incident: %v", err)
	}
	// a GitHub incident never belongs to a host
	repo, _ := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if _, err := s.OpenIncident(ctx, store.NewIncident{RepoID: repo.ID, Ref: "pr:1", CheckName: "go", Conclusion: "failure", HostID: h.ID},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "o"}); !errors.Is(err, store.ErrInvalidIncident) {
		t.Errorf("a GitHub incident with a host: %v", err)
	}
}

func TestListUnresolvedBySourceIncludesIgnoredAndLeavesResolvedAndOtherSources(t *testing.T) {
	// open three alertmanager incidents (one resolved, one ignored) and one argocd incident; expect the open and the ignored
	// alertmanager ones, oldest first, and none of the others.
}
```
(Write the second test out in full: `IgnoreIncident(ctx, id, act)` and `ResolveIncident` exist; `store.ConnectionID` and `AddRepo` are used in `incidents_test.go`; for `AddRepo` you may first need a stored connection as in the existing tests, copy their setup.)

`internal/sshhost/validate_test.go`: add `TestMatchersAreValidatedAndWrittenCanonically` (a valid text is canonicalised in `f.Matchers`, an invalid one gives a `*FieldError` with field `matchers` and the parse message, the empty text is valid). `internal/server/hosts_test.go`: add a test that `PATCH /api/hosts/{id}` with `{"matchers":"instance =~ \"ugreen-nas.*\""}` answers 200 with `"matchers":"instance=~\"ugreen-nas.*\""`, that `{"matchers":"nonsense"}` answers 400 with a text starting `the alert rules: line 1:`, and that `GET /api/hosts/{id}` returns the stored rules (use the existing helpers of that file: `newHostEnv`, `e.do`).

Run: `go test ./internal/store ./internal/sshhost ./internal/server -run 'Migration013|Matchers|AnotherSource|Unresolved'`
Expected: FAIL to compile (`HostID`, `Matchers` undefined).

- [ ] **Step 4: The store**

`hosts.go`: `HostFields` gets `Matchers string`; `hostSelect` adds `h.matchers` **after** `h.enabled`; `scanHostEntry` scans `&h.Matchers` right after `&enabled`; `CreateHost` inserts and `UpdateHost` updates `matchers` (add the column to both SQL statements and their argument lists).

`incidents.go`: `Incident` gets

```go
	// HostID and HostName name the host an alert was mapped to, zero and empty when it was mapped to none (or the host is gone).
	HostID   int64
	HostName string
```
`NewIncident` gets `HostID int64`; `normalize` refuses a GitHub incident with a host (`bad("only an incident of another source can belong to a host")`). `incidentCols` ends with `..., i.run_id, i.host_id, h.name`; `incidentFrom` becomes
`FROM incidents i LEFT JOIN repos r ON r.id = i.repo_id LEFT JOIN hosts h ON h.id = i.host_id`; `scanIncident` scans `hostID sql.NullInt64, hostName sql.NullString` after `runID` and sets `in.HostID`, `in.HostName`. `OpenIncident` inserts `host_id` (`nullInt(n.HostID)`). Add:

```go
// TouchIncidentSignal records that a signal is still there: it moves last_seen and writes the host the signal maps to now (0 for
// none) and its current details. It changes an incident that is not resolved and returns ErrNotFound for any other.
func (s *Store) TouchIncidentSignal(ctx context.Context, id, hostID int64, details json.RawMessage) error {
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	if !json.Valid(details) || details[0] != '{' {
		return fmt.Errorf("%w: details must be a JSON object", ErrInvalidIncident)
	}
	return oneRow(s.db.ExecContext(ctx, `
		UPDATE incidents SET last_seen = ?, host_id = ?, details = ? WHERE id = ? AND state <> 'resolved'`,
		formatTS(time.Now()), nullInt(hostID), string(details), id))
}

// ListUnresolvedBySource returns the incidents of a source that are not resolved, ignored ones included, oldest first.
func (s *Store) ListUnresolvedBySource(ctx context.Context, source string) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+incidentFrom+
		` WHERE i.source = ? AND i.state <> 'resolved' ORDER BY i.first_seen, i.id`, source)
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}
```
Check that every other query that selects `incidentCols` still compiles and runs (`go test ./internal/store`); if a query joins another table aliased `h`, rename that alias.

- [ ] **Step 5: `sshhost` and the server**

`sshhost.Fields` gets `Matchers string`; in `Validate`, after the units:

```go
	canon, err := alertmatch.Canonical(f.Matchers)
	if err != nil {
		return &FieldError{"matchers", "the alert rules: " + err.Error()}
	}
	f.Matchers = canon
```
(import `github.com/Jaydee94/remedy/internal/alertmatch`). `internal/server/hosts.go`: `hostView` gets `Matchers string json:"matchers"` (set in `hostViewOf` from `h.Matchers`); `hostBody` gets `Matchers *string json:"matchers"`; `apply` sets `f.Matchers = *b.Matchers`; `checked` passes `Matchers: f.Matchers` into `sshhost.Fields` and copies the canonical text back (`f.Matchers = v.Matchers`).

`web/src/api.ts`: `Host.matchers: string`, `HostBody.matchers?: string`. Fix the TypeScript errors this causes in tests that build a `Host` (add `matchers: ''`).

- [ ] **Step 6: Run and commit**

Run: `gofmt -w internal/store internal/sshhost internal/server && go vet ./... && go test ./internal/store ./internal/sshhost ./internal/alertmatch && go test ./internal/server -run 'Host|Incident|Setting' && npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
Expected: PASS. (The focused server run, not the 25-minute race run of the whole package.)

```bash
git add internal/store internal/sshhost internal/server web
git commit -m "feat: host rules for alerts and the host of an incident (migration 013)

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The Alertmanager client and its fake

**Files:**
- Create: `internal/alertmanager/client.go`, `internal/alertmanager/client_test.go`, `internal/alertmanager/alertmanagertest/server.go`, `internal/alertmanager/alertmanagertest/testdata/home-server.json`, `internal/alertmanager/alertmanagertest/testdata/README.md`

**Interfaces:**
- Produces (used by Tasks 7 and 8):
  - `type Alert struct { Fingerprint string; Labels, Annotations map[string]string; StartsAt time.Time; GeneratorURL string; Status struct{ State string } }` with the JSON names of Alertmanager's v2 API (`fingerprint`, `labels`, `annotations`, `startsAt`, `generatorURL`, `status.state`).
  - `type Credentials func() (baseURL string, token secret.Value, err error)` (asked at every request).
  - `func New(creds Credentials, caFile string, opts ...Option) (*Client, error)`; `func WithLog(*slog.Logger) Option`.
  - `func (c *Client) ListAlerts(ctx context.Context) ([]Alert, error)`: only alerts whose `status.state` is `active`.
  - Errors: `ErrNotConfigured` (empty address), `ErrTooLarge`, `ErrNotAlerts`, `*APIError{Status int}`.
  - `alertmanagertest.New(t, token) *Server` with `URL`, `SetAlerts(...map[string]any)`, `SetRaw(status int, body string)`, `SetBody([]byte)`, `Requests() []Request`, `Request{Method, Path, Query, Auth string}`, and `alertmanagertest.Alert(fingerprint, name string, labels map[string]string, startsAt time.Time) map[string]any`.

- [ ] **Step 1: Check the names are free**

Run: `command ls internal/alertmanager`
Expected: `ls` reports that the directory does not exist.

- [ ] **Step 2: The fake**

`internal/alertmanager/alertmanagertest/server.go`:

```go
// Package alertmanagertest is a fake Alertmanager for tests: it answers GET /api/v2/alerts with what a test sets and records every
// request. testdata/home-server.json is an answer shaped like the one of the home server.
package alertmanagertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Request is what the fake saw.
type Request struct{ Method, Path, Query, Auth string }

// Server is a fake Alertmanager. With a token it answers 401 to a request that does not carry it.
type Server struct {
	URL string

	mu     sync.Mutex
	token  string
	status int
	body   []byte
	reqs   []Request
}

// New starts a fake that answers an empty list.
func New(t testing.TB, token string) *Server {
	t.Helper()
	s := &Server{token: token, status: http.StatusOK, body: []byte("[]")}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")})
	status, body, token := s.status, s.body, s.token
	s.mu.Unlock()
	if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if r.URL.Path != "/api/v2/alerts" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// SetAlerts makes the fake answer these alerts with status 200.
func (s *Server) SetAlerts(alerts ...map[string]any) {
	if alerts == nil {
		alerts = []map[string]any{}
	}
	b, _ := json.Marshal(alerts)
	s.SetBody(b)
}

// SetBody makes the fake answer 200 with exactly these bytes.
func (s *Server) SetBody(b []byte) {
	s.mu.Lock()
	s.status, s.body = http.StatusOK, b
	s.mu.Unlock()
}

// SetRaw makes the fake answer with a status and a body.
func (s *Server) SetRaw(status int, body string) {
	s.mu.Lock()
	s.status, s.body = status, []byte(body)
	s.mu.Unlock()
}

// Requests returns what the fake has seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// Alert builds one alert of the v2 API in the state active.
func Alert(fingerprint, name string, labels map[string]string, startsAt time.Time) map[string]any {
	l := map[string]string{"alertname": name}
	for k, v := range labels {
		l[k] = v
	}
	return map[string]any{
		"fingerprint": fingerprint, "labels": l, "annotations": map[string]string{"summary": name + " is firing"},
		"startsAt": startsAt.UTC().Format(time.RFC3339Nano), "endsAt": startsAt.Add(time.Hour).UTC().Format(time.RFC3339Nano),
		"generatorURL": "http://prometheus.example/graph?g0.expr=up", "receivers": []map[string]string{{"name": "blackhole"}},
		"status": map[string]any{"state": "active", "silencedBy": []string{}, "inhibitedBy": []string{}},
	}
}
```

`testdata/home-server.json`: an array of **four** alerts of the v2 format (the fields above, `updatedAt`, `receivers: [{"name":"blackhole"}]`): `Watchdog` (severity `none`), `NodeFilesystemAlmostFull` with `instance="ugreen-nas"`, `job="node-exporter"`, `severity="warning"`, `mountpoint="/volume1"`, `ContainerHighMemory` with `instance="ugreen-nas"`, `name="paperless-webserver"`, `severity="warning"`, and `KubePodCrashLooping` with `namespace="demo"`, `pod="web-5d9c-abc12"`, `severity="critical"`. Realistic fingerprints (16 hex characters), `startsAt` values in 2026. `testdata/README.md` says in two sentences that this file is **modelled on the v2 API's documentation and the home server's labels, not recorded from it**, and that recording the real answer is a step of the real run (plan 4b).

- [ ] **Step 3: Write the failing client tests**

`internal/alertmanager/client_test.go` (package `alertmanager_test`, plus one internal test file `guard_internal_test.go` in package `alertmanager` for the transport):

```go
package alertmanager_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/alertmanager/alertmanagertest"
	"github.com/Jaydee94/remedy/internal/secret"
)

func creds(url, token string) alertmanager.Credentials {
	return func() (string, secret.Value, error) { return url, secret.NewValue(token), nil }
}

func newClient(t *testing.T, c alertmanager.Credentials) *alertmanager.Client {
	t.Helper()
	cl, err := alertmanager.New(c, "")
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestListAlertsAsksForActiveAlertsAndSendsTheToken(t *testing.T) {
	fake := alertmanagertest.New(t, "tok")
	fake.SetAlerts(alertmanagertest.Alert("f1", "KubePodCrashLooping", map[string]string{"namespace": "demo"}, time.Now()))
	got, err := newClient(t, creds(fake.URL, "tok")).ListAlerts(context.Background())
	if err != nil || len(got) != 1 || got[0].Fingerprint != "f1" || got[0].Labels["alertname"] != "KubePodCrashLooping" || got[0].Status.State != "active" {
		t.Fatalf("%+v, %v", got, err)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != "/api/v2/alerts" ||
		reqs[0].Query != "active=true&inhibited=false&silenced=false" || reqs[0].Auth != "Bearer tok" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestNoTokenMeansNoAuthorizationHeader(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	if _, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reqs := fake.Requests(); len(reqs) != 1 || reqs[0].Auth != "" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestOnlyAlertsInTheStateActiveAreKept(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	silenced := alertmanagertest.Alert("f2", "A", nil, time.Now())
	silenced["status"] = map[string]any{"state": "suppressed"}
	fake.SetAlerts(alertmanagertest.Alert("f1", "A", nil, time.Now()), silenced)
	got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
	if err != nil || len(got) != 1 || got[0].Fingerprint != "f1" {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestTheAddressAndTheTokenAreAskedForAtEveryRequest(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	n := 0
	cl := newClient(t, func() (string, secret.Value, error) {
		n++
		return fake.URL, secret.NewValue("t" + string(rune('0'+n))), nil
	})
	_, _ = cl.ListAlerts(context.Background())
	_, _ = cl.ListAlerts(context.Background())
	reqs := fake.Requests()
	if len(reqs) != 2 || reqs[0].Auth != "Bearer t1" || reqs[1].Auth != "Bearer t2" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAnEmptyAddressIsNotConfigured(t *testing.T) {
	if _, err := newClient(t, creds("", "")).ListAlerts(context.Background()); !errors.Is(err, alertmanager.ErrNotConfigured) {
		t.Fatalf("error = %v", err)
	}
	boom := errors.New("cannot open the token")
	cl := newClient(t, func() (string, secret.Value, error) { return "", secret.Value{}, boom })
	if _, err := cl.ListAlerts(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
}

func TestAnAnswerThatIsNotAJSONArrayOfAlertsIsAnErrorAndNeverAPartialList(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	for name, body := range map[string]string{
		"object": `{"status":"success"}`, "null": `null`, "string": `"x"`, "number": `5`, "html": `<html>`, "empty": ``,
		"truncated array": `[{"fingerprint":"f1","labels":{"alertname":"A"},"status":{"state":"active"}},{"fingerprint":"f2"`,
		"array of strings": `["a","b"]`,
	} {
		fake.SetRaw(200, body)
		got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
		if err == nil || got != nil {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	fake.SetRaw(200, `{"x":1}`)
	if _, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background()); !errors.Is(err, alertmanager.ErrNotAlerts) {
		t.Errorf("an object: %v", err)
	}
}

func TestAnAnswerOverTheLimitIsAnError(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	big := make([]byte, 8<<20+10)
	for i := range big {
		big[i] = ' '
	}
	big[0] = '['
	fake.SetBody(big)
	if _, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background()); !errors.Is(err, alertmanager.ErrTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestAStatusOtherThan2xxIsAnAPIErrorWithoutTheBody(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	fake.SetRaw(500, `internal secret detail: tok-123456789`)
	_, err := newClient(t, creds(fake.URL, "tok-123456789")).ListAlerts(context.Background())
	var api *alertmanager.APIError
	if !errors.As(err, &api) || api.Status != 500 || strings.Contains(err.Error(), "internal secret detail") || strings.Contains(err.Error(), "tok-123456789") {
		t.Fatalf("error = %v", err)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere++ }))
	t.Cleanup(other.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	_, err := newClient(t, creds(redirecting.URL, "tok")).ListAlerts(context.Background())
	var api *alertmanager.APIError
	if !errors.As(err, &api) || api.Status != http.StatusFound || elsewhere != 0 {
		t.Fatalf("error = %v, requests to the other server = %d", err, elsewhere)
	}
}

func TestAnUnreachableServerIsAnErrorThatDoesNotShowTheToken(t *testing.T) {
	fake := alertmanagertest.New(t, "")
	url := fake.URL
	// closed port: start and close a server to get an address nobody listens on
	dead := httptest.NewServer(http.NotFoundHandler())
	url = dead.URL
	dead.Close()
	_, err := newClient(t, creds(url, "tok-SECRET-123456")).ListAlerts(context.Background())
	if err == nil || strings.Contains(err.Error(), "tok-SECRET-123456") {
		t.Fatalf("error = %v", err)
	}
}

func TestEveryExportedMethodOfTheClientIsAGetOrAList(t *testing.T) {
	typ := reflect.TypeOf(&alertmanager.Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("the method %s is neither a Get nor a List: the client must stay read-only", name)
		}
	}
}

func TestACAFileThatIsNotACertificateIsRefused(t *testing.T) {
	f := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(f, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := alertmanager.New(creds("http://x", ""), f); err == nil {
		t.Fatal("a CA file without a certificate was accepted")
	}
	if _, err := alertmanager.New(creds("http://x", ""), t.TempDir()+"/missing.pem"); err == nil {
		t.Fatal("a missing CA file was accepted")
	}
}

func TestTheAnswerOfTheHomeServerIsReadAsItIs(t *testing.T) {
	raw, err := os.ReadFile("alertmanagertest/testdata/home-server.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := alertmanagertest.New(t, "")
	fake.SetBody(raw)
	got, err := newClient(t, creds(fake.URL, "")).ListAlerts(context.Background())
	if err != nil || len(got) != 4 {
		t.Fatalf("%d alerts, %v", len(got), err)
	}
	var nas int
	for _, a := range got {
		if a.Labels["instance"] == "ugreen-nas" {
			nas++
		}
	}
	if nas != 2 {
		t.Errorf("%d alerts of the NAS, want 2", nas)
	}
}
```

`internal/alertmanager/guard_internal_test.go` (package `alertmanager`):

```go
package alertmanager

import (
	"net/http"
	"strings"
	"testing"
)

func TestTheTransportRefusesEveryMethodButGetAndHead(t *testing.T) {
	cl, err := New(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		req, _ := http.NewRequest(m, "http://127.0.0.1:1/api/v2/alerts", nil)
		if _, err := cl.http.Do(req); err == nil || !strings.Contains(err.Error(), "refusing to send") {
			t.Errorf("%s: error = %v", m, err)
		}
	}
}
```
(`New(nil, "")` must not call `creds`; only `ListAlerts` does.)

Run: `go test ./internal/alertmanager/...`
Expected: FAIL to compile.

- [ ] **Step 4: Write `internal/alertmanager/client.go`**

```go
// Package alertmanager is Remedy's read-only client of an Alertmanager. It asks for the active alerts and nothing else: every
// exported method is a Get or a List, the transport refuses every method but GET and HEAD before it is sent, redirects are not
// followed and no proxy is used. The address and the token are asked for at every request, because the Setup page can change
// them while the control plane runs.
package alertmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

// maxBody bounds an answer: a bigger one is an error, never a partial list.
const maxBody = 8 << 20

// requestTimeout is the time one request may take. It is a variable only so that a test does not wait for it.
var requestTimeout = 15 * time.Second

var (
	// ErrNotConfigured means no address is set. ErrTooLarge means the answer is bigger than the client reads. ErrNotAlerts means
	// the answer is not a JSON array of alerts.
	ErrNotConfigured = errors.New("alertmanager: no address is set")
	ErrTooLarge      = errors.New("alertmanager: the answer is too large")
	ErrNotAlerts     = errors.New("alertmanager: the answer is not a JSON array of alerts")
)

// APIError is an answer that is not a success. The body is not shown: it is text of another system.
type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("alertmanager: HTTP %d", e.Status) }

// Alert is one alert of the v2 API.
type Alert struct {
	Fingerprint  string            `json:"fingerprint"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Status       struct {
		State string `json:"state"`
	} `json:"status"`
}

// Credentials gives the address and the token of this moment. An empty token means none is sent.
type Credentials func() (baseURL string, token secret.Value, err error)

// Option changes how a client is built.
type Option func(*Client)

// WithLog makes the client log every request at debug level: path, status and duration. Never a header, a query or a body.
func WithLog(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// guard is the transport: whatever calls the client, only GET and HEAD leave the process.
type guard struct{ base http.RoundTripper }

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, fmt.Errorf("alertmanager: refusing to send %s: this client only reads", req.Method)
	}
	return g.base.RoundTrip(req)
}

// Client asks an Alertmanager for its active alerts.
type Client struct {
	creds Credentials
	http  *http.Client
	log   *slog.Logger
}

// New builds a client. caFile is a PEM bundle for an https address; empty means the system's roots.
func New(creds Credentials, caFile string, opts ...Option) (*Client, error) {
	var pool *x509.CertPool
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("alertmanager: the CA file: %w", err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("alertmanager: the CA file holds no certificate")
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	c := &Client{
		creds: creds,
		http: &http.Client{
			Timeout:   requestTimeout,
			Transport: &guard{base: tr},
			// An answer that redirects is an answer, not an instruction to send the token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func scrub(s string, token secret.Value) string {
	if t := token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

// ListAlerts returns the alerts that are active (not silenced, not inhibited). The answer is all or nothing: anything that is not a
// complete JSON array is an error.
func (c *Client) ListAlerts(ctx context.Context) ([]Alert, error) {
	base, token, err := c.creds()
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return nil, ErrNotConfigured
	}
	q := url.Values{"active": {"true"}, "silenced": {"false"}, "inhibited": {"false"}}
	target := base + "/api/v2/alerts?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, errors.New(scrub(err.Error(), token))
	}
	req.Header.Set("Accept", "application/json")
	if t := token.Reveal(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logRequest(0, started)
		return nil, errors.New(scrub(err.Error(), token))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	c.logRequest(resp.StatusCode, started)
	if err != nil {
		return nil, errors.New(scrub(err.Error(), token))
	}
	if len(raw) > maxBody {
		return nil, ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode}
	}
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, ErrNotAlerts
	}
	var all []Alert
	if err := json.Unmarshal([]byte(trimmed), &all); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAlerts, errors.New("it is not valid JSON of alerts"))
	}
	active := make([]Alert, 0, len(all))
	for _, a := range all {
		if a.Status.State == "active" {
			active = append(active, a)
		}
	}
	return active, nil
}

func (c *Client) logRequest(status int, started time.Time) {
	if c.log == nil {
		return
	}
	c.log.Debug("alertmanager request", "path", "/api/v2/alerts", "status", status, "took", time.Since(started).Round(time.Millisecond))
}
```

- [ ] **Step 5: Run and commit**

Run: `gofmt -w internal/alertmanager && go vet ./internal/alertmanager/... && go test -race ./internal/alertmanager/...`
Expected: PASS. If the oversized-answer test is slow, that is acceptable (8 MB).

```bash
git add internal/alertmanager
git commit -m "feat: a read-only Alertmanager client and its fake

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The engine learns the host, the signal and "everything absent resolves"

**Files:**
- Modify: `internal/incident/incident.go`
- Create or modify: `internal/incident/signal_test.go` (check the package of the existing tests with `command head -3 internal/incident/*_test.go` and use the same package clause)

**Interfaces:**
- Consumes: Task 4 (`NewIncident.HostID`, `TouchIncidentSignal`, `ListUnresolvedBySource`).
- Produces:
  - `Observation.HostID int64` (0 for none).
  - `func (e *Engine) ObserveSignal(ctx context.Context, o Observation) (opened bool, err error)`; `Observe` calls it and drops the bool; behavior for GitHub unchanged.
  - For an incident of a source other than GitHub, a touch updates `last_seen`, the host and the details (`o.Details`).
  - `func (e *Engine) ResolveAbsent(ctx context.Context, source string, present map[string]bool) (int, error)`: resolves every unresolved incident of the source (ignored ones too) whose key is not in `present`, with the reason `ReasonCleared` and the usual activity entry; returns how many it resolved.

- [ ] **Step 1: Write the failing tests**

`internal/incident/signal_test.go`. Use the helpers of the existing incident tests for a store and engine (look at how `engine_test.go`/`incident_test.go` builds them; if there is none, `store.Open(filepath.Join(t.TempDir(), "test.db"))` and `&incident.Engine{Store: st}`). Tests (write them out in full):

1. `TestAnAlertOpensAnIncidentOnceAndTouchesItAfterwards`: `ObserveSignal` with `Source: store.SourceAlertmanager, Key: "A/1", Title: "t", Severity: "critical", AutoDiagnose: true, Details: {"labels":{"a":"b"}}, Class: incident.Bad, Conclusion: "firing"` returns `opened == true`; the incident has `AutoDiagnose`, severity and details; a second call returns `false`; the incident count is 1; `Occurrences` stays 1; the activity has exactly one `incident_opened` entry.
2. `TestATouchUpdatesTheHostAndTheDetailsButNotTheAutoDecision`: second call with `HostID: h.ID` (a host created with `st.CreateHost`), `AutoDiagnose: false` and new details: the incident now has `HostID == h.ID`, `HostName`, the new details, and `AutoDiagnose` is still `true`; a third call with `HostID: 0` clears the host.
3. `TestAnIgnoredIncidentStaysIgnoredWhenItsAlertKeepsFiring`: ignore it (`engine.Ignore`), observe again: state stays `ignored` and the call returns no error and `opened == false`.
4. `TestResolveAbsentResolvesOnlyWhatIsGoneOfThatSource`: open three alertmanager incidents `A/1`, `A/2`, `A/3` (ignore `A/3`), one `argocd` incident `X` and one GitHub incident; `ResolveAbsent(ctx, "alertmanager", map[string]bool{"A/1": true})` returns 2; `A/2` and `A/3` are `resolved` with `ResolvedReason == incident.ReasonCleared`, `A/1` is open, the argocd and GitHub incidents are untouched; a second call returns 0.
5. `TestResolveAbsentWithNothingPresentResolvesEveryIncidentOfTheSource` (an empty map resolves all unresolved ones).
6. `TestObserveStillWorksForGitHub`: the old `Observe` on a GitHub observation behaves as before (open then touch), so existing tests keep their meaning (run `go test ./internal/incident` for the whole package).

Run: `go test ./internal/incident -run 'Signal|Absent|Alert'`
Expected: FAIL to compile.

- [ ] **Step 2: Implement**

In `internal/incident/incident.go`:

1. `Observation` gets `HostID int64 // the host the signal maps to, 0 for none`.
2. Rename the body of `Observe` to `ObserveSignal` returning `(bool, error)`, and make `Observe` a wrapper:

```go
// Observe applies one observation. It is idempotent: observing the same state again changes nothing except last_seen (and, for a
// source other than GitHub, the host and the details of the signal).
func (e *Engine) Observe(ctx context.Context, o Observation) error {
	_, err := e.ObserveSignal(ctx, o)
	return err
}

// ObserveSignal is Observe that also says whether it opened an incident.
func (e *Engine) ObserveSignal(ctx context.Context, o Observation) (bool, error) {
```
Every `return X` in the body becomes `return false, X`, except the successful open path which returns `true, nil`; `errors.Is(err, store.ErrExists)` returns `false, nil`. The open call passes `HostID: o.HostID` in the `store.NewIncident`. In the `switch` that decides what to do with an existing incident, insert **after** the `cur.State == store.IncIgnored` case and **before** the `cur.HeadSHA == o.HeadSHA` case:

```go
	case cur.Source != store.SourceGitHub:
		change = e.Store.TouchIncidentSignal(ctx, cur.ID, o.HostID, o.Details)
```
Keep everything else in the function as it is.
3. Add:

```go
// ResolveAbsent resolves the unresolved incidents of a source (ignored ones too) whose key is not in present. The caller calls it only
// after a complete and successful fetch: an incomplete answer would resolve incidents that are still firing.
func (e *Engine) ResolveAbsent(ctx context.Context, source string, present map[string]bool) (int, error) {
	list, err := e.Store.ListUnresolvedBySource(ctx, source)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, in := range list {
		if present[in.Key] {
			continue
		}
		err := e.Store.ResolveIncident(ctx, in.ID, ReasonCleared, store.NewActivity{
			Kind:    store.KindIncidentResolved,
			Summary: resolvedText(in),
		})
		if errors.Is(err, store.ErrNotFound) {
			continue // resolved by someone else in the meantime
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
```
Check that `resolvedText`, `ReasonCleared` and `Engine.Ignore` exist with these names; the incident ids in the activity entry are filled by `ResolveIncident` as in the existing path (look at how `Observe`'s Green path calls it and copy its `NewActivity` fields, adding nothing that GitHub-specific).

- [ ] **Step 3: Run and commit**

Run: `gofmt -w internal/incident && go vet ./internal/incident && go test -race ./internal/incident ./internal/poller`
Expected: PASS (the poller's tests prove the GitHub behavior is unchanged).

```bash
git add internal/incident
git commit -m "feat: the engine keeps the host and the details of a signal and resolves what is gone

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The signals: the Alertmanager source and the runner

**Files:**
- Create: `internal/signals/signal.go`, `internal/signals/alertmanager.go`, `internal/signals/runner.go`, `internal/signals/hostmap.go`, `internal/signals/alertmanager_test.go`, `internal/signals/runner_test.go`

**Interfaces:**
- Consumes: Task 1 (`alertmatch`), Task 5 (`alertmanager.Alert`), Task 6 (`ObserveSignal`, `ResolveAbsent`, `Observation.HostID`), Task 4 (`store.HostEntry`).
- Produces (used by Task 8):
  - `type Signal struct { Key, Title, Severity string; Auto bool; Details json.RawMessage; Labels map[string]string; URL string; SeenAt time.Time }`.
  - `type Source interface { Name() string; Enabled() bool; Fetch(ctx context.Context) ([]Signal, error) }`.
  - `type AlertLister interface { ListAlerts(ctx context.Context) ([]alertmanager.Alert, error) }`.
  - `type Alertmanager struct { Client AlertLister; On func() bool; MaxAge func() time.Duration; Now func() time.Time }` implementing `Source` (`Name()` is `"alertmanager"`).
  - `type HostRules struct { ID int64; Name string; Matchers string }`.
  - `type State struct { Name, State string; CheckedAt time.Time; Active int; Error string }`, constants `StateOff`, `StateOK`, `StateError`, `StatePending` (`"off"`, `"ok"`, `"error"`, `"pending"`).
  - `type Runner struct { Sources []Source; Engine *incident.Engine; Hosts func(context.Context) ([]HostRules, error); Interval func() time.Duration; Log *slog.Logger; Now func() time.Time; ... }`, `(*Runner).Run(ctx)`, `(*Runner).RunOnce(ctx)`, `(*Runner).States() []State`, `const MaxNewPerCycle = 50`.

- [ ] **Step 1: Check the names are free**

Run: `command ls internal/signals`
Expected: `ls` reports that the directory does not exist.

- [ ] **Step 2: Write the failing tests for the source**

`internal/signals/alertmanager_test.go` (package `signals_test`):

```go
package signals_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/signals"
)

type lister struct {
	alerts []alertmanager.Alert
	err    error
}

func (l *lister) ListAlerts(context.Context) ([]alertmanager.Alert, error) { return l.alerts, l.err }

func alert(fp, name string, labels map[string]string, startsAt time.Time) alertmanager.Alert {
	a := alertmanager.Alert{Fingerprint: fp, Labels: map[string]string{"alertname": name}, Annotations: map[string]string{}, StartsAt: startsAt}
	for k, v := range labels {
		a.Labels[k] = v
	}
	a.Status.State = "active"
	return a
}

var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func source(l *lister, maxAge time.Duration) *signals.Alertmanager {
	return &signals.Alertmanager{Client: l, On: func() bool { return true }, MaxAge: func() time.Duration { return maxAge }, Now: func() time.Time { return now }}
}

func fetch(t *testing.T, l *lister, maxAge time.Duration) []signals.Signal {
	t.Helper()
	got, err := source(l, maxAge).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTheSourceNamesItselfAndFollowsItsSwitch(t *testing.T) {
	on := false
	s := &signals.Alertmanager{Client: &lister{}, On: func() bool { return on }}
	if s.Name() != "alertmanager" || s.Enabled() {
		t.Fatal("the source is on while its switch is off")
	}
	on = true
	if !s.Enabled() {
		t.Fatal("the source is off while its switch is on")
	}
}

func TestAnAlertBecomesASignalWithItsKeyAndSeverity(t *testing.T) {
	l := &lister{alerts: []alertmanager.Alert{
		alert("f1", "KubePodCrashLooping", map[string]string{"severity": "CRITICAL", "namespace": "demo", "pod": "web-1"}, now.Add(-time.Minute)),
		alert("f2", "Odd", map[string]string{"severity": "page"}, now.Add(-time.Minute)),
		alert("f3", "NoSeverity", nil, now.Add(-time.Minute)),
	}}
	got := fetch(t, l, time.Hour)
	if len(got) != 3 || got[0].Key != "KubePodCrashLooping/f1" || got[0].Severity != "critical" || got[1].Severity != "none" || got[2].Severity != "none" {
		t.Fatalf("%+v", got)
	}
	if got[0].Labels["pod"] != "web-1" || got[0].SeenAt != now {
		t.Errorf("labels = %v, seen = %v", got[0].Labels, got[0].SeenAt)
	}
}

func TestWatchdogAndInfoInhibitorAndIncompleteAlertsAreNotSignals(t *testing.T) {
	l := &lister{alerts: []alertmanager.Alert{
		alert("w", "Watchdog", nil, now), alert("i", "InfoInhibitor", nil, now),
		alert("", "NoFingerprint", nil, now), alert("x", "", nil, now), alert("ok", "Real", nil, now),
		alert("ok", "Real", nil, now), // the same key twice
	}}
	got := fetch(t, l, time.Hour)
	if len(got) != 1 || got[0].Key != "Real/ok" {
		t.Fatalf("%+v", got)
	}
}

func TestOnlyAYoungCriticalOrWarningAlertIsDiagnosedOnItsOwn(t *testing.T) {
	young, old := now.Add(-10*time.Minute), now.Add(-3*time.Hour)
	l := &lister{alerts: []alertmanager.Alert{
		alert("a", "A", map[string]string{"severity": "critical"}, young),
		alert("b", "B", map[string]string{"severity": "warning"}, young),
		alert("c", "C", map[string]string{"severity": "info"}, young),
		alert("d", "D", nil, young),
		alert("e", "E", map[string]string{"severity": "critical"}, old),
		alert("f", "F", map[string]string{"severity": "critical"}, time.Time{}),
		alert("g", "G", map[string]string{"severity": "warning"}, now.Add(-time.Hour)), // exactly the maximum age: still young
		alert("h", "H", map[string]string{"severity": "warning"}, now.Add(time.Hour)),  // starts in the future: young
	}}
	want := map[string]bool{"A/a": true, "B/b": true, "C/c": false, "D/d": false, "E/e": false, "F/f": false, "G/g": true, "H/h": true}
	for _, s := range fetch(t, l, time.Hour) {
		if s.Auto != want[s.Key] {
			t.Errorf("%s: Auto = %v, want %v", s.Key, s.Auto, want[s.Key])
		}
	}
}

func TestTheTitleIsTheSummaryOrTheNameWithItsPlace(t *testing.T) {
	a1 := alert("1", "A", map[string]string{"namespace": "demo", "pod": "web-1"}, now)
	a1.Annotations["summary"] = "  The pod\nweb-1 keeps\trestarting  "
	a2 := alert("2", "B", map[string]string{"namespace": "demo", "pod": "web-2"}, now)
	a3 := alert("3", "C", nil, now)
	a4 := alert("4", "D", nil, now)
	a4.Annotations["summary"] = strings.Repeat("x", 500)
	got := fetch(t, &lister{alerts: []alertmanager.Alert{a1, a2, a3, a4}}, time.Hour)
	if got[0].Title != "The pod web-1 keeps restarting" || got[1].Title != "B in demo/web-2" || got[2].Title != "C" {
		t.Fatalf("titles = %q, %q, %q", got[0].Title, got[1].Title, got[2].Title)
	}
	if n := len([]rune(got[3].Title)); n != 200 {
		t.Errorf("a long title has %d characters, want 200", n)
	}
	for _, s := range got {
		if strings.ContainsAny(s.Title, "\n\r\t") {
			t.Errorf("the title %q has a control character", s.Title)
		}
	}
}

func TestTheDetailsAreBoundedAndRedacted(t *testing.T) {
	labels := map[string]string{"severity": "warning"}
	for i := 0; i < 40; i++ {
		labels["l"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	labels[strings.Repeat("k", 65)] = "key too long"
	labels["long"] = strings.Repeat("v", 400)
	a := alert("1", "A", labels, now)
	for i := 0; i < 25; i++ {
		a.Annotations["n"+string(rune('a'+i))] = "x"
	}
	a.Annotations["description"] = strings.Repeat("d", 1500)
	a.Annotations["leak"] = "password=hunter2hunter2 and AKIAIOSFODNN7EXAMPLE" // use a pattern internal/redact removes: see redact_test.go
	got := fetch(t, &lister{alerts: []alertmanager.Alert{a}}, time.Hour)[0]
	var d struct {
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		StartsAt     string            `json:"startsAt"`
		Fingerprint  string            `json:"fingerprint"`
		GeneratorURL string            `json:"generatorURL"`
		Truncated    bool              `json:"truncated"`
	}
	if err := json.Unmarshal(got.Details, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Labels) > 30 || len(d.Annotations) > 20 || !d.Truncated || d.Fingerprint != "1" || d.StartsAt == "" {
		t.Fatalf("labels %d, annotations %d, truncated %v, %+v", len(d.Labels), len(d.Annotations), d.Truncated, d)
	}
	for k, v := range d.Labels {
		if len(k) > 64 || len([]rune(v)) > 300 {
			t.Errorf("label %q is not bounded", k)
		}
	}
	for k, v := range d.Annotations {
		if len([]rune(v)) > 1000 {
			t.Errorf("annotation %q is not bounded", k)
		}
	}
	if strings.Contains(string(got.Details), "hunter2hunter2") {
		t.Errorf("a secret survived in the details: %s", got.Details)
	}
	if len(got.Labels) != len(labels) {
		t.Errorf("the labels used for the host mapping were cut: %d of %d", len(got.Labels), len(labels))
	}
}

func TestAFailedListingIsAnError(t *testing.T) {
	boom := errors.New("down")
	if _, err := source(&lister{err: boom}, time.Hour).Fetch(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
}
```

(For the redaction case look into `internal/redact/redact_test.go`, pick a string that package replaces and use it in `a.Annotations["leak"]`, assert that string's secret part is gone.)

- [ ] **Step 3: Write the failing tests for the runner**

`internal/signals/runner_test.go` (package `signals_test`; real store and engine):

```go
package signals_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/signals"
	"github.com/Jaydee94/remedy/internal/store"
)

type fakeSource struct {
	on      bool
	sigs    []signals.Signal
	err     error
	fetched int
}

func (f *fakeSource) Name() string  { return "alertmanager" }
func (f *fakeSource) Enabled() bool { return f.on }
func (f *fakeSource) Fetch(context.Context) ([]signals.Signal, error) {
	f.fetched++
	return f.sigs, f.err
}

func sig(key, severity string, auto bool, labels map[string]string) signals.Signal {
	return signals.Signal{Key: key, Title: "title of " + key, Severity: severity, Auto: auto, Labels: labels, Details: json.RawMessage(`{"fingerprint":"` + key + `"}`)}
}

type rig struct {
	st    *store.Store
	src   *fakeSource
	r     *signals.Runner
	hosts []signals.HostRules
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := &rig{st: st, src: &fakeSource{on: true}}
	g.r = &signals.Runner{
		Sources: []signals.Source{g.src}, Engine: &incident.Engine{Store: st},
		Hosts:    func(context.Context) ([]signals.HostRules, error) { return g.hosts, nil },
		Interval: func() time.Duration { return time.Minute },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return g
}

func (g *rig) incidents(t *testing.T) map[string]store.Incident {
	t.Helper()
	list, err := g.st.ListIncidents(context.Background(), store.IncidentFilter{State: "all", Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Incident{}
	for _, in := range list {
		out[in.Key] = in
	}
	return out
}

func (g *rig) activityCount(t *testing.T) int {
	t.Helper()
	log, _ := g.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 10000})
	return len(log)
}
```
Tests (write each out in full with the helpers above; the host rows are created with `st.CreateHost(ctx, store.HostFields{Name: "nas", Address: "192.168.178.118", Port: 22, Account: "remedy", Enabled: true, Matchers: ...})` and `g.hosts` is filled from them):

1. `TestACycleOpensAnIncidentPerSignalAndASecondCycleChangesNothing`: two signals; after `RunOnce`: two incidents with `Source == store.SourceAlertmanager`, the titles, severities, `AutoDiagnose` as given, `Conclusion == "firing"`; `activityCount` after the first cycle `n`; after a second `RunOnce` the activity count is still `n` and `Occurrences` is 1; `States()[0]` is `{Name: "alertmanager", State: signals.StateOK, Active: 2}` with a non-zero `CheckedAt`.
2. `TestASignalThatIsGoneResolvesItsIncident`: three cycles: `[a, b]`, then `[a]` (b resolved with `ResolvedReason == incident.ReasonCleared`, a open), then `[]` (a resolved).
3. `TestAnIgnoredIncidentIsResolvedWhenItsAlertIsGone`.
4. `TestAFailedFetchResolvesNothingAndShowsTheError`: after `[a, b]` set `src.err = errors.New("alertmanager: HTTP 500")` and `src.sigs = nil`: `RunOnce`; both incidents still open; `States()[0].State == signals.StateError` and the error text equals the message; a later good cycle returns to ok.
5. `TestAnEmptyAnswerResolvesEverything`: `src.sigs = []` with no error resolves the open incidents (a complete empty answer is "all clear").
6. `TestAtMostFiftyNewIncidentsAreOpenedPerCycle`: 60 signals: first cycle 50 incidents and **none resolved**; second cycle 60; a third cycle changes nothing. While 10 are deferred, the first cycle's state is ok with `Active == 60`.
7. `TestADisabledSourceIsNotAskedAndLeavesItsIncidentsAlone`: after a cycle, set `on = false`: `RunOnce` does not fetch (`fetched` unchanged), the incidents stay, `States()[0].State == signals.StateOff`; before any cycle an enabled source shows `signals.StatePending`.
8. `TestAnAlertIsMappedToTheFirstMatchingHostAndTheTieIsShown`: hosts `nas` (id 1, rules `instance=~"ugreen-nas(:[0-9]+)?"`), `backup` (id 2, rules `job="node-exporter"`), `empty` (id 3, no rules), `broken` (id 4, matchers text `this is not a rule`, written straight into the `HostRules`, not through the store): a signal with `instance="ugreen-nas"`, `job="node-exporter"` maps to the host `nas`, and `details.alsoMatches == ["backup"]`; a signal with only `job="node-exporter"` maps to `backup` with no `alsoMatches`; a signal with no matching label has no host (`HostID == 0`); the broken host never matches and does not stop the cycle.
9. `TestAChangedRuleAppliesInTheNextCycle`: the signal first maps to `nas`; change `g.hosts[0].Matchers` to a rule that no longer matches: next cycle `HostID == 0`; and when a host is deleted (`st.DeleteHost`), the incident keeps existing without a host.
10. `TestOtherSourcesAreNeverResolvedByThisRunner`: a GitHub incident and an `argocd` incident exist; the alert cycles do not touch them.
11. `TestRunStopsWithItsContextAndRunsOnce`: `Run(ctx)` with `Interval` of 20 ms and a counting source: it fetches at least twice, stops soon after `cancel()`, and `Run` returns.
12. `TestTheHostListIsAskedOncePerCycleAndItsFailureDoesNotStopTheCycle`: `Hosts` returns an error: incidents are still opened (no host), one warning is logged.

Run: `go test ./internal/signals`
Expected: FAIL to compile.

- [ ] **Step 4: `signal.go` and `hostmap.go`**

`internal/signals/signal.go`:

```go
// Package signals turns what a monitoring source reports into incidents. A Source says, in one answer, everything that is wrong
// right now; the Runner asks every source once per poll interval, maps each signal to a host and gives it to the incident engine.
package signals

import (
	"context"
	"encoding/json"
	"time"
)

// Signal is one thing that is wrong, normalised and bounded. It is all the engine knows about a source.
type Signal struct {
	Key      string
	Title    string
	Severity string // critical, warning, info or none
	Auto     bool   // the responder may start on its own; decided once, when the incident opens
	Details  json.RawMessage
	// Labels are the labels the host rules are compared with. They are not stored (Details holds a bounded copy).
	Labels map[string]string
	URL    string
	SeenAt time.Time
}

// Source reports everything that is wrong right now. A source that cannot give a complete answer returns an error and nothing else:
// the runner then resolves nothing.
type Source interface {
	Name() string
	// Enabled says whether the source is set up. A source that is not is not asked.
	Enabled() bool
	Fetch(ctx context.Context) ([]Signal, error)
}
```

`internal/signals/hostmap.go`:

```go
package signals

import (
	"encoding/json"
	"sort"

	"github.com/Jaydee94/remedy/internal/alertmatch"
)

// HostRules are the alert rules of one host as they are stored.
type HostRules struct {
	ID       int64
	Name     string
	Matchers string
}

type compiledHost struct {
	id    int64
	name  string
	rules []alertmatch.Matcher
}

// compile parses the rules of every host, in the order of their ids. A host whose rules cannot be read is skipped and its name is
// returned (the server checks the rules, so this means a row that was changed by hand).
func compile(hosts []HostRules) (ok []compiledHost, skipped []string) {
	sorted := append([]HostRules(nil), hosts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, h := range sorted {
		rules, err := alertmatch.Parse(h.Matchers)
		if err != nil {
			skipped = append(skipped, h.Name)
			continue
		}
		if len(rules) > 0 {
			ok = append(ok, compiledHost{id: h.ID, name: h.Name, rules: rules})
		}
	}
	return ok, skipped
}

// mapHost returns the first host whose rules all hold for the labels (0 for none) and the names of the others that match too.
func mapHost(hosts []compiledHost, labels map[string]string) (int64, []string) {
	var first int64
	var also []string
	for _, h := range hosts {
		if !alertmatch.Matches(h.rules, labels) {
			continue
		}
		if first == 0 {
			first = h.id
			continue
		}
		also = append(also, h.name)
	}
	return first, also
}

// withAlso writes the names of the further matching hosts into the details; none removes the key.
func withAlso(details json.RawMessage, also []string) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(details, &m); err != nil || m == nil {
		m = map[string]any{}
	}
	if len(also) > 0 {
		m["alsoMatches"] = also
	} else {
		delete(m, "alsoMatches")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return details
	}
	return b
}
```

- [ ] **Step 5: `alertmanager.go`**

```go
package signals

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/redact"
)

// Limits of what an alert may put into an incident (the spec, section 5).
const (
	maxLabels      = 30
	maxLabelKey    = 64
	maxLabelValue  = 300
	maxAnnotations = 20
	maxAnnotation  = 1000
	maxTitle       = 200
)

// AlertLister is what the source needs from the client.
type AlertLister interface {
	ListAlerts(ctx context.Context) ([]alertmanager.Alert, error)
}

// Alertmanager is the source of the alerts of an Alertmanager.
type Alertmanager struct {
	Client AlertLister
	// On says whether an address is set. MaxAge is how old (since startsAt) an alert may be to be diagnosed on its own.
	On     func() bool
	MaxAge func() time.Duration
	Now    func() time.Time // default time.Now
}

func (a *Alertmanager) Name() string { return "alertmanager" }

func (a *Alertmanager) Enabled() bool { return a.On != nil && a.On() }

func (a *Alertmanager) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Fetch lists the active alerts. Watchdog and InfoInhibitor are heartbeats, not outages; an alert without a name or a fingerprint cannot
// be keyed and is skipped.
func (a *Alertmanager) Fetch(ctx context.Context) ([]Signal, error) {
	alerts, err := a.Client.ListAlerts(ctx)
	if err != nil {
		return nil, err
	}
	now, maxAge := a.now(), a.MaxAge()
	seen := map[string]bool{}
	out := make([]Signal, 0, len(alerts))
	for _, al := range alerts {
		name := al.Labels["alertname"]
		if name == "" || al.Fingerprint == "" || name == "Watchdog" || name == "InfoInhibitor" {
			continue
		}
		key := name + "/" + al.Fingerprint
		if seen[key] {
			continue
		}
		seen[key] = true
		sev := severityOf(al.Labels["severity"])
		out = append(out, Signal{
			Key: key, Title: titleOf(al), Severity: sev,
			Auto:    (sev == "critical" || sev == "warning") && !al.StartsAt.IsZero() && now.Sub(al.StartsAt) <= maxAge,
			Details: detailsOf(al), Labels: al.Labels, URL: al.GeneratorURL, SeenAt: now,
		})
	}
	return out, nil
}

func severityOf(label string) string {
	switch s := strings.ToLower(strings.TrimSpace(label)); s {
	case "critical", "warning", "info":
		return s
	}
	return "none"
}

// oneLine turns every run of white space and control characters into one space and trims the ends.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

func cut(s string, max int) (string, bool) {
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	return string([]rune(s)[:max]), true
}

func titleOf(al alertmanager.Alert) string {
	t := oneLine(al.Annotations["summary"])
	if t == "" {
		t = al.Labels["alertname"]
		if ns, pod := al.Labels["namespace"], al.Labels["pod"]; ns != "" && pod != "" {
			t += " in " + ns + "/" + pod
		}
		t = oneLine(t)
	}
	t, _ = cut(redact.Redact(t), maxTitle)
	return t
}

// bounded keeps at most max entries (the smallest keys first, so the cut is the same every cycle), drops keys that are too long and
// cuts and redacts the values. It says whether it left anything out.
func bounded(in map[string]string, max, maxValue int, keyLimit bool) (map[string]string, bool) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	cutAny := false
	for _, k := range keys {
		if keyLimit && utf8.RuneCountInString(k) > maxLabelKey {
			cutAny = true
			continue
		}
		if len(out) == max {
			cutAny = true
			continue
		}
		v, c := cut(redact.Redact(in[k]), maxValue)
		cutAny = cutAny || c
		out[k] = v
	}
	return out, cutAny
}

func detailsOf(al alertmanager.Alert) json.RawMessage {
	labels, c1 := bounded(al.Labels, maxLabels, maxLabelValue, true)
	annotations, c2 := bounded(al.Annotations, maxAnnotations, maxAnnotation, false)
	d := map[string]any{
		"labels": labels, "annotations": annotations, "fingerprint": al.Fingerprint,
		"generatorURL": al.GeneratorURL,
	}
	if !al.StartsAt.IsZero() {
		d["startsAt"] = al.StartsAt.UTC().Format(time.RFC3339)
	}
	if c1 || c2 {
		d["truncated"] = true
	}
	b, _ := json.Marshal(d)
	return b
}
```
(The test expects `d.StartsAt != ""` for an alert with a `startsAt`; the test alerts built in `alert()` have one.)

- [ ] **Step 6: `runner.go`**

```go
package signals

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

// MaxNewPerCycle bounds how many incidents one source may open in one cycle; the rest is picked up in the next, which protects the
// list and the daily limit against an alert storm.
const MaxNewPerCycle = 50

// The states of a source.
const (
	StateOff     = "off"     // no address is set
	StateOK      = "ok"      // the last attempt worked
	StateError   = "error"   // the last attempt failed
	StatePending = "pending" // set up, not asked yet
)

// maxErrorLen bounds the error text kept in the state.
const maxErrorLen = 300

// State is what the runner knows about a source. It is held in memory: a restart forgets it and the next cycle fills it in.
type State struct {
	Name      string
	State     string
	CheckedAt time.Time
	Active    int // how many signals the last good answer held
	Error     string
}

// Runner asks every source once per interval and gives what it finds to the incident engine.
type Runner struct {
	Sources  []Source
	Engine   *incident.Engine
	Hosts    func(ctx context.Context) ([]HostRules, error)
	Interval func() time.Duration
	Log      *slog.Logger
	Now      func() time.Time // default time.Now

	mu     sync.Mutex
	states map[string]State
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) interval() time.Duration {
	d := time.Minute
	if r.Interval != nil {
		d = r.Interval()
	}
	if d <= 0 {
		d = time.Minute
	}
	return d
}

// pollTick is the longest Run sleeps before it looks at the interval again, so that a new interval counts soon.
const pollTick = 5 * time.Second

func pause(interval time.Duration) time.Duration { return min(interval, pollTick) }

// Run runs a cycle at once and then whenever the interval has passed since the last, until ctx ends. It works without a GitHub
// connection: it does not depend on the GitHub poller.
func (r *Runner) Run(ctx context.Context) {
	r.RunOnce(ctx)
	last := time.Now()
	for {
		t := time.NewTimer(pause(r.interval()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if time.Since(last) >= r.interval() {
			r.RunOnce(ctx)
			last = time.Now()
		}
	}
}

// RunOnce asks every source that is set up once.
func (r *Runner) RunOnce(ctx context.Context) {
	for _, src := range r.Sources {
		if ctx.Err() != nil {
			return
		}
		r.runSource(ctx, src)
	}
}

func (r *Runner) set(s State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.states == nil {
		r.states = map[string]State{}
	}
	r.states[s.Name] = s
}

func (r *Runner) fail(name string, err error) {
	msg := err.Error()
	if len([]rune(msg)) > maxErrorLen {
		msg = string([]rune(msg)[:maxErrorLen]) + "..."
	}
	r.Log.Warn("a signal source could not be read; nothing was resolved", "source", name, "err", msg)
	r.set(State{Name: name, State: StateError, CheckedAt: r.now(), Error: msg})
}

// States returns the state of every source, in the order of Sources.
func (r *Runner) States() []State {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]State, 0, len(r.Sources))
	for _, src := range r.Sources {
		switch s, ok := r.states[src.Name()]; {
		case !src.Enabled():
			out = append(out, State{Name: src.Name(), State: StateOff})
		case ok && s.State != StateOff:
			out = append(out, s)
		default:
			out = append(out, State{Name: src.Name(), State: StatePending})
		}
	}
	return out
}

func (r *Runner) hostRules(ctx context.Context) []compiledHost {
	hosts, err := r.Hosts(ctx)
	if err != nil {
		r.Log.Warn("the hosts could not be read; the signals of this cycle have no host", "err", err)
		return nil
	}
	ok, skipped := compile(hosts)
	if len(skipped) > 0 {
		sort.Strings(skipped)
		r.Log.Warn("the alert rules of these hosts cannot be read and are skipped", "hosts", skipped)
	}
	return ok
}

func (r *Runner) runSource(ctx context.Context, src Source) {
	name := src.Name()
	if !src.Enabled() {
		r.set(State{Name: name, State: StateOff})
		return
	}
	sigs, err := src.Fetch(ctx)
	if err != nil {
		r.fail(name, err)
		return
	}
	hosts := r.hostRules(ctx)
	present := make(map[string]bool, len(sigs))
	opened, deferred := 0, 0
	for _, sg := range sigs {
		present[sg.Key] = true
		if opened >= MaxNewPerCycle {
			if _, err := r.Engine.Store.FindActiveIncidentByKey(ctx, name, sg.Key); errors.Is(err, store.ErrNotFound) {
				deferred++
				continue
			} else if err != nil {
				r.fail(name, err)
				return
			}
		}
		host, also := mapHost(hosts, sg.Labels)
		wasNew, err := r.Engine.ObserveSignal(ctx, incident.Observation{
			Source: name, Key: sg.Key, Title: sg.Title, Severity: sg.Severity, AutoDiagnose: sg.Auto,
			Details: withAlso(sg.Details, also), Class: incident.Bad, Conclusion: "firing", URL: sg.URL, HostID: host,
		})
		if err != nil {
			r.fail(name, err)
			return
		}
		if wasNew {
			opened++
		}
	}
	if deferred > 0 {
		r.Log.Warn("more new incidents than one cycle opens; the rest follows in the next cycle", "source", name, "deferred", deferred)
	}
	if _, err := r.Engine.ResolveAbsent(ctx, name, present); err != nil {
		r.fail(name, err)
		return
	}
	r.set(State{Name: name, State: StateOK, CheckedAt: r.now(), Active: len(sigs)})
}
```

- [ ] **Step 7: Run and commit**

Run: `gofmt -w internal/signals && go vet ./internal/signals && go test -race ./internal/signals`
Expected: PASS.

```bash
git add internal/signals
git commit -m "feat: the Alertmanager source and the signal runner

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Wiring: configuration, the app, `GET /api/signals`, the incident fields and a chain test

**Files:**
- Modify: `internal/config/config.go`, `internal/config/env_layer_test.go` (or a new `internal/config/alertmanager_test.go`), `internal/app/app.go`, `cmd/remedy-server/main.go`, `internal/server/server.go`, `internal/server/incidents.go`
- Create: `internal/server/signals.go`, `internal/server/signals_test.go`, `internal/app/signals_chain_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `config.Server.AlertmanagerCAFile string` (`REMEDY_ALERTMANAGER_CA_FILE`; when set the file must exist and be readable).
  - `app.App.Signals *signals.Runner`, started by `cmd/remedy-server` next to the poller.
  - `server.Deps.Signals func() []signals.State` and `GET /api/signals` → `{"sources":[{"name","state","checkedAt","active","error"}]}` (`checkedAt` and `error` omitted when empty).
  - The incident view carries `hostId`, `hostName` (omitted when none) and `alsoMatches` (read from `details.alsoMatches`).
  - `settings.New` in the app gets `settings.WithKey(cfg.MasterKey)`; the Setup facts list the CA file when set.

- [ ] **Step 1: Write the failing tests**

`internal/server/signals_test.go` (package `server_test`; uses `newSettingsEnv(t, tune)` from `claimtimeout_test.go`):

```go
package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/signals"
)

func TestTheSourcesAreListedWithTheirState(t *testing.T) {
	checked := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	e, _ := newSettingsEnv(t, func(d *server.Deps) {
		d.Signals = func() []signals.State {
			return []signals.State{{Name: "alertmanager", State: signals.StateOK, CheckedAt: checked, Active: 7}}
		}
	})
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/signals", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := decode[struct {
		Sources []struct {
			Name, State, CheckedAt, Error string
			Active                        int
		} `json:"sources"`
	}](t, resp)
	if len(got.Sources) != 1 || got.Sources[0].Name != "alertmanager" || got.Sources[0].State != "ok" || got.Sources[0].Active != 7 || got.Sources[0].CheckedAt == "" || got.Sources[0].Error != "" {
		t.Fatalf("%+v", got)
	}
}

func TestTheSignalsNeedASessionAndAreNotThereWithoutARunner(t *testing.T) {
	e, _ := newSettingsEnv(t, func(d *server.Deps) { d.Signals = func() []signals.State { return nil } })
	if got := e.do(t, e.adminClient(t, false), http.MethodGet, "/api/signals", "", false).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("without a session = %d", got)
	}
	empty := decode[struct{ Sources []any }](t, e.do(t, e.adminClient(t, true), http.MethodGet, "/api/signals", "", false))
	if empty.Sources == nil {
		t.Error("no sources must be [] and not null")
	}
	none, _ := newSettingsEnv(t, nil)
	if got := none.do(t, none.adminClient(t, true), http.MethodGet, "/api/signals", "", false).StatusCode; got != http.StatusNotFound {
		t.Errorf("without Deps.Signals = %d, want 404", got)
	}
}
```

`internal/config`: a test that `ServerFromEnv` reads `REMEDY_ALERTMANAGER_CA_FILE` into `AlertmanagerCAFile` (use the `baseEnv()` of `env_layer_test.go`; a readable temp file works, a missing file is an error starting with `REMEDY_ALERTMANAGER_CA_FILE:`), and that without the variable the field is empty.

`internal/app/signals_chain_test.go` (package `app_test`; use `appWithEnv` of `settings_chain_test.go` and the admin-client pattern of `TestTheActionsFollowTheAllowlistOfTheSettings`; factor a helper `adminDo(t, ts, client, method, path, body) (int, string)` if the file has none):

```go
func TestAnAlertBecomesAnIncidentMappedToAHostAndResolvesWhenItIsGone(t *testing.T) {
	fake := alertmanagertest.New(t, "tok-0123456789abcdef")
	a := appWithEnv(t, nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	// log in, then:
	//  1. PUT /api/settings/alertmanager_url {"value": fake.URL}  and  alertmanager_token {"value": "tok-0123456789abcdef"}
	//  2. POST /api/hosts {"name":"nas","address":"192.168.178.118"} and PATCH it with
	//     {"matchers":"instance=~\"ugreen-nas(:[0-9]+)?\""}
	//  3. fake.SetAlerts(young critical alert with labels instance=ugreen-nas, an old warning alert without instance, Watchdog)
	//  4. a.Signals.RunOnce(ctx)
	//  5. GET /api/incidents?state=active returns two incidents (not Watchdog): the NAS alert has hostId == the host's id,
	//     hostName "nas", autoDiagnose true; the other has no host and autoDiagnose false
	//  6. GET /api/signals: alertmanager ok, active 3 (the answer held three alerts, the Watchdog counts as skipped: expect 2)
	//  7. the fake saw Authorization "Bearer tok-0123456789abcdef" and the query active=true&inhibited=false&silenced=false
	//  8. a second RunOnce changes nothing; fake.SetRaw(500, "x") + RunOnce: still two active incidents, signals state error;
	//     fake.SetAlerts() (empty) + RunOnce: both resolved
	//  9. the activity feed (GET /api/activity) never contains the token
}
```
Write the test out in full (the incident list route and its JSON names are `GET /api/incidents?state=active` → an array of the incident view; `hostId`/`hostName` are the new fields).

Run: `go test ./internal/config ./internal/server ./internal/app -run 'Signals|AnAlertBecomes|AlertmanagerCA'`
Expected: FAIL to compile.

- [ ] **Step 2: Configuration**

`internal/config/config.go`: `Server` gets `AlertmanagerCAFile string // REMEDY_ALERTMANAGER_CA_FILE: a PEM bundle for an https Alertmanager; empty means the system's roots`. In `ServerFromEnv`, after the cluster validation:

```go
	c.AlertmanagerCAFile = get("REMEDY_ALERTMANAGER_CA_FILE")
	if c.AlertmanagerCAFile != "" {
		if _, err := os.ReadFile(c.AlertmanagerCAFile); err != nil {
			return Server{}, fmt.Errorf("REMEDY_ALERTMANAGER_CA_FILE: %w", err)
		}
	}
```

- [ ] **Step 3: The server**

`internal/server/signals.go`:

```go
package server

import (
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/signals"
)

type signalView struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Active    int        `json:"active"`
	Error     string     `json:"error,omitempty"`
}

func (s *srv) getSignals(w http.ResponseWriter, _ *http.Request) {
	states := s.d.Signals()
	views := make([]signalView, 0, len(states))
	for _, st := range states {
		v := signalView{Name: st.Name, State: st.State, Active: st.Active, Error: st.Error}
		if !st.CheckedAt.IsZero() {
			at := st.CheckedAt
			v.CheckedAt = &at
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": views})
}
```
(`signals` is imported for the doc of the field type; if the compiler reports it unused, use `signals.State` in a signature or drop the import.) `internal/server/server.go`: `Deps` gets `Signals func() []signals.State // the state of the signal sources; when nil there is no GET /api/signals`; in `routes`: `if d.Signals != nil { public.HandleFunc("GET /api/signals", s.session(s.getSignals)) }`.

`internal/server/incidents.go`: `incidentView` gets

```go
	HostID      int64    `json:"hostId,omitempty"`
	HostName    string   `json:"hostName,omitempty"`
	AlsoMatches []string `json:"alsoMatches,omitempty"`
```
and `incidentViewOf` sets `HostID: in.HostID, HostName: in.HostName, AlsoMatches: alsoMatches(in.Details)` with

```go
// alsoMatches reads the names of the further hosts an alert matched from the details of an incident.
func alsoMatches(details json.RawMessage) []string {
	var d struct {
		Also []string `json:"alsoMatches"`
	}
	if json.Unmarshal(details, &d) != nil {
		return nil
	}
	return d.Also
}
```

- [ ] **Step 4: The app and the binary**

`internal/app/app.go`:
1. `settings.New(..., settings.WithKey(cfg.MasterKey))`.
2. After the settings reader and the engine exist, build the source and the runner:

```go
	var amClient *alertmanager.Client
	if c, err := alertmanager.New(func() (string, secret.Value, error) {
		base := set.Text(settings.AlertmanagerURL)
		tok, err := set.Secret(settings.AlertmanagerToken)
		switch {
		case errors.Is(err, settings.ErrNotSet):
			return base, secret.Value{}, nil
		case err != nil:
			return "", secret.Value{}, err
		}
		return base, tok, nil
	}, cfg.AlertmanagerCAFile, alertmanager.WithLog(log)); err != nil {
		log.Error("the Alertmanager source is off: cannot set up its client", "err", err)
	} else {
		amClient = c
	}
	sigs := &signals.Runner{
		Sources: []signals.Source{&signals.Alertmanager{
			Client: amClient,
			On:     func() bool { return amClient != nil && set.Text(settings.AlertmanagerURL) != "" },
			MaxAge: func() time.Duration { return set.Duration(settings.AlertmanagerAutoMaxAge) },
		}},
		Engine:   engine,
		Interval: func() time.Duration { return set.Duration(settings.PollInterval) },
		Log:      log,
		Hosts: func(ctx context.Context) ([]signals.HostRules, error) {
			entries, err := st.ListHosts(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]signals.HostRules, 0, len(entries))
			for _, e := range entries {
				out = append(out, signals.HostRules{ID: e.Host.ID, Name: e.Host.Name, Matchers: e.Host.Matchers})
			}
			return out, nil
		},
	}
```
(When `amClient` is nil, `Client` is a nil `*alertmanager.Client` inside the interface: `On` is false so `Fetch` is never called; guard anyway by giving `signals.Alertmanager` `Client: alertLister(amClient)` where `alertLister` returns `nil` interface for a nil pointer, or set `Client` only when non-nil.)
3. `App` gets `Signals *signals.Runner`, set in the returned struct. `server.Deps` gets `Signals: sigs.States`. The facts: when `cfg.AlertmanagerCAFile != ""` add `{Label: "Alertmanager CA file", Value: cfg.AlertmanagerCAFile}`.

`cmd/remedy-server/main.go`: after `background(a.Poller.Run)` add `background(a.Signals.Run)`.

- [ ] **Step 5: Run and commit**

Run: `gofmt -w internal cmd && go vet ./... && go build ./... && go test ./internal/config ./internal/signals ./internal/alertmanager/... ./internal/app && go test ./internal/server -run 'Signals|Incident|Host|Setting|Limits|Claim|Capab'`
Expected: PASS. Fix every existing test that counts settings or builds `server.Deps`/`incidentView` literals if the compiler asks.

```bash
git add internal cmd
git commit -m "feat: wire the Alertmanager source into the control plane

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: The Setup block, the host rules field and the host of an incident (web)

**Files:**
- Modify: `web/src/api.ts`, `web/src/settings.ts`, `web/src/settings.test.ts`, `web/src/incidents.ts`, `web/src/components/setup/SettingsSection.tsx`, `web/src/components/hosts/HostForm.tsx`, `web/src/IncidentThreadPage.tsx`, `web/src/hosts.test.ts` (if it builds a `Host`)
- Create: `web/src/signals.ts`, `web/src/signals.test.ts`, `web/src/components/setup/SecretRow.tsx`

**Interfaces:**
- Consumes: `GET /api/settings` (kinds `text` and `secret`, `hint`), `GET /api/signals`, the incident fields `hostId`, `hostName`, `alsoMatches`, the host field `matchers`.
- Produces (pure, in `web/src/signals.ts` and `incidents.ts`): `signalLine(s: SignalSource, now: number): string`, `hostLine(incident: Incident): { name: string; id: number } | null`, `alsoLine(incident: Incident): string`.

- [ ] **Step 1: Types and the pure modules, tests first**

`web/src/api.ts` additions:

```ts
export interface SignalSource {
  name: string
  state: 'off' | 'ok' | 'error' | 'pending'
  checkedAt?: string
  active: number
  error?: string
}
```
`api.getSignals: () => request<{ sources: SignalSource[] }>('GET', '/api/signals')`. `Incident` gets `hostId?: number`, `hostName?: string`, `alsoMatches?: string[]`.

`web/src/signals.test.ts` (new):

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { SignalSource } from './api.ts'
import { signalLine } from './signals.ts'

const at = Date.parse('2026-10-16T12:00:00Z')
const src = (over: Partial<SignalSource> = {}): SignalSource => ({ name: 'alertmanager', state: 'ok', checkedAt: '2026-10-16T11:59:48Z', active: 7, ...over })

describe('signalLine', () => {
  it('says that no address is set when the source is off', () => {
    assert.equal(signalLine(src({ state: 'off', checkedAt: undefined, active: 0 }), at), "I don't ask Alertmanager: no address is set.")
  })
  it('says that it has not asked yet', () => {
    assert.equal(signalLine(src({ state: 'pending', checkedAt: undefined }), at), "I haven't asked Alertmanager yet.")
  })
  it('says how long ago it asked and how many alerts it listed', () => {
    assert.equal(signalLine(src(), at), 'I asked Alertmanager 12 seconds ago and it listed 7 active alerts.')
    assert.equal(signalLine(src({ active: 1 }), at), 'I asked Alertmanager 12 seconds ago and it listed 1 active alert.')
    assert.equal(signalLine(src({ active: 0 }), at), 'I asked Alertmanager 12 seconds ago and it listed no active alerts.')
  })
  it('shows the error, and does not call the answer current', () => {
    assert.equal(
      signalLine(src({ state: 'error', error: 'alertmanager: HTTP 401' }), at),
      "I couldn't ask Alertmanager 12 seconds ago: alertmanager: HTTP 401. I changed nothing because of it.",
    )
  })
  it('copes with a missing or future time', () => {
    assert.equal(signalLine(src({ checkedAt: undefined }), at), 'I asked Alertmanager and it listed 7 active alerts.')
    assert.equal(signalLine(src({ checkedAt: '2026-10-16T12:05:00Z' }), at), 'I asked Alertmanager just now and it listed 7 active alerts.')
  })
})
```
`web/src/signals.ts`:

```ts
import type { SignalSource } from './api.ts'
import { duration } from './setup.ts'

function ago(checkedAt: string | undefined, now: number): string {
  if (!checkedAt) return ''
  const seconds = Math.round((now - Date.parse(checkedAt)) / 1000)
  if (!Number.isFinite(seconds)) return ''
  return seconds < 1 ? ' just now' : ` ${duration(seconds)} ago`
}

/** The state of a signal source as one sentence in Remedy's voice. `duration` words are those of the Setup page ("12 seconds"). */
export function signalLine(s: SignalSource, now: number): string {
  const name = s.name === 'alertmanager' ? 'Alertmanager' : s.name
  switch (s.state) {
    case 'off':
      return `I don't ask ${name}: no address is set.`
    case 'pending':
      return `I haven't asked ${name} yet.`
    case 'error':
      return `I couldn't ask ${name}${ago(s.checkedAt, now)}: ${s.error ?? 'it did not answer'}. I changed nothing because of it.`
  }
  const alerts = s.active === 0 ? 'no active alerts' : s.active === 1 ? '1 active alert' : `${s.active} active alerts`
  return `I asked ${name}${ago(s.checkedAt, now)} and it listed ${alerts}.`
}
```
Check `duration(seconds)` of `setup.ts`: its output for 12 is "12 seconds" (see `unit()`); adjust the expected strings of the test to what it returns if it differs (for example `1 minute`), keeping the test meaningful. Run `npm --prefix web test` to see the module tests fail, then pass.

`web/src/incidents.ts` additions with tests in `web/src/incidents.test.ts` (create it if missing; check `command ls web/src/incidents*.ts`):

```ts
export function hostLine(i: Incident): { name: string; id: number } | null {
  return i.hostId && i.hostName ? { name: i.hostName, id: i.hostId } : null
}

/** "also matches: a, b", or '' when no other host matched. */
export function alsoLine(i: Incident): string {
  return i.alsoMatches && i.alsoMatches.length > 0 ? `also matches: ${i.alsoMatches.join(', ')}` : ''
}
```
(tests: null without a host, the pair with one, `alsoLine` empty and joined.)

`web/src/settings.ts` additions with tests: `export const isSecret = (s: Setting) => s.kind === 'secret'` and

```ts
/** What a secret setting says about itself: whether a value is stored, never the value. */
export function secretLine(s: Setting): string {
  if (s.problem) return s.problem
  if (s.source !== 'set here') return 'No token is stored.'
  return s.hint && s.hint !== '…' ? `A token is stored (${s.hint}).` : 'A token is stored.'
}
```
and `groupSettings` keeps working. In `SettingsSection` the secret setting must not be rendered by the generic `Row`.

- [ ] **Step 2: `SecretRow.tsx`**

Create `web/src/components/setup/SecretRow.tsx`: the row of a secret setting, modelled on `GitHubSection` (look at how it takes its token: an uncontrolled `Input` with `type="password"`, `autoComplete="new-password"`, `data-1p-ignore data-lpignore="true" data-bwignore`, emptied as soon as the request is sent, also when it fails, never in storage or a URL). Props: `{ setting: Setting; busy: boolean; onSave: (value: string) => Promise<string>; onRemove: () => Promise<string> }` where each callback returns an error message or `''`. Behaviour:
- a heading line with the label and the badge (as `Row`), the `rule` and `effect` text, `secretLine(setting)` as a paragraph;
- an uncontrolled password input labelled by the setting's label ("Alertmanager token"), `Save` button (`aria-disabled={busy}`, guard in the handler; an empty field is a no-op) and, only while `setting.source === 'set here'`, a `ConfirmButton` "Remove token" / "Confirm: remove the token" (it keeps its own `disabled` prop unset, like the allowlist button; the handler guards `busy`);
- the field is cleared right after the request starts; a server message is shown under it as `role="alert"` text; after a successful save the focus stays on the Save button (it stays mounted); after a successful removal the focus moves to the input (the Remove button unmounts);
- the typed token is never put into React state (read it from the ref at submit).

- [ ] **Step 3: `SettingsSection.tsx`**

1. Render a setting with `isSecret` through `SecretRow`, every other through `Row` (inside `groupSettings`). `onSave` calls `api.putSetting(key, value)` and `onRemove` calls `api.deleteSetting(key)` through the existing `run` helper so the one-request-at-a-time guard, the best-effort refresh and the live-region notice work as for the other rows (notice texts: `"<label>: saved."` and `"<label>: removed."`, with no value in them).
2. Load `api.getSignals()` with the settings and after every successful change of a key of the group "Alertmanager"; keep it in state (`SignalSource | undefined`, the entry named `alertmanager`) and show under the Alertmanager group's rows a paragraph with `signalLine(state, now)` (`role="status"` is **not** needed: it is not announced). A failure to load it shows nothing.
3. Use `useNow` (see `web/src/useNow.ts`) or `Date.now()` re-evaluated when the state changes, as other pages do for relative times.
4. Keep every focus rule of the existing rows.

- [ ] **Step 4: The host rules field**

`web/src/components/hosts/HostForm.tsx`: add `matchers: host.matchers` to the form state and a `Field` after the notes:

```tsx
      <Field
        label="Alerts of this host"
        hint={'One rule per line, as in Alertmanager: for example instance=~"ugreen-nas(:[0-9]+)?". All lines must match. Empty: no alert is mapped to this host.'}
      >
        <textarea
          value={f.matchers}
          onChange={(e) => set('matchers')(e.target.value)}
          rows={3}
          spellCheck={false}
          className="min-h-20 rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm"
        />
      </Field>
```
and `matchers: f.matchers` in the `patchHost` body. The server's 400 text (`the alert rules: line 2: …`) reaches the page through `run` as for the other fields.

- [ ] **Step 5: The host on an incident**

`web/src/IncidentThreadPage.tsx`: in the header definition list (next to the severity row for non-GitHub incidents) add, when `hostLine(incident)` is not null, a row "Host" with a `Link` to `/hosts/${id}` showing the name, and when `alsoLine(incident)` is not empty a muted line below it. Nothing else on the page changes; `I can't diagnose incidents from Alertmanager yet.` stays (plan 4b).

- [ ] **Step 6: Check**

Run: `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
Expected: PASS. (Editor diagnostics in this worktree about missing modules are stale; the build is the authority.)

- [ ] **Step 7: Commit**

```bash
git add web
git commit -m "feat(web): the Alertmanager block in Setup, the rules of a host and the host of an incident

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Documents, the whole check, a look in the browser, the pull request

**Files:**
- Modify: `docs/design.md` (sections 2.5 and 2.7 and the roadmap line for signals), `docs/specs/2026-10-05-phase-2d-signals-design.md` (status table: the Alertmanager source is built by cycle 4a; the Argo CD source stays postponed), `docs/specs/2026-10-09-hosts-and-skills-design.md` (section 7: "4a built"), `docs/specs/2026-10-16-alertmanager-4-design.md` (the `Status:` line: 4a implemented, 4b not), `CLAUDE.md` ("Current state" bullets for hosts and skills and for phase 2D; the environment-variable paragraph gets `REMEDY_ALERTMANAGER_URL` as a default and `REMEDY_ALERTMANAGER_CA_FILE`), `README.md` (a short paragraph on connecting an Alertmanager and writing host rules)
- Create: `docs/runbook/alertmanager.md`

- [ ] **Step 1: Documents**

Check every fact against the code before you write it (routes, keys, file names). `docs/runbook/alertmanager.md` (short, practical): where to enter the address (an in-cluster example `http://vmalertmanager-...:9093` is **not** to be guessed: say "the address your cluster gives Alertmanager"), the optional token, what the state line says, how to write the rule for a host with the NAS example, what happens to an alert without a host (an incident without host tools), that an old alert is not diagnosed on its own and why, that nothing diagnoses these incidents until plan 4b, and the known limits (an Alertmanager that is switched off in Setup leaves its incidents as they are). In `CLAUDE.md` keep every other line.

- [ ] **Step 2: The whole check**

Run: `make check`
Expected: PASS (fmt, vet, `go test ./...`, chart check, shell tests, web lint, web test, web build). It can take many minutes: allow up to 10 minutes per command; if it is cut, run its parts separately (`gofmt -l .`, `go vet ./...`, `go test ./...`, `make chart-check`, `make shell-test`, `npm --prefix web run lint`, `npm --prefix web test`, `npm --prefix web run build`). Do not run `go test -race ./internal/server` (about 25 minutes); run the focused `-race` tests of the packages this plan added: `go test -race ./internal/alertmatch ./internal/alertmanager/... ./internal/signals ./internal/incident ./internal/settings`.

- [ ] **Step 3: Commit the documents**

```bash
git add docs CLAUDE.md README.md
git commit -m "docs: the Alertmanager source (cycle 4a) in the design, specs, CLAUDE.md, README and a runbook

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 4 (controller): a look in a real browser**

Build the UI and a throwaway server as CLAUDE.md describes (`make web-build`, `go build -tags webui -o <scratchpad>/remedy-server ./cmd/remedy-server`, `env REMEDY_ADDR=127.0.0.1:18080 REMEDY_DB=<scratchpad>/browser.db ... <scratchpad>/remedy-server`). Run a small fake Alertmanager (a Go test server, `python3 -m http.server` cannot serve the path; write a ten-line Go program in the scratchpad that serves `internal/alertmanager/alertmanagertest/testdata/home-server.json` at `/api/v2/alerts`, with the token check), then with Playwright and real clicks: enter the address and the token in Setup (the token field empties, the hint shows `…`+last four, the activity feed has no value), watch the state line change to "listed 4 active alerts" within one poll interval (set the poll interval to 10 s), see the incidents in Conversations, give the host `nas` the rule `instance=~"ugreen-nas(:[0-9]+)?"` (an invalid rule shows the server's message; a valid one maps the two NAS incidents within a cycle and the thread shows "Host: nas"), stop the fake and see the state line say it could not ask while the incidents stay open, remove the token (the confirm button arms on the first click). Delete stray `*.png` files and `.playwright-mcp/` afterwards.

- [ ] **Step 5 (controller): push and open the pull request**

Push the branch and open the pull request `feat: Alertmanager source and host rules (cycle 4a)` against `main`: what it does, the three rulings and decisions of this cycle, what was checked and what was not (the real run is plan 4b), and the attribution line. Watch CI; when all checks are green, squash-merge (standing permission), then update `main`.
