package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

const (
	sess = "s1"
	// plantedURL passes loginrelay.ValidateURL; its state value is unique, so a search for it finds only this URL.
	plantedURL   = "https://claude.com/cai/oauth/authorize?code=true&client_id=fake&response_type=code&code_challenge=fake&state=PLANTEDSTATEVALUE42"
	plantedState = "PLANTEDSTATEVALUE42"
	plantedCode  = "planted-code-0123456789"
	badURL       = "https://claude.com.evil.test/cai/oauth/authorize?code=true&client_id=fake&response_type=code&code_challenge=fake&state=fake"
)

// fakeCP is a control plane with only the two login routes. It hands out the commands a test pushes and, like the
// Manager, answers cancel 0 to a poll that names a session it has ended (a terminal report) or forgotten.
type fakeCP struct {
	mu           sync.Mutex
	queue        []loginrelay.PollAnswer
	polls        []loginrelay.PollRequest
	pollBodies   []string
	reports      []loginrelay.Report
	reportBodies []string
	other        []string // paths of every other request
	ended        map[string]bool
	cancels0     int
	wake         chan struct{}
}

func newFakeCP(t *testing.T) (*fakeCP, *httptest.Server) {
	cp := &fakeCP{ended: map[string]bool{}, wake: make(chan struct{}, 1)}
	ts := httptest.NewServer(cp)
	t.Cleanup(ts.Close)
	return cp, ts
}

func (cp *fakeCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/runner/v1/login/poll":
		var req loginrelay.PollRequest
		_ = json.Unmarshal(body, &req)
		cp.mu.Lock()
		cp.polls = append(cp.polls, req)
		cp.pollBodies = append(cp.pollBodies, string(body))
		cp.mu.Unlock()
		deadline := time.After(30 * time.Millisecond)
		for {
			if a, ok := cp.answer(req); ok {
				b, _ := json.Marshal(a)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
				return
			}
			select {
			case <-cp.wake:
			case <-deadline:
				w.WriteHeader(http.StatusNoContent)
				return
			case <-r.Context().Done():
				return
			}
		}
	case "/runner/v1/login/report":
		var rep loginrelay.Report
		_ = json.Unmarshal(body, &rep)
		cp.mu.Lock()
		cp.reports = append(cp.reports, rep)
		cp.reportBodies = append(cp.reportBodies, string(body))
		if rep.State == string(loginrelay.StateSucceeded) ||
			(rep.State == string(loginrelay.StateFailed) && rep.Reason != loginrelay.ReasonInvalidCode) {
			cp.ended[rep.Session] = true
		}
		cp.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		cp.mu.Lock()
		cp.other = append(cp.other, r.URL.Path)
		cp.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}
}

func (cp *fakeCP) answer(req loginrelay.PollRequest) (loginrelay.PollAnswer, bool) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if len(cp.queue) > 0 {
		a := cp.queue[0]
		cp.queue = cp.queue[1:]
		return a, true
	}
	if req.Session != "" && cp.ended[req.Session] {
		cp.cancels0++
		return loginrelay.PollAnswer{Session: req.Session, Action: string(loginrelay.ActionCancel)}, true
	}
	return loginrelay.PollAnswer{}, false
}

func (cp *fakeCP) push(answers ...loginrelay.PollAnswer) {
	cp.mu.Lock()
	cp.queue = append(cp.queue, answers...)
	cp.mu.Unlock()
	select {
	case cp.wake <- struct{}{}:
	default:
	}
}

func (cp *fakeCP) forget(session string) {
	cp.mu.Lock()
	cp.ended[session] = true
	cp.mu.Unlock()
}

func (cp *fakeCP) snapshotReports() []loginrelay.Report {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return slices.Clone(cp.reports)
}

func (cp *fakeCP) snapshotPolls() []loginrelay.PollRequest {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return slices.Clone(cp.polls)
}

func (cp *fakeCP) cancelsAnswered() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.cancels0
}

// waitReport waits for a report with this state, reason and attempt and returns it.
func (cp *fakeCP) waitReport(t *testing.T, state loginrelay.State, reason loginrelay.Reason, attempt int) loginrelay.Report {
	t.Helper()
	var found loginrelay.Report
	eventuallyWithin(t, 8*time.Second, "a report "+string(state)+"/"+string(reason), func() bool {
		for _, r := range cp.snapshotReports() {
			if r.State == string(state) && r.Reason == reason && r.Attempt == attempt {
				found = r
				return true
			}
		}
		return false
	})
	return found
}

// pollsAfter waits until n more polls than now arrived.
func (cp *fakeCP) pollsAfter(t *testing.T, n int) {
	t.Helper()
	start := len(cp.snapshotPolls())
	eventually(t, "more polls", func() bool { return len(cp.snapshotPolls()) >= start+n })
}

func start(seq int64) loginrelay.PollAnswer {
	return loginrelay.PollAnswer{Seq: seq, Session: sess, Action: string(loginrelay.ActionStart)}
}

func code(seq int64, c string) loginrelay.PollAnswer {
	return loginrelay.PollAnswer{Seq: seq, Session: sess, Action: string(loginrelay.ActionCode), Code: c}
}

func cancelCmd(seq int64) loginrelay.PollAnswer {
	return loginrelay.PollAnswer{Seq: seq, Session: sess, Action: string(loginrelay.ActionCancel)}
}

func eventuallyWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for: %s", d, what)
}

// syncBuffer is a log sink that the relay's goroutines and the test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type rig struct {
	cp     *fakeCP
	relay  *runner.LoginRelay
	fake   *testutil.Relay
	status *runner.Status
	pause  *runner.LoginPause
	logs   *syncBuffer
	stop   func() // ends Run and waits for it
}

func fastTimings() runner.LoginTimings {
	return runner.LoginTimings{
		URL: 3 * time.Second, Submit: 5 * time.Second, ConfirmEvery: 20 * time.Millisecond,
		ExitGrace: 200 * time.Millisecond, Relogin: 5 * time.Second, Backoff: 50 * time.Millisecond,
	}
}

// startRelay runs a LoginRelay against a fake control plane and the given fake CLI. tweak may change the relay
// before Run starts.
func startRelay(t *testing.T, fake *testutil.Relay, tweak func(*runner.LoginRelay)) *rig {
	t.Helper()
	cp, ts := newFakeCP(t)
	logs := &syncBuffer{}
	r := &rig{cp: cp, fake: fake, status: runner.NewStatus(), pause: &runner.LoginPause{}, logs: logs}
	claude := provider.Claude{Binary: fake.Path}
	r.relay = &runner.LoginRelay{
		Client:  &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()},
		Claude:  claude,
		Check:   func(ctx context.Context) (provider.LoginState, string) { return claude.LoginCheck(ctx, os.Environ()) },
		Status:  r.status,
		Env:     os.Environ(),
		Pause:   r.pause,
		Timings: fastTimings(),
		Log:     slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if tweak != nil {
		tweak(r.relay)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.relay.Run(ctx); close(done) }()
	var once sync.Once
	r.stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("Run did not return after its context ended")
			}
		})
	}
	t.Cleanup(func() {
		r.stop()
		// Nothing may outlive a test; a failure above may have left a child.
		for _, pid := range fake.PIDs() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return r
}

// waitGone waits until no process the fake recorded is alive.
func waitGone(t *testing.T, fake *testutil.Relay, within time.Duration) {
	t.Helper()
	eventuallyWithin(t, within, "every child of the fake CLI gone", func() bool { return len(fake.Alive()) == 0 })
}

// scriptCLI writes a fake CLI of the test's own: header prints the opening lines, body follows. It answers
// `auth status` from the marker file like FakeClaudeRelay and records its pid and the lines it read in the same files,
// so the Relay helpers work on it.
func scriptCLI(t *testing.T, header, body string) *testutil.Relay {
	t.Helper()
	dir := t.TempDir()
	for name, v := range map[string]string{"url": plantedURL, "code": plantedCode} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nD='" + dir + "'\n" + `if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  if [ -f "$D/loggedin" ]; then
    exit 0
  fi
  echo "Not logged in. Run claude auth login to authenticate." >&2
  exit 1
fi
echo $$ >> "$D/pids"
read -r URL < "$D/url"
read -r CODE < "$D/code"
` + header + body
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &testutil.Relay{Path: path, Dir: dir}
}

const standardHeader = `printf 'Opening browser to sign in'
printf '\342\200\246\n'
printf "If the browser didn't open, visit: %s\n" "$URL"
printf 'Paste code here if prompted > '
`

// quietReader reads every line and says nothing, so a second write of the code would be counted.
const quietReader = `while read -r line; do
  echo x >> "$D/lines"
done
`

func fakeURL(t *testing.T, fake *testutil.Relay) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fake.Dir, "url"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheDriverReportsTheURLAndWritesTheCodeOnce(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", Code: plantedCode})
	var mu sync.Mutex
	var written []loginrelay.Material
	r := startRelay(t, fake, func(l *runner.LoginRelay) {
		runner.SetOnCodeWritten(l, func(m loginrelay.Material) {
			mu.Lock()
			written = append(written, m)
			mu.Unlock()
		})
	})
	r.status.SetLogin(provider.LoginMissing, time.Now())

	r.cp.push(start(1))
	got := r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	if got.URL != fakeURL(t, fake) {
		t.Fatalf("the reported URL is not the planted one")
	}
	if !r.pause.Active() {
		t.Fatal("the login checker is not paused while the child runs")
	}
	r.cp.push(code(2, plantedCode))
	r.cp.waitReport(t, loginrelay.StateSucceeded, "", 1)
	if n := fake.LinesRead(); n != 1 {
		t.Fatalf("the CLI read %d lines, want 1", n)
	}
	mu.Lock()
	if len(written) != 1 {
		t.Fatalf("%d code writes, want 1", len(written))
	}
	m := written[0]
	mu.Unlock()
	if m.Len() != len(plantedCode) || bytes.Count(m.Bytes(), []byte{0}) != m.Len() {
		t.Fatal("the code holder was not zeroed after the write")
	}
	waitGone(t, fake, 5*time.Second)
	eventually(t, "the checker unpaused", func() bool { return !r.pause.Active() })

	// The session is over: the control plane answers cancel 0 once, then the runner stops naming it.
	eventually(t, "the cancel 0 for the ended session", func() bool { return r.cp.cancelsAnswered() >= 1 })
	r.cp.pollsAfter(t, 3)
	if n := r.cp.cancelsAnswered(); n != 1 {
		t.Fatalf("the control plane had to answer %d cancels, want 1 (the runner keeps naming an ended session)", n)
	}
	polls := r.cp.snapshotPolls()
	if last := polls[len(polls)-1]; last.Session != "" || last.Ack != 0 {
		t.Fatalf("the last poll = %+v, want no session and no ack", last)
	}
}

func TestACodeResentAfterALostResponseIsWrittenOnce(t *testing.T) {
	fake := scriptCLI(t, standardHeader, quietReader)
	r := startRelay(t, fake, func(l *runner.LoginRelay) {
		l.Check = func(context.Context) (provider.LoginState, string) { return provider.LoginMissing, "" }
	})
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	// The first answer reached the runner, but the control plane did not see the ack and sends seq 2 again.
	r.cp.push(code(2, plantedCode), code(2, plantedCode))
	eventually(t, "two polls that acknowledge seq 2", func() bool {
		n := 0
		for _, p := range r.cp.snapshotPolls() {
			if p.Session == sess && p.Ack == 2 {
				n++
			}
		}
		return n >= 2
	})
	eventually(t, "the code read", func() bool { return fake.LinesRead() >= 1 })
	time.Sleep(150 * time.Millisecond)
	if n := fake.LinesRead(); n != 1 {
		t.Fatalf("the CLI read %d lines, want 1: a resent code was written again", n)
	}
}

func TestARestartedRunnerNeverReplaysACode(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", Code: plantedCode})
	r := startRelay(t, fake, nil)
	r.cp.push(loginrelay.PollAnswer{Seq: 2, Session: "old", Action: string(loginrelay.ActionCode), Code: plantedCode})
	r.cp.pollsAfter(t, 3)
	if n := len(r.cp.snapshotReports()); n != 0 {
		t.Fatalf("%d reports, want none", n)
	}
	if pids := fake.PIDs(); len(pids) != 0 {
		t.Fatalf("a child was started: %v", pids)
	}
	if n := fake.LinesRead(); n != 0 {
		t.Fatalf("%d lines written", n)
	}
	for _, p := range r.cp.snapshotPolls() {
		if p.Session != "" || p.Ack != 0 {
			t.Fatalf("a poll named a session the runner does not have: %+v", p)
		}
	}
}

func TestBadURLIsReportedWithoutItAndTheChildIsKilled(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "bad-url"})
	r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Timings.ExitGrace = time.Second })
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonBadURL, 1)
	r.cp.mu.Lock()
	bodies := slices.Clone(r.cp.reportBodies)
	r.cp.mu.Unlock()
	for _, b := range bodies {
		if strings.Contains(b, "evil.test") || strings.Contains(b, `"url":`) {
			t.Fatalf("a report carries the hostile address: %s", b)
		}
	}
	waitGone(t, fake, time.Second)
	if strings.Contains(r.logs.String(), "evil.test") {
		t.Fatal("the hostile address reached the log")
	}
}

func TestNoURLAndAnUnrecognisedFirstLine(t *testing.T) {
	t.Run("no url", func(t *testing.T) {
		fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "no-url"})
		r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Timings.URL = 300 * time.Millisecond })
		began := time.Now()
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonNoURL, 1)
		if d := time.Since(began); d < 300*time.Millisecond {
			t.Fatalf("no_url after %s, before the wait of 300ms", d)
		}
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("an unrecognised first line", func(t *testing.T) {
		fake := scriptCLI(t, `printf 'Welcome to something else\n'
printf "If the browser didn't open, visit: %s\n" "$URL"
`, quietReader)
		r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Timings.URL = 10 * time.Second })
		began := time.Now()
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonNoURL, 1)
		if d := time.Since(began); d > 5*time.Second {
			t.Fatalf("no_url only after %s: an unrecognised first line must fail at once", d)
		}
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("an unrecognised second line", func(t *testing.T) {
		fake := scriptCLI(t, `printf 'Opening browser to sign in'
printf '\342\200\246\n'
printf 'Go to: %s\n' "$URL"
`, quietReader)
		r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Timings.URL = 10 * time.Second })
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonNoURL, 1)
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("a header that never ends", func(t *testing.T) {
		fake := scriptCLI(t, `i=0
while [ $i -lt 600 ]; do
  printf 'xxxxxxxxxx'
  i=$((i+1))
done
`, quietReader)
		r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Timings.URL = 10 * time.Second })
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonNoURL, 1)
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("exit early", func(t *testing.T) {
		fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "exit-early"})
		r := startRelay(t, fake, nil)
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonProcessExited, 1)
		waitGone(t, fake, 5*time.Second)
	})
}

func TestInvalidCodeOnEitherStreamRestarts(t *testing.T) {
	for _, mode := range []string{"invalid-stdout", "invalid-stderr"} {
		t.Run(mode, func(t *testing.T) {
			fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: mode})
			r := startRelay(t, fake, func(l *runner.LoginRelay) {
				l.Check = func(context.Context) (provider.LoginState, string) { return provider.LoginMissing, "" }
			})
			r.cp.push(start(1))
			r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
			r.cp.push(code(2, plantedCode))
			r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonInvalidCode, 1)
			// The report comes after the kill: the old child is gone before the control plane can send the next start.
			waitGone(t, fake, time.Second)
			if !r.pause.Active() {
				t.Fatal("the checker was unpaused between two attempts")
			}
			r.cp.push(start(3))
			r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 2)
			r.cp.push(code(4, plantedCode))
			r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonInvalidCode, 2)
			pids := fake.PIDs()
			if len(pids) != 2 || pids[0] == pids[1] {
				t.Fatalf("pids = %v, want two distinct children", pids)
			}
			r.cp.push(cancelCmd(5))
			waitGone(t, fake, 5*time.Second)
			eventually(t, "the checker unpaused after the cancel", func() bool { return !r.pause.Active() })
		})
	}
}

func TestSuccessPaths(t *testing.T) {
	t.Run("missing before, the child stays", func(t *testing.T) {
		fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-stays", Code: plantedCode})
		r := startRelay(t, fake, nil)
		r.status.SetLogin(provider.LoginMissing, time.Now())
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
		began := time.Now()
		r.cp.push(code(2, plantedCode))
		r.cp.waitReport(t, loginrelay.StateSucceeded, "", 1)
		if d := time.Since(began); d < 200*time.Millisecond {
			t.Fatalf("succeeded after %s: the child did not get its ExitGrace of 200ms", d)
		}
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("missing before, exits but the check says missing", func(t *testing.T) {
		fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", Code: plantedCode})
		r := startRelay(t, fake, func(l *runner.LoginRelay) {
			l.Check = func(context.Context) (provider.LoginState, string) { return provider.LoginMissing, "" }
		})
		r.status.SetLogin(provider.LoginMissing, time.Now())
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
		r.cp.push(code(2, plantedCode))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonNotConfirmed, 1)
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("ok before, only the exit code counts", func(t *testing.T) {
		fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", Code: plantedCode, LoggedIn: true})
		r := startRelay(t, fake, func(l *runner.LoginRelay) {
			l.Check = func(context.Context) (provider.LoginState, string) { return provider.LoginMissing, "" }
		})
		r.status.SetLogin(provider.LoginOK, time.Now())
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
		r.cp.push(code(2, plantedCode))
		r.cp.waitReport(t, loginrelay.StateSucceeded, "", 1)
		waitGone(t, fake, 5*time.Second)
	})
	t.Run("ok before, a child alive after Relogin", func(t *testing.T) {
		fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-stays", Code: plantedCode, LoggedIn: true})
		r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Timings.Relogin = 300 * time.Millisecond })
		r.status.SetLogin(provider.LoginOK, time.Now())
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
		began := time.Now()
		r.cp.push(code(2, plantedCode))
		r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonTimeout, 1)
		if d := time.Since(began); d < 300*time.Millisecond {
			t.Fatalf("timeout after %s, before Relogin", d)
		}
		waitGone(t, fake, 5*time.Second)
	})
}

func TestRunActiveGuard(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-stays"})
	r := startRelay(t, fake, func(l *runner.LoginRelay) { l.Busy = func() bool { return true } })
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonRunActive, 1)
	if pids := fake.PIDs(); len(pids) != 0 {
		t.Fatalf("a child was started while a run is running: %v", pids)
	}
	if r.pause.Active() {
		t.Fatal("the checker is paused without a child")
	}
}

func TestTheLoginCheckerIsPausedWhileASessionIsOpen(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-stays", Flap: true})
	claude := provider.Claude{Binary: fake.Path}
	check := func(ctx context.Context) (provider.LoginState, string) { return claude.LoginCheck(ctx, os.Environ()) }
	r := startRelay(t, fake, nil)
	checker := &runner.LoginChecker{
		Check: check, Status: r.status, Pause: r.pause.Active,
		OKEvery: time.Hour, ElseEvery: 20 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first check", func() bool { return r.status.Report().Login == "missing" })
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	// Without the pause a check now flaps: the fake answers something else while its login runs.
	if st, _ := check(context.Background()); st != provider.LoginUnknown {
		t.Fatalf("the fake does not flap (%s): the test would prove nothing", st)
	}
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if got := r.status.Report().Login; got != "missing" {
			t.Fatalf("the published login turned %q while a session was open", got)
		}
	}
	r.cp.push(cancelCmd(2))
	waitGone(t, fake, 5*time.Second)
	eventually(t, "the checker unpaused", func() bool { return !r.pause.Active() })
	before := r.status.Report().LoginCheckedAt
	eventually(t, "a check after the session", func() bool { return r.status.Report().LoginCheckedAt.After(before) })
	if got := r.status.Report().Login; got != "missing" {
		t.Fatalf("login = %q after the session, want missing", got)
	}
}

func TestTheCheckerIsKickedWhenTheSessionEnds(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", Code: plantedCode})
	claude := provider.Claude{Binary: fake.Path}
	r := startRelay(t, fake, nil)
	checker := &runner.LoginChecker{
		Check:  func(ctx context.Context) (provider.LoginState, string) { return claude.LoginCheck(ctx, os.Environ()) },
		Status: r.status, Pause: r.pause.Active, OKEvery: time.Hour, ElseEvery: time.Hour, Log: quiet(),
	}
	checker.Kick() // safe before Run
	r.relay.Kick = checker.Kick
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first check", func() bool { return r.status.Report().Login == "missing" })
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	r.cp.push(code(2, plantedCode))
	r.cp.waitReport(t, loginrelay.StateSucceeded, "", 1)
	eventuallyWithin(t, 2*time.Second, "the status ok", func() bool { return r.status.Report().Login == "ok" })
}

func TestProcessHygiene(t *testing.T) {
	type scenario struct {
		name string
		url  string
		run  func(t *testing.T, r *rig)
	}
	awaiting := func(t *testing.T, r *rig) {
		r.cp.push(start(1))
		r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	}
	scenarios := []scenario{
		{name: "cancel", run: func(t *testing.T, r *rig) {
			awaiting(t, r)
			r.cp.push(cancelCmd(2))
		}},
		{name: "expiry", run: func(t *testing.T, r *rig) {
			// The control plane queues a cancel when the session expires; the runner cannot tell the two apart.
			awaiting(t, r)
			time.Sleep(100 * time.Millisecond)
			r.cp.push(cancelCmd(2))
		}},
		{name: "bad_url", url: badURL, run: func(t *testing.T, r *rig) {
			r.cp.push(start(1))
			r.cp.waitReport(t, loginrelay.StateFailed, loginrelay.ReasonBadURL, 1)
		}},
		{name: "new attempt", run: func(t *testing.T, r *rig) {
			awaiting(t, r)
			first := r.fake.PIDs()
			r.cp.push(start(2))
			r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 2)
			for _, pid := range first {
				if syscall.Kill(pid, 0) == nil {
					t.Errorf("pid %d of the first attempt is alive in the second", pid)
				}
			}
			r.cp.push(cancelCmd(3))
		}},
		{name: "context end", run: func(t *testing.T, r *rig) {
			awaiting(t, r)
			r.stop()
		}},
	}
	for _, sc := range scenarios {
		for _, mode := range []string{"valid-stays", "ignores-term", "lingering"} {
			t.Run(sc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: mode, URL: sc.url})
				r := startRelay(t, fake, nil)
				sc.run(t, r)
				// ignores-term needs WaitDelay (3 s) before Go kills it.
				waitGone(t, fake, 8*time.Second)
				if mode == "lingering" && len(fake.PIDs()) < 2 {
					t.Fatalf("the lingering fake recorded no sleeper: %v", fake.PIDs())
				}
			})
		}
	}
}

func TestCancelForAnUnknownSessionKillsTheChild(t *testing.T) {
	fake := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-stays"})
	r := startRelay(t, fake, nil)
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	r.cp.forget(sess) // the control plane restarted: it answers cancel 0 to the session it does not know
	waitGone(t, fake, 5*time.Second)
	eventually(t, "the checker unpaused", func() bool { return !r.pause.Active() })
	r.cp.pollsAfter(t, 3)
	if n := r.cp.cancelsAnswered(); n != 1 {
		t.Fatalf("%d cancels answered, want 1: the runner kept naming the session", n)
	}
	if n := len(r.cp.snapshotReports()); n != 1 {
		t.Fatalf("%d reports, want only the awaiting_code one", n)
	}
}

func TestNothingOfTheCLIOutputIsLoggedOrPosted(t *testing.T) {
	const secret = "SECRET-IN-STDERR"
	fake := scriptCLI(t, standardHeader, `echo "`+secret+`"
echo "`+secret+`" >&2
read -r line
echo x >> "$D/lines"
if [ "$line" = "$CODE" ]; then
  : > "$D/loggedin"
  echo "Login successful `+secret+`"
  echo "Logged in as `+secret+`" >&2
  exit 0
fi
exit 1
`)
	r := startRelay(t, fake, nil)
	r.status.SetLogin(provider.LoginMissing, time.Now())
	r.cp.push(start(1))
	r.cp.waitReport(t, loginrelay.StateAwaitingCode, "", 1)
	r.cp.push(code(2, plantedCode))
	r.cp.waitReport(t, loginrelay.StateSucceeded, "", 1)
	waitGone(t, fake, 5*time.Second)
	r.stop()

	logs := r.logs.String()
	if !strings.Contains(logs, "login session") {
		t.Fatalf("the relay logged nothing, the test would prove nothing: %q", logs)
	}
	for _, s := range []string{plantedState, "claude.com", plantedCode, secret} {
		if strings.Contains(logs, s) {
			t.Fatalf("the log holds %q:\n%s", s, logs)
		}
	}
	r.cp.mu.Lock()
	defer r.cp.mu.Unlock()
	withURL := 0
	for i, b := range r.cp.reportBodies {
		if strings.Contains(b, plantedCode) || strings.Contains(b, secret) {
			t.Fatalf("a report holds CLI output or the code: %s", b)
		}
		if strings.Contains(b, plantedState) {
			withURL++
			if r.cp.reports[i].State != string(loginrelay.StateAwaitingCode) {
				t.Fatalf("the URL is in a %s report", r.cp.reports[i].State)
			}
		}
	}
	if withURL != 1 {
		t.Fatalf("the URL is in %d reports, want exactly the awaiting_code one", withURL)
	}
	for _, b := range r.cp.pollBodies {
		if strings.Contains(b, plantedCode) || strings.Contains(b, plantedState) || strings.Contains(b, secret) {
			t.Fatalf("a poll holds material: %s", b)
		}
	}
	if len(r.cp.other) != 0 {
		t.Fatalf("the relay called other routes (run events?): %v", r.cp.other)
	}
}

func TestTheRelayBacksOffWhenTheControlPlaneHasNoRelay(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	relay := &runner.LoginRelay{
		Client:  &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()},
		Timings: runner.LoginTimings{Backoff: 200 * time.Millisecond},
		Log:     quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { relay.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "two polls", func() bool { mu.Lock(); defer mu.Unlock(); return len(times) >= 2 })
	mu.Lock()
	defer mu.Unlock()
	if d := times[1].Sub(times[0]); d < 200*time.Millisecond {
		t.Fatalf("the second poll came %s after a 404, want the backoff of 200ms", d)
	}
}

func TestLoginPollAnswers(t *testing.T) {
	cases := map[int]error{http.StatusNoContent: nil, http.StatusNotFound: runner.ErrLoginAbsent}
	for status, want := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/runner/v1/login/poll" || r.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("request %s %s", r.URL.Path, r.Header.Get("Authorization"))
			}
			w.WriteHeader(status)
		}))
		c := &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()}
		a, err := c.LoginPoll(context.Background(), loginrelay.PollRequest{Boot: "b"})
		if a != nil || err != want {
			t.Errorf("status %d: answer %v, err %v, want nil, %v", status, a, err, want)
		}
		ts.Close()
	}
}
