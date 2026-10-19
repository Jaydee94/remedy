package signals_test

import (
	"context"
	"errors"
	"testing"
)

// The hook runs after every cycle of a source that is on, also after a failed fetch (an incident opened earlier may be due), and never
// when the source is off or the context has ended.
func TestAfterCycleRunsForASourceThatIsOnEvenWhenItsFetchFailedAndNeverForOneThatIsOff(t *testing.T) {
	g := newRig(t)
	calls := 0
	g.r.AfterCycle = func(context.Context) { calls++ }
	g.cycle(sig("A/1", "critical", true, nil))
	if calls != 1 {
		t.Fatalf("hook ran %d times after a good cycle, want 1", calls)
	}
	g.src.mu.Lock()
	g.src.err = errors.New("alertmanager: HTTP 500")
	g.src.mu.Unlock()
	g.r.RunOnce(context.Background())
	if calls != 2 {
		t.Fatalf("hook ran %d times after a failed fetch, want 2", calls)
	}
	g.src.mu.Lock()
	g.src.on = false
	g.src.mu.Unlock()
	g.r.RunOnce(context.Background())
	if calls != 2 {
		t.Fatalf("hook ran %d times with the source off, want 2", calls)
	}
	g.src.mu.Lock()
	g.src.on = true
	g.src.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g.r.RunOnce(ctx)
	if calls != 2 {
		t.Fatalf("hook ran %d times with an ended context, want 2", calls)
	}
}
