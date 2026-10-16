# Settings in the database (cycle 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Six values (poll interval, three diagnosis limits, the namespace allowlist, the run timeout) are changed in the Setup page and take effect without a restart; the environment stays their default.

**Architecture:** A new package `internal/settings` holds a registry (key, type, bounds, environment name, default) and a `Settings` reader with an atomic in-memory snapshot, rebuilt after every committed write. The table `settings` (migration 012) holds the overrides. Consumers (poller, responder, reaper, kube writer, gatekeeper action tools, claim handler) take a function instead of a value. `internal/config` parses the environment through the same registry.

**Tech Stack:** Go (stdlib, `modernc.org/sqlite`), React 19 + TypeScript with `node --test` for pure logic, Helm chart render tests.

**Spec:** `docs/specs/2026-10-14-settings-3-design.md` (umbrella: `docs/specs/2026-10-09-hosts-and-skills-design.md`, section 6).

## Global Constraints

- Everything in the repo is English: code, comments, docs, commit messages, UI copy.
- Go is stdlib-first; **no new dependency**.
- `Store.inTx` bodies use only the `tx`; a call on `s.db` inside deadlocks (one connection).
- Timestamps in SQLite have nine fractional digits (`formatTS`/`parseTS`).
- Every state change writes its `activity` entry **in the same transaction**.
- The environment is read at start-up and parsed with the **same bounds and the same error text** as today (`REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m`); the existing tests in `internal/config` must pass unchanged.
- Bounds (verbatim from the spec): `poll_interval` at least 10 s; `diagnose_cooldown` at least 1 m; `diagnose_max_per_incident` 0 to 20; `diagnose_max_per_day` 0 to 200 (0 turns automatic diagnosis off); `k8s_write_namespaces` checked by `kube.ParseNamespaces`; `run_timeout` at least 10 s. Defaults: 60 s, 15 m, 3, 20, empty, 10 m.
- Reaper threshold: `max(15 min, run_timeout + 5 min)`.
- Secrets never appear in `GET /api/settings`: not the admin password, not the runner token, not the master key, not even as "set".
- UI: a button that starts a request keeps the keyboard focus (`aria-disabled` plus a guard in the handler, not `disabled`); the namespace list saves through `ConfirmButton`; `web/tsconfig.app.json` has `erasableSyntaxOnly` and `verbatimModuleSyntax` (no enums, `import type`); modules in `web/src/*.ts` import relatively with `.ts` extensions.
- Run `gofmt -w` on every Go file you wrote before you commit (`make check` fails on an unformatted file).
- Fake `claude` scripts and shell in tests must run on macOS and Linux (no BSD-only flags).
- A new test file name must be free before creating it (`command ls <path>` first).
- Commit messages end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Pull request title: `feat: settings in the database (cycle 3)`.

## Review Focus

1. **An allowlist changed between the question and the action.** A namespace removed after the approval was asked for must be refused when the action runs (Task 5 test), and an empty list refuses everything.
2. **An invalid stored row after a downgrade or a hand edit.** It must not stop the start, must not shadow the environment's value, and must show up as a `problem` (Task 3 tests).
3. **A failed write must leave the snapshot as it was** (Task 3 test with a closed store).
4. **Readers racing a writer** (`-race`, Task 3).
5. **A long `run_timeout` and the reaper** (Task 6 test: threshold follows the timeout).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/settings/value.go` (new) | `Kind`, `Value`, its JSON and text forms |
| `internal/settings/registry.go` (new) | the six `Spec`s, `Lookup`, `Specs`, `ParseText`, `ParseJSON`, `Layer`, `EnvLayer`, `EnvValue` |
| `internal/settings/settings.go` (new) | `Settings`: snapshot, getters, `Set`, `Delete`, `Entries` |
| `internal/store/migrations/012_settings.sql` (new), `internal/store/settings.go` (new) | the table and its three methods |
| `internal/config/config.go` | environment parsed through the registry; `Server.Env` |
| `internal/kube/config.go`, `internal/kube/writer.go` | a live allowlist |
| `internal/gatekeeper/tools_cluster_actions.go` | reads the allowlist per call |
| `internal/poller/poller.go`, `internal/responder/responder.go`, `internal/reaper/reaper.go` | function hooks |
| `internal/run/run.go`, `internal/server/runnerapi.go`, `internal/runner/loop.go` | the timeout travels with the claim |
| `internal/server/settings.go` (new), `server.go`, `responder.go`, `capabilities.go` | the API |
| `internal/app/app.go`, `cmd/remedy-server/main.go` | wiring |
| `deploy/chart/templates/server-deployment.yaml`, `values.yaml`, `deploy/chart_test.go` | the timeout reaches the control plane |
| `web/src/api.ts`, `web/src/settings.ts` (new), `web/src/components/setup/SettingsSection.tsx` (new), `SetupPage.tsx` | the Setup page |

---

### Task 1: The registry and the values

**Files:**
- Create: `internal/settings/value.go`, `internal/settings/registry.go`, `internal/settings/registry_test.go`

**Interfaces:**
- Produces (used by every later task):
  - `type Kind string` with `KindDuration`, `KindNumber`, `KindList`.
  - `type Value struct { Dur time.Duration; Num int; List []string }`, `func (v Value) JSON(k Kind) json.RawMessage`, `func (v Value) Text(k Kind) string`.
  - Key constants `PollInterval`, `DiagnoseCooldown`, `DiagnoseMaxPerIncident`, `DiagnoseMaxPerDay`, `K8sWriteNamespaces`, `RunTimeout`.
  - `type Spec struct { Key, Group, Label, Env string; Kind Kind; Default Value; Rule, Effect string; ... }`, `func Specs() []Spec`, `func Lookup(key string) (Spec, bool)`, `func (s Spec) ParseText(text string) (Value, error)`, `func (s Spec) ParseJSON(raw json.RawMessage) (Value, error)`.
  - `type Layer map[string]Value` with `Duration(key) time.Duration`, `Int(key) int`, `List(key) []string` (the layer's value or the registry default), `func EnvLayer(get func(string) string) (Layer, error)`, `func EnvValue(key string, get func(string) string) (Value, bool, error)`.

- [ ] **Step 1: Check the names are free**

Run: `command ls internal/settings`
Expected: `ls` reports that the directory does not exist.

- [ ] **Step 2: Write the failing tests**

Create `internal/settings/registry_test.go`:

```go
package settings_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/settings"
)

func spec(t *testing.T, key string) settings.Spec {
	t.Helper()
	s, ok := settings.Lookup(key)
	if !ok {
		t.Fatalf("no spec for %q", key)
	}
	return s
}

func TestEveryDefaultPassesItsOwnBoundsAndSurvivesTheJSONRoundTrip(t *testing.T) {
	for _, s := range settings.Specs() {
		v, err := s.ParseJSON(s.Default.JSON(s.Kind))
		if err != nil {
			t.Errorf("%s: the default %s does not parse: %v", s.Key, s.Default.JSON(s.Kind), err)
			continue
		}
		if v.Text(s.Kind) != s.Default.Text(s.Kind) {
			t.Errorf("%s: round trip %q, want %q", s.Key, v.Text(s.Kind), s.Default.Text(s.Kind))
		}
		if s.Env == "" || s.Label == "" || s.Group == "" || s.Rule == "" || s.Effect == "" {
			t.Errorf("%s: a field of the spec is empty: %+v", s.Key, s)
		}
	}
}

func TestTheTextOfAValueIsCheckedAgainstTheBounds(t *testing.T) {
	cases := []struct {
		key, text string
		ok        bool
	}{
		{settings.PollInterval, "10s", true}, {settings.PollInterval, "2m", true}, {settings.PollInterval, "9s", false},
		{settings.PollInterval, "abc", false}, {settings.PollInterval, "60", false},
		{settings.DiagnoseCooldown, "1m", true}, {settings.DiagnoseCooldown, "59s", false},
		{settings.DiagnoseMaxPerIncident, "0", true}, {settings.DiagnoseMaxPerIncident, "20", true},
		{settings.DiagnoseMaxPerIncident, "21", false}, {settings.DiagnoseMaxPerIncident, "-1", false}, {settings.DiagnoseMaxPerIncident, "x", false},
		{settings.DiagnoseMaxPerDay, "0", true}, {settings.DiagnoseMaxPerDay, "200", true}, {settings.DiagnoseMaxPerDay, "201", false},
		{settings.RunTimeout, "10s", true}, {settings.RunTimeout, "9s", false}, {settings.RunTimeout, "1h", true},
		{settings.K8sWriteNamespaces, "demo,staging", true}, {settings.K8sWriteNamespaces, "", true}, {settings.K8sWriteNamespaces, "Not A Name", false},
	}
	for _, c := range cases {
		if _, err := spec(t, c.key).ParseText(c.text); (err == nil) != c.ok {
			t.Errorf("%s %q: error = %v, want ok = %v", c.key, c.text, err, c.ok)
		}
	}
}

func TestTheListDropsDuplicatesAndEmptyEntries(t *testing.T) {
	v, err := spec(t, settings.K8sWriteNamespaces).ParseText("demo, staging,,demo")
	if err != nil || !slices.Equal(v.List, []string{"demo", "staging"}) {
		t.Fatalf("list = %v, %v", v.List, err)
	}
}

func TestTheJSONFormIsAStringForADurationANumberAndAnArray(t *testing.T) {
	good := []struct {
		key, raw string
		check    func(settings.Value) bool
	}{
		{settings.PollInterval, `"30s"`, func(v settings.Value) bool { return v.Dur == 30*time.Second }},
		{settings.DiagnoseMaxPerDay, `5`, func(v settings.Value) bool { return v.Num == 5 }},
		{settings.K8sWriteNamespaces, `["demo","staging"]`, func(v settings.Value) bool { return slices.Equal(v.List, []string{"demo", "staging"}) }},
	}
	for _, c := range good {
		v, err := spec(t, c.key).ParseJSON(json.RawMessage(c.raw))
		if err != nil || !c.check(v) {
			t.Errorf("%s %s: %+v, %v", c.key, c.raw, v, err)
		}
	}
	for _, c := range []struct{ key, raw string }{
		{settings.PollInterval, `900`}, {settings.PollInterval, `"soon"`}, {settings.DiagnoseMaxPerDay, `"5"`}, {settings.DiagnoseMaxPerDay, `3.5`},
		{settings.K8sWriteNamespaces, `"demo"`}, {settings.K8sWriteNamespaces, `[1]`}, {settings.PollInterval, `null`}, {settings.DiagnoseMaxPerDay, ``},
	} {
		if _, err := spec(t, c.key).ParseJSON(json.RawMessage(c.raw)); err == nil {
			t.Errorf("%s %s was accepted", c.key, c.raw)
		}
	}
}

func TestADurationIsWrittenTheWayAPersonWritesIt(t *testing.T) {
	for d, want := range map[time.Duration]string{90 * time.Second: `"90s"`, 15 * time.Minute: `"15m"`, time.Hour: `"1h"`, 61 * time.Second: `"1m1s"`} {
		if got := string((settings.Value{Dur: d}).JSON(settings.KindDuration)); got != want {
			t.Errorf("%s = %s, want %s", d, got, want)
		}
	}
	if got := string((settings.Value{List: []string{"a", "b"}}).JSON(settings.KindList)); got != `["a","b"]` {
		t.Errorf("list = %s", got)
	}
	if got := string((settings.Value{}).JSON(settings.KindList)); got != `[]` {
		t.Errorf("empty list = %s, want []", got)
	}
	if got := (settings.Value{}).Text(settings.KindList); got != "none" {
		t.Errorf("empty list text = %q", got)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestTheEnvironmentLayerHoldsOnlyWhatWasSet(t *testing.T) {
	l, err := settings.EnvLayer(env(map[string]string{"REMEDY_POLL_INTERVAL": "30s", "REMEDY_K8S_WRITE_NAMESPACES": "demo"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l[settings.DiagnoseCooldown]; ok || len(l) != 2 {
		t.Fatalf("layer = %v", l)
	}
	if l.Duration(settings.PollInterval) != 30*time.Second || l.Duration(settings.DiagnoseCooldown) != 15*time.Minute {
		t.Errorf("poll = %v, cooldown = %v", l.Duration(settings.PollInterval), l.Duration(settings.DiagnoseCooldown))
	}
	if l.Int(settings.DiagnoseMaxPerDay) != 20 || !slices.Equal(l.List(settings.K8sWriteNamespaces), []string{"demo"}) {
		t.Errorf("per day = %d, namespaces = %v", l.Int(settings.DiagnoseMaxPerDay), l.List(settings.K8sWriteNamespaces))
	}
}

func TestAnInvalidEnvironmentValueIsRefusedWithTheTextTheEnvironmentAlwaysHad(t *testing.T) {
	for name, want := range map[string]string{
		"REMEDY_POLL_INTERVAL":               "REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m",
		"REMEDY_DIAGNOSE_COOLDOWN":           "REMEDY_DIAGNOSE_COOLDOWN must be a duration of at least 1m, for example 15m",
		"REMEDY_DIAGNOSE_MAX_PER_INCIDENT":   "REMEDY_DIAGNOSE_MAX_PER_INCIDENT must be a whole number from 0 to 20",
		"REMEDY_DIAGNOSE_MAX_PER_DAY":        "REMEDY_DIAGNOSE_MAX_PER_DAY must be a whole number from 0 to 200",
		"REMEDY_RUN_TIMEOUT":                 "REMEDY_RUN_TIMEOUT must be a duration of at least 10s, for example 10m",
	} {
		_, err := settings.EnvLayer(env(map[string]string{name: "nonsense"}))
		if err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", name, err, want)
		}
	}
	_, err := settings.EnvLayer(env(map[string]string{"REMEDY_K8S_WRITE_NAMESPACES": "Not A Name"}))
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_K8S_WRITE_NAMESPACES: ") {
		t.Errorf("namespaces: error = %v", err)
	}
}

func TestEnvValueReadsOneKey(t *testing.T) {
	v, set, err := settings.EnvValue(settings.RunTimeout, env(map[string]string{"REMEDY_RUN_TIMEOUT": "20m", "REMEDY_POLL_INTERVAL": "nonsense"}))
	if err != nil || !set || v.Dur != 20*time.Minute {
		t.Fatalf("%+v %v %v: another key's mistake must not matter", v, set, err)
	}
	if _, set, err := settings.EnvValue(settings.RunTimeout, env(nil)); err != nil || set {
		t.Fatalf("unset: set = %v, err = %v", set, err)
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/settings`
Expected: FAIL, the package does not compile (`undefined: settings.Lookup` and so on).

- [ ] **Step 4: Write `value.go`**

```go
package settings

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of a setting.
type Kind string

const (
	KindDuration Kind = "duration"
	KindNumber   Kind = "number"
	KindList     Kind = "list"
)

// Value is the value of one setting. Only the field of the spec's Kind is used.
type Value struct {
	Dur  time.Duration
	Num  int
	List []string
}

// JSON is the form that is stored and sent: a string for a duration ("15m"), a number, an array of strings.
func (v Value) JSON(k Kind) json.RawMessage {
	var b []byte
	switch k {
	case KindDuration:
		b, _ = json.Marshal(formatDuration(v.Dur))
	case KindNumber:
		b = []byte(strconv.Itoa(v.Num))
	default:
		list := v.List
		if list == nil {
			list = []string{}
		}
		b, _ = json.Marshal(list)
	}
	return b
}

// Text is the form a person reads, in the activity entries and the logs: "15m", "3", "demo,staging", "none".
func (v Value) Text(k Kind) string {
	switch k {
	case KindDuration:
		return formatDuration(v.Dur)
	case KindNumber:
		return strconv.Itoa(v.Num)
	}
	if len(v.List) == 0 {
		return "none"
	}
	return strings.Join(v.List, ",")
}

// formatDuration writes a duration the way it is typed: 15m, 90s, 1h; an odd one in Go's own form.
func formatDuration(d time.Duration) string {
	switch {
	case d > 0 && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d > 0 && d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d > 0 && d%time.Second == 0:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return d.String()
}
```

- [ ] **Step 5: Write `registry.go`**

```go
// Package settings holds the values the maintainer changes in the Setup page. A registry in the code says which keys exist and what
// each may hold; the environment is the default, a row in the table `settings` overrides it, and a Settings reader answers every
// component from an in-memory snapshot.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
)

// The keys.
const (
	PollInterval           = "poll_interval"
	DiagnoseCooldown       = "diagnose_cooldown"
	DiagnoseMaxPerIncident = "diagnose_max_per_incident"
	DiagnoseMaxPerDay      = "diagnose_max_per_day"
	K8sWriteNamespaces     = "k8s_write_namespaces"
	RunTimeout             = "run_timeout"
)

// Spec says what one setting is. problem is the sentence that follows the variable's name when a value is refused; check is the
// bounds of the value.
type Spec struct {
	Key     string
	Group   string // the heading on the Setup page
	Label   string
	Env     string // the environment variable that gives the default
	Kind    Kind
	Default Value
	Rule    string // the bounds, for the Setup page
	Effect  string // when a change takes effect, for the Setup page
	problem string
	check   func(Value) bool
}

var specs = []Spec{
	{
		Key: PollInterval, Group: "Polling", Label: "How often I check GitHub", Env: "REMEDY_POLL_INTERVAL",
		Kind: KindDuration, Default: Value{Dur: time.Minute}, Rule: "At least 10s, for example 60s or 2m.",
		Effect:  "Takes effect at the next check.",
		problem: "must be a duration of at least 10s, for example 60s or 2m",
		check:   func(v Value) bool { return v.Dur >= 10*time.Second },
	},
	{
		Key: DiagnoseCooldown, Group: "Diagnosis limits", Label: "Time between two automatic diagnoses of one incident", Env: "REMEDY_DIAGNOSE_COOLDOWN",
		Kind: KindDuration, Default: Value{Dur: 15 * time.Minute}, Rule: "At least 1m, for example 15m.",
		Effect:  "Takes effect at the next automatic diagnosis.",
		problem: "must be a duration of at least 1m, for example 15m",
		check:   func(v Value) bool { return v.Dur >= time.Minute },
	},
	{
		Key: DiagnoseMaxPerIncident, Group: "Diagnosis limits", Label: "Automatic diagnoses per incident", Env: "REMEDY_DIAGNOSE_MAX_PER_INCIDENT",
		Kind: KindNumber, Default: Value{Num: 3}, Rule: "A whole number from 0 to 20.",
		Effect:  "Takes effect at the next automatic diagnosis.",
		problem: "must be a whole number from 0 to 20",
		check:   func(v Value) bool { return v.Num >= 0 && v.Num <= 20 },
	},
	{
		Key: DiagnoseMaxPerDay, Group: "Diagnosis limits", Label: "Automatic diagnoses per 24 hours", Env: "REMEDY_DIAGNOSE_MAX_PER_DAY",
		Kind: KindNumber, Default: Value{Num: 20}, Rule: "A whole number from 0 to 200. 0 turns automatic diagnosis off.",
		Effect:  "Takes effect at the next automatic diagnosis.",
		problem: "must be a whole number from 0 to 200",
		check:   func(v Value) bool { return v.Num >= 0 && v.Num <= 200 },
	},
	{
		Key: K8sWriteNamespaces, Group: "Cluster actions", Label: "Namespaces where I may act", Env: "REMEDY_K8S_WRITE_NAMESPACES",
		Kind: KindList, Rule: "Namespace names, separated by commas. Empty: I refuse every action.",
		Effect: "Checked before an approval is asked for and again when the action runs, so a change counts for a call that already waits.",
	},
	{
		Key: RunTimeout, Group: "Runs", Label: "Longest a run may take", Env: "REMEDY_RUN_TIMEOUT",
		Kind: KindDuration, Default: Value{Dur: 10 * time.Minute}, Rule: "At least 10s, for example 10m. Time spent waiting for an approval does not count.",
		Effect:  "Takes effect for the next run that starts.",
		problem: "must be a duration of at least 10s, for example 10m",
		check:   func(v Value) bool { return v.Dur >= 10*time.Second },
	},
}

// Specs returns the registry in the order the Setup page shows it.
func Specs() []Spec { return append([]Spec(nil), specs...) }

// Lookup finds the spec of a key.
func Lookup(key string) (Spec, bool) {
	for _, s := range specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// ParseText reads the form of the environment and of the input field: "30s", "5", "demo,staging". The error says what is wrong
// and does not name the setting.
func (s Spec) ParseText(text string) (Value, error) {
	var v Value
	switch s.Kind {
	case KindDuration:
		d, err := time.ParseDuration(text)
		if err != nil {
			return Value{}, errors.New(s.problem)
		}
		v.Dur = d
	case KindNumber:
		n, err := strconv.Atoi(text)
		if err != nil {
			return Value{}, errors.New(s.problem)
		}
		v.Num = n
	default:
		list, err := kube.ParseNamespaces(text)
		if err != nil {
			return Value{}, err
		}
		v.List = list
	}
	if s.check != nil && !s.check(v) {
		return Value{}, errors.New(s.problem)
	}
	return v, nil
}

// ParseJSON reads the stored and transported form (see Value.JSON).
func (s Spec) ParseJSON(raw json.RawMessage) (Value, error) {
	switch s.Kind {
	case KindDuration:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return Value{}, fmt.Errorf("%s (write it as a string such as \"15m\")", s.problem)
		}
		return s.ParseText(strings.TrimSpace(text))
	case KindNumber:
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return Value{}, errors.New(s.problem)
		}
		return s.ParseText(strconv.Itoa(n))
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return Value{}, errors.New("must be a list of namespace names")
	}
	return s.ParseText(strings.Join(list, ","))
}

// envError names the variable in front of the sentence the way the configuration always did.
func (s Spec) envError(err error) error {
	if s.Kind == KindList {
		return fmt.Errorf("%s: %w", s.Env, err)
	}
	return fmt.Errorf("%s %w", s.Env, err)
}

// Layer is a set of values by key. The environment layer holds only the variables that were set.
type Layer map[string]Value

func (l Layer) value(key string) Value {
	if v, ok := l[key]; ok {
		return v
	}
	s, ok := Lookup(key)
	if !ok {
		panic("settings: unknown key " + key)
	}
	return s.Default
}

// Duration is the layer's value of a key, or the default.
func (l Layer) Duration(key string) time.Duration { return l.value(key).Dur }

// Int is the layer's value of a key, or the default.
func (l Layer) Int(key string) int { return l.value(key).Num }

// List is the layer's value of a key, or the default.
func (l Layer) List(key string) []string { return append([]string(nil), l.value(key).List...) }

// EnvLayer reads every variable of the registry. An empty variable counts as not set. A value that is refused stops the start.
func EnvLayer(get func(string) string) (Layer, error) {
	l := Layer{}
	for _, s := range specs {
		text := get(s.Env)
		if text == "" {
			continue
		}
		v, err := s.ParseText(text)
		if err != nil {
			return nil, s.envError(err)
		}
		l[s.Key] = v
	}
	return l, nil
}

// EnvValue reads the variable of one key. The runner uses it for REMEDY_RUN_TIMEOUT, the only one it knows.
func EnvValue(key string, get func(string) string) (Value, bool, error) {
	s, ok := Lookup(key)
	if !ok {
		panic("settings: unknown key " + key)
	}
	text := get(s.Env)
	if text == "" {
		return Value{}, false, nil
	}
	v, err := s.ParseText(text)
	if err != nil {
		return Value{}, false, s.envError(err)
	}
	return v, true, nil
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/settings && go vet ./internal/settings`
Expected: PASS. If `TestTheJSONFormIs...` fails on `` (empty raw): `json.Unmarshal` of empty input errors, so it is refused; a failure means a branch accepts it, fix the branch.

- [ ] **Step 7: Make the spec say `run_timeout_seconds`**

In `docs/specs/2026-10-14-settings-3-design.md` replace `runTimeoutSeconds` (two places: section 3 "Runner path" and section 4) with `run_timeout_seconds` (the claim's other fields are snake case: `mcp_token`).

- [ ] **Step 8: Commit**

```bash
git add internal/settings docs/specs/2026-10-14-settings-3-design.md
git commit -m "feat: the registry of settings and their values

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The table and its store methods

**Files:**
- Create: `internal/store/migrations/012_settings.sql`, `internal/store/settings.go`, `internal/store/settings_test.go`

**Interfaces:**
- Consumes: `insertActivity(ctx, tx, NewActivity)`, `s.inTx`, `formatTS`, `parseTS` (all in package `store`).
- Produces:
  - `const KindSettingChanged = "setting_changed"`.
  - `type Setting struct { Key string; Value string; UpdatedAt time.Time }`.
  - `func (s *Store) ListSettings(ctx context.Context) ([]Setting, error)`.
  - `func (s *Store) SetSetting(ctx context.Context, key, value string, act NewActivity) error` (upsert and the activity entry in one transaction).
  - `func (s *Store) DeleteSetting(ctx context.Context, key string, act NewActivity) (bool, error)` (the activity entry only when a row was removed).

- [ ] **Step 1: Check the names are free**

Run: `command ls internal/store/settings.go internal/store/migrations/012_settings.sql`
Expected: both reported missing.

- [ ] **Step 2: Write the failing tests**

Create `internal/store/settings_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func settingActivity(summary string) store.NewActivity {
	return store.NewActivity{Kind: store.KindSettingChanged, Summary: summary}
}

func TestASettingIsSetReplacedListedAndDeleted(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	if rows, err := s.ListSettings(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("a new database: %+v, %v", rows, err)
	}
	if err := s.SetSetting(ctx, "poll_interval", `"30s"`, settingActivity("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "poll_interval", `"45s"`, settingActivity("second")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "diagnose_max_per_day", `5`, settingActivity("third")); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListSettings(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if rows[0].Key != "diagnose_max_per_day" || rows[0].Value != `5` || rows[1].Key != "poll_interval" || rows[1].Value != `"45s"` || rows[1].UpdatedAt.IsZero() {
		t.Fatalf("rows = %+v", rows)
	}

	removed, err := s.DeleteSetting(ctx, "poll_interval", settingActivity("reset"))
	if err != nil || !removed {
		t.Fatalf("delete = %v, %v", removed, err)
	}
	removed, err = s.DeleteSetting(ctx, "poll_interval", settingActivity("reset again"))
	if err != nil || removed {
		t.Fatalf("deleting a key that is not there = %v, %v", removed, err)
	}
	if rows, _ := s.ListSettings(ctx); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestEveryChangeOfASettingWritesItsActivityEntryAndNothingElseDoes(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	_ = s.SetSetting(ctx, "poll_interval", `"30s"`, settingActivity("set"))
	_, _ = s.DeleteSetting(ctx, "poll_interval", settingActivity("reset"))
	_, _ = s.DeleteSetting(ctx, "poll_interval", settingActivity("must not appear"))

	got := activitySummaries(t, s) // newest first
	if len(got) != 2 || got[0] != "reset" || got[1] != "set" {
		t.Fatalf("activity = %v", got)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{})
	if log[0].Kind != store.KindSettingChanged {
		t.Errorf("kind = %q", log[0].Kind)
	}
}
```

(`activitySummaries` and `openStore` already exist in the `store_test` package; if `activitySummaries` returns oldest first in this repository, flip the two expected positions.)

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/store -run 'Setting'`
Expected: FAIL to compile (`s.ListSettings undefined`).

- [ ] **Step 4: Write the migration**

`internal/store/migrations/012_settings.sql`:

```sql
-- Settings the maintainer changes in the Setup page. The registry in the code knows the keys; a row whose key or value the
-- registry no longer accepts is ignored at start-up. value is JSON: a string for a duration, a number, an array of strings.
CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

- [ ] **Step 5: Write `internal/store/settings.go`**

```go
package store

import (
	"context"
	"database/sql"
	"time"
)

// KindSettingChanged is the activity entry of a setting that was set in the Setup page or reset.
const KindSettingChanged = "setting_changed"

// Setting is one override: the key, its value as JSON and when it was written.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// ListSettings returns every override, by key.
func (s *Store) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, updated_at FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var st Setting
		var at string
		if err := rows.Scan(&st.Key, &st.Value, &at); err != nil {
			return nil, err
		}
		if st.UpdatedAt, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SetSetting writes an override, and the activity entry that says so, in one transaction.
func (s *Store) SetSetting(ctx context.Context, key, value string, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, value, formatTS(time.Now())); err != nil {
			return err
		}
		return insertActivity(ctx, tx, act)
	})
}

// DeleteSetting removes an override. It reports whether there was one; only then is the activity entry written.
func (s *Store) DeleteSetting(ctx context.Context, key string, act NewActivity) (bool, error) {
	removed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		removed = true
		return insertActivity(ctx, tx, act)
	})
	return removed, err
}
```

- [ ] **Step 6: Run the store tests**

Run: `go test ./internal/store`
Expected: PASS (all of them: a test that counts migrations or names the newest must still hold; if one asserts the number 11, change it to 12).

- [ ] **Step 7: Commit**

```bash
git add internal/store
git commit -m "feat: the table of settings (migration 012)

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The reader

**Files:**
- Create: `internal/settings/settings.go`, `internal/settings/settings_test.go`

**Interfaces:**
- Consumes: Task 1 (`Spec`, `Layer`, `Value`, `Lookup`, `Specs`), Task 2 (`store.Setting`, `ListSettings`, `SetSetting`, `DeleteSetting`, `store.KindSettingChanged`, `store.NewActivity`, `store.DiagnosisLimits`).
- Produces:
  - `type Source string` with `SourceDefault = "default"`, `SourceEnvironment = "environment"`, `SourceSet = "set here"`.
  - `type Entry struct { Spec Spec; Value Value; Source Source; Fallback Source; Problem string }` (`Fallback` is the source the value has after a reset).
  - `func New(ctx context.Context, st *store.Store, env Layer, log *slog.Logger) *Settings` (never fails: an unreadable table is logged and the environment and the defaults apply).
  - `(*Settings).Duration(key) time.Duration`, `Int(key) int`, `List(key) []string` (a copy), `Entry(key) (Entry, bool)`, `Entries() []Entry` (registry order), `DiagnosisLimits() store.DiagnosisLimits`.
  - `(*Settings).Set(ctx, key string, raw json.RawMessage) (Entry, error)`, `(*Settings).Delete(ctx, key string) (Entry, error)`.
  - `var ErrUnknownKey`, `type InvalidError struct{ Message string }` (implements `error`).
  - The getters panic on an unknown key: a key is a constant of this package, so that is a programming error.

- [ ] **Step 1: Write the failing tests**

Create `internal/settings/settings_test.go`:

```go
package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

func newReader(t *testing.T, env settings.Layer) (*settings.Settings, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return settings.New(context.Background(), st, env, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func entry(t *testing.T, s *settings.Settings, key string) settings.Entry {
	t.Helper()
	e, ok := s.Entry(key)
	if !ok {
		t.Fatalf("no entry for %q", key)
	}
	return e
}

func TestTheValueComesFromTheDatabaseThenTheEnvironmentThenTheDefault(t *testing.T) {
	ctx := context.Background()
	s, _ := newReader(t, settings.Layer{settings.PollInterval: {Dur: 30 * time.Second}})

	if e := entry(t, s, settings.DiagnoseCooldown); e.Source != settings.SourceDefault || e.Value.Dur != 15*time.Minute || e.Fallback != settings.SourceDefault {
		t.Errorf("cooldown = %+v", e)
	}
	if e := entry(t, s, settings.PollInterval); e.Source != settings.SourceEnvironment || e.Value.Dur != 30*time.Second || e.Fallback != settings.SourceEnvironment {
		t.Errorf("poll = %+v", e)
	}
	e, err := s.Set(ctx, settings.PollInterval, json.RawMessage(`"20s"`))
	if err != nil || e.Source != settings.SourceSet || e.Value.Dur != 20*time.Second || e.Fallback != settings.SourceEnvironment {
		t.Fatalf("after Set: %+v, %v", e, err)
	}
	if s.Duration(settings.PollInterval) != 20*time.Second {
		t.Errorf("Duration = %v", s.Duration(settings.PollInterval))
	}
	e, err = s.Delete(ctx, settings.PollInterval)
	if err != nil || e.Source != settings.SourceEnvironment || e.Value.Dur != 30*time.Second {
		t.Fatalf("after Delete: %+v, %v", e, err)
	}
}

func TestTheGettersAnswerForEveryKind(t *testing.T) {
	ctx := context.Background()
	s, _ := newReader(t, nil)
	if _, err := s.Set(ctx, settings.K8sWriteNamespaces, json.RawMessage(`["demo","staging"]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, settings.DiagnoseMaxPerDay, json.RawMessage(`7`)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.List(settings.K8sWriteNamespaces), []string{"demo", "staging"}) || s.Int(settings.DiagnoseMaxPerDay) != 7 {
		t.Fatalf("list = %v, int = %d", s.List(settings.K8sWriteNamespaces), s.Int(settings.DiagnoseMaxPerDay))
	}
	got := s.List(settings.K8sWriteNamespaces)
	got[0] = "changed"
	if s.List(settings.K8sWriteNamespaces)[0] != "demo" {
		t.Error("List hands out the snapshot's own slice")
	}
	limits := s.DiagnosisLimits()
	if limits.MaxPerDay != 7 || limits.MaxPerIncident != 3 || limits.Cooldown != 15*time.Minute {
		t.Errorf("limits = %+v", limits)
	}
}

func TestEntriesComeInTheOrderOfTheRegistry(t *testing.T) {
	s, _ := newReader(t, nil)
	var got, want []string
	for _, e := range s.Entries() {
		got = append(got, e.Spec.Key)
	}
	for _, sp := range settings.Specs() {
		want = append(want, sp.Key)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestAValueOutsideTheBoundsChangesNothing(t *testing.T) {
	ctx := context.Background()
	s, st := newReader(t, nil)
	_, err := s.Set(ctx, settings.PollInterval, json.RawMessage(`"5s"`))
	var invalid *settings.InvalidError
	if !errors.As(err, &invalid) || !strings.HasPrefix(invalid.Message, "poll_interval must be a duration of at least 10s") {
		t.Fatalf("error = %v", err)
	}
	if s.Duration(settings.PollInterval) != time.Minute {
		t.Errorf("the snapshot changed: %v", s.Duration(settings.PollInterval))
	}
	if rows, _ := st.ListSettings(ctx); len(rows) != 0 {
		t.Errorf("a row was written: %+v", rows)
	}
	if got := activityTexts(t, st); len(got) != 0 {
		t.Errorf("an activity entry was written: %v", got)
	}
	if _, err := s.Set(ctx, "no_such_key", json.RawMessage(`1`)); !errors.Is(err, settings.ErrUnknownKey) {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := s.Delete(ctx, "no_such_key"); !errors.Is(err, settings.ErrUnknownKey) {
		t.Errorf("unknown key on delete: %v", err)
	}
}

func activityTexts(t *testing.T, st *store.Store) []string {
	t.Helper()
	log, err := st.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		out = append(out, a.Summary)
	}
	return out
}

func TestEveryChangeIsAnActivityEntryWithTheOldAndTheNewValue(t *testing.T) {
	ctx := context.Background()
	s, st := newReader(t, nil)
	_, _ = s.Set(ctx, settings.PollInterval, json.RawMessage(`"30s"`))
	_, _ = s.Delete(ctx, settings.PollInterval)
	_, _ = s.Delete(ctx, settings.PollInterval) // nothing to reset: no entry

	got := activityTexts(t, st) // newest first
	if len(got) != 2 ||
		got[1] != "Setting poll_interval changed from 1m (default) to 30s (set here)" ||
		got[0] != "Setting poll_interval reset from 30s (set here) to 1m (default)" {
		t.Fatalf("activity = %q", got)
	}
}

func TestAStoredValueThatIsNoLongerValidIsIgnoredAndReported(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	act := store.NewActivity{Kind: store.KindSettingChanged, Summary: "x"}
	_ = st.SetSetting(ctx, settings.PollInterval, `"5s"`, act)         // below the bound
	_ = st.SetSetting(ctx, "a_key_from_the_future", `1`, act)         // not in the registry
	_ = st.SetSetting(ctx, settings.DiagnoseMaxPerDay, `"many"`, act) // the wrong type

	s := settings.New(ctx, st, settings.Layer{settings.PollInterval: {Dur: 40 * time.Second}}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	e := entry(t, s, settings.PollInterval)
	if e.Value.Dur != 40*time.Second || e.Source != settings.SourceEnvironment || e.Problem == "" {
		t.Errorf("poll = %+v: the environment's value must stay, with a problem", e)
	}
	if e := entry(t, s, settings.DiagnoseMaxPerDay); e.Value.Num != 20 || e.Source != settings.SourceDefault || e.Problem == "" {
		t.Errorf("per day = %+v", e)
	}
	if e := entry(t, s, settings.RunTimeout); e.Problem != "" {
		t.Errorf("run timeout has a problem: %+v", e)
	}
	// A good value replaces the bad row and clears the problem.
	if e, err := s.Set(ctx, settings.PollInterval, json.RawMessage(`"20s"`)); err != nil || e.Problem != "" || e.Source != settings.SourceSet {
		t.Errorf("after a good Set: %+v, %v", e, err)
	}
}

func TestAFailedWriteLeavesTheSnapshotAsItWas(t *testing.T) {
	s, st := newReader(t, nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(context.Background(), settings.PollInterval, json.RawMessage(`"20s"`)); err == nil {
		t.Fatal("a write to a closed store succeeded")
	}
	if s.Duration(settings.PollInterval) != time.Minute {
		t.Errorf("the snapshot changed to %v", s.Duration(settings.PollInterval))
	}
}

func TestAnUnreadableTableMeansTheEnvironmentAndTheDefaults(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	s := settings.New(context.Background(), st, settings.Layer{settings.RunTimeout: {Dur: 20 * time.Minute}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s.Duration(settings.RunTimeout) != 20*time.Minute || s.Duration(settings.PollInterval) != time.Minute {
		t.Fatalf("run timeout = %v, poll = %v", s.Duration(settings.RunTimeout), s.Duration(settings.PollInterval))
	}
}

func TestReadingWhileWritingIsSafe(t *testing.T) {
	ctx := context.Background()
	s, _ := newReader(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = s.Duration(settings.PollInterval)
				_ = s.List(settings.K8sWriteNamespaces)
				_ = s.Entries()
			}
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := s.Set(ctx, settings.DiagnoseMaxPerDay, json.RawMessage(strconv.Itoa(i+j))); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestAnUnknownKeyInAGetterIsAProgrammingError(t *testing.T) {
	s, _ := newReader(t, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	s.Duration("poll_intervall")
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/settings`
Expected: FAIL to compile (`undefined: settings.New`).

- [ ] **Step 3: Write `internal/settings/settings.go`**

```go
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// Source says where a value comes from.
type Source string

const (
	SourceDefault     Source = "default"
	SourceEnvironment Source = "environment"
	SourceSet         Source = "set here"
)

// Entry is one setting as it applies now.
type Entry struct {
	Spec     Spec
	Value    Value
	Source   Source
	Fallback Source // the source the value has after a reset: the environment if it set one, else the default
	Problem  string // a stored value that cannot be used; the value is then the environment's or the default
}

// ErrUnknownKey means the key is not in the registry.
var ErrUnknownKey = errors.New("settings: unknown key")

// InvalidError is a value that the registry refuses. Message is fit to show.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

type snapshot struct{ byKey map[string]Entry }

// Settings answers every component from a snapshot in memory, so reading never touches the database (the store has one
// connection, and some readers run inside a transaction). The snapshot is rebuilt after every committed write. The control plane
// is one process, so it is never behind the table.
type Settings struct {
	store *store.Store
	env   Layer
	log   *slog.Logger
	mu    sync.Mutex // serialises Set and Delete together with the rebuild
	snap  atomic.Pointer[snapshot]
}

// New reads the table once. It never fails: when the table cannot be read, that is logged and the environment and the defaults
// apply until the next successful write.
func New(ctx context.Context, st *store.Store, env Layer, log *slog.Logger) *Settings {
	s := &Settings{store: st, env: env, log: log}
	if err := s.reload(ctx); err != nil {
		log.Error("cannot read the settings table; the environment and the defaults apply", "err", err)
		s.snap.Store(s.build(nil))
	}
	return s
}

func (s *Settings) reload(ctx context.Context) error {
	rows, err := s.store.ListSettings(ctx)
	if err != nil {
		return err
	}
	s.snap.Store(s.build(rows))
	return nil
}

// lower is the value a key has without an override.
func (s *Settings) lower(sp Spec) (Value, Source) {
	if v, ok := s.env[sp.Key]; ok {
		return v, SourceEnvironment
	}
	return sp.Default, SourceDefault
}

func (s *Settings) build(rows []store.Setting) *snapshot {
	stored := map[string]string{}
	for _, r := range rows {
		stored[r.Key] = r.Value
	}
	snap := &snapshot{byKey: map[string]Entry{}}
	for _, sp := range specs {
		v, src := s.lower(sp)
		e := Entry{Spec: sp, Value: v, Source: src, Fallback: src}
		if raw, ok := stored[sp.Key]; ok {
			if sv, err := sp.ParseJSON(json.RawMessage(raw)); err != nil {
				e.Problem = fmt.Sprintf("The stored value cannot be used (%s). The %s applies.", err, src)
				s.log.Warn("ignoring a stored setting that is no longer valid", "key", sp.Key, "err", err)
			} else {
				e.Value, e.Source = sv, SourceSet
			}
			delete(stored, sp.Key)
		}
		snap.byKey[sp.Key] = e
	}
	for key := range stored {
		s.log.Warn("ignoring a stored setting this version does not know", "key", key)
	}
	return snap
}

// Entry returns one setting.
func (s *Settings) Entry(key string) (Entry, bool) {
	e, ok := s.snap.Load().byKey[key]
	return e, ok
}

func (s *Settings) must(key string) Entry {
	e, ok := s.Entry(key)
	if !ok {
		panic("settings: unknown key " + key)
	}
	return e
}

// Duration is the value of a duration setting.
func (s *Settings) Duration(key string) time.Duration { return s.must(key).Value.Dur }

// Int is the value of a number setting.
func (s *Settings) Int(key string) int { return s.must(key).Value.Num }

// List is the value of a list setting. The caller owns the slice.
func (s *Settings) List(key string) []string { return slices.Clone(s.must(key).Value.List) }

// Entries returns every setting in the order of the registry.
func (s *Settings) Entries() []Entry {
	snap := s.snap.Load()
	out := make([]Entry, 0, len(specs))
	for _, sp := range specs {
		out = append(out, snap.byKey[sp.Key])
	}
	return out
}

// DiagnosisLimits are the three limits of automatic diagnosis as the store checks them.
func (s *Settings) DiagnosisLimits() store.DiagnosisLimits {
	return store.DiagnosisLimits{
		Cooldown:       s.Duration(DiagnoseCooldown),
		MaxPerIncident: s.Int(DiagnoseMaxPerIncident),
		MaxPerDay:      s.Int(DiagnoseMaxPerDay),
	}
}

func (s *Settings) activity(sp Spec, verb string, from Value, fromSrc Source, to Value, toSrc Source) store.NewActivity {
	data, _ := json.Marshal(map[string]string{
		"key": sp.Key, "from": from.Text(sp.Kind), "fromSource": string(fromSrc), "to": to.Text(sp.Kind), "toSource": string(toSrc),
	})
	return store.NewActivity{
		Kind:    store.KindSettingChanged,
		Summary: fmt.Sprintf("Setting %s %s from %s (%s) to %s (%s)", sp.Key, verb, from.Text(sp.Kind), fromSrc, to.Text(sp.Kind), toSrc),
		Data:    data,
	}
}

// Set checks a value against the registry, stores it with its activity entry and rebuilds the snapshot.
func (s *Settings) Set(ctx context.Context, key string, raw json.RawMessage) (Entry, error) {
	sp, ok := Lookup(key)
	if !ok {
		return Entry{}, ErrUnknownKey
	}
	v, err := sp.ParseJSON(raw)
	if err != nil {
		return Entry{}, &InvalidError{Message: key + " " + err.Error()}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.must(key)
	act := s.activity(sp, "changed", old.Value, old.Source, v, SourceSet)
	if err := s.store.SetSetting(ctx, key, string(v.JSON(sp.Kind)), act); err != nil {
		return Entry{}, err
	}
	if err := s.reload(ctx); err != nil {
		return Entry{}, err
	}
	return s.must(key), nil
}

// Delete removes the override of a key. Without one it changes nothing.
func (s *Settings) Delete(ctx context.Context, key string) (Entry, error) {
	sp, ok := Lookup(key)
	if !ok {
		return Entry{}, ErrUnknownKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.must(key)
	to, toSrc := s.lower(sp)
	removed, err := s.store.DeleteSetting(ctx, key, s.activity(sp, "reset", old.Value, old.Source, to, toSrc))
	if err != nil {
		return Entry{}, err
	}
	if removed {
		if err := s.reload(ctx); err != nil {
			return Entry{}, err
		}
	}
	return s.must(key), nil
}
```

- [ ] **Step 4: Run the tests with the race detector**

Run: `go test -race ./internal/settings`
Expected: PASS. (`TestAFailedWriteLeavesTheSnapshotAsItWas` closes the store, so the `t.Cleanup` close in `newReader` returns an error that it ignores.)

- [ ] **Step 5: Commit**

```bash
git add internal/settings
git commit -m "feat: the settings reader with an in-memory snapshot

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The configuration reads the environment through the registry

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/env_layer_test.go`

**Interfaces:**
- Consumes: `settings.EnvLayer`, `settings.EnvValue`, key constants, `Layer.Duration/Int/List`.
- Produces: `config.Server.Env settings.Layer` (only the variables that were set); the fields `PollInterval`, `DiagnoseCooldown`, `DiagnoseMaxPerIncident`, `DiagnoseMaxPerDay` and `Cluster.WriteNamespaces` keep their meaning (the environment's value or the default).

- [ ] **Step 1: Write the failing test**

Create `internal/config/env_layer_test.go` (check `command ls internal/config/env_layer_test.go` first):

```go
package config_test

import (
	"bytes"
	"encoding/base64"
	"slices"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/settings"
)

func baseEnv() map[string]string {
	return map[string]string{
		"REMEDY_ADMIN_PASSWORD": "a-long-enough-password",
		"REMEDY_RUNNER_TOKEN":   "a-runner-token-of-24-chars!!",
		"REMEDY_MASTER_KEY":     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
	}
}

func TestTheServerKeepsTheEnvironmentAsALayerOfItsOwn(t *testing.T) {
	env := baseEnv()
	env["REMEDY_POLL_INTERVAL"] = "30s"
	env["REMEDY_DIAGNOSE_MAX_PER_DAY"] = "5"
	env["REMEDY_RUN_TIMEOUT"] = "20m"
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 30*time.Second || c.DiagnoseMaxPerDay != 5 || c.DiagnoseCooldown != 15*time.Minute || c.DiagnoseMaxPerIncident != 3 {
		t.Errorf("fields = %v %d %v %d", c.PollInterval, c.DiagnoseMaxPerDay, c.DiagnoseCooldown, c.DiagnoseMaxPerIncident)
	}
	if len(c.Env) != 3 || c.Env.Duration(settings.PollInterval) != 30*time.Second || c.Env.Int(settings.DiagnoseMaxPerDay) != 5 ||
		c.Env.Duration(settings.RunTimeout) != 20*time.Minute {
		t.Errorf("layer = %v", c.Env)
	}
	if _, set := c.Env[settings.DiagnoseCooldown]; set {
		t.Error("a variable that was not set is in the layer")
	}
}

func TestTheNamespacesOfTheEnvironmentAreInTheLayerAndOnTheCluster(t *testing.T) {
	env := baseEnv()
	env["REMEDY_K8S_READ_TOKEN_FILE"] = writeTokenFile(t)
	env["REMEDY_K8S_WRITE_TOKEN_FILE"] = writeTokenFile(t)
	env["REMEDY_K8S_WRITE_NAMESPACES"] = "demo,staging"
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"demo", "staging"}
	if !slices.Equal(c.Cluster.WriteNamespaces, want) || !slices.Equal(c.Env.List(settings.K8sWriteNamespaces), want) {
		t.Errorf("cluster = %v, layer = %v", c.Cluster.WriteNamespaces, c.Env.List(settings.K8sWriteNamespaces))
	}
}

func TestThePrecedenceOfTheErrorsStaysAsItWas(t *testing.T) {
	env := baseEnv()
	env["REMEDY_POLL_INTERVAL"] = "nonsense"
	env["REMEDY_RUN_TIMEOUT"] = "nonsense"
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || err.Error() != "REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m" {
		t.Fatalf("error = %v", err)
	}
}
```

`writeTokenFile` may already exist in `cluster_test.go` of this package (it writes a token file); if its name differs, use the existing helper (`command grep -n 'func ' internal/config/cluster_test.go`), otherwise add:

```go
func writeTokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
```
(with `os` and `path/filepath` imported.)

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./internal/config -run 'Layer|Namespaces|Precedence'`
Expected: FAIL to compile (`c.Env undefined`).

- [ ] **Step 3: Change `internal/config/config.go`**

1. Imports: add `"github.com/Jaydee94/remedy/internal/settings"`; drop `strconv` if `wholeNumber` goes away.
2. Delete the constants `defaultPollInterval`, `minPollInterval`, `defaultRunTimeout`, `minRunTimeout`, `defaultDiagnoseCooldown`, `minDiagnoseCooldown` and the function `wholeNumber`. If a test in package `config` still names a deleted constant, replace it with the literal value.
3. Add to `type Server` a field and update the comments (the registry owns the bounds):

```go
	PollInterval           time.Duration // REMEDY_POLL_INTERVAL: the environment's value or the default; the registry in internal/settings has the bounds
	DiagnoseCooldown       time.Duration // REMEDY_DIAGNOSE_COOLDOWN
	DiagnoseMaxPerIncident int           // REMEDY_DIAGNOSE_MAX_PER_INCIDENT
	DiagnoseMaxPerDay      int           // REMEDY_DIAGNOSE_MAX_PER_DAY
	Cluster                kube.Config   // REMEDY_K8S_*: how to reach the cluster; the zero value means no cluster
	// Env holds the variables of the registry that were set (poll interval, diagnosis limits, namespaces, run timeout). They are
	// the defaults of the settings, which the Setup page can override.
	Env settings.Layer
```

4. In `ServerFromEnv`, replace everything from `c.PollInterval = defaultPollInterval` through the `kube.ParseNamespaces` error handling with:

```go
	layer, err := settings.EnvLayer(get)
	if err != nil {
		return Server{}, err
	}
	c.Env = layer
	c.PollInterval = layer.Duration(settings.PollInterval)
	c.DiagnoseCooldown = layer.Duration(settings.DiagnoseCooldown)
	c.DiagnoseMaxPerIncident = layer.Int(settings.DiagnoseMaxPerIncident)
	c.DiagnoseMaxPerDay = layer.Int(settings.DiagnoseMaxPerDay)
```

and in the `kube.Config{...}` literal use `WriteNamespaces: layer.List(settings.K8sWriteNamespaces),` (delete the `namespaces` variable).

5. In `RunnerFromEnv`, replace the `c.RunTimeout = defaultRunTimeout` block with:

```go
	c.RunTimeout = settings.Layer(nil).Duration(settings.RunTimeout) // the default
	if v, set, err := settings.EnvValue(settings.RunTimeout, get); err != nil {
		return Runner{}, err
	} else if set {
		c.RunTimeout = v.Dur
	}
```

- [ ] **Step 4: Run all config tests and the build**

Run: `go build ./... && go test ./internal/config ./internal/settings`
Expected: PASS, including `poll_test`, `diagnose_test`, `runtimeout_test`, `cluster_test` untouched. (A cycle `config → settings → kube` is fine: `kube` imports neither.)

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat: the configuration reads the registry's variables through the registry

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: A live namespace allowlist

**Files:**
- Modify: `internal/kube/config.go`, `internal/kube/writer.go`, `internal/kube/config_test.go`, `internal/kube/writer_test.go`, `internal/gatekeeper/tools_cluster_actions.go`, `internal/gatekeeper/tools_cluster_actions_test.go`

**Interfaces:**
- Produces:
  - `kube.Config.Namespaces func() []string` (when set, the allowlist at every use; `WriteNamespaces` is then ignored).
  - `func (c Config) AllowedNamespaces() []string`.
  - `Config.WriteEnabled()` is now `c.WriteTokenFile != ""`.
  - `Config.Warnings()` no longer warns about an empty allowlist (the allowlist may come from the database; Task 9 logs that warning from the settings).
  - `kube.NewWriter` builds a writer that reads the allowlist per call.
  - `ClusterActionTools(r, w, cfg, now)` keeps its signature and reads `cfg.AllowedNamespaces()` per call.

- [ ] **Step 1: Change the existing tests that assert the old rules**

In `internal/kube/config_test.go`, `TestWarnings`: the first assertion (`WriteTokenFile` without namespaces warns about `REMEDY_K8S_WRITE_NAMESPACES`) becomes: no warning.

```go
	if w := (Config{ReadTokenFile: read, WriteTokenFile: write}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v: an empty allowlist may be filled in the Setup page, so it is no warning here", w)
	}
```

In `internal/kube/writer_test.go`, `TestANewWriterNeedsTheWriteSideToBeConfigured`: the second case (token, no namespaces) now builds a writer, and an empty allowlist refuses every namespace:

```go
	w, err := NewWriter(Config{API: "http://k8s", ReadTokenFile: read, WriteTokenFile: writeFile(t, "w", "w")})
	if err != nil {
		t.Fatalf("a writer with a token and no namespaces: %v", err)
	}
	if err := w.DeletePod(context.Background(), "demo", "web"); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("an empty allowlist: %v, want ErrNamespaceNotAllowed", err)
	}
```
(add `context` and `errors` imports if missing).

- [ ] **Step 2: Write the failing tests for the live list**

Append to `internal/kube/writer_test.go`:

```go
func TestTheWriterReadsTheAllowlistAtEveryCall(t *testing.T) {
	api := newFakeAPI(t, okReply()) // the helpers of the other writer tests
	var mu sync.Mutex
	list := []string{"demo"}
	get := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), list...) }
	w, err := NewWriter(Config{API: api.URL, ReadTokenFile: writeFile(t, "r", "r"), WriteTokenFile: writeFile(t, "w", "w"), Namespaces: get})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.target("other", "web"); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("before: %v", err)
	}
	mu.Lock()
	list = []string{"other"}
	mu.Unlock()
	if err := w.target("other", "web"); err != nil {
		t.Fatalf("after the list changed: %v", err)
	}
	if err := w.target("demo", "web"); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("a namespace that was removed: %v", err)
	}
}
```

Add the `sync` import to `writer_test.go`.

Append to `internal/kube/config_test.go`:

```go
func TestAConfigReadsItsAllowlistFromTheFunctionWhenThereIsOne(t *testing.T) {
	c := Config{WriteNamespaces: []string{"static"}}
	if !c.NamespaceAllowed("static") || c.NamespaceAllowed("live") {
		t.Fatal("the static list")
	}
	c.Namespaces = func() []string { return []string{"live"} }
	if c.NamespaceAllowed("static") || !c.NamespaceAllowed("live") {
		t.Fatal("the function must replace the static list")
	}
	if got := c.AllowedNamespaces(); len(got) != 1 || got[0] != "live" {
		t.Fatalf("AllowedNamespaces = %v", got)
	}
	if !(Config{WriteTokenFile: "/x"}).WriteEnabled() || (Config{}).WriteEnabled() {
		t.Fatal("WriteEnabled follows the write token file alone")
	}
}
```

Run: `go test ./internal/kube`
Expected: FAIL (compile error: `Namespaces` and `AllowedNamespaces` undefined).

- [ ] **Step 3: Change `internal/kube/config.go`**

Replace the `Config` struct's namespace line and the three methods:

```go
	WriteNamespaces []string // REMEDY_K8S_WRITE_NAMESPACES: where actions may be used
	// Namespaces, when set, is the allowlist: it is asked at every use and replaces WriteNamespaces, so that a change in the Setup page
	// counts at once.
	Namespaces    func() []string
	ArgoNamespace string // REMEDY_K8S_ARGO_NAMESPACE
```

```go
// WriteEnabled says whether actions are possible at all: a write token. Where they may be used is the allowlist, which can be empty
// (then every action is refused) and can change while the control plane runs.
func (c Config) WriteEnabled() bool { return c.WriteTokenFile != "" }

// AllowedNamespaces is the allowlist now.
func (c Config) AllowedNamespaces() []string {
	if c.Namespaces != nil {
		return c.Namespaces()
	}
	return c.WriteNamespaces
}

// NamespaceAllowed says whether an action may be used in the namespace.
func (c Config) NamespaceAllowed(namespace string) bool {
	return namespace != "" && slices.Contains(c.AllowedNamespaces(), namespace)
}
```

In `Warnings()`, delete the block `if c.WriteTokenFile != "" && len(c.WriteNamespaces) == 0 { ... }`.

- [ ] **Step 4: Change `internal/kube/writer.go`**

```go
type Writer struct {
	*client
	allowed func() []string
	argo    string
}

// NewWriter builds a Writer for a configuration whose write side is on: a write token. The allowlist is read at every call.
func NewWriter(c Config, opts ...Option) (*Writer, error) {
	if !c.WriteEnabled() {
		return nil, errors.New("kube: the write side is not configured (REMEDY_K8S_WRITE_TOKEN_FILE)")
	}
	cl, err := newClient(c, c.WriteTokenFile, func(m string) bool { return m == http.MethodPatch || m == http.MethodDelete },
		"this client only patches and deletes", opts)
	if err != nil {
		return nil, err
	}
	return &Writer{client: cl, allowed: c.AllowedNamespaces, argo: c.argoNamespace()}, nil
}
```

and in `target`: `if !slices.Contains(w.allowed(), namespace) {`. (`slices` stays imported; drop it only if no other use remains.)

- [ ] **Step 5: Change `internal/gatekeeper/tools_cluster_actions.go`**

Replace both `a.cfg.WriteNamespaces` in the messages by `a.cfg.AllowedNamespaces()`:

```go
	return ArgumentError(fmt.Sprintf("actions are not allowed in the namespace %q; they are allowed in: %s", namespace, strings.Join(a.cfg.AllowedNamespaces(), ", ")))
```
```go
			name, app.DestinationNamespace, strings.Join(a.cfg.AllowedNamespaces(), ", ")))
```

- [ ] **Step 6: Write the gatekeeper test (live list, checked again at execution)**

In `internal/gatekeeper/tools_cluster_actions_test.go`, split `newActionRigFor` so a test can tune the config:

```go
func newActionRigFor(t *testing.T, url string, allowlist []string) *env {
	t.Helper()
	return newActionRigWith(t, url, func(c *kube.Config) { c.WriteNamespaces = allowlist })
}

func newActionRigWith(t *testing.T, url string, tune func(*kube.Config)) *env {
	t.Helper()
	cfg := kube.Config{API: url, ReadTokenFile: tokenFileOf(t, "read", "read-token"), WriteTokenFile: tokenFileOf(t, "write", "write-token")}
	tune(&cfg)
	reader, err := kube.NewReader(cfg)
	// ... the rest of the old body, unchanged
```

Then add the test and its helper:

```go
type liveList struct {
	mu   sync.Mutex
	list []string
}

func (l *liveList) get() []string { l.mu.Lock(); defer l.mu.Unlock(); return append([]string(nil), l.list...) }
func (l *liveList) set(v ...string) { l.mu.Lock(); defer l.mu.Unlock(); l.list = v }

func newLiveRig(t *testing.T, al *liveList) *actionRig {
	t.Helper()
	api := kubetest.New(t, "read-token")
	api.Accept("write-token")
	return &actionRig{env: newActionRigWith(t, api.URL, func(c *kube.Config) { c.Namespaces = al.get }), api: api}
}

func TestAnAllowlistThatChangesCountsAtOnceAndAgainWhenTheActionRuns(t *testing.T) {
	restart := map[string]any{"kind": "deployment", "namespace": "demo", "name": "web"}

	// Empty: refused before an approval is asked for.
	al := &liveList{}
	r := newLiveRig(t, al)
	text, isErr := r.ask(t, "cluster_rollout_restart", restart)
	if !isErr || !strings.Contains(text, `actions are not allowed in the namespace "demo"`) {
		t.Fatalf("an empty list: %q (%v)", text, isErr)
	}
	noApproval(t, r.env)

	// Set while the control plane runs: the same call now waits for an approval.
	al.set("demo")
	s := r.open(t, "toolu_2", "cluster_rollout_restart", restart)
	call := r.pending(t)

	// Removed after the question was asked: the approved action is refused and sends nothing.
	al.set()
	if _, err := r.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	text, isErr = s.result()
	if !isErr || !strings.Contains(text, "allowlist") && !strings.Contains(text, "not allowed") {
		t.Fatalf("answer = %q (%v), want a refusal", text, isErr)
	}
	if n := len(r.changes()); n != 0 {
		t.Fatalf("%d changes were sent after the namespace was removed", n)
	}
}
```
(add the `sync` import.)

- [ ] **Step 7: Run the suites**

Run: `go build ./... && go test ./internal/kube ./internal/gatekeeper`
Expected: PASS. Other callers of `kube.NewWriter` (`internal/app`, live tests behind build tags) still compile because the signature is unchanged; `go vet -tags kind,kindwrite,sshlive ./...` must also pass.

- [ ] **Step 8: Commit**

```bash
git add internal/kube internal/gatekeeper
git commit -m "feat: the namespace allowlist of cluster actions is read at every use

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Function hooks for the poller, the responder and the reaper

**Files:**
- Modify: `internal/poller/poller.go`, `internal/responder/responder.go`, `internal/reaper/reaper.go`
- Create: `internal/poller/interval_internal_test.go`, `internal/responder/limitsfunc_test.go`, `internal/reaper/maxagefor_test.go`

**Interfaces:**
- Produces:
  - `Poller.IntervalFunc func() time.Duration` (read before each wait; takes precedence over `Interval`).
  - `Responder.LimitsFunc func() store.DiagnosisLimits` (read at every automatic start; takes precedence over `Limits`).
  - `Reaper.MaxAgeFunc func() time.Duration` (read at every sweep; takes precedence over `MaxAge`) and `func reaper.MaxAgeFor(runTimeout time.Duration) time.Duration` = `max(DefaultMaxAge, runTimeout + 5 min)`.

- [ ] **Step 1: Poller test**

`internal/poller/interval_internal_test.go` (package `poller`; check the name is free):

```go
package poller

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestPauseIsTheIntervalWhileItIsShortAndAShortTickWhileItIsLong(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		20 * time.Millisecond: 20 * time.Millisecond, 3 * time.Second: 3 * time.Second, time.Minute: pollTick, time.Hour: pollTick,
	} {
		if got := pause(in); got != want {
			t.Errorf("pause(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestRunAsksForTheIntervalAgainAndStopsPollingWhenItGrows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var interval atomic.Int64
	interval.Store(int64(20 * time.Millisecond))
	var cycles atomic.Int64
	p := &Poller{
		Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IntervalFunc: func() time.Duration { return time.Duration(interval.Load()) },
		Now:          func() time.Time { cycles.Add(1); return time.Now() }, // PollOnce asks for the time once per cycle
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for cycles.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cycles.Load() < 4 {
		t.Fatalf("only %d cycles with an interval of 20ms", cycles.Load())
	}
	interval.Store(int64(time.Hour))
	time.Sleep(60 * time.Millisecond) // a cycle that was already due may still run
	settled := cycles.Load()
	time.Sleep(300 * time.Millisecond)
	if cycles.Load() != settled {
		t.Fatalf("cycles went from %d to %d after the interval grew to an hour", settled, cycles.Load())
	}
}
```

Run: `go test ./internal/poller -run 'Pause|AsksForTheInterval'`
Expected: FAIL to compile (`pause`, `pollTick`, `IntervalFunc` undefined).

- [ ] **Step 2: Change `internal/poller/poller.go`**

Add the field after `Interval`:

```go
	Interval     time.Duration // default one minute
	IntervalFunc func() time.Duration // when set, asked before each wait instead of Interval, so that a new interval counts at once
```

Replace `Run` with:

```go
// pollTick is the longest Run sleeps before it looks at the clock and at the interval again.
const pollTick = 5 * time.Second

// pause is how long Run sleeps before it checks whether the interval has passed: the interval itself while it is short, otherwise
// a short tick, so that a shorter interval is noticed soon.
func pause(interval time.Duration) time.Duration { return min(interval, pollTick) }

func (p *Poller) interval() time.Duration {
	d := p.Interval
	if p.IntervalFunc != nil {
		d = p.IntervalFunc()
	}
	if d <= 0 {
		d = time.Minute
	}
	return d
}

// Run polls at once and then whenever the interval has passed since the last cycle, until ctx ends. The interval is asked for again
// before every wait.
func (p *Poller) Run(ctx context.Context) {
	p.PollOnce(ctx)
	last := time.Now()
	for {
		t := time.NewTimer(pause(p.interval()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if time.Since(last) >= p.interval() {
			p.PollOnce(ctx)
			last = time.Now()
		}
	}
}
```

Run: `go test ./internal/poller -race`
Expected: PASS (the existing tests included).

- [ ] **Step 3: Responder**

`internal/responder/limitsfunc_test.go` (check the name is free; `responder_test.go` is in package `responder_test` and has `newEnv`, `e.incident`, `e.get`, `e.src.callList()` and the constant `sha`):

```go
package responder_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestTheLimitsAreAskedForAtEveryAutomaticStart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	limits := store.DiagnosisLimits{Cooldown: time.Minute, MaxPerIncident: 3, MaxPerDay: 0} // switched off
	e.r.LimitsFunc = func() store.DiagnosisLimits { return limits }
	in := e.incident("pr:20", "web", sha, "failure")

	e.r.AutoStart(ctx)
	if got := e.get(in); got.State != store.IncOpen || len(e.src.callList()) != 0 {
		t.Fatalf("switched off: %+v, %d GitHub calls", got, len(e.src.callList()))
	}
	limits.MaxPerDay = 5 // changed in the Setup page
	e.r.AutoStart(ctx)
	if got := e.get(in); got.Diagnoses != 1 {
		t.Fatalf("after the daily limit was raised the incident must be diagnosed: %+v", got)
	}
}
```
The test compiles only after Step 3b.

Step 3b: in `internal/responder/responder.go` add next to `Limits store.DiagnosisLimits`:

```go
	LimitsFunc func() store.DiagnosisLimits // when set, asked at every use instead of Limits
```
and a method:

```go
func (r *Responder) limits() store.DiagnosisLimits {
	if r.LimitsFunc != nil {
		return r.LimitsFunc()
	}
	return r.Limits
}
```
Replace the four uses of `r.Limits` (`Limits: r.Limits` in the `StartParams` literal near line 142, `r.Limits.MaxPerDay` and `r.Limits` near 240–243, `r.Limits.Cooldown` near 263) by `r.limits()` — in `AutoStart` call it **once** into a local (`limits := r.limits()`) so one cycle uses one set of numbers. Keep the exported field `Limits` (tests and `server.limits` read it).

Run: `go test ./internal/responder -race`
Expected: PASS.

- [ ] **Step 4: Reaper**

`internal/reaper/maxagefor_test.go` (same package clause as `reaper_test.go`, `reaper_test`):

```go
func TestTheThresholdFollowsALongRunTimeout(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		10 * time.Second: 15 * time.Minute, 10 * time.Minute: 15 * time.Minute, 11 * time.Minute: 16 * time.Minute, time.Hour: 65 * time.Minute,
	} {
		if got := reaper.MaxAgeFor(in); got != want {
			t.Errorf("MaxAgeFor(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestSweepAsksForTheThresholdAtEverySweep(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	stuck, _ := st.CreateRun(ctx, "claude", "stuck")
	_, _ = st.ClaimNext(ctx)
	now := time.Now()
	maxAge := time.Hour
	r := &reaper.Reaper{Store: st, MaxAgeFunc: func() time.Duration { return maxAge }, Log: quiet, Now: func() time.Time { return now }}

	now = now.Add(30 * time.Minute)
	if n, _ := r.Sweep(ctx); n != 0 {
		t.Fatalf("with a threshold of an hour a run of 30 minutes was failed (%d)", n)
	}
	maxAge = 20 * time.Minute // shortened in the Setup page
	if n, _ := r.Sweep(ctx); n != 1 {
		t.Fatalf("with a threshold of 20 minutes the run of 30 minutes must be failed (%d)", n)
	}
	if got, _ := st.GetRun(ctx, stuck.ID); got.Status != run.Failed {
		t.Fatalf("run = %+v", got)
	}
}
```
(imports as in `reaper_test.go`: `context`, `time`, `testing`, `reaper`, `run`.)

In `internal/reaper/reaper.go` add:

```go
	MaxAgeFunc      func() time.Duration // when set, asked at every sweep instead of MaxAge
```
and in `Sweep` replace the first lines:

```go
	maxAge := r.MaxAge
	if r.MaxAgeFunc != nil {
		maxAge = r.MaxAgeFunc()
	}
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
```
and add:

```go
// MaxAgeFor is how long a run without gatekeeper access may stay running before it is failed: DefaultMaxAge, or five minutes more
// than the run timeout when that is longer, so the reaper never overtakes a run's own limit.
func MaxAgeFor(runTimeout time.Duration) time.Duration {
	return max(DefaultMaxAge, runTimeout+5*time.Minute)
}
```

Run: `go test ./internal/reaper ./internal/responder ./internal/poller -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/poller internal/responder internal/reaper
git commit -m "feat: the poller, the responder and the reaper ask for their numbers at every use

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The run timeout travels with the claim

**Files:**
- Modify: `internal/run/run.go`, `internal/server/runnerapi.go`, `internal/runner/loop.go`
- Create: `internal/runner/claimtimeout_test.go`

**Interfaces:**
- Consumes: `server.Deps.Settings` (the field is added in Step 4 of this task; Task 8 adds the routes).
- Produces: `run.Claim.RunTimeoutSeconds int` (`json:"run_timeout_seconds,omitempty"`); the runner uses it for that run when it is above zero, otherwise `Loop.RunTimeout`.

- [ ] **Step 1: Write the failing runner test**

The registry's lower bound for `run_timeout` is 10 s, so the claim's timeout in this test is 10 s (`SlowClaude` sleeps 60 s). The runner's own limit is 500 ms: if the runner ignored the claim, the run would stop after half a second.

Create `internal/runner/claimtimeout_test.go` (check the name is free). `loop_test.go` has `const token`, `waitTerminal` (it waits only 10 s, too short here, so this file waits by itself):

```go
package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestTheTimeoutOfTheClaimReplacesTheRunnersOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about ten seconds: the smallest run timeout the settings accept")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	set := settings.New(context.Background(), st, nil, quiet)
	if _, err := set.Set(context.Background(), settings.RunTimeout, json.RawMessage(`"10s"`)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New("correct horse battery"), RunnerToken: token, Settings: set}))
	t.Cleanup(ts.Close)

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()},
		Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.SlowClaude(t)}},
		WorkspaceRoot: t.TempDir(),
		Env:           os.Environ(),
		Log:           quiet,
		Backoff:       50 * time.Millisecond,
		RunTimeout:    500 * time.Millisecond, // what the runner would use without the claim's number
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	r, err := st.CreateRun(context.Background(), "claude", "hang")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var got run.Run
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got, err = st.GetRun(context.Background(), r.ID); err != nil {
			t.Fatal(err)
		} else if got.Status.Terminal() {
			break
		}
	}
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout {
		t.Fatalf("run = %+v, want failed with reason timeout", got)
	}
	if elapsed := time.Since(started); elapsed < 8*time.Second || elapsed > 30*time.Second {
		t.Fatalf("the run was stopped after %s, want about 10s (the claim's timeout), not the runner's 500ms", elapsed)
	}
}
```

`TestARunThatTakesTooLongIsStopped` (`timeout_test.go`) already covers the fallback: a control plane without settings sends no number and the runner's own limit applies.

Run: `go test ./internal/runner -run TimeoutOfTheClaim`
Expected: FAIL to compile (`unknown field Settings in struct literal of type server.Deps`): the field comes in Step 4. That is the failing run; after Steps 2 to 4 the test passes.

- [ ] **Step 2: Add the field**

In `internal/run/run.go`, in `Claim`, after `MCPToken`:

```go
	// RunTimeoutSeconds is the longest the run may take while it does not wait for an approval, from the control plane's settings.
	// Zero (an older control plane) means the runner's own limit.
	RunTimeoutSeconds int `json:"run_timeout_seconds,omitempty"`
```

- [ ] **Step 3: Use it in the runner**

In `internal/runner/loop.go`, replace

```go
	timeout := l.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
```
with
```go
	timeout := l.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	if c.RunTimeoutSeconds > 0 {
		timeout = time.Duration(c.RunTimeoutSeconds) * time.Second
	}
```
(`c` is the claim in that function, as `c.MCPToken` below shows.)

(Do Step 4 before you run the tests: the runner package compiles against `server.Deps.Settings`.)

- [ ] **Step 4: The control plane sets it**

In `internal/server/server.go` add to `Deps` (the rest of the API is Task 8):

```go
	// Settings holds the values the maintainer changes in the Setup page. When it is nil the settings routes are not registered and
	// the claim carries no run timeout.
	Settings *settings.Settings
```
(import `github.com/Jaydee94/remedy/internal/settings`).

In `internal/server/runnerapi.go`, in `claim`, replace `writeJSON(w, http.StatusOK, claimFor(*claimed, token))` with:

```go
			c := claimFor(*claimed, token)
			if s.d.Settings != nil {
				c.RunTimeoutSeconds = int(s.d.Settings.Duration(settings.RunTimeout) / time.Second)
			}
			writeJSON(w, http.StatusOK, c)
```
(import `settings`.)

- [ ] **Step 5: Run the runner tests, then test the claim on the server side**

Run: `go test ./internal/runner -race`
Expected: PASS (the new test takes about ten seconds).

(The server-side test follows.)



Create `internal/server/claimtimeout_test.go` (name free?). It builds a server with a `Settings` and claims a run:

```go
package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

// newSettingsEnv is a server with settings over a new store; the settings tests of this package use it.
func newSettingsEnv(t *testing.T, tune func(*server.Deps)) (*env, *settings.Settings) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set}
	if tune != nil {
		tune(&d)
	}
	ts := httptest.NewServer(server.New(d))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}, set
}

func TestTheClaimCarriesTheRunTimeoutOfTheSettings(t *testing.T) {
	e, set := newSettingsEnv(t, nil)
	if _, err := set.Set(context.Background(), settings.RunTimeout, json.RawMessage(`"25m"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateRun(context.Background(), "claude", "hello"); err != nil {
		t.Fatal(err)
	}
	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d", resp.StatusCode)
	}
	got := decode[struct {
		Seconds int `json:"run_timeout_seconds"`
	}](t, resp)
	if want := int((25 * time.Minute).Seconds()); got.Seconds != want {
		t.Fatalf("run_timeout_seconds = %d, want %d", got.Seconds, want)
	}
}
```

Run: `go build ./... && go test ./internal/server -run TheClaimCarries && go test ./internal/run ./internal/runner`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/run internal/runner internal/server
git commit -m "feat: the claim carries the run timeout of the settings

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: The settings API

**Files:**
- Create: `internal/server/settings.go`, `internal/server/settings_test.go`
- Modify: `internal/server/server.go` (routes, `Facts`), `internal/server/responder.go` (`getLimits`), `internal/server/capabilities.go`

**Interfaces:**
- Consumes: `settings.Settings` (Task 3), `newSettingsEnv` (Task 7 test helper), `reaper.MaxAgeFor` (Task 6).
- Produces:
  - `type server.Fact struct { Label, Value string }`; `Deps.Facts []Fact` (read-only bootstrap facts).
  - `GET /api/settings` → `{"settings":[{key,group,label,kind,value,source,fallback,env,rule,effect,problem?}],"facts":[{label,value}]}`.
  - `PUT /api/settings/{key}` body `{"value": <json>}` → 200 with the setting; 404 unknown key; 400 `{"error": "..."}` for a refused value.
  - `DELETE /api/settings/{key}` → 204; 404 unknown key.
  - `GET /api/limits` reads from the settings when `Deps.Settings != nil`, with `staleRunMinutes` from `reaper.MaxAgeFor(run_timeout)`.
  - `Cluster.NamespacesFunc func() []string` (when set, `GET /api/capabilities` asks it).

- [ ] **Step 1: Write the failing tests**

`internal/server/settings_test.go`:

```go
package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/server"
)

type settingJSON struct {
	Key      string          `json:"key"`
	Group    string          `json:"group"`
	Kind     string          `json:"kind"`
	Value    json.RawMessage `json:"value"`
	Source   string          `json:"source"`
	Fallback string          `json:"fallback"`
	Env      string          `json:"env"`
	Problem  string          `json:"problem"`
}

type settingsPage struct {
	Settings []settingJSON `json:"settings"`
	Facts    []struct{ Label, Value string }
}

func TestTheSettingsAreListedWithTheirSourceAndTheBootstrapFacts(t *testing.T) {
	e, _ := newSettingsEnv(t, func(d *server.Deps) {
		d.Facts = []server.Fact{{Label: "Listener", Value: ":8080"}}
	})
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/settings", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	page := decode[settingsPage](t, resp)
	if len(page.Settings) != 6 || page.Settings[0].Key != "poll_interval" || string(page.Settings[0].Value) != `"1m"` ||
		page.Settings[0].Source != "default" || page.Settings[0].Env != "REMEDY_POLL_INTERVAL" || page.Settings[0].Kind != "duration" {
		t.Fatalf("settings = %+v", page.Settings)
	}
	if len(page.Facts) != 1 || page.Facts[0].Value != ":8080" {
		t.Fatalf("facts = %+v", page.Facts)
	}
}

func TestASettingIsChangedAndResetThroughTheAPI(t *testing.T) {
	e, set := newSettingsEnv(t, nil)
	c := e.adminClient(t, true)

	resp := e.do(t, c, http.MethodPut, "/api/settings/poll_interval", `{"value":"30s"}`, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	if got := decode[settingJSON](t, resp); got.Source != "set here" || string(got.Value) != `"30s"` {
		t.Fatalf("answer = %+v", got)
	}
	if set.Duration("poll_interval").String() != "30s" {
		t.Fatalf("the reader still has %v", set.Duration("poll_interval"))
	}
	resp = e.do(t, c, http.MethodPut, "/api/settings/k8s_write_namespaces", `{"value":["demo","staging"]}`, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT list = %d", resp.StatusCode)
	}
	if resp = e.do(t, c, http.MethodDelete, "/api/settings/poll_interval", "", true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", resp.StatusCode)
	}
	if resp = e.do(t, c, http.MethodDelete, "/api/settings/poll_interval", "", true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("a second DELETE = %d, want 204", resp.StatusCode)
	}
	if set.Duration("poll_interval").String() != "1m0s" {
		t.Fatalf("after the reset: %v", set.Duration("poll_interval"))
	}
}

func TestARefusedValueIsA400WithAReadableMessage(t *testing.T) {
	e, _ := newSettingsEnv(t, nil)
	c := e.adminClient(t, true)
	for _, body := range []string{`{"value":"5s"}`, `{"value":30}`, `{}`, `not json`} {
		resp := e.do(t, c, http.MethodPut, "/api/settings/poll_interval", body, true)
		got := decode[struct{ Error string }](t, resp)
		if resp.StatusCode != http.StatusBadRequest || got.Error == "" {
			t.Errorf("%s: %d %q", body, resp.StatusCode, got.Error)
		}
		if body == `{"value":"5s"}` && !strings.HasPrefix(got.Error, "poll_interval must be a duration of at least 10s") {
			t.Errorf("message = %q", got.Error)
		}
	}
	if resp := e.do(t, c, http.MethodPut, "/api/settings/no_such_key", `{"value":1}`, true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown key = %d", resp.StatusCode)
	}
	if resp := e.do(t, c, http.MethodDelete, "/api/settings/no_such_key", "", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown key on DELETE = %d", resp.StatusCode)
	}
}

func TestTheSettingsRoutesNeedASessionAndTheCSRFHeader(t *testing.T) {
	e, _ := newSettingsEnv(t, nil)
	anon := e.adminClient(t, false)
	if got := e.do(t, anon, http.MethodGet, "/api/settings", "", false).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("GET without a session = %d", got)
	}
	c := e.adminClient(t, true)
	if got := e.do(t, c, http.MethodPut, "/api/settings/poll_interval", `{"value":"30s"}`, false).StatusCode; got != http.StatusForbidden && got != http.StatusUnauthorized {
		t.Errorf("PUT without the CSRF header = %d", got)
	}
}

func TestNoBootstrapSecretIsInAnyAnswer(t *testing.T) {
	e, _ := newSettingsEnv(t, func(d *server.Deps) { d.Facts = []server.Fact{{Label: "Listener", Value: ":8080"}} })
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/settings", "", false)
	var sb strings.Builder
	_, _ = sb.WriteString(string(mustReadAll(t, resp)))
	for _, secret := range []string{password, runnerToken} {
		if strings.Contains(sb.String(), secret) {
			t.Errorf("the answer contains a bootstrap secret")
		}
	}
}
```

Add the small helper `mustReadAll(t, resp) []byte` (`io.ReadAll` and a `t.Fatal`) in this file if the package has none (`command grep -n 'func mustReadAll\|func readBody' internal/server/*_test.go`).

Also append a test that `/api/limits` follows the settings and the stale run threshold. `getLimits` only needs a `Responder` value (its `Limits` field is the fallback), so a bare one is enough; add the imports `context`, `github.com/Jaydee94/remedy/internal/responder`, `github.com/Jaydee94/remedy/internal/settings`, `github.com/Jaydee94/remedy/internal/store` and `time` to the file:

```go
func TestTheLimitsAreReadFromTheSettings(t *testing.T) {
	e, set := newSettingsEnv(t, func(d *server.Deps) {
		d.Responder = &responder.Responder{Store: d.Store, Limits: store.DefaultLimits()}
		d.PollInterval = 90 * time.Second // the old source, which the settings must replace
	})
	ctx := context.Background()
	for key, raw := range map[string]string{settings.DiagnoseMaxPerDay: `7`, settings.RunTimeout: `"30m"`, settings.PollInterval: `"2m"`} {
		if _, err := set.Set(ctx, key, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodGet, "/api/limits", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := decode[struct {
		PollIntervalSeconds int `json:"pollIntervalSeconds"`
		DiagnoseMaxPerDay   int `json:"diagnoseMaxPerDay"`
		StaleRunMinutes     int `json:"staleRunMinutes"`
	}](t, resp)
	if got.PollIntervalSeconds != 120 || got.DiagnoseMaxPerDay != 7 || got.StaleRunMinutes != 35 {
		t.Fatalf("limits = %+v, want 120 s, 7 a day and a stale threshold of 35 minutes", got)
	}
}
```

Run: `go test ./internal/server -run 'Settings|Limits|Refused|Bootstrap'`
Expected: FAIL to compile (`server.Fact` undefined) and then FAIL at runtime.

- [ ] **Step 2: Write `internal/server/settings.go`**

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Jaydee94/remedy/internal/settings"
)

const maxSettingBody = 4 << 10

// Fact is something about the process that is set outside the Setup page, shown there as a read-only fact. It never holds a secret.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type settingView struct {
	Key      string          `json:"key"`
	Group    string          `json:"group"`
	Label    string          `json:"label"`
	Kind     string          `json:"kind"`
	Value    json.RawMessage `json:"value"`
	Source   string          `json:"source"`
	Fallback string          `json:"fallback"` // where the value comes from after a reset
	Env      string          `json:"env"`
	Rule     string          `json:"rule"`
	Effect   string          `json:"effect"`
	Problem  string          `json:"problem,omitempty"`
}

func viewOf(e settings.Entry) settingView {
	return settingView{
		Key: e.Spec.Key, Group: e.Spec.Group, Label: e.Spec.Label, Kind: string(e.Spec.Kind), Value: e.Value.JSON(e.Spec.Kind),
		Source: string(e.Source), Fallback: string(e.Fallback), Env: e.Spec.Env, Rule: e.Spec.Rule, Effect: e.Spec.Effect, Problem: e.Problem,
	}
}

func (s *srv) getSettings(w http.ResponseWriter, _ *http.Request) {
	entries := s.d.Settings.Entries()
	views := make([]settingView, 0, len(entries))
	for _, e := range entries {
		views = append(views, viewOf(e))
	}
	facts := s.d.Facts
	if facts == nil {
		facts = []Fact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": views, "facts": facts})
}

func (s *srv) putSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil || len(body.Value) == 0 {
		writeErr(w, http.StatusBadRequest, `send {"value": ...}`)
		return
	}
	e, err := s.d.Settings.Set(r.Context(), r.PathValue("key"), body.Value)
	var invalid *settings.InvalidError
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		writeErr(w, http.StatusNotFound, "setting not found")
	case errors.As(err, &invalid):
		writeErr(w, http.StatusBadRequest, invalid.Message)
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not save the setting")
	default:
		writeJSON(w, http.StatusOK, viewOf(e))
	}
}

func (s *srv) deleteSetting(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Settings.Delete(r.Context(), r.PathValue("key"))
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		writeErr(w, http.StatusNotFound, "setting not found")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not reset the setting")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 3: Routes and `Facts`**

In `internal/server/server.go` add to `Deps`:

```go
	// Facts are shown on the Setup page as read-only facts of the process (listeners, the GitHub address, whether the cluster is set up).
	Facts []Fact
```
and in `routes`, next to the other optional blocks:

```go
	if d.Settings != nil {
		public.HandleFunc("GET /api/settings", s.session(s.getSettings))
		public.HandleFunc("PUT /api/settings/{key}", s.session(s.putSetting))
		public.HandleFunc("DELETE /api/settings/{key}", s.session(s.deleteSetting))
	}
```

- [ ] **Step 4: `getLimits` and the capabilities**

In `internal/server/responder.go`, `getLimits`: after `used, err := ...`, build the view from the settings when there are some:

```go
	view := limitsView{
		PollIntervalSeconds:     int(s.d.PollInterval.Seconds()),
		DiagnoseCooldownSeconds: int(l.Cooldown.Seconds()),
		DiagnoseMaxPerIncident:  l.MaxPerIncident,
		DiagnoseMaxPerDay:       l.MaxPerDay,
		StaleRunMinutes:         int(reaper.DefaultMaxAge.Minutes()),
		DiagnosesLast24h:        used,
	}
	if set := s.d.Settings; set != nil {
		limits := set.DiagnosisLimits()
		view.PollIntervalSeconds = int(set.Duration(settings.PollInterval).Seconds())
		view.DiagnoseCooldownSeconds = int(limits.Cooldown.Seconds())
		view.DiagnoseMaxPerIncident = limits.MaxPerIncident
		view.DiagnoseMaxPerDay = limits.MaxPerDay
		view.StaleRunMinutes = int(reaper.MaxAgeFor(set.Duration(settings.RunTimeout)).Minutes())
	}
	writeJSON(w, http.StatusOK, view)
```
(`l := s.d.Responder.Limits` stays as the fallback; import `settings`.)

In `internal/server/capabilities.go`: add `NamespacesFunc func() []string` to `Cluster` with the comment "when set, asked at every request instead of Namespaces", and in `capabilities`:

```go
	ns := s.d.Cluster.Namespaces
	if s.d.Cluster.NamespacesFunc != nil {
		ns = s.d.Cluster.NamespacesFunc()
	}
	v := clusterView{Read: s.d.Cluster.Read, Write: s.d.Cluster.Write, Namespaces: ns}
```

- [ ] **Step 5: Run**

Run: `go build ./... && go test ./internal/server -race -run 'Settings|Limits|Refused|Bootstrap|Claim|Capab|Runner'`
Expected: PASS. (The full `./internal/server` package under `-race` takes minutes because of argon2; the focused run is the check, the full run happens in `make check` at the end.)

- [ ] **Step 6: Commit**

```bash
git add internal/server
git commit -m "feat: the settings API (list, set, reset) and limits from the settings

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Wiring in the app and the server binary

**Files:**
- Modify: `internal/app/app.go`, `cmd/remedy-server/main.go`
- Create: `internal/app/settings_chain_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `app.App.Settings *settings.Settings`; a control plane in which the six settings are live.

- [ ] **Step 1: Write the failing chain test**

`internal/app/settings_chain_test.go` (package `app_test`; the helpers `newStack`, `s.admin`, `clusterApp`, `tokenOf`, `capabilities`'s login pattern exist in `app_test.go` / `cluster_test.go`):

```go
package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
)

func TestAChangedSettingReachesTheComponentsThatUseIt(t *testing.T) {
	s := newStack(t, false)

	put := func(key, body string) {
		t.Helper()
		if code, out := s.admin(http.MethodPut, "/api/settings/"+key, body); code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", key, code, out)
		}
	}
	put("poll_interval", `{"value":"30s"}`)
	put("diagnose_max_per_day", `{"value":5}`)
	put("diagnose_cooldown", `{"value":"20m"}`)
	put("run_timeout", `{"value":"30m"}`)

	if got := s.app.Poller.IntervalFunc(); got != 30*time.Second {
		t.Errorf("the poller's interval = %v", got)
	}
	if got := s.app.Responder.LimitsFunc(); got.MaxPerDay != 5 || got.Cooldown != 20*time.Minute || got.MaxPerIncident != 3 {
		t.Errorf("the responder's limits = %+v", got)
	}
	if got := s.app.Reaper.MaxAgeFunc(); got != 35*time.Minute {
		t.Errorf("the reaper's threshold = %v, want 35m", got)
	}
	code, body := s.admin(http.MethodGet, "/api/limits", "")
	var limits struct {
		PollIntervalSeconds int `json:"pollIntervalSeconds"`
		DiagnoseMaxPerDay   int `json:"diagnoseMaxPerDay"`
		StaleRunMinutes     int `json:"staleRunMinutes"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &limits) != nil || limits.PollIntervalSeconds != 30 || limits.DiagnoseMaxPerDay != 5 || limits.StaleRunMinutes != 35 {
		t.Errorf("limits = %d %s", code, body)
	}

	// A reset falls back to the default.
	if code, out := s.admin(http.MethodDelete, "/api/settings/poll_interval", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", code, out)
	}
	if got := s.app.Poller.IntervalFunc(); got != time.Minute {
		t.Errorf("after the reset the interval = %v", got)
	}
}

func TestTheEnvironmentIsTheDefaultOfTheSettings(t *testing.T) {
	a := appWithEnv(t, map[string]string{"REMEDY_DIAGNOSE_MAX_PER_DAY": "9", "REMEDY_RUN_TIMEOUT": "20m"})
	if got := a.Responder.LimitsFunc().MaxPerDay; got != 9 {
		t.Errorf("per day from the environment = %d", got)
	}
	if got := a.Reaper.MaxAgeFunc(); got != 25*time.Minute {
		t.Errorf("the reaper's threshold = %v, want 25m", got)
	}
}

func TestTheActionsFollowTheAllowlistOfTheSettings(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	a := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read"), WriteTokenFile: tokenOf(t, "write")}, log)
	if a.KubeWriter == nil {
		t.Fatal("a write token is enough for the actions to exist; the allowlist may be empty")
	}
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	do := func(method, path, body string) string {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("X-Remedy-CSRF", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	do(http.MethodPost, "/api/login", `{"password":"`+password+`"}`)
	if got := do(http.MethodGet, "/api/capabilities", ""); got != `{"cluster":{"read":true,"write":true,"namespaces":[]}}`+"\n" {
		t.Fatalf("before: %s", got)
	}
	do(http.MethodPut, "/api/settings/k8s_write_namespaces", `{"value":["demo"]}`)
	if got := do(http.MethodGet, "/api/capabilities", ""); got != `{"cluster":{"read":true,"write":true,"namespaces":["demo"]}}`+"\n" {
		t.Fatalf("after: %s", got)
	}
}
```

`appWithEnv(t, env)` is a new helper in this file: it builds `cfg, err := config.ServerFromEnv(...)` from a base environment (admin password `password`, runner token `runnerToken`, a master key as in `clusterApp`, plus the given variables), opens a store as `clusterApp` does and returns `app.New(cfg, st, log, nil)`:

```go
func appWithEnv(t *testing.T, extra map[string]string) *app.App {
	t.Helper()
	env := map[string]string{
		"REMEDY_ADMIN_PASSWORD": password, "REMEDY_RUNNER_TOKEN": runnerToken,
		"REMEDY_MASTER_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}
```
(imports: `encoding/base64`, `path/filepath`, `app`, `config`, `store`.)

Run: `go test ./internal/app -run 'ChangedSetting|EnvironmentIsTheDefault|FollowTheAllowlist'`
Expected: FAIL to compile (`s.app.Poller.IntervalFunc` is a field that is nil; `a.Settings` missing).

- [ ] **Step 2: Wire `internal/app/app.go`**

1. `App` gets `Settings *settings.Settings` (with a comment: "the values the Setup page changes").
2. In `New`, right after `engine := ...`, build the layer and the reader:

```go
	set := settings.New(context.Background(), st, envLayer(cfg), log)
```
and the helper at the end of the file:

```go
// envLayer is the environment's part of the settings. A configuration that was built by hand (a test) and not read from the
// environment has no layer; its allowlist is then the environment's value.
func envLayer(cfg config.Server) settings.Layer {
	layer := maps.Clone(cfg.Env)
	if layer == nil {
		layer = settings.Layer{}
	}
	if _, ok := layer[settings.K8sWriteNamespaces]; !ok && len(cfg.Cluster.WriteNamespaces) > 0 {
		layer[settings.K8sWriteNamespaces] = settings.Value{List: slices.Clone(cfg.Cluster.WriteNamespaces)}
	}
	return layer
}
```
(imports `maps`, `slices`, `settings`.)

3. Responder: replace the `Limits: store.DiagnosisLimits{...},` literal by `LimitsFunc: set.DiagnosisLimits,`.
4. Cluster: before `kubeReader, kubeWriter := clusterClients(...)`:

```go
	cluster := cfg.Cluster
	cluster.Namespaces = func() []string { return set.List(settings.K8sWriteNamespaces) }
	kubeReader, kubeWriter := clusterClients(cluster, log)
	if kubeWriter != nil && len(set.List(settings.K8sWriteNamespaces)) == 0 {
		log.Warn("cluster actions are on but the allowlist is empty: every action is refused until a namespace is set (REMEDY_K8S_WRITE_NAMESPACES or the Setup page)")
	}
```
and pass `cluster` (not `cfg.Cluster`) to `gatekeeper.ClusterActionTools(kubeReader, kubeWriter, cluster, nil)` and to `clusterCapabilities`.
5. `clusterCapabilities(c kube.Config, reader, writer)`: replace `caps.Namespaces = slices.Clone(c.WriteNamespaces)` by `caps.NamespacesFunc = c.AllowedNamespaces` (inside `if writer != nil`).
6. `server.Deps`: add `Settings: set, Facts: facts(cfg, kubeReader, kubeWriter),`; keep `PollInterval: cfg.PollInterval` (the fallback). Add:

```go
// facts are what the Setup page shows as read-only: set outside the page, never a secret.
func facts(cfg config.Server, reader *kube.Reader, writer *kube.Writer) []server.Fact {
	yesNo := func(b bool) string {
		if b {
			return "set up"
		}
		return "not set up"
	}
	internal := cfg.InternalAddr
	if internal == "" {
		internal = "none: one port serves everything"
	}
	return []server.Fact{
		{Label: "Listener", Value: cfg.Addr},
		{Label: "Internal listener (runner and tools)", Value: internal},
		{Label: "GitHub API", Value: cfg.GitHubAPIURL},
		{Label: "Cluster, reading", Value: yesNo(reader != nil)},
		{Label: "Cluster, actions", Value: yesNo(writer != nil)},
	}
}
```
7. Poller: replace `Interval: cfg.PollInterval,` by `IntervalFunc: func() time.Duration { return set.Duration(settings.PollInterval) },`.
8. Reaper: add `MaxAgeFunc: func() time.Duration { return reaper.MaxAgeFor(set.Duration(settings.RunTimeout)) },`.
9. The returned `&App{...}` gets `Settings: set`.

- [ ] **Step 3: `cmd/remedy-server/main.go`**

The loop `for _, w := range cfg.Cluster.Warnings()` stays. Change the last log line to read the live value:

```go
	log.Info("control plane listening", "addr", cfg.Addr, "db", cfg.DBPath, "pollInterval", a.Settings.Duration(settings.PollInterval))
```
(import `settings`).

- [ ] **Step 4: Run**

Run: `go build ./... && go vet ./... && go test ./internal/app ./cmd/... -race`
Expected: PASS. The existing app tests (`TestTheUIIsToldWhatIsConfiguredAndNothingSecret`, the cluster action tests) must pass unchanged; if one asserted "token without namespaces means no writer", change it to the new rule (the writer exists, every namespace is refused).

- [ ] **Step 5: Commit**

```bash
git add internal/app cmd
git commit -m "feat: wire the settings into the control plane

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 10: The chart gives the run timeout to the control plane

**Files:**
- Modify: `deploy/chart/templates/server-deployment.yaml`, `deploy/chart/values.yaml`, `deploy/chart_test.go`

- [ ] **Step 1: Write the failing render test**

Append to `deploy/chart_test.go`:

```go
func TestTheRunTimeoutReachesTheServerAsWellAsTheRunner(t *testing.T) {
	v := baseValues()
	set(v, "runner.runTimeout", "20m")
	d := mustRender(t, v)
	server := env(container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server"))
	if server["REMEDY_RUN_TIMEOUT"]["value"] != "20m" {
		t.Errorf("the server's REMEDY_RUN_TIMEOUT = %v, want 20m: the Setup page shows the runner's default", server["REMEDY_RUN_TIMEOUT"]["value"])
	}
	runner := env(container(t, podSpec(t, d.find("StatefulSet", "remedy-runner")), "runner"))
	if runner["REMEDY_RUN_TIMEOUT"]["value"] != "20m" {
		t.Errorf("the runner's REMEDY_RUN_TIMEOUT = %v", runner["REMEDY_RUN_TIMEOUT"]["value"])
	}

	none := env(container(t, podSpec(t, mustRender(t, baseValues()).find("Deployment", "remedy-server")), "server"))
	if _, set := none["REMEDY_RUN_TIMEOUT"]; set {
		t.Error("without runner.runTimeout the server has no REMEDY_RUN_TIMEOUT")
	}

	both := baseValues()
	set(both, "runner.runTimeout", "20m")
	set(both, "server.env", map[string]any{"REMEDY_RUN_TIMEOUT": "15m"})
	got := env(container(t, podSpec(t, mustRender(t, both).find("Deployment", "remedy-server")), "server"))
	if got["REMEDY_RUN_TIMEOUT"]["value"] != "15m" {
		t.Errorf("server.env must win over runner.runTimeout: %v", got["REMEDY_RUN_TIMEOUT"]["value"])
	}
}
```
(Use the actual container and workload names: check `command grep -n 'name: ' deploy/chart/templates/runner-statefulset.yaml | head` and the existing runner tests, which call `container(t, podSpec(t, d.find("StatefulSet", ...)), ...)`; match them.)

Run: `make chart-check` (or `go test ./deploy` with `REMEDY_REQUIRE_HELM=1`)
Expected: FAIL on the first assertion.

- [ ] **Step 2: Change the template**

In `deploy/chart/templates/server-deployment.yaml`, before `{{- range $name, $value := .Values.server.env }}`:

```yaml
            {{- if and .Values.runner.runTimeout (not (hasKey .Values.server.env "REMEDY_RUN_TIMEOUT")) }}
            - {name: REMEDY_RUN_TIMEOUT, value: {{ .Values.runner.runTimeout | quote }}}
            {{- end }}
```

- [ ] **Step 3: Comment the values**

In `deploy/chart/values.yaml` change the two comments:

```yaml
  # Extra environment variables for the control plane, for example REMEDY_POLL_INTERVAL or REMEDY_LOG_LEVEL. A name that
  # contains PASSWORD, TOKEN or KEY is refused: secrets do not belong in values. REMEDY_POLL_INTERVAL, REMEDY_DIAGNOSE_* and
  # REMEDY_RUN_TIMEOUT are only defaults: the Setup page can override them.
  env: {}
```
```yaml
  runTimeout: ""       # REMEDY_RUN_TIMEOUT, for example 10m. The control plane gets it too, as the default of the Setup page's setting
```

- [ ] **Step 4: Run and commit**

Run: `make chart-check`
Expected: PASS.

```bash
git add deploy
git commit -m "feat(chart): the control plane gets runner.runTimeout as the default of its setting

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 11: The Setup page

**Files:**
- Modify: `web/src/api.ts`, `web/src/SetupPage.tsx`
- Create: `web/src/settings.ts`, `web/src/settings.test.ts`, `web/src/components/setup/SettingsSection.tsx`
- Delete: `web/src/components/setup/LimitsSection.tsx`

**Interfaces:**
- Consumes: `GET/PUT/DELETE /api/settings` (Task 8), `api.getLimits` (unchanged).
- Produces in `web/src/settings.ts` (pure, tested): `groupSettings`, `valueText`, `inputText`, `sourceBadge`, `resetLabel`, `parseInput`, `unchanged`.

- [ ] **Step 1: Write the failing tests**

`web/src/settings.test.ts` (check the name is free):

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Setting } from './api.ts'
import { groupSettings, inputText, parseInput, resetLabel, sourceBadge, unchanged, valueText } from './settings.ts'

const setting = (over: Partial<Setting> = {}): Setting => ({
  key: 'poll_interval',
  group: 'Polling',
  label: 'How often I check GitHub',
  kind: 'duration',
  value: '1m',
  source: 'default',
  fallback: 'default',
  env: 'REMEDY_POLL_INTERVAL',
  rule: 'At least 10s.',
  effect: 'Takes effect at the next check.',
  ...over,
})

describe('groupSettings', () => {
  it('keeps the order of the server and puts settings of one group together', () => {
    const groups = groupSettings([
      setting({ key: 'a', group: 'Polling' }),
      setting({ key: 'b', group: 'Diagnosis limits' }),
      setting({ key: 'c', group: 'Diagnosis limits' }),
      setting({ key: 'd', group: 'Runs' }),
    ])
    assert.deepEqual(groups.map((g) => [g.title, g.settings.map((s) => s.key)]), [
      ['Polling', ['a']],
      ['Diagnosis limits', ['b', 'c']],
      ['Runs', ['d']],
    ])
  })
})

describe('valueText and inputText', () => {
  it('shows a duration and a number as they are, and a list with its names or "none"', () => {
    assert.equal(valueText(setting({ value: '15m' })), '15m')
    assert.equal(valueText(setting({ kind: 'number', value: 3 })), '3')
    assert.equal(valueText(setting({ kind: 'list', value: ['demo', 'staging'] })), 'demo, staging')
    assert.equal(valueText(setting({ kind: 'list', value: [] })), 'none')
  })
  it('puts a list into the input field as comma separated names, empty for none', () => {
    assert.equal(inputText(setting({ kind: 'list', value: ['demo', 'staging'] })), 'demo, staging')
    assert.equal(inputText(setting({ kind: 'list', value: [] })), '')
    assert.equal(inputText(setting({ kind: 'number', value: 0 })), '0')
  })
})

describe('sourceBadge and resetLabel', () => {
  it('names the three sources', () => {
    assert.equal(sourceBadge('default'), 'Default')
    assert.equal(sourceBadge('environment'), 'Environment')
    assert.equal(sourceBadge('set here'), 'Set here')
  })
  it('says what a reset falls back to, and offers none unless the value is set here', () => {
    assert.equal(resetLabel(setting({ source: 'set here', fallback: 'environment' })), 'Reset to environment')
    assert.equal(resetLabel(setting({ source: 'set here', fallback: 'default' })), 'Reset to default')
    assert.equal(resetLabel(setting({ source: 'environment', fallback: 'environment' })), null)
    assert.equal(resetLabel(setting({ source: 'default' })), null)
  })
})

describe('parseInput', () => {
  it('turns a duration into the string the server wants and refuses what is not one', () => {
    assert.deepEqual(parseInput(setting(), ' 30s '), { ok: true, value: '30s' })
    assert.deepEqual(parseInput(setting(), '1h30m'), { ok: true, value: '1h30m' })
    for (const bad of ['', 'soon', '30', '-5s', '1.5']) assert.equal(parseInput(setting(), bad).ok, false, bad)
  })
  it('turns a number into a number and refuses what is not a whole number', () => {
    assert.deepEqual(parseInput(setting({ kind: 'number' }), '12'), { ok: true, value: 12 })
    assert.deepEqual(parseInput(setting({ kind: 'number' }), '0'), { ok: true, value: 0 })
    for (const bad of ['', '-1', '3.5', 'x']) assert.equal(parseInput(setting({ kind: 'number' }), bad).ok, false, bad)
  })
  it('splits a list at commas, trims it and drops empty entries; an empty field is the empty list', () => {
    assert.deepEqual(parseInput(setting({ kind: 'list' }), 'demo, staging ,,'), { ok: true, value: ['demo', 'staging'] })
    assert.deepEqual(parseInput(setting({ kind: 'list' }), ''), { ok: true, value: [] })
  })
  it('gives a message that names what is wrong', () => {
    const r = parseInput(setting(), 'soon')
    assert.equal(r.ok, false)
    if (!r.ok) assert.match(r.message, /duration/)
  })
})

describe('unchanged', () => {
  it('compares the field with what the setting shows now, ignoring spaces around list entries', () => {
    assert.equal(unchanged(setting({ value: '1m' }), '1m'), true)
    assert.equal(unchanged(setting({ value: '1m' }), '90s'), false)
    assert.equal(unchanged(setting({ kind: 'list', value: ['a', 'b'] }), 'a,b'), true)
    assert.equal(unchanged(setting({ kind: 'list', value: ['a', 'b'] }), 'a, b, c'), false)
  })
})
```

Run: `cd web && npm test`
Expected: FAIL (cannot find `./settings.ts`).

- [ ] **Step 2: Types and calls in `web/src/api.ts`**

Next to `Limits`:

```ts
export type SettingValue = string | number | string[]

/** One setting of the Setup page. `fallback` is where the value comes from after a reset. */
export interface Setting {
  key: string
  group: string
  label: string
  kind: 'duration' | 'number' | 'list'
  value: SettingValue
  source: 'default' | 'environment' | 'set here'
  fallback: 'default' | 'environment'
  env: string
  rule: string
  effect: string
  /** A stored value that cannot be used; the value shown is then the environment's or the default. */
  problem?: string
}

/** Something about the process that is set outside the page. */
export interface Fact {
  label: string
  value: string
}

export interface SettingsPage {
  settings: Setting[]
  facts: Fact[]
}
```
and in `api`, next to `getLimits`:

```ts
  getSettings: () => request<SettingsPage>('GET', '/api/settings'),
  putSetting: (key: string, value: SettingValue) => request<Setting>('PUT', `/api/settings/${key}`, { value }),
  deleteSetting: (key: string) => request<void>('DELETE', `/api/settings/${key}`),
```

- [ ] **Step 3: `web/src/settings.ts`**

```ts
import type { Setting, SettingValue } from './api.ts'

export interface SettingGroup {
  title: string
  settings: Setting[]
}

/** The settings by group, in the order the server sends them. */
export function groupSettings(settings: Setting[]): SettingGroup[] {
  const groups: SettingGroup[] = []
  for (const s of settings) {
    const group = groups.find((g) => g.title === s.group)
    if (group) group.settings.push(s)
    else groups.push({ title: s.group, settings: [s] })
  }
  return groups
}

/** The value as a person reads it. */
export function valueText(s: Setting): string {
  if (Array.isArray(s.value)) return s.value.length === 0 ? 'none' : s.value.join(', ')
  return String(s.value)
}

/** The value as it stands in the input field. */
export function inputText(s: Setting): string {
  if (Array.isArray(s.value)) return s.value.join(', ')
  return String(s.value)
}

export function sourceBadge(source: Setting['source']): string {
  return source === 'set here' ? 'Set here' : source === 'environment' ? 'Environment' : 'Default'
}

/** The label of the reset button, or null when there is nothing to reset. */
export function resetLabel(s: Setting): string | null {
  return s.source === 'set here' ? `Reset to ${s.fallback}` : null
}

export type Parsed = { ok: true; value: SettingValue } | { ok: false; message: string }

const duration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/

/** A first check of what was typed, so that an obvious slip needs no request. The server has the last word on the bounds. */
export function parseInput(s: Setting, text: string): Parsed {
  const t = text.trim()
  if (s.kind === 'duration') {
    return duration.test(t) ? { ok: true, value: t } : { ok: false, message: 'Enter a duration such as 30s, 15m or 1h.' }
  }
  if (s.kind === 'number') {
    return /^\d+$/.test(t) ? { ok: true, value: Number(t) } : { ok: false, message: 'Enter a whole number.' }
  }
  return {
    ok: true,
    value: t
      .split(',')
      .map((n) => n.trim())
      .filter((n) => n !== ''),
  }
}

/** Whether the field holds what the setting shows now, so that Save has nothing to do. */
export function unchanged(s: Setting, text: string): boolean {
  const parsed = parseInput(s, text)
  if (!parsed.ok) return false
  return JSON.stringify(parsed.value) === JSON.stringify(s.value)
}
```

Run: `cd web && npm test && npm run lint`
Expected: PASS.

- [ ] **Step 4: `SettingsSection.tsx`**

Create `web/src/components/setup/SettingsSection.tsx`. It replaces `LimitsSection.tsx` (copy its summary card with the paragraph and the progress bar, drop the sentence "cannot be changed here") and adds the editable groups. Structure and behaviour (all of it, no placeholders):

```tsx
import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Fact, Limits, Setting } from '@/api.ts'
import { groupSettings, inputText, parseInput, resetLabel, sourceBadge, unchanged } from '@/settings.ts'
import { diagnosisBar, limitsText } from '@/setup.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** What Remedy does on its own, how much of it, and the values behind it. The environment gives the defaults; a value set here wins. */
export default function SettingsSection() {
  const [settings, setSettings] = useState<Setting[] | null>(null)
  const [facts, setFacts] = useState<Fact[]>([])
  const [limits, setLimits] = useState<Limits | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState<{ n: number; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)

  const loadLimits = useCallback(() => {
    api.getLimits().then(setLimits).catch(() => undefined)
  }, [])

  useEffect(() => {
    api
      .getSettings()
      .then((p) => {
        setSettings(p.settings)
        setFacts(p.facts)
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the settings'))
    loadLimits()
  }, [loadLimits])

  /** One request at a time. A failure is shown at the row that asked (the row gets the message), not for the whole page. */
  async function run(key: string, action: () => Promise<Setting | undefined>, said: (s: Setting) => string): Promise<string> {
    if (busyRef.current) return ''
    busyRef.current = true
    setBusy(true)
    try {
      const updated = await action()
      const fresh = await api.getSettings()
      setSettings(fresh.settings)
      setFacts(fresh.facts)
      loadLimits()
      const s = fresh.settings.find((x) => x.key === key) ?? updated
      setNotice((prev) => ({ n: (prev?.n ?? 0) + 1, text: s ? said(s) : 'Done.' }))
      return ''
    } catch (e) {
      return e instanceof ApiError ? e.message : 'Request failed'
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const bar = limits ? diagnosisBar(limits) : null

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">My limits</h2>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {!settings && !error && <Skeleton className="h-24" />}
      {limits && (
        <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
          <p className="font-serif text-[17px] leading-relaxed text-pretty">{limitsText(limits)}</p>
          {bar && (
            <div className="flex flex-col gap-2">
              <span className="text-[13px] text-muted-foreground">
                Automatic diagnoses in the last 24 hours: {bar.used} of {bar.max}
              </span>
              <div
                role="progressbar"
                aria-label="Automatic diagnoses in the last 24 hours"
                aria-valuemin={0}
                aria-valuemax={bar.max}
                aria-valuenow={Math.min(bar.used, bar.max)}
                aria-valuetext={`${bar.used} of ${bar.max}`}
                className="h-2 overflow-hidden rounded-full bg-input"
              >
                <div className={cn('h-full rounded-full', bar.full ? 'bg-destructive' : 'bg-primary')} style={{ width: `${Math.round(bar.ratio * 100)}%` }} />
              </div>
            </div>
          )}
        </div>
      )}

      <div role="status" aria-live="polite" className="text-[13px] text-muted-foreground empty:hidden">
        {notice && <span key={notice.n}>{notice.text}</span>}
      </div>

      {settings &&
        groupSettings(settings).map((g) => (
          <div key={g.title} className="flex flex-col gap-3 rounded-3xl border border-border bg-card p-5">
            <h3 className="text-[13px] font-semibold text-muted-foreground">{g.title}</h3>
            {g.settings.map((s) => (
              <Row key={s.key + String(s.value)} setting={s} busy={busy} run={run} />
            ))}
          </div>
        ))}

      {facts.length > 0 && (
        <div className="flex flex-col gap-2 rounded-3xl border border-border bg-card p-5">
          <h3 className="text-[13px] font-semibold text-muted-foreground">Set outside this page</h3>
          <dl className="flex flex-col gap-1 text-[14px]">
            {facts.map((f) => (
              <div key={f.label} className="flex flex-wrap gap-x-2">
                <dt className="text-muted-foreground">{f.label}:</dt>
                <dd className="break-all">{f.value}</dd>
              </div>
            ))}
          </dl>
          <span className="text-[13px] text-subtle">These come from the deployment (Helm values or the environment). They cannot be changed here.</span>
        </div>
      )}
    </section>
  )
}

type RunFn = (key: string, action: () => Promise<Setting | undefined>, said: (s: Setting) => string) => Promise<string>

function Row({ setting: s, busy, run }: { setting: Setting; busy: boolean; run: RunFn }) {
  const [text, setText] = useState(inputText(s))
  const [problem, setProblem] = useState('')
  const id = `setting-${s.key}`
  const isList = s.kind === 'list'

  async function save() {
    if (busy) return
    const parsed = parseInput(s, text)
    if (!parsed.ok) {
      setProblem(parsed.message)
      return
    }
    setProblem(await run(s.key, () => api.putSetting(s.key, parsed.value), (n) => `${n.label}: now ${inputText(n) || 'none'}.`))
  }

  async function reset() {
    if (busy) return
    setProblem(await run(s.key, () => api.deleteSetting(s.key).then(() => undefined), (n) => `${n.label}: back to ${inputText(n) || 'none'} (${n.source}).`))
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!isList) void save()
  }

  const reset_ = resetLabel(s)
  const same = unchanged(s, text)
  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-1.5 border-t border-border pt-3 first:border-t-0 first:pt-0" autoComplete="off">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <label htmlFor={id} className="text-[15px] font-semibold">
          {s.label}
        </label>
        <span className="rounded-full bg-muted px-2.5 py-0.5 text-[12px] text-muted-foreground">{sourceBadge(s.source)}</span>
      </div>
      <span className="text-[13px] text-muted-foreground">
        {s.rule} {s.effect}
      </span>
      {s.problem && <span className="text-[13px] text-destructive">{s.problem}</span>}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          id={id}
          value={text}
          onChange={(e) => {
            setText(e.target.value)
            setProblem('')
          }}
          inputMode={s.kind === 'number' ? 'numeric' : undefined}
          spellCheck={false}
          aria-describedby={problem ? `${id}-problem` : undefined}
          aria-invalid={problem ? true : undefined}
          className="h-11 max-w-64 text-base md:text-sm"
        />
        {isList ? (
          <ConfirmButton label="Save" confirmLabel="Confirm: change where I may act" className="h-10" disabled={busy || same} onConfirm={() => void save()} />
        ) : (
          <Button type="submit" aria-disabled={busy || same} className="h-10 aria-disabled:pointer-events-none aria-disabled:opacity-50">
            Save
          </Button>
        )}
        {reset_ && (
          <Button type="button" variant="ghost" aria-disabled={busy} className="h-10" onClick={() => void reset()}>
            {reset_}
          </Button>
        )}
      </div>
      {problem && (
        <span id={`${id}-problem`} role="alert" className="text-[13px] text-destructive">
          {problem}
        </span>
      )}
    </form>
  )
}
```

Notes that matter (do these, they are the conventions of the UI): the Save of a non-list setting is a `type="submit"` button with `aria-disabled`; `onSubmit` must also do nothing while `same` is true (`save` is called, parses the same value and the server answers idempotently, which is acceptable; if you prefer, return early when `same`). The row's `key` includes the value so a refresh from the server resets the field to the new value. `ConfirmButton` uses `disabled`; it is the only control of the list row, and its confirm click is a real second click.

- [ ] **Step 5: Use it on the page**

In `web/src/SetupPage.tsx` replace the import and the usage of `LimitsSection` by `SettingsSection`:

```tsx
import SettingsSection from '@/components/setup/SettingsSection'
...
      <SettingsSection />
```
and delete `web/src/components/setup/LimitsSection.tsx`.

- [ ] **Step 6: Check, then commit**

Run: `cd web && npm run lint && npm test && npm run build`
Expected: PASS (the build type-checks the component).

Run: `command grep -rn LimitsSection web/src` — Expected: no match.

```bash
git add web
git commit -m "feat(web): change the settings on the Setup page

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Documents, the live check and the whole check

**Files:**
- Modify: `docs/design.md` (section 2.7), `docs/specs/2026-10-05-phase-2d-signals-design.md`, `docs/specs/2026-10-09-hosts-and-skills-design.md` (sections 6 and 13, the status table), `docs/specs/2026-10-14-settings-3-design.md` (status line), `CLAUDE.md`, `README.md`
- Create: `internal/gatekeeper/live_settings_test.go` (build tag `kindwrite`)

- [ ] **Step 1: Documents**

1. `docs/design.md` section 2.7: say that the bootstrap stays in Helm and the environment and that the six values of cycle 3 are defaults which the Setup page overrides, with the source shown per value. Find the paragraph with `command grep -n '2.7' docs/design.md`.
2. `docs/specs/2026-10-05-phase-2d-signals-design.md`: find the sentence "Configuration: environment variables. No UI for it." and replace it with "Configuration: bootstrap in Helm and the environment; everything else is a setting (cycle 3, `2026-10-14-settings-3-design.md`)." Keep the line the umbrella already says supersedes it.
3. Umbrella spec: in section 6 add "Built; see `2026-10-14-settings-3-design.md`." and in section 13 replace the cycle 3 open point by its answer (the registry has six keys; a stored value that fails is shown on its row as a problem). Update the cycle status table row 3 to "implemented".
4. The cycle 3 spec: first lines become `Status: implemented 2026-10-14 (pull request pending). Implementation plan: docs/plans/settings-3-settings-in-db.md.` (use the date of the merge day if later).
5. `CLAUDE.md`, "Current state": in the hosts and skills bullet, change "cycles 3 to 6 are not" to "cycle 3 is implemented (settings in the database: `internal/settings`, migration 012, `/api/settings`, the Setup page; `docs/specs/2026-10-14-settings-3-design.md`), cycles 4 to 6 are not", and add to the paragraph on the environment variables: "`REMEDY_POLL_INTERVAL`, `REMEDY_DIAGNOSE_*`, `REMEDY_K8S_WRITE_NAMESPACES` and `REMEDY_RUN_TIMEOUT` are defaults: the Setup page overrides them (the control plane reads `REMEDY_RUN_TIMEOUT` too, the chart passes `runner.runTimeout` to both). An allowlist that is empty refuses every cluster action; a write token alone builds the actions."
6. `README.md`: the paragraphs that name `REMEDY_POLL_INTERVAL`, `REMEDY_RUN_TIMEOUT` and the three `REMEDY_DIAGNOSE_*` variables get one sentence: "These are defaults; you can change them on the Setup page without a restart."

- [ ] **Step 2: The live test against the kind testbed**

Create `internal/gatekeeper/live_settings_test.go` (check the name is free). It builds the real action tools on the testbed with an allowlist that changes while they exist. It sends **nothing** that changes the cluster: the refused and the checked calls stop before any request is made, so it leaves the testbed as it was.

```go
//go:build kindwrite

package gatekeeper_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/kube"
)

// . ~/remedy-kind/env.sh && go test -tags kindwrite -run LiveAllowlist -v ./internal/gatekeeper
func TestLiveAllowlistChangeIsUsedWithoutARestart(t *testing.T) {
	if os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE") == "" {
		t.Skip("REMEDY_K8S_WRITE_TOKEN_FILE is not set: start the testbed (dev/kind/up.sh) and source its env.sh")
	}
	var mu sync.Mutex
	var list []string
	setList := func(v ...string) { mu.Lock(); defer mu.Unlock(); list = v }
	cfg := kube.Config{
		API: os.Getenv("REMEDY_K8S_API"), CAFile: os.Getenv("REMEDY_K8S_CA_FILE"), ReadTokenFile: os.Getenv("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile: os.Getenv("REMEDY_K8S_WRITE_TOKEN_FILE"),
		Namespaces:     func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(list) },
	}
	reader, err := kube.NewReader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := kube.NewWriter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restart gatekeeper.Tool
	for _, tool := range gatekeeper.ClusterActionTools(reader, writer, cfg, nil) {
		if tool.Name == "cluster_rollout_restart" {
			restart = tool
		}
	}
	ctx := context.Background()
	args, err := restart.Decode(json.RawMessage(`{"kind":"deployment","namespace":"demo","name":"web"}`))
	if err != nil {
		t.Fatal(err)
	}

	var argErr gatekeeper.ArgumentError
	if err := restart.Check(ctx, args); !errors.As(err, &argErr) || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("with an empty list: %v, want a refusal", err)
	}
	setList("demo")
	if err := restart.Check(ctx, args); err != nil {
		t.Fatalf("after the list was set to demo: %v", err)
	}
	setList() // removed after the question was asked: the action must not be sent
	if _, err := restart.Run(ctx, gatekeeper.Call{Args: args, RequestedAt: time.Now()}); !errors.As(err, &argErr) {
		t.Fatalf("running after the namespace was removed: %v, want a refusal", err)
	}
}
```

Run it only with the testbed up:

Run: `. ~/remedy-kind/env.sh && go test -tags kindwrite -run 'LiveAllowlist' ./internal/gatekeeper`
Expected: PASS. If the testbed is not up (`dev/kind/up.sh`), say so in the final report instead of skipping silently.

Run also: `go vet -tags kind,kindwrite,sshlive ./...` — Expected: no output.

- [ ] **Step 3: The whole check**

Run: `make check`
Expected: PASS (fmt, vet, `go test ./...`, chart check, shell tests, web lint, web test, web build). If `web/node_modules` is missing in this worktree run `make web-install` first. A full `go test -race ./internal/server` takes about 25 minutes; `make check` does not use `-race`, run the focused `-race` runs of Tasks 3 and 6 to 9 instead.

- [ ] **Step 4: Check the page in a real browser**

Follow CLAUDE.md "throwaway server": `make web-build`, then `go build -tags webui -o <scratchpad>/remedy-server ./cmd/remedy-server`, start it with `REMEDY_ADDR`, `REMEDY_DB` in the scratchpad, `REMEDY_ADMIN_PASSWORD`, `REMEDY_RUNNER_TOKEN`, `REMEDY_MASTER_KEY` and `REMEDY_DIAGNOSE_MAX_PER_DAY=9`. With Playwright (real clicks, `getByLabel(/password/i)` to sign in) check on `/settings`: the six rows with their sources (the day limit shows "Environment"); change the poll interval to `30s` (badge turns "Set here", the status line announces it, the summary sentence says "every 30 seconds"); "Reset to default" returns it; `5s` is refused with the server's message under the field; the namespace row arms on the first click ("Confirm: change where I may act") and disarms on blur; the Save button keeps the keyboard focus while a request runs; a refused save leaves the typed text. Look at Today's feed: a "Setting poll_interval changed from ..." entry is there. Delete stray `*.png` and `.playwright-mcp/` files afterwards.

- [ ] **Step 5: Commit, push, open the pull request**

```bash
git add -A
git commit -m "docs: settings in the database (cycle 3) in the design, specs, CLAUDE.md and README

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

Push the branch and open the pull request with the title `feat: settings in the database (cycle 3)`; the description lists the six settings, the two changes of behaviour (an empty allowlist refuses instead of switching the actions off; the reaper threshold follows the run timeout), the chart change, and ends with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`. Watch CI (`gh pr checks`); when all checks are green merge with squash (standing permission), then pull `main` in the main checkout.
