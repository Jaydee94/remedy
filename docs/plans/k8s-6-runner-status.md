# K-6: The Runner's Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In a cluster nobody sees the runner's terminal, so Remedy says whether its runner is connected and whether the agent CLI is logged in: the runner checks its own login with the CLI's own status command and reports it, the control plane shows it on the Setup page, and the runner pod gets liveness and readiness probes. The dummy setup reports the login at the end of `make dummy-up` and fails the smoke test early, before spending quota, when the runner is not logged in.

**Architecture:** The runner keeps a small `Status` (connected, login state, CLI version) that its claim loop, a login checker and a reporter share. A tiny HTTP listener (`REMEDY_RUNNER_STATUS_ADDR`) serves `/livez` and `/readyz` for the probes. The reporter posts the login state to a new runner route `POST /runner/v1/status`; the control plane keeps the last report and the time of the runner's last authenticated request in memory and shows both at `GET /api/runner`. The UI adds one section to Setup, built by a pure, tested module.

**Tech Stack:** Go 1.27 stdlib, React 19, TypeScript, Helm. No new dependencies.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), decision D8 and section 4 (probes). The record of spike S2 ([`docs/research/k8s-s2-login-check.md`](../research/k8s-s2-login-check.md)) fixes the command that tells "logged in" from "not logged in". Needs [K-1](k8s-1-images-and-listener.md) (the internal listener the new route lives on), [K-2](k8s-2-chart-core.md) and [K-4](k8s-4-dummy-setup.md).

**Scope note:** The status is held in memory and is not a database table: a restart of the control plane forgets it and the runner reports again within seconds. The login check starts the unmodified CLI and looks at its exit code and the shape of its output; it never opens a file of the login, and the output (which may name an account) is never logged, sent or stored.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| "Connected" | the runner reports it | The control plane notes the time of the runner's last authenticated request (any runner route) and calls the runner connected for 45 seconds after it. The runner also reports every 15 seconds, so the clock stays fresh while a long run is silent. No separate heartbeat is added for it. |
| What is reported | login and connection | Only the login state (`ok`, `missing`, `unknown`), when it was checked, and the CLI's version line. The control plane stamps the time it received the report and treats a checked-at time that is in the future or older than a day as "now". |
| Login `unknown` | not mentioned | The state when the check could not tell (the binary is missing, the command timed out, an exit code and text nobody expected) and while the runner is not connected. The UI says it does not know; it never says "logged in" from a stale report. |
| How often the login is checked | regularly | At start, then every 10 minutes while logged in and every minute while not, so that the page turns green within a minute of `make dummy-login`. |
| Probes | liveness and readiness | `/livez` answers 200 whenever the process serves HTTP. `/readyz` answers 200 once the runner has reached the control plane and 503 after a failed claim or report, and never depends on the login (an unattended cluster without a login must still become ready). A hung claim loop is not detected by liveness: runs have their own time limit and the reaper. |
| Where the route lives | not mentioned | `POST /runner/v1/status` is a runner route like the others, so it is on the internal port when `REMEDY_INTERNAL_ADDR` is set. `GET /api/runner` is an admin route behind the session. |
| Command of the check | from S2 | `provider.loginCheckArgs` is `{"auth", "status", "--text"}` (S2's record). The text form matters: the default output is JSON, which has `"loggedIn": false` but not the phrase "Not logged in", so a check without `--text` would class a logged-out CLI as `unknown`. The fake `claude` of the tests behaves like the real one in this respect (it prints the phrase only for `auth status --text`), so a missing `--text` fails a test. |
| What means "not logged in" | the CLI's text | Exit 1 with the sentence `Not logged in. Run claude auth login to authenticate.` (S2's record; the interactive CLI's `/login` wording is a different text and is not matched). `notLoggedIn` is `(?i)not logged in`. Exit 0 is logged in. Anything else (the binary does not start, another exit code, a timeout) is `unknown`. |
| Time limit of one check | 30 s | 10 s (`loginCheckTimeout`). The measured time is 0.06 to 0.10 s, so 10 s is a wide margin and far below a hang. A timeout is `unknown`, never `missing`: the runner reports "could not tell", checks again within the minute, and never asks the UI to show a login hint because of a slow or hung check. |
| Does the check need the network? | not mentioned | Unknown: S2 measured 0.06 to 0.10 s, consistent with a purely local read, but did not cut the network. Task 6 step 5a runs the check once with an unreachable proxy, in a throwaway pod built like the runner pod; until it has, the plan relies on neither answer (a failing check is `unknown`, which the page already says plainly). A login that has expired on the server side probably still reads as logged in; a failed run, not this check, is the real test of a login. |

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, UI copy, commit messages.
- The login check starts the unmodified CLI with the allowlisted environment (`provider.FilterEnv`) and nothing else. It never reads, copies, logs or stores a credential or the CLI's output; only the state it concludes goes anywhere.
- No new Go or web dependencies. `web/tsconfig.app.json` keeps `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types.
- `GET /api/runner` is behind the admin session. `POST /runner/v1/status` needs the runner token (the existing middleware, constant-time compare). Neither shows or accepts a token, an address or a path.
- A runner without a `Status`, a reporter or a checker (every existing test) behaves exactly as before: a nil `*Status` does nothing.
- The UI's copy is in the first person ("my runner"), a sentence is a template filled with values, and nothing the runner sends decides a sentence's structure.
- Every UI change is checked in a real browser before it is called done. `go test ./... -race -count=1` and `make check` pass at the end of every task. Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Shell in tests runs on macOS and on Linux.

## Review Focus

- A login check that hangs, exits oddly, or prints an account name: the state is `unknown` or `missing`, the reason names the exit code or the timeout and never the output (task 1).
- A runner report with a login word the server does not know, a version line with control characters or 5 000 characters, a time in the year 2040: refused or clamped, and the page is unchanged (task 4).
- A control plane that restarts or a runner that stops: the page says "not connected" within the minute and "login unknown", not "logged in" (task 4's unit test of `View`, task 6's live check).
- A runner under the chart's network policy: it must still become ready (the kubelet's probe reaches the status port); if the policy blocks it, that is a finding to fix in the chart, not a reason to drop the probe (task 6).
- Setup in a browser: never seen, connected and logged in, connected and not logged in, connected and unknown, and not connected each read right, on a phone width too (task 5).

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/provider/login.go`, `internal/testutil/loginclaude.go` | The login check and the version line of the CLI; a fake `claude` for them |
| `internal/runner/status.go` | `Status`, `Report`, the probe handler |
| `internal/runner/report.go` | `Reporter`, `LoginChecker` and their schedule |
| `internal/runner/loop.go`, `internal/runner/client.go` | `Loop.Status`; `Client.ReportStatus` |
| `internal/config/config.go`, `cmd/remedy-runner/main.go` | `REMEDY_RUNNER_STATUS_ADDR`; the wiring |
| `internal/server/runner_status.go` | `RunnerStatus`, `POST /runner/v1/status`, `GET /api/runner` |
| `internal/server/server.go`, `internal/server/runnerapi.go`, `internal/app/app.go` | Routes; the touch in the runner middleware; the wiring |
| `web/src/api.ts`, `web/src/runnerstatus.ts`, `web/src/components/setup/RunnerSection.tsx`, `web/src/SetupPage.tsx` | The Setup section |
| `deploy/chart/templates/runner-statefulset.yaml`, `deploy/chart_test.go` | The status port and the probes |
| `dev/kind/lib.sh`, `dummy-up.sh`, `dummy-status.sh`, `smoke.sh` | The dummy reports and checks the login |

---

### Task 1: The login check

**Files:**
- Create: `internal/provider/login.go`, `internal/testutil/loginclaude.go`
- Test: `internal/provider/login_test.go`

**Interfaces:**
- Produces: `provider.LoginState` (`LoginOK`, `LoginMissing`, `LoginUnknown`; string values `ok`, `missing`, `unknown`), `func (c Claude) LoginCheck(ctx context.Context, parentEnv []string) (LoginState, string)` (the string is a short reason, never output of the CLI), `func (c Claude) Version(ctx context.Context, parentEnv []string) string`, `testutil.FakeClaudeLogin(t, mode string) string` with modes `ok`, `missing`, `broken`, `hang`.

Before this task read `docs/research/k8s-s2-login-check.md`. Its Decision block gives the command (`claude auth status --text`, so `loginCheckArgs` is `{"auth", "status", "--text"}`), what means "logged in" (exit 0; the text starts with `Login method: ...` and holds the account's e-mail, which is why the output never leaves the check) and what means "not logged in" (exit 1 and `Not logged in. Run claude auth login to authenticate.`, so `notLoggedIn` is `(?i)not logged in`). The default JSON form has the same exit codes but `"loggedIn": false` and no such phrase: with it a logged-out CLI would be `unknown`. The fake `claude` below mimics both forms for that reason. If the record is re-measured and a value changes, change `loginCheckArgs`, `notLoggedIn` and the fake together.

- [ ] **Step 1: A fake `claude` for the login commands**

Create `internal/testutil/loginclaude.go`:

```go
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaudeLogin writes an executable script that mimics the login commands of `claude` and returns its path.
// `--version` prints a version line. Any other call is the status command, and mode says what it does:
//
//	ok      exits 0 and prints the shape of the real text form, with a line that names an account (the output must
//	        never go anywhere)
//	missing exits 1; called as `auth status --text` it prints the real sentence "Not logged in. Run claude auth login
//	        to authenticate." to stderr, called any other way it prints the JSON form (`"loggedIn": false`, no such
//	        phrase), as the real CLI does (spike S2)
//	broken  exits 3 and prints something nobody expects
//	hang    sleeps far longer than any test waits
func FakeClaudeLogin(t testing.TB, mode string) string {
	t.Helper()
	var status string
	switch mode {
	case "ok":
		status = `echo "Login method: Claude Max Account"; echo "Email: someone@example.invalid"; exit 0`
	case "missing":
		status = `if [ "$*" = "auth status --text" ]; then echo "Not logged in. Run claude auth login to authenticate." >&2; else echo '{"loggedIn": false, "authMethod": "none"}'; fi; exit 1`
	case "broken":
		status = `echo "unexpected failure of the status command" >&2; exit 3`
	case "hang":
		status = `exec sleep 60`
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "2.1.288 (Claude Code)"
  exit 0
fi
%s
`, status)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/provider/login_test.go`:

```go
package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestLoginCheck(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin"}
	cases := []struct {
		mode string
		want provider.LoginState
	}{
		{"ok", provider.LoginOK},
		{"missing", provider.LoginMissing},
		{"broken", provider.LoginUnknown},
	}
	for _, c := range cases {
		claude := provider.Claude{Binary: testutil.FakeClaudeLogin(t, c.mode)}
		got, reason := claude.LoginCheck(context.Background(), env)
		if got != c.want {
			t.Errorf("%s: state = %q, want %q (reason %q)", c.mode, got, c.want, reason)
		}
		for _, leak := range []string{"someone@example.invalid", "Login method", "Not logged in", "loggedIn", "unexpected failure"} {
			if strings.Contains(reason, leak) {
				t.Errorf("%s: the reason %q contains the CLI's output %q: it must never leave the check", c.mode, reason, leak)
			}
		}
	}
}

func TestLoginCheckSaysWhyItCouldNotTell(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin"}
	_, reason := provider.Claude{Binary: testutil.FakeClaudeLogin(t, "broken")}.LoginCheck(context.Background(), env)
	if !strings.Contains(reason, "3") {
		t.Errorf("reason = %q, want the exit code", reason)
	}
	state, reason := provider.Claude{Binary: "/no/such/claude"}.LoginCheck(context.Background(), env)
	if state != provider.LoginUnknown || reason == "" {
		t.Errorf("a missing binary: %q %q, want unknown with a reason", state, reason)
	}
}

func TestLoginCheckGivesUpWhenTheCLIHangs(t *testing.T) {
	claude := provider.Claude{Binary: testutil.FakeClaudeLogin(t, "hang")}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	state, reason := claude.LoginCheck(ctx, []string{"PATH=/usr/bin:/bin"})
	if state != provider.LoginUnknown || !strings.Contains(reason, "time") {
		t.Fatalf("state %q reason %q, want unknown and a reason that says it timed out", state, reason)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("the check took %s: it must stop when its context ends", time.Since(started))
	}
}

func TestLoginCheckDoesNotPassTheAPIKeyOn(t *testing.T) {
	// Like every start of the CLI it goes through FilterEnv: an API key in the runner's environment must not reach it.
	script := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\nif [ -n \"$ANTHROPIC_API_KEY\" ]; then echo leaked >&2; exit 9; fi\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	state, _ := provider.Claude{Binary: script}.LoginCheck(context.Background(), []string{"PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=sk-secret"})
	if state != provider.LoginOK {
		t.Fatalf("state = %q: the API key reached the CLI", state)
	}
}

func TestVersion(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin"}
	if got := (provider.Claude{Binary: testutil.FakeClaudeLogin(t, "ok")}).Version(context.Background(), env); got != "2.1.288 (Claude Code)" {
		t.Errorf("version = %q", got)
	}
	if got := (provider.Claude{Binary: "/no/such/claude"}).Version(context.Background(), env); got != "" {
		t.Errorf("a missing binary: version = %q, want empty", got)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/provider -run 'Login|Version' -v`
Expected: FAIL to compile with `undefined: provider.LoginState`.

- [ ] **Step 4: Implement**

Create `internal/provider/login.go`:

```go
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// LoginState is what a login check concluded.
type LoginState string

const (
	LoginOK      LoginState = "ok"      // the CLI says it is logged in
	LoginMissing LoginState = "missing" // the CLI says it is not
	LoginUnknown LoginState = "unknown" // the check could not tell
)

// loginCheckArgs is the CLI's own command for "am I logged in" (the record of spike S2,
// docs/research/k8s-s2-login-check.md). --text matters: the default output is JSON, which says "loggedIn": false and
// never "Not logged in".
var loginCheckArgs = []string{"auth", "status", "--text"}

// notLoggedIn matches the CLI's sentence for a missing login ("Not logged in. Run claude auth login to authenticate.").
// It is matched against output that is never kept.
var notLoggedIn = regexp.MustCompile(`(?i)not logged in`)

// maxCheckOutput bounds what the check reads of the CLI's output.
const maxCheckOutput = 8 << 10

// capBuffer keeps at most maxCheckOutput bytes and drops the rest, so that a chatty CLI cannot fill memory. It claims to
// have written everything, because a short write would fail the command.
type capBuffer struct{ bytes.Buffer }

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := maxCheckOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// LoginCheck runs the CLI's status command and says whether it is logged in. It starts the unmodified binary with the
// allowlisted environment and looks at the exit code and at the shape of the output; it never opens a file of the login,
// and the output (which may name an account) is not returned, logged or kept. The reason names an exit code or a
// timeout, never output. The caller sets the time limit with ctx.
func (c Claude) LoginCheck(ctx context.Context, parentEnv []string) (LoginState, string) {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, loginCheckArgs...)
	cmd.Env = FilterEnv(parentEnv)
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	var out capBuffer
	cmd.Stdout, cmd.Stderr = &out, &out

	err := cmd.Run()
	switch {
	case err == nil:
		return LoginOK, ""
	case ctx.Err() != nil:
		return LoginUnknown, "the login check timed out"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if notLoggedIn.Match(out.Bytes()) {
			return LoginMissing, ""
		}
		return LoginUnknown, fmt.Sprintf("the login check exited with code %d", exit.ExitCode())
	}
	// The binary is missing or cannot be executed. The underlying error may carry a path: the reason stays a fixed text.
	return LoginUnknown, "the login check could not start"
}

// Version is the first line of `claude --version`, shortened to what a version line can hold, or "" when the CLI cannot
// say. It is shown to the maintainer as text.
func (c Claude) Version(ctx context.Context, parentEnv []string) string {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = FilterEnv(parentEnv)
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	var out capBuffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	line = strings.TrimSpace(line)
	if len(line) > 64 || strings.IndexFunc(line, func(r rune) bool { return r < ' ' || r > '~' }) >= 0 {
		return ""
	}
	return line
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/provider -race -count=1 -v 2>&1 | tail -20`
Expected: all PASS, including the older provider tests. `TestLoginCheckGivesUpWhenTheCLIHangs` finishes in well under 10 seconds because the fake script `exec`s `sleep`, which dies with the context.

- [ ] **Step 6: Commit**

```bash
git add internal/provider internal/testutil
git commit -m "feat(provider): a login check and a version line of the CLI that never keep its output

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The runner's status, the loop and the probes

**Files:**
- Create: `internal/runner/status.go`
- Modify: `internal/runner/loop.go`, `internal/config/config.go`
- Test: `internal/runner/status_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `provider.LoginState` (task 1).
- Produces: `runner.Status` with `NewStatus() *Status`, `SetConnected(bool)`, `Connected() bool`, `SetLogin(provider.LoginState, time.Time)`, `SetCLIVersion(string)`, `Report() Report`; `runner.Report{Login string; LoginCheckedAt time.Time; CLIVersion string}` (JSON `login`, `loginCheckedAt`, `cliVersion`); `runner.StatusHandler(*Status) http.Handler`; `Loop.Status *Status`; `config.Runner.StatusAddr`. A nil `*Status` does nothing and reports `unknown`.

- [ ] **Step 1: Write the failing tests**

Create `internal/runner/status_test.go`:

```go
package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestStatusStartsUnknownAndNotConnected(t *testing.T) {
	s := runner.NewStatus()
	if s.Connected() {
		t.Fatal("a new status must not be connected")
	}
	r := s.Report()
	if r.Login != "unknown" || !r.LoginCheckedAt.IsZero() || r.CLIVersion != "" {
		t.Fatalf("report = %+v", r)
	}
}

func TestStatusKeepsWhatItIsTold(t *testing.T) {
	s := runner.NewStatus()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.SetConnected(true)
	s.SetLogin(provider.LoginMissing, at)
	s.SetCLIVersion("2.1.288 (Claude Code)")
	if !s.Connected() {
		t.Fatal("not connected")
	}
	r := s.Report()
	if r.Login != "missing" || !r.LoginCheckedAt.Equal(at) || r.CLIVersion != "2.1.288 (Claude Code)" {
		t.Fatalf("report = %+v", r)
	}
	raw, _ := json.Marshal(r)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	if back["login"] != "missing" || back["loginCheckedAt"] == nil || back["cliVersion"] != "2.1.288 (Claude Code)" {
		t.Fatalf("json = %s", raw)
	}
}

func TestANilStatusDoesNothing(t *testing.T) {
	var s *runner.Status
	s.SetConnected(true)
	s.SetLogin(provider.LoginOK, time.Now())
	s.SetCLIVersion("x")
	if s.Connected() {
		t.Fatal("a nil status is never connected")
	}
	if s.Report().Login != "unknown" {
		t.Fatal("a nil status reports unknown")
	}
}

func get(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestTheProbesLiveAlwaysAndReadyWhenConnectedWhateverTheLogin(t *testing.T) {
	s := runner.NewStatus()
	h := runner.StatusHandler(s)
	if got := get(t, h, "/livez"); got != http.StatusOK {
		t.Fatalf("/livez = %d, want 200", got)
	}
	if got := get(t, h, "/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("/readyz before the control plane answered = %d, want 503", got)
	}
	s.SetConnected(true)
	s.SetLogin(provider.LoginMissing, time.Now())
	if got := get(t, h, "/readyz"); got != http.StatusOK {
		t.Fatalf("/readyz connected but not logged in = %d, want 200: the login must never gate readiness", got)
	}
	s.SetConnected(false)
	if got := get(t, h, "/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("/readyz after a failure = %d, want 503", got)
	}
	if got := get(t, h, "/livez"); got != http.StatusOK {
		t.Fatalf("/livez after a failure = %d, want 200", got)
	}
	if got := get(t, h, "/other"); got != http.StatusNotFound {
		t.Fatalf("/other = %d, want 404", got)
	}
}

func TestTheLoopTracksWhetherTheControlPlaneAnswers(t *testing.T) {
	var up atomic.Bool
	up.Store(true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Millisecond) // a real claim long-polls; do not spin
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	status := runner.NewStatus()
	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()},
		WorkspaceRoot: t.TempDir(),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:       20 * time.Millisecond,
		Status:        status,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "connected after an answer", status.Connected)
	up.Store(false)
	eventually(t, "not connected after a failed claim", func() bool { return !status.Connected() })
	up.Store(true)
	eventually(t, "connected again", status.Connected)
}
```

Append to `internal/config/config_test.go`:

```go
func TestRunnerFromEnvStatusAddr(t *testing.T) {
	base := map[string]string{"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"}
	c, err := config.RunnerFromEnv(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.StatusAddr != "" {
		t.Fatalf("the default is no status listener, got %q", c.StatusAddr)
	}
	base["REMEDY_RUNNER_STATUS_ADDR"] = ":8082"
	c, err = config.RunnerFromEnv(env(base))
	if err != nil || c.StatusAddr != ":8082" {
		t.Fatalf("StatusAddr = %q, err %v", c.StatusAddr, err)
	}
	for _, bad := range []string{"8082", "localhost", "localhost:"} {
		base["REMEDY_RUNNER_STATUS_ADDR"] = bad
		if _, err := config.RunnerFromEnv(env(base)); err == nil || !strings.Contains(err.Error(), "REMEDY_RUNNER_STATUS_ADDR") {
			t.Errorf("%q: err = %v, want one that names the variable", bad, err)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/runner ./internal/config -run 'Status|Probes|TracksWhether' -v 2>&1 | head -20`
Expected: FAIL to compile (`undefined: runner.NewStatus`, `c.StatusAddr undefined`).

- [ ] **Step 3: The status and the probe handler**

Create `internal/runner/status.go`:

```go
package runner

import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
)

// Status is what the runner knows about itself: whether the control plane answers, whether the CLI is logged in, and
// the CLI's version line. The claim loop, the login checker and the reporter share it. Every method is safe for
// concurrent use, and a nil *Status does nothing and says "unknown": a runner without one (the older tests) needs no
// checks.
type Status struct {
	mu         sync.Mutex
	connected  bool
	login      provider.LoginState
	checkedAt  time.Time
	cliVersion string
}

func NewStatus() *Status { return &Status{login: provider.LoginUnknown} }

// SetConnected records whether the control plane answered the runner's last request.
func (s *Status) SetConnected(ok bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.connected = ok
	s.mu.Unlock()
}

func (s *Status) Connected() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// SetLogin records what the last login check found and when.
func (s *Status) SetLogin(state provider.LoginState, at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.login, s.checkedAt = state, at
	s.mu.Unlock()
}

func (s *Status) SetCLIVersion(v string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cliVersion = v
	s.mu.Unlock()
}

// Report is what the runner tells the control plane: the body of POST /runner/v1/status. It carries a state and a version
// line, never output of the CLI.
type Report struct {
	Login          string    `json:"login"`
	LoginCheckedAt time.Time `json:"loginCheckedAt"`
	CLIVersion     string    `json:"cliVersion,omitempty"`
}

func (s *Status) Report() Report {
	if s == nil {
		return Report{Login: string(provider.LoginUnknown)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Report{Login: string(s.login), LoginCheckedAt: s.checkedAt, CLIVersion: s.cliVersion}
}

// StatusHandler serves the probes of the runner pod. /livez answers 200 whenever the process serves HTTP. /readyz
// answers 200 once the control plane has answered the runner and 503 after a failed claim or report. The login never
// decides readiness: a cluster without a login must still become ready.
func StatusHandler(s *Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.Connected() {
			http.Error(w, "the control plane has not answered", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "connected\n")
	})
	return mux
}
```

- [ ] **Step 4: The loop and the configuration**

In `internal/runner/loop.go`, in the `Loop` struct, replace:

```go
	HeartbeatInterval time.Duration // how often a run with tools reports to the control plane, default DefaultHeartbeatInterval
}
```

with:

```go
	HeartbeatInterval time.Duration // how often a run with tools reports to the control plane, default DefaultHeartbeatInterval
	Status            *Status       // optional: told whether the control plane answers the claim
}
```

and in `Run`, replace:

```go
			l.Log.Warn("claim failed", "err", err)
			sleep(ctx, backoff)
			continue
		}
		if r == nil {
```

with:

```go
			l.Status.SetConnected(false)
			l.Log.Warn("claim failed", "err", err)
			sleep(ctx, backoff)
			continue
		}
		l.Status.SetConnected(true)
		if r == nil {
```

In `internal/config/config.go`, in the `Runner` struct add after `RunTimeout`:

```go
	StatusAddr    string        // REMEDY_RUNNER_STATUS_ADDR, default empty: no status listener; set, it serves /livez and /readyz
```

and in `RunnerFromEnv`, replace:

```go
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
```

with:

```go
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	c.StatusAddr = get("REMEDY_RUNNER_STATUS_ADDR")
	if c.StatusAddr != "" {
		if _, port, err := net.SplitHostPort(c.StatusAddr); err != nil || port == "" {
			return Runner{}, errors.New("REMEDY_RUNNER_STATUS_ADDR must be host:port or :port, for example :8082")
		}
	}
```

(`net` is imported since plan K-1.)

- [ ] **Step 5: Run the tests**

Run: `gofmt -l . ; go vet ./... && go test ./internal/runner ./internal/config -race -count=1`
Expected: no `gofmt` output; PASS. Run `gofmt -w internal/runner/loop.go` if it reports the struct alignment.

- [ ] **Step 6: Commit**

```bash
git add internal/runner internal/config
git commit -m "feat(runner): a status the loop keeps, probes that never depend on the login, REMEDY_RUNNER_STATUS_ADDR

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The reporter, the login checker and the wiring

**Files:**
- Create: `internal/runner/report.go`
- Modify: `internal/runner/client.go`, `cmd/remedy-runner/main.go`
- Test: `internal/runner/report_test.go`

**Interfaces:**
- Consumes: `Status`, `Report` (task 2), `provider.Claude.LoginCheck`, `Version` (task 1).
- Produces: `Client.ReportStatus(ctx, Report) error`; `runner.Reporter{Client *Client; Status *Status; Interval time.Duration; Log *slog.Logger}` with `Run(ctx)`; `runner.LoginChecker{Check func(context.Context) (provider.LoginState, string); Version func(context.Context) string; Status *Status; OKEvery, ElseEvery time.Duration; Log *slog.Logger}` with `Run(ctx)`; the constants `DefaultReportInterval` (15 s), `DefaultLoginCheckOK` (10 min), `DefaultLoginCheckElse` (1 min), `loginCheckTimeout` (10 s); `NextCheck(state, okEvery, elseEvery) time.Duration`.

- [ ] **Step 1: Write the failing tests**

Create `internal/runner/report_test.go`:

```go
package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func TestNextCheckIsLazyWhenLoggedInAndEagerOtherwise(t *testing.T) {
	ok, other := 10*time.Minute, time.Minute
	for state, want := range map[provider.LoginState]time.Duration{
		provider.LoginOK: ok, provider.LoginMissing: other, provider.LoginUnknown: other,
	} {
		if got := runner.NextCheck(state, ok, other); got != want {
			t.Errorf("%s: %s, want %s", state, got, want)
		}
	}
}

type statusServer struct {
	mu      sync.Mutex
	reports []runner.Report
	auths   []string
	status  int // 0 means 204
}

func (s *statusServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var rep runner.Report
	_ = json.NewDecoder(r.Body).Decode(&rep)
	s.mu.Lock()
	s.reports = append(s.reports, rep)
	s.auths = append(s.auths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
	code := s.status
	s.mu.Unlock()
	if code == 0 {
		code = http.StatusNoContent
	}
	w.WriteHeader(code)
}

func (s *statusServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

func (s *statusServer) last() runner.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reports[len(s.reports)-1]
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTheReporterPostsAtOnceAndThenAgainWithWhatChanged(t *testing.T) {
	srv := &statusServer{}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	status := runner.NewStatus()
	rep := &runner.Reporter{
		Client: &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()},
		Status: status, Interval: 20 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rep.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first report", func() bool { return srv.count() >= 1 })
	if first := srv.last(); first.Login != "unknown" {
		t.Fatalf("the first report = %+v, want unknown", first)
	}
	if srv.auths[0] != "POST /runner/v1/status Bearer runner-token-with-at-least-24-chars" {
		t.Fatalf("the request was %q", srv.auths[0])
	}
	status.SetLogin(provider.LoginOK, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	eventually(t, "a report with the login", func() bool { return srv.last().Login == "ok" })
	if !status.Connected() {
		t.Fatal("a report the control plane accepted means the runner is connected")
	}
}

func TestAFailedReportMeansNotConnectedAndASuccessMeansConnected(t *testing.T) {
	srv := &statusServer{status: http.StatusServiceUnavailable}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	status := runner.NewStatus()
	status.SetConnected(true)
	rep := &runner.Reporter{
		Client: &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()},
		Status: status, Interval: 20 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rep.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "not connected after a refused report", func() bool { return srv.count() >= 1 && !status.Connected() })
	srv.mu.Lock()
	srv.status = 0
	srv.mu.Unlock()
	eventually(t, "connected after an accepted one", status.Connected)
}

func TestTheLoginCheckerChecksAtOnceAndKeepsTheVersion(t *testing.T) {
	status := runner.NewStatus()
	calls := make(chan struct{}, 10)
	checker := &runner.LoginChecker{
		Check:      func(context.Context) (provider.LoginState, string) { calls <- struct{}{}; return provider.LoginMissing, "" },
		Version:    func(context.Context) string { return "2.1.288 (Claude Code)" },
		Status:     status,
		OKEvery:    time.Hour,
		ElseEvery:  20 * time.Millisecond,
		Log:        quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the login state", func() bool { return status.Report().Login == "missing" })
	if r := status.Report(); r.CLIVersion != "2.1.288 (Claude Code)" || r.LoginCheckedAt.IsZero() {
		t.Fatalf("report = %+v", r)
	}
	// Not logged in: it asks again soon, so the page turns green a minute after the login.
	eventually(t, "a second check while not logged in", func() bool { return len(calls) >= 2 })
}

func TestTheLoginCheckerLeavesALoginAloneForAWhile(t *testing.T) {
	status := runner.NewStatus()
	var n int
	var mu sync.Mutex
	checker := &runner.LoginChecker{
		Check: func(context.Context) (provider.LoginState, string) {
			mu.Lock()
			n++
			mu.Unlock()
			return provider.LoginOK, ""
		},
		Status: status, OKEvery: time.Hour, ElseEvery: 10 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "logged in", func() bool { return status.Report().Login == "ok" })
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("%d checks in 150 ms while logged in with an hour between checks, want 1", n)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/runner -run 'NextCheck|Reporter|FailedReport|LoginChecker' -v 2>&1 | head -10`
Expected: FAIL to compile (`undefined: runner.NextCheck`, `runner.Reporter`).

- [ ] **Step 3: The client method**

In `internal/runner/client.go`, append at the end of the file:

```go
// ReportStatus tells the control plane the runner's login state. It is best effort: the reporter decides what a failure means.
func (c *Client) ReportStatus(ctx context.Context, r Report) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/status", r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}
```

- [ ] **Step 4: The reporter and the checker**

Create `internal/runner/report.go`:

```go
package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
)

const (
	// DefaultReportInterval is how often the runner tells the control plane its login state. It also keeps the control
	// plane's "last heard from the runner" fresh while a long run is silent.
	DefaultReportInterval = 15 * time.Second
	// DefaultLoginCheckOK and DefaultLoginCheckElse are how often the login is checked while it is fine and while it is
	// not: the page should turn green within a minute of a login.
	DefaultLoginCheckOK   = 10 * time.Minute
	DefaultLoginCheckElse = time.Minute

	// loginCheckTimeout bounds one check: it takes 0.06 to 0.10 s (spike S2), so 10 s only ever ends a hang. A timeout is
	// "unknown", never "not logged in".
	loginCheckTimeout = 10 * time.Second
)

// NextCheck is how long to wait before the next login check.
func NextCheck(state provider.LoginState, okEvery, elseEvery time.Duration) time.Duration {
	if state == provider.LoginOK {
		return okEvery
	}
	return elseEvery
}

// Reporter posts the runner's status to the control plane at once and then every Interval. An answer from the control
// plane means the runner is connected; a failure means it is not.
type Reporter struct {
	Client   *Client
	Status   *Status
	Interval time.Duration // default DefaultReportInterval
	Log      *slog.Logger
}

func (r *Reporter) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultReportInterval
	}
	for {
		if err := r.Client.ReportStatus(ctx, r.Status.Report()); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.Status.SetConnected(false)
			r.Log.Warn("status report failed", "err", err)
		} else {
			r.Status.SetConnected(true)
		}
		if !sleepCtx(ctx, interval) {
			return
		}
	}
}

// LoginChecker asks the CLI whether it is logged in, at once and then every OKEvery while it is and every ElseEvery
// while it is not, and keeps the answer, and the CLI's version line, in the status. Check is the CLI's own command; its
// output never gets here, only the state and a short reason.
type LoginChecker struct {
	Check     func(context.Context) (provider.LoginState, string)
	Version   func(context.Context) string // optional
	Status    *Status
	OKEvery   time.Duration // default DefaultLoginCheckOK
	ElseEvery time.Duration // default DefaultLoginCheckElse
	Log       *slog.Logger
}

func (c *LoginChecker) Run(ctx context.Context) {
	okEvery, elseEvery := c.OKEvery, c.ElseEvery
	if okEvery <= 0 {
		okEvery = DefaultLoginCheckOK
	}
	if elseEvery <= 0 {
		elseEvery = DefaultLoginCheckElse
	}
	if c.Version != nil {
		vctx, cancel := context.WithTimeout(ctx, loginCheckTimeout)
		c.Status.SetCLIVersion(c.Version(vctx))
		cancel()
	}
	for {
		cctx, cancel := context.WithTimeout(ctx, loginCheckTimeout)
		state, reason := c.Check(cctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		c.Status.SetLogin(state, time.Now())
		if state == provider.LoginUnknown {
			c.Log.Warn("cannot tell whether the CLI is logged in", "reason", reason)
		} else {
			c.Log.Info("login check", "login", string(state))
		}
		if !sleepCtx(ctx, NextCheck(state, okEvery, elseEvery)) {
			return
		}
	}
}

// sleepCtx waits for d or until ctx ends, and says whether the full wait passed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
```

(`loop.go` already has a `sleep(ctx, d)` helper without a result; `sleepCtx` is separate on purpose so that the existing function's behaviour does not change.)

- [ ] **Step 5: The wiring in the binary**

In `cmd/remedy-runner/main.go`, replace:

```go
	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: cfg.ServerURL, Token: cfg.Token, HTTP: &http.Client{}},
		Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: cfg.ClaudeBin, Model: cfg.ClaudeModel}},
```

with:

```go
	claude := provider.Claude{Binary: cfg.ClaudeBin, Model: cfg.ClaudeModel}
	status := runner.NewStatus()
	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: cfg.ServerURL, Token: cfg.Token, HTTP: &http.Client{}},
		Providers:     map[string]provider.Provider{"claude": claude},
		Status:        status,
```

and replace:

```go
	log.Info("runner started", "server", cfg.ServerURL, "workspaces", cfg.WorkspaceRoot)
	loop.Run(ctx)
	log.Info("runner stopped")
```

with:

```go
	if cfg.StatusAddr != "" {
		statusSrv := &http.Server{Addr: cfg.StatusAddr, Handler: runner.StatusHandler(status), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := statusSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("status listener failed", "err", err)
				os.Exit(1)
			}
		}()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = statusSrv.Shutdown(shutdown)
		}()
		log.Info("status listener", "addr", cfg.StatusAddr)
	}
	go (&runner.Reporter{Client: loop.Client, Status: status, Log: log}).Run(ctx)
	go (&runner.LoginChecker{
		Check:   func(c context.Context) (provider.LoginState, string) { return claude.LoginCheck(c, os.Environ()) },
		Version: func(c context.Context) string { return claude.Version(c, os.Environ()) },
		Status:  status,
		Log:     log,
	}).Run(ctx)

	log.Info("runner started", "server", cfg.ServerURL, "workspaces", cfg.WorkspaceRoot)
	loop.Run(ctx)
	log.Info("runner stopped")
```

and add `"errors"` and `"time"` to the imports of `main.go` (alphabetical: `"context"`, `"errors"`, `"log/slog"`, `"net/http"`, `"os"`, `"os/signal"`, `"syscall"`, `"time"`).

- [ ] **Step 6: Run everything**

Run: `gofmt -l . ; go vet ./... && go test ./... -race -count=1`
Expected: no `gofmt` output (run `gofmt -w internal/runner/report_test.go` if it complains about the struct alignment in the checker test); all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/runner cmd/remedy-runner
git commit -m "feat(runner): report the login state, check it on a schedule, and serve the probes

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The control plane shows the runner

**Files:**
- Create: `internal/server/runner_status.go`
- Modify: `internal/server/server.go`, `internal/server/runnerapi.go`, `internal/app/app.go`
- Test: `internal/server/runner_status_test.go`

**Interfaces:**
- Consumes: `runner.Report`'s JSON shape (task 2): `{"login": "...", "loginCheckedAt": "...", "cliVersion": "..."}`.
- Produces: `server.RunnerStatus` with `NewRunnerStatus()`, `Touch(now time.Time)` (nil-safe), `Report(login string, checkedAt time.Time, cliVersion string, now time.Time) error`, `View(now time.Time) RunnerView`; `server.Deps.RunnerStatus`; `POST /runner/v1/status` (runner token) and `GET /api/runner` (session) answering `RunnerView` JSON `{"connected": bool, "lastSeenAt"?: time, "login": "ok|missing|unknown", "loginCheckedAt"?: time, "cliVersion"?: string}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/server/runner_status_test.go`:

```go
package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

func newRunnerEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, RunnerStatus: server.NewRunnerStatus(),
	}))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}
}

type runnerView struct {
	Connected      bool       `json:"connected"`
	LastSeenAt     *time.Time `json:"lastSeenAt"`
	Login          string     `json:"login"`
	LoginCheckedAt *time.Time `json:"loginCheckedAt"`
	CLIVersion     string     `json:"cliVersion"`
}

func getRunnerView(t *testing.T, e *env, c *http.Client) runnerView {
	t.Helper()
	resp := e.do(t, c, http.MethodGet, "/api/runner", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runner = %d", resp.StatusCode)
	}
	return decode[runnerView](t, resp)
}

func TestTheRunnerViewNeedsASession(t *testing.T) {
	e := newRunnerEnv(t)
	if resp := e.do(t, e.adminClient(t, false), http.MethodGet, "/api/runner", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBeforeTheRunnerSaysAnythingItIsNotConnectedAndItsLoginIsUnknown(t *testing.T) {
	e := newRunnerEnv(t)
	v := getRunnerView(t, e, e.adminClient(t, true))
	if v.Connected || v.Login != "unknown" || v.LastSeenAt != nil || v.LoginCheckedAt != nil || v.CLIVersion != "" {
		t.Fatalf("view = %+v", v)
	}
}

func TestAReportMakesTheRunnerConnectedAndShowsItsLogin(t *testing.T) {
	e := newRunnerEnv(t)
	body := `{"login":"ok","loginCheckedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","cliVersion":"2.1.288 (Claude Code)"}`
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	v := getRunnerView(t, e, e.adminClient(t, true))
	if !v.Connected || v.Login != "ok" || v.CLIVersion != "2.1.288 (Claude Code)" || v.LastSeenAt == nil || v.LoginCheckedAt == nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestOnlyTheRunnerTokenCanReport(t *testing.T) {
	e := newRunnerEnv(t)
	for _, tok := range []string{"", "wrong-token-wrong-token-wrong-t"} {
		if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", `{"login":"ok"}`, tok); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", tok, resp.StatusCode)
		}
	}
	if v := getRunnerView(t, e, e.adminClient(t, true)); v.Connected || v.Login != "unknown" {
		t.Fatalf("a refused report changed the view: %+v", v)
	}
}

func TestABadReportIsRefusedAndChangesNothing(t *testing.T) {
	e := newRunnerEnv(t)
	good := `{"login":"missing","loginCheckedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","cliVersion":"2.1.288"}`
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", good, runnerToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("good report = %d", resp.StatusCode)
	}
	bad := map[string]string{
		"an unknown login word":     `{"login":"maybe"}`,
		"no login":                  `{"loginCheckedAt":"2026-10-07T12:00:00Z"}`,
		"control characters":        `{"login":"ok","cliVersion":"2.1\u0000.288\n<script>"}`,
		"a very long version":       `{"login":"ok","cliVersion":"` + strings.Repeat("1", 5000) + `"}`,
		"an unknown field":          `{"login":"ok","token":"x"}`,
		"not JSON":                  `login=ok`,
		"a time that is not a time": `{"login":"ok","loginCheckedAt":"yesterday"}`,
	}
	for name, body := range bad {
		if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/status", body, runnerToken); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
	v := getRunnerView(t, e, e.adminClient(t, true))
	if v.Login != "missing" || v.CLIVersion != "2.1.288" {
		t.Fatalf("bad reports changed the view: %+v", v)
	}
}

// The unit tests below use a clock of their own.

func TestConnectedForFortyFiveSecondsAfterTheLastRequestAndThenTheLoginIsUnknown(t *testing.T) {
	rs := server.NewRunnerStatus()
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if err := rs.Report("ok", t0, "2.1.288", t0); err != nil {
		t.Fatal(err)
	}
	if v := rs.View(t0.Add(44 * time.Second)); !v.Connected || v.Login != "ok" {
		t.Fatalf("44 s after: %+v, want connected and ok", v)
	}
	rs.Touch(t0.Add(30 * time.Second)) // any authenticated request keeps it connected
	if v := rs.View(t0.Add(74 * time.Second)); !v.Connected {
		t.Fatalf("44 s after a touch: %+v, want connected", v)
	}
	v := rs.View(t0.Add(76 * time.Second))
	if v.Connected {
		t.Fatalf("46 s after the last touch: %+v, want not connected", v)
	}
	if v.Login != "unknown" {
		t.Fatalf("not connected but the login is %q: a stale report must never say logged in", v.Login)
	}
	if v.LastSeenAt == nil || !v.LastSeenAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("lastSeenAt = %v, want the last touch", v.LastSeenAt)
	}
}

func TestACheckedAtFromTheFutureOrTheDistantPastIsReplacedByNow(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for name, at := range map[string]time.Time{
		"year 2040": time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC),
		"2020":      time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		"zero":      {},
	} {
		rs := server.NewRunnerStatus()
		if err := rs.Report("ok", at, "", now); err != nil {
			t.Fatal(err)
		}
		if v := rs.View(now); v.LoginCheckedAt == nil || !v.LoginCheckedAt.Equal(now) {
			t.Errorf("%s: loginCheckedAt = %v, want now", name, v.LoginCheckedAt)
		}
	}
	rs := server.NewRunnerStatus()
	recent := now.Add(-time.Hour)
	_ = rs.Report("ok", recent, "", now)
	if v := rs.View(now); !v.LoginCheckedAt.Equal(recent) {
		t.Errorf("a recent time must be kept: %v", v.LoginCheckedAt)
	}
}

func TestANilRunnerStatusDoesNothing(t *testing.T) {
	var rs *server.RunnerStatus
	rs.Touch(time.Now()) // must not panic
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/server -run 'RunnerView|RunnerSays|Report|Connected|CheckedAt|NilRunner' -v 2>&1 | head -10`
Expected: FAIL to compile (`undefined: server.NewRunnerStatus`).

- [ ] **Step 3: The status and its routes**

Create `internal/server/runner_status.go`:

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// runnerGrace is how long after its last authenticated request the runner counts as connected. The runner claims every
// 25 seconds at most and reports every 15, so a connected runner is never silent this long.
const runnerGrace = 45 * time.Second

// maxVersionLen bounds the CLI's version line a runner may report.
const maxVersionLen = 64

// RunnerStatus is what the control plane knows about its runner: when it last made an authenticated request, and what it
// last said about the CLI's login. It is held in memory: a restart forgets it and the runner reports again within seconds.
type RunnerStatus struct {
	mu         sync.Mutex
	lastSeen   time.Time
	login      string
	checkedAt  time.Time
	cliVersion string
}

func NewRunnerStatus() *RunnerStatus { return &RunnerStatus{login: "unknown"} }

// Touch notes an authenticated request of the runner. A nil *RunnerStatus does nothing.
func (r *RunnerStatus) Touch(now time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.lastSeen = now
	r.mu.Unlock()
}

// Report records what the runner says. It refuses a login word it does not know and a version line that is not a
// short line of printable ASCII, and replaces a checked-at time that is zero, in the future or older than a day by now.
func (r *RunnerStatus) Report(login string, checkedAt time.Time, cliVersion string, now time.Time) error {
	switch login {
	case "ok", "missing", "unknown":
	default:
		return errors.New("login must be ok, missing or unknown")
	}
	if len(cliVersion) > maxVersionLen {
		return errors.New("cliVersion is too long")
	}
	for _, c := range cliVersion {
		if c < ' ' || c > '~' {
			return errors.New("cliVersion must be printable ASCII")
		}
	}
	if checkedAt.IsZero() || checkedAt.After(now.Add(time.Minute)) || checkedAt.Before(now.Add(-24*time.Hour)) {
		checkedAt = now
	}
	r.mu.Lock()
	r.login, r.checkedAt, r.cliVersion = login, checkedAt, cliVersion
	r.mu.Unlock()
	return nil
}

// RunnerView is what GET /api/runner answers.
type RunnerView struct {
	Connected      bool       `json:"connected"`
	LastSeenAt     *time.Time `json:"lastSeenAt,omitempty"`
	Login          string     `json:"login"` // ok, missing or unknown; unknown whenever the runner is not connected
	LoginCheckedAt *time.Time `json:"loginCheckedAt,omitempty"`
	CLIVersion     string     `json:"cliVersion,omitempty"`
}

// View is the status as of now. A runner that is not connected has an unknown login, whatever it said last: a stale
// report must never say "logged in".
func (r *RunnerStatus) View(now time.Time) RunnerView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := RunnerView{Login: "unknown", CLIVersion: r.cliVersion}
	if !r.lastSeen.IsZero() {
		seen := r.lastSeen
		v.LastSeenAt = &seen
		v.Connected = now.Sub(seen) <= runnerGrace
	}
	if !r.checkedAt.IsZero() {
		checked := r.checkedAt
		v.LoginCheckedAt = &checked
	}
	if v.Connected {
		v.Login = r.login
	}
	return v
}

// postRunnerStatus is POST /runner/v1/status, behind the runner token.
func (s *srv) postRunnerStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login          string    `json:"login"`
		LoginCheckedAt time.Time `json:"loginCheckedAt"`
		CLIVersion     string    `json:"cliVersion"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	if err := s.d.RunnerStatus.Report(body.Login, body.LoginCheckedAt, body.CLIVersion, time.Now()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getRunner is GET /api/runner, behind the admin session.
func (s *srv) getRunner(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.d.RunnerStatus.View(time.Now()))
}
```

In `internal/server/server.go`, add to `Deps`, after `Cluster Cluster`:

```go

	// RunnerStatus is what the control plane knows about its runner. When it is nil there are neither the report route
	// nor GET /api/runner.
	RunnerStatus *RunnerStatus
```

and in `routes`, replace:

```go
	internal.HandleFunc("POST /runner/v1/runs/{id}/heartbeat", s.runner(s.heartbeat))
```

with:

```go
	internal.HandleFunc("POST /runner/v1/runs/{id}/heartbeat", s.runner(s.heartbeat))
	if d.RunnerStatus != nil {
		internal.HandleFunc("POST /runner/v1/status", s.runner(s.postRunnerStatus))
		public.HandleFunc("GET /api/runner", s.session(s.getRunner))
	}
```

In `internal/server/runnerapi.go`, replace:

```go
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.d.RunnerToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "invalid runner token")
			return
		}
		next(w, r)
```

with:

```go
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.d.RunnerToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "invalid runner token")
			return
		}
		s.d.RunnerStatus.Touch(time.Now()) // an authenticated request means the runner is there
		next(w, r)
```

(`time` is already imported in `runnerapi.go`: it uses `time.NewTimer`.)

In `internal/app/app.go`, in the `deps := server.Deps{ ... }` literal (plan K-1) add after the `Cluster:` line:

```go
		RunnerStatus: server.NewRunnerStatus(),
```

- [ ] **Step 4: Run everything**

Run: `gofmt -l . ; go vet ./... && go test ./... -race -count=1`
Expected: no `gofmt` output; all PASS, including every earlier server test (a `Deps` without `RunnerStatus` registers neither route and `Touch` on nil does nothing).

- [ ] **Step 5: Check it against the real runner binary**

```sh
go build -o /tmp/remedy-server-rs ./cmd/remedy-server && go build -o /tmp/remedy-runner-rs ./cmd/remedy-runner
cd "$(mktemp -d)" && export REMEDY_ADMIN_PASSWORD=twelve-chars-pw REMEDY_RUNNER_TOKEN=a-runner-token-of-24-chars-or-more REMEDY_MASTER_KEY=$(openssl rand -base64 32) REMEDY_ADDR=127.0.0.1:18090
/tmp/remedy-server-rs & sleep 1
REMEDY_SERVER_URL=http://127.0.0.1:18090 REMEDY_RUNNER_STATUS_ADDR=127.0.0.1:18091 REMEDY_CLAUDE_BIN=/no/such/claude /tmp/remedy-runner-rs 2> runner.log & sleep 3
printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" | curl -s -c jar -H 'X-Remedy-CSRF: 1' --data-binary @- http://127.0.0.1:18090/api/login -o /dev/null -w 'login %{http_code}\n'
curl -s -b jar http://127.0.0.1:18090/api/runner; echo
curl -s -o /dev/null -w 'livez %{http_code}\n' http://127.0.0.1:18091/livez
curl -s -o /dev/null -w 'readyz %{http_code}\n' http://127.0.0.1:18091/readyz
kill %1 %2
```

Expected: `login 204`; the view is `{"connected":true,"lastSeenAt":"…","login":"unknown","loginCheckedAt":"…"}` (the CLI binary does not exist, so the check says unknown and `runner.log` has `cannot tell whether the CLI is logged in` with the reason `the login check could not start`); `livez 200`; `readyz 200`. Then `rm -f /tmp/remedy-server-rs /tmp/remedy-runner-rs`.

- [ ] **Step 6: Commit**

```bash
git add internal/server internal/app
git commit -m "feat(server): GET /api/runner and POST /runner/v1/status, an in-memory view of the runner

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Setup shows the runner

**Files:**
- Modify: `web/src/api.ts`, `web/src/SetupPage.tsx`
- Create: `web/src/runnerstatus.ts`, `web/src/runnerstatus.test.ts`, `web/src/components/setup/RunnerSection.tsx`

**Interfaces:**
- Consumes: `GET /api/runner` (task 4); `duration(seconds)` of `web/src/setup.ts`.
- Produces: `RunnerStatus` (type) and `api.getRunner()`; `runnerView(status, now)`, `loginHint`.

Read `docs/specs/2026-10-05-ui-conversation-redesign-design.md` voice rules once before writing copy, and keep the sentences in the first person.

- [ ] **Step 1: The API type**

In `web/src/api.ts`, after the `Capabilities` interface, add:

```ts
/** What the control plane knows about its runner. The login is `unknown` whenever the runner is not connected. */
export interface RunnerStatus {
  /** The runner made an authenticated request in the last 45 seconds. */
  connected: boolean
  /** When the runner last made one. Left out when it never has. */
  lastSeenAt?: string
  login: 'ok' | 'missing' | 'unknown'
  /** When the runner last checked its login. */
  loginCheckedAt?: string
  /** The CLI's version line, plain text. */
  cliVersion?: string
}
```

and in the `api` object, after `getLimits`, add:

```ts
  getRunner: () => request<RunnerStatus>('GET', '/api/runner'),
```

- [ ] **Step 2: Write the failing tests**

Create `web/src/runnerstatus.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { RunnerStatus } from './api.ts'
import { loginHint, runnerView } from './runnerstatus.ts'

const now = Date.parse('2026-10-07T12:00:00Z')
const status = (over: Partial<RunnerStatus> = {}): RunnerStatus => ({ connected: true, login: 'ok', lastSeenAt: '2026-10-07T11:59:55Z', ...over })

describe('runnerView', () => {
  it('says so when it has never heard from the runner', () => {
    const v = runnerView({ connected: false, login: 'unknown' }, now)
    assert.equal(v.connection.label, 'Never seen')
    assert.equal(v.login.label, 'Login unknown')
    assert.equal(v.sentence, 'I have not heard from my runner yet.')
    assert.match(v.hint ?? '', /remedy-runner-0/)
  })

  it('is glad when the runner is connected and logged in', () => {
    const v = runnerView(status(), now)
    assert.equal(v.connection.label, 'Connected')
    assert.equal(v.login.label, 'Logged in')
    assert.equal(v.sentence, 'My runner is connected and logged in, so I can run an agent.')
    assert.equal(v.hint, null)
  })

  it('says the agent cannot run when the runner is connected but not logged in, and how to log in', () => {
    const v = runnerView(status({ login: 'missing' }), now)
    assert.equal(v.login.label, 'Not logged in')
    assert.match(v.login.dot, /destructive/)
    assert.equal(v.sentence, 'My runner is connected, but the agent is not logged in, so I cannot run one.')
    assert.equal(v.hint, loginHint)
    assert.match(loginHint, /kubectl exec -it remedy-runner-0 -c runner -- \/opt\/claude\/claude/)
    assert.match(loginHint, /\/login/)
  })

  it('does not claim to know the login when the runner has not said', () => {
    const v = runnerView(status({ login: 'unknown' }), now)
    assert.equal(v.login.label, 'Login unknown')
    assert.equal(v.sentence, 'My runner is connected. I do not know yet whether the agent is logged in.')
    assert.equal(v.hint, null)
  })

  it('says how long it has been silent when the runner is not connected', () => {
    const v = runnerView(status({ connected: false, login: 'unknown', lastSeenAt: '2026-10-07T11:57:00Z' }), now)
    assert.equal(v.connection.label, 'Not connected')
    assert.match(v.connection.dot, /destructive/)
    assert.equal(v.sentence, 'My runner has not been heard from for 3 minutes.')
    assert.equal(v.login.label, 'Login unknown')
  })

  it('never says "logged in" for a runner that is not connected, even if the status says ok', () => {
    const v = runnerView(status({ connected: false, login: 'ok', lastSeenAt: '2026-10-07T11:00:00Z' }), now)
    assert.equal(v.login.label, 'Login unknown')
    assert.ok(!/logged in/i.test(v.sentence), v.sentence)
  })

  it('does not go negative for a clock that is a little behind', () => {
    const v = runnerView(status({ connected: false, login: 'unknown', lastSeenAt: '2026-10-07T12:00:30Z' }), now)
    assert.equal(v.sentence, 'My runner has not been heard from for 0 seconds.')
  })
})
```

- [ ] **Step 3: Run it to see it fail**

Run: `cd web && npm test 2>&1 | tail -8`
Expected: FAIL: `Cannot find module './runnerstatus.ts'`.

- [ ] **Step 4: The pure module**

Create `web/src/runnerstatus.ts`:

```ts
import type { RunnerStatus } from './api.ts'
import { duration } from './setup.ts'

/** A chip, in the colour tokens of the app. */
export interface Chip {
  label: string
  dot: string
  soft: string
  text: string
}

/** What the Setup page says about the runner. Every sentence is a template; nothing the runner sends decides its structure. */
export interface RunnerView {
  connection: Chip
  login: Chip
  sentence: string
  /** What to do, when there is something to do. */
  hint: string | null
}

const good = { dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' }
const bad = { dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
const neutral = { dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }

/** How to log the runner in once. The runner pod's name is fixed by the chart. */
export const loginHint = 'Log in once in the runner pod: kubectl exec -it remedy-runner-0 -c runner -- /opt/claude/claude, then type /login.'

/** The runner's state as the page shows it. A runner that is not connected has an unknown login, whatever the status says. */
export function runnerView(s: RunnerStatus, now: number): RunnerView {
  const connection: Chip = s.connected ? { label: 'Connected', ...good } : s.lastSeenAt ? { label: 'Not connected', ...bad } : { label: 'Never seen', ...neutral }
  const login: Chip =
    !s.connected || s.login === 'unknown'
      ? { label: 'Login unknown', ...neutral }
      : s.login === 'ok'
        ? { label: 'Logged in', ...good }
        : { label: 'Not logged in', ...bad }

  if (!s.connected) {
    if (!s.lastSeenAt) {
      return {
        connection,
        login,
        sentence: 'I have not heard from my runner yet.',
        hint: 'Check that the runner is running: in Kubernetes the pod is remedy-runner-0.',
      }
    }
    const silent = Math.max(0, Math.round((now - Date.parse(s.lastSeenAt)) / 1000))
    return { connection, login, sentence: `My runner has not been heard from for ${duration(silent)}.`, hint: null }
  }
  switch (s.login) {
    case 'ok':
      return { connection, login, sentence: 'My runner is connected and logged in, so I can run an agent.', hint: null }
    case 'missing':
      return { connection, login, sentence: 'My runner is connected, but the agent is not logged in, so I cannot run one.', hint: loginHint }
    default:
      return { connection, login, sentence: 'My runner is connected. I do not know yet whether the agent is logged in.', hint: null }
  }
}
```

- [ ] **Step 5: Run the tests**

Run: `cd web && npm test 2>&1 | tail -12`
Expected: all PASS, including `runnerView`. If the "3 minutes" test fails, `duration(180)` is `3 minutes` per `setup.test.ts`; check the timestamps (11:57:00 to 12:00:00 is 180 seconds).

- [ ] **Step 6: The component and the page**

Create `web/src/components/setup/RunnerSection.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { RunnerStatus } from '@/api.ts'
import { runnerView, type Chip } from '@/runnerstatus.ts'
import { useClock } from '@/useClock.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const POLL_MS = 10_000

function ChipView({ chip }: { chip: Chip }) {
  return (
    <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', chip.soft, chip.text)}>
      <span aria-hidden className={cn('size-2 rounded-full', chip.dot)} />
      {chip.label}
    </span>
  )
}

/** Whether my runner is connected and whether its agent is logged in. In a cluster nobody sees the runner's terminal. */
export default function RunnerSection() {
  const [status, setStatus] = useState<RunnerStatus | null>(null)
  const [error, setError] = useState('')
  const now = useClock()

  useEffect(() => {
    let alive = true
    const load = () =>
      api
        .getRunner()
        .then((s) => {
          if (!alive) return
          setStatus(s)
          setError('')
        })
        .catch((e: unknown) => {
          if (alive) setError(e instanceof ApiError ? e.message : 'Could not load the runner')
        })
    load()
    const timer = setInterval(load, POLL_MS)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [])

  const view = status ? runnerView(status, now) : null

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">My runner</h2>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {!status && !error && <Skeleton className="h-24" />}
      {status && view && (
        <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <ChipView chip={view.connection} />
            <ChipView chip={view.login} />
          </div>
          <p aria-live="polite" className="font-serif text-[17px] leading-relaxed text-pretty">
            {view.sentence}
          </p>
          {view.hint && <p className="text-[13px] leading-relaxed text-muted-foreground">{view.hint}</p>}
          {status.cliVersion && <span className="text-[13px] text-subtle">Agent CLI: {status.cliVersion}</span>}
        </div>
      )}
    </section>
  )
}
```

In `web/src/SetupPage.tsx`, add the import after the `ReposSection` import:

```tsx
import RunnerSection from '@/components/setup/RunnerSection'
```

and replace:

```tsx
      {connection ? (
```

with:

```tsx
      <RunnerSection />

      {connection ? (
```

- [ ] **Step 7: Lint, test, build**

Run: `cd web && npm run lint && npm test && npm run build 2>&1 | tail -5`
Expected: no lint errors, all tests pass, the build succeeds.

- [ ] **Step 8: Check it in a real browser**

Build a throwaway server (CLAUDE.md: `REMEDY_ADDR` and `REMEDY_DB` move it):

```sh
make web-build && go build -tags webui -o /tmp/remedy-server-ui ./cmd/remedy-server
cd "$(mktemp -d)" && export REMEDY_ADMIN_PASSWORD=twelve-chars-pw REMEDY_RUNNER_TOKEN=a-runner-token-of-24-chars-or-more REMEDY_MASTER_KEY=$(openssl rand -base64 32) REMEDY_ADDR=127.0.0.1:18095 REMEDY_DB=$PWD/ui.db
/tmp/remedy-server-ui &
report() { curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $REMEDY_RUNNER_TOKEN" -d "$1" http://127.0.0.1:18095/runner/v1/status; }
```

With the Playwright tools: open `http://127.0.0.1:18095`, sign in (`getByLabel(/password/i)`, the password above), go to Setup, and check each state (the section polls every 10 seconds; reload instead of waiting):

1. Nothing reported yet: chips `Never seen` and `Login unknown`, the sentence "I have not heard from my runner yet." and the hint.
2. `report '{"login":"ok","cliVersion":"2.1.288 (Claude Code)"}'`, reload: `Connected` (green), `Logged in` (green), "My runner is connected and logged in, so I can run an agent.", `Agent CLI: 2.1.288 (Claude Code)`.
3. `report '{"login":"missing"}'`: `Not logged in` (red) and the hint with the `kubectl exec` command.
4. `report '{"login":"unknown"}'`: `Login unknown` and the sentence that says it does not know.
5. Wait 50 seconds without reporting, reload: `Not connected` (red), `Login unknown`, "My runner has not been heard from for …".
6. Resize to a phone width (390 px) in states 3 and 5: nothing overflows horizontally, the chips wrap, the hint wraps.
7. Look at the console messages: no errors.

Take one screenshot of state 3 to a path inside the scratchpad directory. Then `kill %1; rm -f /tmp/remedy-server-ui` and delete any stray `*.png` the browser tool left in the repository checkout (CLAUDE.md).

- [ ] **Step 9: Commit**

```bash
git add web
git commit -m "feat(web): Setup says whether my runner is connected and whether its agent is logged in

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The probes in the chart, the dummy, and the real check

**Files:**
- Modify: `deploy/chart/templates/runner-statefulset.yaml`, `deploy/chart_test.go`
- Modify: `dev/kind/lib.sh`, `dev/kind/dummy-up.sh`, `dev/kind/dummy-status.sh`, `dev/kind/smoke.sh`
- Modify: `docs/runbook/dummy-setup.md`, `docs/runbook/homelab-deploy.md`, `CLAUDE.md`
- Create: `docs/research/k8s-runner-status-real-run.md`

- [ ] **Step 1: Write the failing chart tests**

Append to `deploy/chart_test.go`:

```go
func TestTheRunnerHasAStatusPortAndProbesThatNeverDependOnTheLogin(t *testing.T) {
	c := container(t, podSpec(t, mustRender(t, baseValues()).find("StatefulSet", "remedy-runner")), "runner")
	if got := env(c)["REMEDY_RUNNER_STATUS_ADDR"]["value"]; got != ":8082" {
		t.Errorf("REMEDY_RUNNER_STATUS_ADDR = %v, want :8082", got)
	}
	ports, _ := c["ports"].([]any)
	if len(ports) != 1 || dig(t, ports[0], "name") != "status" || dig(t, ports[0], "containerPort") != 8082 {
		t.Fatalf("ports = %v, want only status 8082", ports)
	}
	if dig(t, c, "livenessProbe", "httpGet", "path") != "/livez" || dig(t, c, "livenessProbe", "httpGet", "port") != "status" {
		t.Errorf("livenessProbe = %v", c["livenessProbe"])
	}
	if dig(t, c, "readinessProbe", "httpGet", "path") != "/readyz" || dig(t, c, "readinessProbe", "httpGet", "port") != "status" {
		t.Errorf("readinessProbe = %v", c["readinessProbe"])
	}
}

func TestNoServiceReachesTheRunnersStatusPort(t *testing.T) {
	d := mustRender(t, baseValues())
	for _, svc := range d.all("Service") {
		if strings.Contains(toString(svc["spec"]), "8082") || strings.Contains(toString(svc["spec"]), "status") {
			t.Errorf("the Service %v exposes the runner's status port", dig(t, svc, "metadata", "name"))
		}
	}
}
```

Run: `go test ./deploy -run 'StatusPort|RunnersStatus' -count=1 2>&1 | head -8`
Expected: FAIL (`REMEDY_RUNNER_STATUS_ADDR = <nil>`).

- [ ] **Step 2: The chart**

In `deploy/chart/templates/runner-statefulset.yaml`, replace:

```yaml
            - {name: TERM, value: xterm-256color}
```

with:

```yaml
            - {name: TERM, value: xterm-256color}
            - {name: REMEDY_RUNNER_STATUS_ADDR, value: ":8082"}
```

and replace:

```yaml
          resources:
            {{- toYaml .Values.runner.resources | nindent 12 }}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - {name: state, mountPath: /state}
```

with:

```yaml
          # The status port serves only the probes. No Service points at it. The login never decides readiness.
          ports:
            - {name: status, containerPort: 8082}
          readinessProbe:
            httpGet: {path: /readyz, port: status}
            periodSeconds: 10
            failureThreshold: 3
          livenessProbe:
            httpGet: {path: /livez, port: status}
            initialDelaySeconds: 10
            periodSeconds: 20
          resources:
            {{- toYaml .Values.runner.resources | nindent 12 }}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - {name: state, mountPath: /state}
```

Run: `go test ./deploy -count=1 && make chart-check`
Expected: PASS.

- [ ] **Step 3: The dummy reports and checks the login**

In `dev/kind/lib.sh`, append:

```sh

# runner_state prints GET /api/runner of the dummy as JSON, or fails. It signs in with the generated admin password, which
# goes only to the dummy's own URL.
runner_state() {
  load_dummy_env
  jar=$(umask 077; mktemp)
  if ! printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" |
       curl -sf -o /dev/null -c "$jar" -H 'X-Remedy-CSRF: 1' --data-binary @- "$REMEDY_URL/api/login"; then
    rm -f "$jar"
    return 1
  fi
  curl -sf -b "$jar" "$REMEDY_URL/api/runner"
  rc=$?
  rm -f "$jar"
  return $rc
}
```

In `dev/kind/dummy-up.sh`, replace:

```sh
echo
echo "Ready."
echo "  url:       http://127.0.0.1:18080"
echo "  password:  REMEDY_ADMIN_PASSWORD in $DUMMY_ENV"
echo "  login:     make dummy-login   (once; the login survives make dummy-down)"
```

with:

```sh
# The runner reports its login within a few seconds of starting; wait for it, up to a minute.
login=unknown
n=0
while [ "$n" -lt 20 ]; do
  login=$(runner_state 2> /dev/null | jq -r 'select(.connected) | .login' 2> /dev/null || true)
  [ -n "$login" ] && [ "$login" != unknown ] && break
  n=$((n + 1))
  sleep 3
done
case ${login:-unknown} in
  ok) login_line="the runner is logged in" ;;
  missing) login_line="the runner is NOT logged in: run make dummy-login (once; it survives make dummy-down)" ;;
  *) login_line="the runner has not said yet: make dummy-status" ;;
esac

echo
echo "Ready."
echo "  url:       http://127.0.0.1:18080"
echo "  password:  REMEDY_ADMIN_PASSWORD in $DUMMY_ENV"
echo "  login:     $login_line"
```

The two lines after it (`then: make dummy-smoke` and the status line) stay as they are.

In `dev/kind/dummy-status.sh`, first rename the pod line. Replace:

```sh
echo "runner: $(k -n "$NS" get pod remedy-runner-0 -o jsonpath='{.status.phase}, restarts {.status.containerStatuses[0].restartCount}' 2>/dev/null || echo 'no pod')"
```

with:

```sh
echo "runner pod: $(k -n "$NS" get pod remedy-runner-0 -o jsonpath='{.status.phase}, restarts {.status.containerStatuses[0].restartCount}' 2>/dev/null || echo 'no pod')"
```

then add the runner's own report. Replace:

```sh
  echo "url: $REMEDY_URL ($(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$REMEDY_URL/healthz" || true) on /healthz)"
```

with:

```sh
  echo "url: $REMEDY_URL ($(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$REMEDY_URL/healthz" || true) on /healthz)"
  echo "runner: $(runner_state 2> /dev/null | jq -r 'if .connected then "connected, login " + .login else "not connected, login unknown" end' 2> /dev/null || echo 'unknown (the control plane did not answer)')"
```

In `dev/kind/smoke.sh`, replace:

```sh
say "run 1: why does something in demo crash?"
```

with:

```sh
state=$(api GET /api/runner) || fail "cannot ask the control plane about its runner"
[ "$(printf '%s' "$state" | jq -r .connected)" = true ] || fail "the runner is not connected to the control plane. make dummy-status"
[ "$(printf '%s' "$state" | jq -r .login)" != missing ] || fail "the runner is not logged in. Run: make dummy-login"

say "run 1: why does something in demo crash?"
```

Run: `for f in dev/kind/*.sh; do sh -n "$f" || echo "SYNTAX $f"; done; make shell-test`
Expected: no `SYNTAX` line; `shell-test` passes (the login-directory check still passes: none of the new lines names the login directory).

- [ ] **Step 4: Redeploy the dummy and look at the page**

```sh
make dummy-redeploy
make dummy-status
```

Expected: `redeployed with the tag dev-…`; `dummy-status` shows `runner pod: Running, restarts 0` and `runner: connected, login ok` (or `login missing` if the maintainer has not logged in yet: then ask them to run `make dummy-login` and continue). If the runner pod does not become ready, look at `kubectl -n remedy-system describe pod remedy-runner-0`: a failing readiness probe that says `connection refused` or a timeout while the control plane is up means the network policy blocks the kubelet's probe. That is a finding: add an ingress rule for the status port to the runner's policy (`from` the node's address range, as a value `networkPolicy.runner.probeFrom`, default empty list, rendered only when not empty), with a render test, and say so in the record.

- [ ] **Step 5: The live checks**

Open `http://127.0.0.1:18080`, sign in (password in `~/remedy-kind/dummy.env`), Setup, and check, in a browser with the Playwright tools, each of these, watching the "My runner" section (it polls every 10 seconds):

1. Logged in: `Connected`, `Logged in`.
2. `make dummy-logout` (answer `y`): within about a minute the section turns to `Not logged in` and shows the `kubectl exec` hint. `make dummy-smoke` now stops at once with `the runner is not logged in. Run: make dummy-login`, and has spent nothing.
3. `make dummy-login`, log in again: within about a minute the section is `Logged in` again.
4. `kubectl --context kind-remedy-dev -n remedy-system delete pod remedy-runner-0`: the section turns to `Not connected` within a minute (the new pod needs a moment to install the CLI), then `Connected` again, and the login is still `Logged in` (the state volume survived).
5. `kubectl --context kind-remedy-dev -n remedy-system rollout restart deployment/remedy-server`: the control plane forgets the runner; the page (after signing in again, sessions are in memory) says `Not connected` or `Never seen` for at most 15 seconds, then `Connected` with the login, because the runner reports every 15 seconds.
6. `make dummy-smoke`: `all ok`.

- [ ] **Step 5a: The check without a network (the follow-up of the S2 record)**

S2 did not measure whether `claude auth status --text` needs the network, and its record asks for one run in the real runner pod's setting with a proxy that cannot be reached, and the same with an empty `CLAUDE_CONFIG_DIR`, with the exit codes and the times. The runner image has no shell and no `env`, and `kubectl exec` cannot set a variable for one command, so the variable has to be in a pod spec. The way is a throwaway pod in `remedy-system` that is built like the runner pod, only with a different command and environment. This is a throwaway step, not part of the product, and it is **not verified here**: it has not been run, so if it does not run, write that in the record instead of guessing a result.

The throwaway pod uses the runner's own image (a distroless base that has the loader the CLI needs). It cannot mount the runner pod's `claude-bin` volume (an `emptyDir` belongs to one pod), so it installs the CLI itself the same way, with an init container that runs `install-cli` with the arguments of the runner pod's init container, so the pinned version and checksums come from one place. It mounts the runner's state claim `claude-state`, because the login is there. The claim is `ReadWriteOnce` and the runner pod uses it: on the testbed's single-node cluster a second pod on the same node can mount it. If the pod stays `Pending` on the claim (another node), run `kubectl --context kind-remedy-dev -n remedy-system scale statefulset remedy-runner --replicas=0` first, wait for the pod to go, and scale back to `1` at the end. The pod has the same hardening as the runner (the namespace enforces the `restricted` standard). Its environment is `HTTPS_PROXY=http://127.0.0.1:1`, `HOME=/state`, `CLAUDE_CONFIG_DIR` and `TMPDIR=/tmp`, and its command is `/opt/claude/claude auth status --text`.

```sh
K="kubectl --context kind-remedy-dev -n remedy-system"
IMG=$($K get pod remedy-runner-0 -o jsonpath='{.spec.containers[0].image}')
ARGS=$($K get pod remedy-runner-0 -o json | jq -c '.spec.initContainers[0].args')
SEC='{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"capabilities":{"drop":["ALL"]}}'

# check <pod name> <CLAUDE_CONFIG_DIR>: runs the status command once with an unreachable proxy and prints
# "<exit code> <started> <finished>". The output of the command is account data: it is never read (no `kubectl logs`).
check() {
  jq -n --arg name "$1" --arg img "$IMG" --argjson args "$ARGS" --arg cfg "$2" --argjson sec "$SEC" '{
    apiVersion: "v1", kind: "Pod", metadata: {name: $name},
    spec: {
      restartPolicy: "Never", automountServiceAccountToken: false,
      securityContext: {runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, fsGroup: 65532, seccompProfile: {type: "RuntimeDefault"}},
      initContainers: [{name: "install-cli", image: $img, imagePullPolicy: "IfNotPresent", args: $args, securityContext: $sec,
        volumeMounts: [{name: "claude-bin", mountPath: "/opt/claude"}]}],
      containers: [{name: "check", image: $img, imagePullPolicy: "IfNotPresent",
        command: ["/opt/claude/claude"], args: ["auth", "status", "--text"],
        env: [{name: "HTTPS_PROXY", value: "http://127.0.0.1:1"}, {name: "HOME", value: "/state"},
              {name: "CLAUDE_CONFIG_DIR", value: $cfg}, {name: "TMPDIR", value: "/tmp"}],
        securityContext: $sec,
        volumeMounts: [{name: "state", mountPath: "/state"}, {name: "claude-bin", mountPath: "/opt/claude", readOnly: true},
                       {name: "tmp", mountPath: "/tmp"}, {name: "empty", mountPath: "/empty"}]}],
      volumes: [{name: "claude-bin", emptyDir: {}}, {name: "tmp", emptyDir: {}}, {name: "empty", emptyDir: {}},
                {name: "state", persistentVolumeClaim: {claimName: "claude-state"}}]
    }}' | $K apply -f -
  i=0
  while [ "$i" -lt 150 ]; do
    phase=$($K get pod "$1" -o jsonpath='{.status.phase}')
    case $phase in Succeeded|Failed) break ;; esac
    i=$((i + 1)); sleep 2
  done
  $K get pod "$1" -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode} {.status.containerStatuses[0].state.terminated.startedAt} {.status.containerStatuses[0].state.terminated.finishedAt}{"\n"}'
  $K delete pod "$1" --wait=true
}

check s2-proxy-login /state/claude     # the login, and a proxy that cannot be reached
check s2-proxy-empty /empty            # an existing empty directory (the case S2 measured), same proxy
```

Look at the exit code and at the time between `startedAt` and `finishedAt` (one-second resolution) only; do not open any file under `/state`.

Expected: exit `0` for the first call and `1` for the second, both within a second or two, means the check is local (the install init container takes the time, not the check); a long wait or another exit code means it needs the network, and then `loginCheckTimeout` and the `unknown` state are doing real work. Either answer is recorded; neither changes the code of tasks 1 to 3. A pod that never starts (the claim, the image, the Pod Security Standard) is not an answer: say so in the record and do not loosen the namespace.

- [ ] **Step 6: The record and the documents**

Create `docs/research/k8s-runner-status-real-run.md`: the date, the commands of steps 4 and 5 with the output that matters (the `dummy-status` lines, what the page showed in each state with the time it took to change, the smoke test's early stop and its final `all ok`), whether the probe worked under the network policy and what was changed if not, the CLI command of the login check from the S2 record (`claude auth status --text`) and how long it takes in the pod, the exit codes and times of step 5a (or that it could not be run, and why), and a "Result" paragraph naming which parts of D8 this proves.

In `docs/runbook/dummy-setup.md`, in the table of commands and the section "For an agent", add that `make dummy-up` ends with the runner's login state, that `make dummy-status` has a `runner:` line (`connected, login ok|missing|unknown`) and that `make dummy-smoke` stops before its first run when the login is missing. In `docs/runbook/homelab-deploy.md`, replace the sentence `(After plan K-6, Setup shows whether the runner is logged in.)` with `Setup shows whether my runner is connected and logged in, and turns to "Not logged in" within a minute of the login expiring.`

In `CLAUDE.md`, in the Kubernetes bullet of "Current state", append: ` The runner reports its login state (the CLI's own status command, never its output) and its connection to the control plane; Setup shows it (`GET /api/runner`, `POST /runner/v1/status`, in memory), and the runner pod has probes on `REMEDY_RUNNER_STATUS_ADDR` (`/livez`, `/readyz`; readiness never depends on the login).` Add to the variables paragraph: `` `REMEDY_RUNNER_STATUS_ADDR` (runner, default empty: no listener) serves `/livez` and `/readyz` for the pod's probes. ``

- [ ] **Step 7: Final check and commit**

Run: `make check`
Expected: PASS.

```bash
git add deploy dev/kind docs CLAUDE.md
git commit -m "feat: probes for the runner pod, and a dummy that reports the login and stops early without one

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
