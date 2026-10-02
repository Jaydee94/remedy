package reaper_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSweepFailsOnlyRunsThatAreRunningForTooLong(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	stuck, _ := st.CreateRun(ctx, "claude", "stuck")
	if c, _ := st.ClaimNext(ctx); c == nil || c.ID != stuck.ID {
		t.Fatalf("ClaimNext = %+v", c)
	}
	done, _ := st.CreateRun(ctx, "claude", "done")
	_, _ = st.ClaimNext(ctx)
	_ = st.FinishRun(ctx, done.ID, run.Outcome{})
	queued, _ := st.CreateRun(ctx, "claude", "queued")

	now := time.Now()
	r := &reaper.Reaper{Store: st, MaxAge: 15 * time.Minute, Log: quiet, Now: func() time.Time { return now }}

	if n, err := r.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("Sweep right after the start = %d, %v; a fresh run must be left alone", n, err)
	}
	now = now.Add(14 * time.Minute)
	if n, _ := r.Sweep(ctx); n != 0 {
		t.Fatalf("Sweep after 14 minutes = %d, want 0", n)
	}

	now = now.Add(2 * time.Minute)
	n, err := r.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Sweep after 16 minutes = %d, %v, want 1", n, err)
	}
	got, _ := st.GetRun(ctx, stuck.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout || got.Result == "" {
		t.Fatalf("stuck run = %+v", got)
	}
	if got, _ := st.GetRun(ctx, done.ID); got.Status != run.Succeeded {
		t.Fatalf("finished run = %+v", got)
	}
	if got, _ := st.GetRun(ctx, queued.ID); got.Status != run.Queued {
		t.Fatalf("queued run = %+v", got)
	}
	if n, _ := r.Sweep(ctx); n != 0 {
		t.Fatalf("a second sweep = %d, want 0 (idempotent)", n)
	}
}

func TestRunSweepsUntilItIsCancelled(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	stuck, _ := st.CreateRun(ctx, "claude", "stuck")
	_, _ = st.ClaimNext(ctx)

	r := &reaper.Reaper{Store: st, MaxAge: time.Millisecond, Interval: 20 * time.Millisecond, Log: quiet}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := st.GetRun(ctx, stuck.ID); got.Status == run.Failed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the reaper did not fail the stuck run")
}
