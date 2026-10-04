package reaper_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func toolRun(t *testing.T) (*store.Store, run.Run) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if _, err := st.CreateToolRun(ctx, "claude", "use the tools"); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return st, *claimed
}

func TestAGatekeeperRunLivesAsLongAsItSendsHeartbeats(t *testing.T) {
	st, r := toolRun(t)
	ctx := context.Background()
	base := time.Now()
	now := base
	rp := &reaper.Reaper{
		Store: st, Log: quiet, MaxAge: 15 * time.Minute, HeartbeatMaxAge: 2 * time.Minute,
		Now: func() time.Time { return now },
	}

	// Half an hour with a heartbeat every minute: far beyond the 15 minute rule, which does not apply to it.
	for i := 1; i <= 30; i++ {
		now = base.Add(time.Duration(i) * time.Minute)
		if _, err := st.Heartbeat(ctx, r.ID, now); err != nil {
			t.Fatal(err)
		}
		if n, err := rp.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("sweep at minute %d = %d, %v, want it left alone", i, n, err)
		}
	}

	// The heartbeats stop.
	now = base.Add(33 * time.Minute)
	if n, err := rp.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep after three silent minutes = %d, %v, want 1", n, err)
	}
	got, _ := st.GetRun(ctx, r.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonRunnerLost {
		t.Fatalf("run = %+v", got)
	}
}

func TestAGatekeeperRunThatNeverBeatsIsLostToo(t *testing.T) {
	st, r := toolRun(t)
	base := time.Now()
	var mu sync.Mutex
	var failed []string
	rp := &reaper.Reaper{
		Store: st, Log: quiet, HeartbeatMaxAge: 2 * time.Minute,
		Now: func() time.Time { return base.Add(3 * time.Minute) }, // counted from its start
		OnFailed: func(_ context.Context, ids []string) {
			mu.Lock()
			defer mu.Unlock()
			failed = append(failed, ids...)
		},
	}
	if n, err := rp.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failed) != 1 || failed[0] != r.ID {
		t.Fatalf("OnFailed got %v, want the lost run", failed)
	}
}
