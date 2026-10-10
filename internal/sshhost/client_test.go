package sshhost_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

func targetOf(s *sshtest.Server) sshhost.Target {
	return sshhost.Target{Address: s.Host, Port: s.Port, Account: "remedy"}
}

func lineOf(t *testing.T, s *sshtest.Server) string {
	t.Helper()
	k, err := sshhost.Probe(context.Background(), targetOf(s))
	if err != nil {
		t.Fatal(err)
	}
	return k.Line
}

func passwordCred(t *testing.T, pw string) sshhost.Credential {
	t.Helper()
	c, err := sshhost.NewPasswordCredential(pw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProbeReturnsTheKeyAndAuthenticatesNothing(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	k, err := sshhost.Probe(context.Background(), targetOf(srv))
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ssh-ed25519" || !strings.HasPrefix(k.Fingerprint, "SHA256:") || !strings.HasPrefix(k.Line, "ssh-ed25519 ") {
		t.Fatalf("host key = %+v", k)
	}
	if got, err := sshhost.FingerprintOfLine(k.Line); err != nil || got != k.Fingerprint {
		t.Fatalf("FingerprintOfLine = %q, %v", got, err)
	}
	if srv.Attempts() != 0 {
		t.Fatalf("Probe made %d authentication attempts, want none", srv.Attempts())
	}
}

func TestDialRunsACommandWithAPassword(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"id -un": {Out: "remedy\n"}, "false": {Exit: 3}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cl.Close()
	got, err := cl.Run(context.Background(), "id -un")
	if err != nil || got.Output != "remedy\n" || got.ExitCode != 0 || got.Truncated {
		t.Fatalf("Run = %+v, %v", got, err)
	}
	if got, err := cl.Run(context.Background(), "false"); err != nil || got.ExitCode != 3 {
		t.Fatalf("a failing command = %+v, %v", got, err)
	}
}

func TestDialRunsACommandWithAKey(t *testing.T) {
	pem, pub := sshtest.KeyPEM(t, "pp")
	srv := sshtest.Start(t, sshtest.Config{Key: pub, Commands: map[string]sshtest.Reply{"id -un": {Out: "remedy\n"}}})
	cred, _, err := sshhost.NewKeyCredential(pem, "pp")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), cred, lineOf(t, srv))
	if err != nil {
		t.Fatalf("Dial with a key: %v", err)
	}
	defer cl.Close()
	if got, err := cl.Run(context.Background(), "id -un"); err != nil || got.Output != "remedy\n" {
		t.Fatalf("Run = %+v, %v", got, err)
	}
}

func TestDialWithoutAPinIsRefused(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), ""); !errors.Is(err, sshhost.ErrNoPin) {
		t.Fatalf("err = %v, want ErrNoPin", err)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host whose key is not pinned")
	}
}

func TestDialRefusesAChangedKey(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	pin := lineOf(t, srv)
	srv.SetHostSigner(sshtest.NewSigner(t))
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), pin); !errors.Is(err, sshhost.ErrHostKeyChanged) {
		t.Fatalf("err = %v, want ErrHostKeyChanged", err)
	}
	if srv.Attempts() != 0 {
		t.Fatal("a login was offered to a host that showed another key")
	}
}

func TestDialRefusesAKeyOfAnotherType(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	pin := lineOf(t, srv) // ed25519
	srv.SetHostSigner(sshtest.NewRSASigner(t))
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), pin); !errors.Is(err, sshhost.ErrHostKeyChanged) {
		t.Fatalf("err = %v, want ErrHostKeyChanged (not unreachable)", err)
	}
}

func TestDialRejectsAWrongPassword(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw"})
	if _, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "nope"), lineOf(t, srv)); !errors.Is(err, sshhost.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestRefusedConnection(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{})
	srv.Close()
	if _, err := sshhost.Probe(context.Background(), targetOf(srv)); !errors.Is(err, sshhost.ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}

func TestRunCutsLongOutput(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"big": {Out: strings.Repeat("x", 20000)}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	got, err := cl.Run(context.Background(), "big")
	if err != nil || len(got.Output) != sshhost.MaxOutput || !got.Truncated {
		t.Fatalf("Run: len %d, truncated %v, err %v", len(got.Output), got.Truncated, err)
	}
}

func TestRunTimesOut(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: map[string]sshtest.Reply{"slow": {Out: "late", Delay: 2 * time.Second}}})
	cl, err := sshhost.Dial(context.Background(), targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	defer sshhost.SetCommandTimeout(200 * time.Millisecond)()
	start := time.Now()
	if _, err := cl.Run(context.Background(), "slow"); !errors.Is(err, sshhost.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the timeout took %v", time.Since(start))
	}
}

func okCommands() map[string]sshtest.Reply {
	return map[string]sshtest.Reply{
		"id -un":                  {Out: "remedy\n"},
		"id -Gn":                  {Out: "remedy users\n"},
		"uname -sr":               {Out: "Linux 6.8.0\n"},
		"env LC_ALL=C sudo -n -l": {Out: "User remedy may run the following commands on nas:\n    (root) NOPASSWD: /usr/bin/docker ps -a\n"},
	}
}

func TestCheckBuildsTheReport(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: okCommands()})
	var svc sshhost.Service
	rep, err := svc.Check(context.Background(), 1, targetOf(srv), passwordCred(t, "pw"), lineOf(t, srv))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rep.Account != "remedy" || rep.System != "Linux 6.8.0" || rep.Sudo != sshhost.SudoLimited || len(rep.SudoRules) != 1 || len(rep.Warnings) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestChecksOfOneHostDoNotOverlap(t *testing.T) {
	cmds := okCommands()
	cmds["id -un"] = sshtest.Reply{Out: "remedy\n", Delay: 150 * time.Millisecond}
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: cmds})
	pin := lineOf(t, srv)
	cred := passwordCred(t, "pw") // made here: t.Fatal must not be called from the goroutines below
	var svc sshhost.Service
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Check(context.Background(), 7, targetOf(srv), cred, pin); err != nil {
				t.Errorf("Check: %v", err)
			}
		}()
	}
	wg.Wait()
	if srv.MaxConcurrent() != 1 {
		t.Fatalf("%d exec sessions on one host were served at the same time, want 1", srv.MaxConcurrent())
	}
}
