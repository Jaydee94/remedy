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

func TestVersionDoesNotPassTheAPIKeyOn(t *testing.T) {
	script := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\nif [ -n \"$ANTHROPIC_API_KEY\" ]; then echo leaked; exit 0; fi\necho \"2.1.288 (Claude Code)\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	got := provider.Claude{Binary: script}.Version(context.Background(), []string{"PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=sk-secret"})
	if got != "2.1.288 (Claude Code)" {
		t.Fatalf("version = %q: the API key reached the CLI", got)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersionIsNothingButAVersion(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin"}
	cases := map[string]string{
		"an e-mail":                  `echo "someone@example.invalid"`,
		"a banner":                   `echo "Claude Code v2.1.288"`,
		"a version with an e-mail":   `echo "2.1.288 someone@example.invalid"`,
		"a version with a long tail": `echo "2.1.288 (Claude Code) ` + strings.Repeat("x", 80) + `"`,
	}
	for name, body := range cases {
		if got := (provider.Claude{Binary: writeScript(t, body)}).Version(context.Background(), env); got != "" {
			t.Errorf("%s: version = %q, want empty", name, got)
		}
	}
	if got := (provider.Claude{Binary: writeScript(t, `echo "2.1.288 (Claude Code)"`)}).Version(context.Background(), env); got != "2.1.288 (Claude Code)" {
		t.Errorf("version = %q", got)
	}
}

func TestLoginCheckSurvivesAChildThatHoldsTheOutput(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin"}
	claude := provider.Claude{Binary: testutil.FakeClaudeLogin(t, "lingering")}
	started := time.Now()
	state, reason := claude.LoginCheck(context.Background(), env)
	if state != provider.LoginOK {
		t.Errorf("state = %q (%q), want ok: the CLI exited 0", state, reason)
	}
	if got := claude.Version(context.Background(), env); got != "2.1.288 (Claude Code)" {
		t.Errorf("version = %q, want the line the CLI printed before it left a child behind", got)
	}
	if time.Since(started) > 10*time.Second {
		t.Errorf("the checks took %s", time.Since(started))
	}
}

func TestLoginCheckSaysWhenASignalStoppedTheCLI(t *testing.T) {
	state, reason := provider.Claude{Binary: testutil.FakeClaudeLogin(t, "killed")}.LoginCheck(context.Background(), []string{"PATH=/usr/bin:/bin"})
	if state != provider.LoginUnknown || reason != "the login check was stopped by a signal" {
		t.Errorf("state %q reason %q, want unknown and the signal reason", state, reason)
	}
}
