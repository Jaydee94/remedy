package sshhost_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

const (
	canaryCmd  = "printf %s 'a b'"
	canaryWant = "a b"
)

func execOpts() sshhost.ExecOptions {
	return sshhost.ExecOptions{Limit: 1000, Timeout: 2 * time.Second, CanaryCommand: canaryCmd, CanaryWant: canaryWant}
}

func TestExecRunsTheCanaryAndThenTheCommandWithTheStreamsApart(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant},
		"the cmd": {Out: "out\n", Stderr: "err\n", Exit: 3},
	}})
	var svc sshhost.Service
	got, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv), "the cmd", execOpts())
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "out\n" || got.Stderr != "err\n" || got.ExitCode != 3 || got.StdoutCut || got.StderrCut {
		t.Fatalf("result = %+v", got)
	}
	if seen := srv.Seen(); !reflect.DeepEqual(seen, []string{canaryCmd, "the cmd"}) {
		t.Fatalf("the server received %q, want the canary and then the command", seen)
	}
}

func TestExecRefusesAShellThatFailsTheCanary(t *testing.T) {
	for name, reply := range map[string]sshtest.Reply{
		"another output": {Out: "something else"},
		"no output":      {},
		"a failing exit": {Out: canaryWant, Exit: 1},
		"an error":       {Stderr: "fish: unknown command", Exit: 127},
	} {
		t.Run(name, func(t *testing.T) {
			srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{canaryCmd: reply, "the cmd": {Out: "ran"}}})
			var svc sshhost.Service
			_, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv), "the cmd", execOpts())
			if !errors.Is(err, sshhost.ErrShell) {
				t.Fatalf("err = %v, want ErrShell", err)
			}
			if seen := srv.Seen(); !reflect.DeepEqual(seen, []string{canaryCmd}) {
				t.Fatalf("the server received %q: the command must not be sent to a shell that failed the canary", seen)
			}
		})
	}
}

func TestExecCutsEachStreamAtTheLimit(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant},
		"noisy":   {Out: strings.Repeat("o", 5000), Stderr: strings.Repeat("e", 5000)},
		"exact":   {Out: strings.Repeat("o", 1000)},
	}})
	var svc sshhost.Service
	cred, pin := passwordCred(t, "pw"), lineOf(t, srv)
	got, err := svc.Exec(context.Background(), 1, targetOf(srv), cred, pin, "noisy", execOpts())
	if err != nil || len(got.Stdout) != 1000 || len(got.Stderr) != 1000 || !got.StdoutCut || !got.StderrCut {
		t.Fatalf("noisy: stdout %d (cut %v), stderr %d (cut %v), err %v", len(got.Stdout), got.StdoutCut, len(got.Stderr), got.StderrCut, err)
	}
	got, err = svc.Exec(context.Background(), 1, targetOf(srv), cred, pin, "exact", execOpts())
	if err != nil || len(got.Stdout) != 1000 || got.StdoutCut {
		t.Fatalf("exactly at the limit: stdout %d (cut %v), err %v", len(got.Stdout), got.StdoutCut, err)
	}
}

func TestExecKeepsTwentyFourKilobytesPerStreamWhenNoLimitIsGiven(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant},
		"noisy":   {Out: strings.Repeat("o", 40<<10), Stderr: strings.Repeat("e", 40<<10)},
	}})
	var svc sshhost.Service
	o := sshhost.ExecOptions{CanaryCommand: canaryCmd, CanaryWant: canaryWant}
	got, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv), "noisy", o)
	if err != nil || len(got.Stdout) != 24<<10 || len(got.Stderr) != 24<<10 || !got.StdoutCut || !got.StderrCut {
		t.Fatalf("stdout %d (cut %v), stderr %d (cut %v), err %v, want 24576 bytes each, both cut", len(got.Stdout), got.StdoutCut, len(got.Stderr), got.StderrCut, err)
	}
}

func TestExecTimesOutOnTheCommandAndNotOnTheCanary(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant}, "slow": {Out: "late", Delay: 2 * time.Second},
	}})
	var svc sshhost.Service
	o := execOpts()
	o.Timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv), "slow", o); !errors.Is(err, sshhost.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the timeout took %v", time.Since(start))
	}
}

func TestExecWithoutAPinSendsNothingAndAFailedLoginNeitherRunsAnything(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{canaryCmd: {Out: canaryWant}, "the cmd": {Out: "ran"}}})
	var svc sshhost.Service
	if _, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), "", "the cmd", execOpts()); !errors.Is(err, sshhost.ErrNoPin) {
		t.Fatalf("without a pin: err = %v, want ErrNoPin", err)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host whose key is not pinned")
	}
	if _, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "wrong"), lineOf(t, srv), "the cmd", execOpts()); !errors.Is(err, sshhost.ErrAuth) {
		t.Fatalf("a wrong password: err = %v, want ErrAuth", err)
	}
	if len(srv.Seen()) != 0 {
		t.Fatalf("the server received %q after a failed login", srv.Seen())
	}
}

func TestExecsOfOneHostDoNotOverlap(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant}, "work": {Out: "done", Delay: 100 * time.Millisecond},
	}})
	var svc sshhost.Service
	cred, pin := passwordCred(t, "pw"), lineOf(t, srv) // made here: t.Fatal must not be called from the goroutines below
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Exec(context.Background(), 5, targetOf(srv), cred, pin, "work", execOpts()); err != nil {
				t.Errorf("Exec: %v", err)
			}
		}()
	}
	wg.Wait()
	if srv.MaxConcurrent() != 1 {
		t.Fatalf("%d exec sessions on one host were served at the same time, want 1", srv.MaxConcurrent())
	}
}

func TestExecWithoutACanaryIsRefusedBeforeAnyLogin(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{canaryCmd: {Out: canaryWant}, "the cmd": {Out: "ran"}}})
	var svc sshhost.Service
	cred, pin := passwordCred(t, "pw"), lineOf(t, srv)
	for name, o := range map[string]sshhost.ExecOptions{
		"neither":          {Limit: 1000, Timeout: time.Second},
		"only the command": {Limit: 1000, Timeout: time.Second, CanaryCommand: canaryCmd},
		"only the answer":  {Limit: 1000, Timeout: time.Second, CanaryWant: canaryWant},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Exec(context.Background(), 1, targetOf(srv), cred, pin, "the cmd", o); !errors.Is(err, sshhost.ErrShell) {
				t.Fatalf("err = %v, want ErrShell", err)
			}
			if srv.Attempts() != 0 || len(srv.Seen()) != 0 {
				t.Fatalf("a host was contacted: %d logins, received %q", srv.Attempts(), srv.Seen())
			}
		})
	}
}

// waitForTerm waits for the server to see exactly one TERM signal request.
func waitForTerm(t *testing.T, srv *sshtest.Server, why string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if reflect.DeepEqual(srv.Signals(), []string{"TERM"}) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the server saw the signals %q, want a TERM %s", srv.Signals(), why)
}

func TestExecAsksTheRemoteCommandToStopWhenItTimesOut(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant}, "slow": {Out: "late", Delay: 2 * time.Second},
	}})
	var svc sshhost.Service
	o := execOpts()
	o.Timeout = 200 * time.Millisecond
	if _, err := svc.Exec(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv), "slow", o); !errors.Is(err, sshhost.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	waitForTerm(t, srv, "after the timeout")
}

func TestExecAsksTheRemoteCommandToStopWhenTheContextEnds(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{
		canaryCmd: {Out: canaryWant}, "slow": {Out: "late", Delay: 2 * time.Second},
	}})
	var svc sshhost.Service
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, err := svc.Exec(ctx, 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv), "slow", execOpts()); !errors.Is(err, sshhost.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	waitForTerm(t, srv, "after the context ended")
}

func TestRunSplitUsesTheDefaultsForAZeroLimitAndTimeout(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"quick": {Out: "out", Stderr: "err"}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	got, err := cl.RunSplit(context.Background(), "quick", 0, 0)
	if err != nil || got.Stdout != "out" || got.Stderr != "err" || got.StdoutCut || got.StderrCut {
		t.Fatalf("result = %+v, err = %v: a zero limit must not keep nothing and a zero timeout must not fire at once", got, err)
	}
}
