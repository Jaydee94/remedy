//go:build sshlive

package sshhost_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/sshhost"
)

// The live tests need the testbed of dev/ssh (up.sh, then `. ~/remedy-ssh/env.sh`). They talk to a real sshd, a real sudo and a real bash.

type liveHost struct {
	target sshhost.Target
	cred   sshhost.Credential
	key    sshhost.HostKey
	pin    string
	svc    sshhost.Service
}

func live(t *testing.T, account string) *liveHost {
	t.Helper()
	host, port, keyFile := os.Getenv("REMEDY_SSH_HOST"), os.Getenv("REMEDY_SSH_PORT"), os.Getenv("REMEDY_SSH_KEY_FILE")
	if host == "" || port == "" || keyFile == "" {
		t.Skip("the SSH testbed is not set up: dev/ssh/up.sh, then . ~/remedy-ssh/env.sh")
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cred, _, err := sshhost.NewKeyCredential(string(pem), "")
	if err != nil {
		t.Fatal(err)
	}
	target := sshhost.Target{Address: host, Port: n, Account: account}
	key, err := sshhost.Probe(context.Background(), target)
	if err != nil {
		t.Fatalf("the testbed does not answer: %v", err)
	}
	return &liveHost{target: target, cred: cred, key: key, pin: key.Line}
}

func (l *liveHost) run(t *testing.T, argv []string, sudo bool) (sshhost.ExecResult, error) {
	t.Helper()
	return l.runWith(t, argv, sudo, sshhost.ExecOptions{})
}

func (l *liveHost) runWith(t *testing.T, argv []string, sudo bool, o sshhost.ExecOptions) (sshhost.ExecResult, error) {
	t.Helper()
	o.CanaryCommand, o.CanaryWant = hostexec.CanaryCommand(), hostexec.CanaryWant()
	return l.svc.Exec(context.Background(), 1, l.target, l.cred, l.pin, hostexec.CommandLine(argv, sudo), o)
}

var liveRules = hostexec.Rules{DockerPattern: "remedy-*", Units: []string{"node_exporter.service"}}

func TestLiveTheReportShowsWhatTheSnippetGrants(t *testing.T) {
	l := live(t, "remedy")
	rep, err := l.svc.Check(context.Background(), 1, l.target, l.cred, l.pin)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Account != "remedy" || rep.Sudo != sshhost.SudoLimited || len(rep.Warnings) != 0 || !strings.Contains(strings.Join(rep.SudoRules, "\n"), "/usr/bin/docker ps -a") {
		t.Fatalf("report = %+v", rep)
	}
}

// Every form of the table, as Classify says it runs (with or without sudo), succeeds on the testbed: the table and the sudoers
// file of the snippet agree.
func TestLiveTheTableOfReadsRunsAsTheAccountAndAsSudo(t *testing.T) {
	l := live(t, "remedy")
	seen := map[string]bool{}
	for _, c := range liveReads {
		v := hostexec.Classify(c.Argv, liveRules)
		if v.Kind != hostexec.Read {
			t.Errorf("%q is %+v, want a read", c.Argv, v)
			continue
		}
		seen[v.Form] = true
		res, err := l.run(t, c.Argv, v.Sudo)
		if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, c.Want) {
			t.Errorf("%q (sudo %v): %+v, %v, want exit 0 and %q", c.Argv, v.Sudo, res, err, c.Want)
		}
	}
	for _, f := range hostexec.ReadForms() {
		if !seen[f] {
			t.Errorf("the form %q was not run", f)
		}
	}
}

// sudo -n lets through exactly what the sudoers file lists and nothing else.
func TestLiveSudoRunsOnlyWhatTheSnippetAllows(t *testing.T) {
	l := live(t, "remedy")
	allowed := [][]string{
		{"docker", "restart", "remedy-drill"},
		{"docker", "stop", "remedy-drill"},
		{"systemctl", "restart", "node_exporter.service"},
	}
	for _, argv := range allowed {
		if res, err := l.run(t, argv, true); err != nil || res.ExitCode != 0 {
			t.Errorf("%q: %+v, %v, want it to run", argv, res, err)
		}
		if err := hostexec.ChangeCheck(argv, liveRules); err != nil {
			t.Errorf("%q: sudo runs it but the rules of the host refuse it: %v", argv, err)
		}
	}
	refused := [][]string{
		{"docker", "restart", "other"},    // outside the container pattern
		{"docker", "run", "alpine", "id"}, // not in the sudoers file
		{"systemctl", "restart", "ssh.service"},
		{"cat", "/etc/shadow"},
		{"id"},
	}
	for _, argv := range refused {
		res, err := l.run(t, argv, true)
		if err != nil || res.ExitCode == 0 {
			t.Errorf("%q: %+v, %v, want sudo to refuse it", argv, res, err)
		}
	}
}

// What the sudoers file is wider than the table: in sudoers a * also matches spaces and further arguments. The barrier that holds
// is Classify (such a command is never a read, and a change needs an approval; the never-list still applies). This test pins
// that Classify does not call any of them a read, and logs what the real sudo does with them.
func TestLiveWhatSudoAllowsBeyondTheTableIsNeverARead(t *testing.T) {
	l := live(t, "remedy")
	for _, argv := range [][]string{
		{"docker", "logs", "--tail", "5", "other", "remedy-drill"},
		{"docker", "logs", "--tail", "5", "remedy-drill", "other"},
		{"docker", "inspect", "remedy-drill", "other"},
		{"docker", "inspect", "remedy-drill", "--format", "{{.Config.Env}}"},
		{"docker", "restart", "remedy-drill", "other"},
		{"docker", "ps", "-a", "--no-trunc"},
		{"docker", "ps"},
	} {
		v := hostexec.Classify(argv, liveRules)
		if v.Kind == hostexec.Read {
			t.Errorf("%q: Classify says it is a read (%+v)", argv, v)
		}
		res, err := l.run(t, argv, true)
		t.Logf("%q: Classify %v; the real sudo: exit %d, err %v (%s)", argv, v.Kind, res.ExitCode, err, strings.TrimSpace(res.Stderr))
	}
}

// Hostile words stay words through a real sshd and a real bash.
func TestLiveQuotedWordsArriveAsTheyWere(t *testing.T) {
	l := live(t, "remedy")
	words := []string{"a b", "it's", "$(id)", "`id`", `a\b`, "*", "; rm -rf /", "$HOME", "日本語", hostexec.CanaryWant()}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = hostexec.Quote(w)
	}
	res, err := l.svc.Exec(context.Background(), 1, l.target, l.cred, l.pin, `printf '%s\0' `+strings.Join(quoted, " "), sshhost.ExecOptions{
		CanaryCommand: hostexec.CanaryCommand(), CanaryWant: hostexec.CanaryWant(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(res.Stdout, "\x00"), "\x00")
	if len(got) != len(words) {
		t.Fatalf("got %d words, want %d: %q", len(got), len(words), got)
	}
	for i := range words {
		if got[i] != words[i] {
			t.Errorf("word %d: got %q, want %q", i, got[i], words[i])
		}
	}
}

func TestLiveAShellThatIsNotPOSIXFailsTheCanary(t *testing.T) {
	l := live(t, "fishy")
	// The marker is a file the command would create: if anything but the canary reached fish, it could show up.
	_, err := l.run(t, []string{"touch", "/tmp/fishy-was-sent-a-command"}, false)
	if !errors.Is(err, sshhost.ErrShell) {
		t.Fatalf("err = %v, want ErrShell: fish must not be sent a command", err)
	}
	// The same account, with bash, proves that the marker would have been made had the command run.
	b := live(t, "remedy")
	if res, err := b.run(t, []string{"ls", "/tmp/fishy-was-sent-a-command"}, false); err != nil || res.ExitCode == 0 {
		t.Fatalf("the file exists or the check failed: %+v, %v", res, err)
	}
	if res, err := l.svc.Exec(context.Background(), 1, l.target, l.cred, l.pin, "ls /tmp", sshhost.ExecOptions{}); !errors.Is(err, sshhost.ErrShell) {
		t.Fatalf("without a canary: %+v, %v, want ErrShell", res, err)
	}
}

// waitForProcess polls the host (on a connection of its own, so the lock of the host under test is free) until a process whose
// command line contains needle is there (want true) or gone (want false).
func (l *liveHost) waitForProcess(t *testing.T, needle string, want bool) {
	t.Helper()
	var other sshhost.Service
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := other.Exec(context.Background(), 2, l.target, l.cred, l.pin, hostexec.CommandLine([]string{"ps", "-eo", "args"}, false), sshhost.ExecOptions{
			CanaryCommand: hostexec.CanaryCommand(), CanaryWant: hostexec.CanaryWant(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Stdout, needle) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q: running = %v after 10 s, want %v:\n%s", needle, !want, want, res.Stdout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// A command that does not end in time gets SIGTERM, and a real sshd and a real shell then stop it on the host.
func TestLiveATimeoutStopsTheCommandOnTheHost(t *testing.T) {
	l := live(t, "remedy")
	errc := make(chan error, 1)
	go func() {
		_, err := l.runWith(t, []string{"sleep", "37"}, false, sshhost.ExecOptions{Timeout: 4 * time.Second})
		errc <- err
	}()
	l.waitForProcess(t, "sleep 37", true) // it did start: the test below is not vacuous
	if err := <-errc; !errors.Is(err, sshhost.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	l.waitForProcess(t, "sleep 37", false)
}

// A command that is cancelled through its context is stopped the same way.
func TestLiveACancelledContextStopsTheCommandOnTheHost(t *testing.T) {
	l := live(t, "remedy")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		_, err := l.svc.Exec(ctx, 1, l.target, l.cred, l.pin, hostexec.CommandLine([]string{"sleep", "38"}, false), sshhost.ExecOptions{
			CanaryCommand: hostexec.CanaryCommand(), CanaryWant: hostexec.CanaryWant(),
		})
		errc <- err
	}()
	l.waitForProcess(t, "sleep 38", true)
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("want an error")
	} else {
		t.Logf("a cancelled context answers: %v", err)
	}
	l.waitForProcess(t, "sleep 38", false)
}

// The output is bounded per stream, against output the host really produces, and the command still ends.
func TestLiveOutputIsBounded(t *testing.T) {
	l := live(t, "remedy")
	big := `head -c 300000 /dev/zero | tr '\0' x; head -c 300000 /dev/zero | tr '\0' y >&2`
	res, err := l.svc.Exec(context.Background(), 1, l.target, l.cred, l.pin, big, sshhost.ExecOptions{
		Limit: 1000, CanaryCommand: hostexec.CanaryCommand(), CanaryWant: hostexec.CanaryWant(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 1000 || !res.StdoutCut || len(res.Stderr) != 1000 || !res.StderrCut || res.ExitCode != 0 {
		t.Fatalf("stdout %d (cut %v), stderr %d (cut %v), exit %d; want 1000 bytes of each, cut", len(res.Stdout), res.StdoutCut, len(res.Stderr), res.StderrCut, res.ExitCode)
	}
	// The default is 24 KB per stream.
	res, err = l.svc.Exec(context.Background(), 1, l.target, l.cred, l.pin, big, sshhost.ExecOptions{CanaryCommand: hostexec.CanaryCommand(), CanaryWant: hostexec.CanaryWant()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 24<<10 || !res.StdoutCut {
		t.Fatalf("default: stdout %d (cut %v), want 24576, cut", len(res.Stdout), res.StdoutCut)
	}
	// Output that fits is not cut.
	res, err = l.run(t, []string{"uname", "-a"}, false)
	if err != nil || res.StdoutCut || res.StderrCut {
		t.Fatalf("uname: %+v, %v", res, err)
	}
}

// A failing command shows its exit code and its stderr, apart from stdout.
func TestLiveAFailingCommandKeepsItsExitCodeAndStderr(t *testing.T) {
	l := live(t, "remedy")
	res, err := l.run(t, []string{"ls", "/nonexistent-remedy"}, false)
	if err != nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "No such file") || res.Stdout != "" {
		t.Fatalf("%+v, %v", res, err)
	}
}

// The pin: the real sshd's key is the pinned one, a different key or no pin is refused before any login, and the key that Probe
// shows is one of the host's own.
func TestLiveTheHostKeyPin(t *testing.T) {
	l := live(t, "remedy")
	if fp, err := sshhost.FingerprintOfLine(l.key.Line); err != nil || fp != l.key.Fingerprint {
		t.Fatalf("fingerprint of the line = %q, %v; Probe said %q", fp, err, l.key.Fingerprint)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	wrong := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(other)))
	if _, err := l.svc.Check(context.Background(), 1, l.target, l.cred, wrong); !errors.Is(err, sshhost.ErrHostKeyChanged) {
		t.Errorf("a different pinned key: err = %v, want ErrHostKeyChanged", err)
	}
	if _, err := l.svc.Check(context.Background(), 1, l.target, l.cred, ""); !errors.Is(err, sshhost.ErrNoPin) {
		t.Errorf("no pin: err = %v, want ErrNoPin", err)
	}
	if _, err := l.run(t, []string{"uptime"}, false); err != nil {
		t.Errorf("the pinned key: %v", err)
	}
	// The key is one the container's sshd really has (skipped without docker).
	out, err := exec.Command("docker", "exec", "remedy-ssh-testbed", "sh", "-c", "for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $f; done").Output()
	if err != nil {
		t.Logf("docker exec is not available, the fingerprint is not compared: %v", err)
		return
	}
	if !strings.Contains(string(out), l.key.Fingerprint) {
		t.Errorf("Probe showed %s, the container has:\n%s", l.key.Fingerprint, out)
	}
}

// Another account's login (the key is the same for both) works, and a wrong account is refused as a login.
func TestLiveAWrongAccountIsRefused(t *testing.T) {
	l := live(t, "remedy")
	l.target.Account = "nobody-here"
	if _, err := l.run(t, []string{"uptime"}, false); !errors.Is(err, sshhost.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}
