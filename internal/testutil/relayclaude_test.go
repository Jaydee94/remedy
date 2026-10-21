package testutil_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/testutil"
)

const (
	testURL  = "https://claude.com/cai/oauth/authorize?code=true&client_id=fake&response_type=code&code_challenge=fake&state=fake"
	testCode = "planted-code-0123456789"
)

// proc is one run of the fake CLI. Its output goes to files, so that a sleeper that holds them open cannot block Wait.
type proc struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	out, err string
	done     chan error
}

func start(t *testing.T, r *testutil.Relay, args ...string) *proc {
	t.Helper()
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	errf, err := os.Create(filepath.Join(dir, "err"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(r.Path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stdout, cmd.Stderr = out, errf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, stdin: stdin, out: out.Name(), err: errf.Name(), done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		for _, pid := range r.PIDs() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		out.Close()
		errf.Close()
	})
	return p
}

func (p *proc) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		t.Fatalf("write to stdin: %v", err)
	}
}

// stillRunning is what wait answers when the process has not ended (a process ended by a signal has exit code -1).
const stillRunning = -100

// wait returns the exit code, or stillRunning when the process is still running after d.
func (p *proc) wait(d time.Duration) int {
	select {
	case err := <-p.done:
		p.done <- err
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -2
	case <-time.After(d):
		return stillRunning
	}
}

func (p *proc) stdout() string { b, _ := os.ReadFile(p.out); return string(b) }
func (p *proc) stderr() string { b, _ := os.ReadFile(p.err); return string(b) }

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func status(t *testing.T, r *testutil.Relay) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(r.Path, "auth", "status", "--text")
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), o.String(), e.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, o.String(), e.String()
}

func relay(t *testing.T, mode string) *testutil.Relay {
	return testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: mode, URL: testURL, Code: testCode})
}

const (
	prompt      = "Paste code here if prompted > "
	successLine = "Login successful. Logged in as someone@example.invalid"
)

var loginArgs = []string{"auth", "login", "--claudeai"}

func TestFakeClaudeRelayPrintsTheRealShape(t *testing.T) {
	r := relay(t, "valid-stays")
	p := start(t, r, loginArgs...)
	_ = p.stdin.Close() // EOF before any code
	if code := p.wait(5 * time.Second); code != 1 {
		t.Fatalf("exit code on EOF = %d, want 1", code)
	}
	want := "Opening browser to sign in" + string(rune(0x2026)) + "\n" +
		"If the browser didn't open, visit: " + testURL + "\n" + prompt
	if got := p.stdout(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if r.LoggedIn() {
		t.Fatal("logged in without a code")
	}
	if n := r.LinesRead(); n != 0 {
		t.Fatalf("LinesRead = %d", n)
	}
	if pids := r.PIDs(); len(pids) != 1 || pids[0] != p.cmd.Process.Pid {
		t.Fatalf("PIDs = %v, want [%d]", pids, p.cmd.Process.Pid)
	}
}

// The defaults must be what the relay's own validator accepts (and, for bad-url, rejects), or a session with a default
// URL would end in bad_url before it reached awaiting_code.
func TestFakeClaudeRelayDefaultURLsMeetTheValidator(t *testing.T) {
	for mode, wantValid := range map[string]bool{"valid-exits": true, "bad-url": false} {
		r := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: mode})
		b, err := os.ReadFile(filepath.Join(r.Dir, "url"))
		if err != nil {
			t.Fatal(err)
		}
		err = loginrelay.ValidateURL(string(b))
		if wantValid && err != nil {
			t.Errorf("%s: the default URL is refused: %v", mode, err)
		}
		if !wantValid && err == nil {
			t.Errorf("%s: the default URL is accepted", mode)
		}
	}
	if err := loginrelay.ValidateURL(testURL); err != nil {
		t.Errorf("testURL is refused: %v", err)
	}
}

func TestFakeClaudeRelayVersion(t *testing.T) {
	out, err := exec.Command(relay(t, "valid-exits").Path, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "2.1.288 (Claude Code)" {
		t.Fatalf("--version = %q, %v", out, err)
	}
}

func TestFakeClaudeRelayModes(t *testing.T) {
	invalid := "Invalid code. Please make sure the full code was copied."

	t.Run("valid-exits accepts the planted code and exits 0", func(t *testing.T) {
		r := relay(t, "valid-exits")
		p := start(t, r, loginArgs...)
		p.send(t, "wrong-code-abcdef")
		eventually(t, "the first line to be counted", func() bool { return r.LinesRead() == 1 })
		if r.LoggedIn() || p.wait(100*time.Millisecond) != stillRunning {
			t.Fatal("a wrong code ended or logged in the fake")
		}
		if !strings.Contains(p.stdout(), invalid) {
			t.Fatalf("stdout = %q, want the rejection", p.stdout())
		}
		p.send(t, testCode)
		if code := p.wait(5 * time.Second); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
		if !r.LoggedIn() || r.LinesRead() != 2 {
			t.Fatalf("LoggedIn = %v, LinesRead = %d", r.LoggedIn(), r.LinesRead())
		}
		if !strings.Contains(p.stdout(), successLine) {
			t.Fatalf("stdout = %q, want the success line", p.stdout())
		}
		if code, _, _ := status(t, r); code != 0 {
			t.Fatalf("status exit = %d", code)
		}
	})

	t.Run("valid-stays creates the marker and stays until signalled", func(t *testing.T) {
		r := relay(t, "valid-stays")
		p := start(t, r, loginArgs...)
		p.send(t, testCode)
		eventually(t, "the marker", r.LoggedIn)
		eventually(t, "the success line", func() bool { return strings.Contains(p.stdout(), successLine) })
		if p.wait(300*time.Millisecond) != stillRunning {
			t.Fatal("exited by itself")
		}
		if err := syscall.Kill(p.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if p.wait(5*time.Second) == stillRunning {
			t.Fatal("still alive after TERM")
		}
	})

	t.Run("invalid-stdout rejects even the planted code on stdout", func(t *testing.T) {
		r := relay(t, "invalid-stdout")
		p := start(t, r, loginArgs...)
		p.send(t, testCode)
		eventually(t, "the rejection", func() bool { return strings.Contains(p.stdout(), invalid) })
		if strings.Contains(p.stderr(), invalid) || r.LoggedIn() {
			t.Fatalf("stderr = %q, LoggedIn = %v", p.stderr(), r.LoggedIn())
		}
		if p.wait(200*time.Millisecond) != stillRunning {
			t.Fatal("exited")
		}
	})

	t.Run("invalid-stderr rejects on stderr", func(t *testing.T) {
		r := relay(t, "invalid-stderr")
		p := start(t, r, loginArgs...)
		p.send(t, testCode)
		eventually(t, "the rejection", func() bool { return strings.Contains(p.stderr(), invalid) })
		if strings.Contains(p.stdout(), invalid) || r.LoggedIn() {
			t.Fatalf("stdout = %q, LoggedIn = %v", p.stdout(), r.LoggedIn())
		}
	})

	t.Run("no-url prints no URL line", func(t *testing.T) {
		r := relay(t, "no-url")
		p := start(t, r, loginArgs...)
		eventually(t, "the prompt", func() bool { return strings.HasSuffix(p.stdout(), prompt) })
		if strings.Contains(p.stdout(), "visit:") || strings.Contains(p.stdout(), testURL) {
			t.Fatalf("stdout = %q", p.stdout())
		}
	})

	t.Run("bad-url prints a URL on another host", func(t *testing.T) {
		r := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "bad-url", Code: testCode})
		p := start(t, r, loginArgs...)
		eventually(t, "the prompt", func() bool { return strings.HasSuffix(p.stdout(), prompt) })
		if !strings.Contains(p.stdout(), "visit: https://claude.com.evil.test/") {
			t.Fatalf("stdout = %q", p.stdout())
		}
	})

	t.Run("exit-early exits 1 without reading", func(t *testing.T) {
		r := relay(t, "exit-early")
		p := start(t, r, loginArgs...)
		if code := p.wait(5 * time.Second); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if r.LinesRead() != 0 || r.LoggedIn() {
			t.Fatalf("LinesRead = %d, LoggedIn = %v", r.LinesRead(), r.LoggedIn())
		}
	})

	t.Run("lingering leaves a sleeper behind", func(t *testing.T) {
		r := relay(t, "lingering")
		p := start(t, r, loginArgs...)
		p.send(t, testCode)
		if code := p.wait(5 * time.Second); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
		if pids := r.PIDs(); len(pids) != 2 {
			t.Fatalf("PIDs = %v, want the login and the sleeper", pids)
		}
		alive := r.Alive()
		if len(alive) != 1 || alive[0] != r.PIDs()[1] {
			t.Fatalf("Alive = %v, PIDs = %v", alive, r.PIDs())
		}
	})

	t.Run("ignores-term survives TERM and dies on KILL", func(t *testing.T) {
		r := relay(t, "ignores-term")
		p := start(t, r, loginArgs...)
		p.send(t, testCode)
		eventually(t, "the marker", r.LoggedIn)
		if err := syscall.Kill(p.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if code := p.wait(1500 * time.Millisecond); code != stillRunning {
			t.Fatalf("died on TERM with %d", code)
		}
		if len(r.Alive()) != 1 {
			t.Fatalf("Alive = %v", r.Alive())
		}
		if err := syscall.Kill(p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		if p.wait(5*time.Second) == stillRunning {
			t.Fatal("alive after KILL")
		}
	})
}

func TestFakeClaudeRelayStatus(t *testing.T) {
	t.Run("not logged in", func(t *testing.T) {
		code, _, stderr := status(t, relay(t, "valid-exits"))
		if code != 1 || !strings.Contains(stderr, "Not logged in. Run claude auth login to authenticate.") {
			t.Fatalf("code = %d, stderr = %q", code, stderr)
		}
	})

	t.Run("logged in from the start", func(t *testing.T) {
		r := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", LoggedIn: true})
		if !r.LoggedIn() {
			t.Fatal("LoggedIn is false")
		}
		if code, _, _ := status(t, r); code != 0 {
			t.Fatalf("code = %d", code)
		}
	})

	t.Run("flap fails while the login runs and the marker is absent", func(t *testing.T) {
		r := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-stays", URL: testURL, Code: testCode, Flap: true})
		if code, _, stderr := status(t, r); code != 1 || !strings.Contains(stderr, "Not logged in") {
			t.Fatalf("before the login: code = %d, stderr = %q", code, stderr)
		}
		p := start(t, r, loginArgs...)
		eventually(t, "the prompt", func() bool { return strings.HasSuffix(p.stdout(), prompt) })
		code, stdout, stderr := status(t, r)
		if code != 3 || strings.Contains(stdout+stderr, "Not logged in") {
			t.Fatalf("while it runs: code = %d, out = %q, err = %q", code, stdout, stderr)
		}
		p.send(t, testCode)
		eventually(t, "the marker", r.LoggedIn)
		if code, _, _ := status(t, r); code != 0 {
			t.Fatalf("with the marker: code = %d", code)
		}
	})

	t.Run("flap is quiet once the login has ended", func(t *testing.T) {
		r := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "exit-early", URL: testURL, Flap: true})
		p := start(t, r, loginArgs...)
		if p.wait(5*time.Second) != 1 {
			t.Fatal("did not exit")
		}
		if code, _, _ := status(t, r); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})
}

// TestServeRelayForBrowser serves a fake CLI for a look at the login wizard in a browser (Task 11): it prints the
// path, the planted code and the URL, and keeps the files until it is interrupted.
//
//	REMEDY_BROWSER_RELAY=1 go test ./internal/testutil -run TestServeRelayForBrowser -v -timeout 0
func TestServeRelayForBrowser(t *testing.T) {
	if os.Getenv("REMEDY_BROWSER_RELAY") != "1" {
		t.Skip("set REMEDY_BROWSER_RELAY=1 to serve a fake login CLI for a browser check")
	}
	r := testutil.FakeClaudeRelay(t, testutil.RelayOptions{Mode: "valid-exits", URL: testURL, Code: testCode})
	fmt.Printf("RELAY_PATH=%s\nRELAY_CODE=%s\nRELAY_URL=%s\n", r.Path, testCode, testURL)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
