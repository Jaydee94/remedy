package runner_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// initThenWait is a CLI that says how it is billed and then keeps running for ten seconds, so that a guard that works
// shows as a run that ends long before that. The sleep replaces the shell (exec): a child would keep the output pipe open
// after the shell is killed, and the runner reads until the pipe closes.
func initThenWait(t *testing.T, line string) string {
	return testutil.ScriptClaude(t, "cat >/dev/null\necho '"+line+"'\nexec sleep 10\necho '{\"type\":\"result\",\"result\":\"done\",\"total_cost_usd\":0.5}'")
}

func executeWithGuard(t *testing.T, mode provider.Mode, line string) (run.Outcome, time.Duration) {
	t.Helper()
	spec := provider.Spec{Prompt: "p", Workdir: t.TempDir(), OnInit: func(source string, known bool) bool { return mode.Accepts(source, known) }}
	start := time.Now()
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: initThenWait(t, line), Mode: mode}, spec, os.Environ(), &recordingSink{})
	if err != nil {
		t.Fatal(err)
	}
	return out, time.Since(start)
}

func TestAKeyBilledRunIsStoppedInSubscriptionMode(t *testing.T) {
	out, took := executeWithGuard(t, provider.ModeSubscription, `{"type":"system","subtype":"init","apiKeySource":"ANTHROPIC_API_KEY"}`)
	if out.FailureReason != run.ReasonBillingMismatch || out.ExitCode == 0 {
		t.Fatalf("outcome = %+v, want a failure with reason %s", out, run.ReasonBillingMismatch)
	}
	if took > 5*time.Second {
		t.Errorf("the run took %s: the guard did not stop it before the CLI's ten seconds were up", took)
	}
	if out.Result != "The run was stopped: it would have been billed differently than configured." {
		t.Errorf("Result = %q, want the fixed sentence", out.Result)
	}
	if out.AuthSource != "api_key" {
		t.Errorf("AuthSource = %q, want api_key (what the CLI said it would use)", out.AuthSource)
	}
}

func TestASubscriptionRunIsStoppedInKeyMode(t *testing.T) {
	for name, line := range map[string]string{
		"none":     `{"type":"system","subtype":"init","apiKeySource":"none"}`,
		"no field": `{"type":"system","subtype":"init"}`,
	} {
		out, took := executeWithGuard(t, provider.ModeAPIKey, line)
		if out.FailureReason != run.ReasonBillingMismatch || took > 5*time.Second {
			t.Errorf("%s: outcome = %+v after %s, want billing_mismatch quickly", name, out, took)
		}
	}
}

func TestARunBilledAsConfiguredIsNotStopped(t *testing.T) {
	bin := testutil.ScriptClaude(t, "cat >/dev/null\necho '{\"type\":\"system\",\"subtype\":\"init\",\"apiKeySource\":\"none\"}'\necho '{\"type\":\"result\",\"result\":\"done\",\"total_cost_usd\":0.5}'")
	var asked int
	spec := provider.Spec{Prompt: "p", Workdir: t.TempDir(), OnInit: func(string, bool) bool { asked++; return provider.ModeSubscription.Accepts("none", true) }}
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: bin}, spec, os.Environ(), &recordingSink{})
	if err != nil {
		t.Fatal(err)
	}
	if out.FailureReason != "" || out.ExitCode != 0 || out.Result != "done" || out.AuthSource != "subscription" || asked != 1 {
		t.Errorf("outcome = %+v, asked %d times", out, asked)
	}
}

func TestAnInitWithoutTheFieldRunsInSubscriptionModeAndIsRecordedEmpty(t *testing.T) {
	bin := testutil.ScriptClaude(t, "cat >/dev/null\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\necho '{\"type\":\"result\",\"result\":\"done\"}'")
	spec := provider.Spec{Prompt: "p", Workdir: t.TempDir(), OnInit: func(s string, k bool) bool { return provider.ModeSubscription.Accepts(s, k) }}
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: bin}, spec, os.Environ(), &recordingSink{})
	if err != nil || out.FailureReason != "" || out.AuthSource != "" || !strings.Contains(out.Result, "done") {
		t.Errorf("outcome = %+v, err %v", out, err)
	}
}

// Through the loop and the control plane: the run ends failed with the reason, and the finish request is accepted.
func TestAMismatchedRunFailsWithItsReasonAtTheControlPlane(t *testing.T) {
	bin := initThenWait(t, `{"type":"system","subtype":"init","apiKeySource":"ANTHROPIC_API_KEY"}`)
	st, _ := startLoop(t, map[string]provider.Provider{"claude": provider.Claude{Binary: bin}})
	queued, err := st.CreateRun(context.Background(), "claude", "hello")
	if err != nil {
		t.Fatal(err)
	}
	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonBillingMismatch {
		t.Fatalf("run = status %s, reason %q, want failed with %s", got.Status, got.FailureReason, run.ReasonBillingMismatch)
	}
}
