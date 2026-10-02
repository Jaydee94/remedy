package runner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestARunThatTakesTooLongIsStopped(t *testing.T) {
	st, _ := startLoopWithTimeout(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.SlowClaude(t)},
	}, 500*time.Millisecond)

	r, err := st.CreateRun(context.Background(), "claude", "hang")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got := waitTerminal(t, st, r.ID)

	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout {
		t.Fatalf("run = %+v, want failed with reason timeout", got)
	}
	if got.ExitCode == nil || *got.ExitCode == 0 || !strings.Contains(got.Result, "stopped") {
		t.Fatalf("exit code = %v, result = %q", got.ExitCode, got.Result)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("the run took %s to stop; the sleep must have been left running", elapsed)
	}
}

func TestARunThatFinishesInTimeIsNotMarkedAsTimedOut(t *testing.T) {
	st, _ := startLoopWithTimeout(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.FakeClaude(t, 0)},
	}, 30*time.Second)

	r, _ := st.CreateRun(context.Background(), "claude", "quick")
	got := waitTerminal(t, st, r.ID)
	if got.Status != run.Succeeded || got.FailureReason != "" {
		t.Fatalf("run = %+v", got)
	}
}
