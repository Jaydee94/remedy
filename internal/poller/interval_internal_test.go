package poller

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestPauseIsTheIntervalWhileItIsShortAndAShortTickWhileItIsLong(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		20 * time.Millisecond: 20 * time.Millisecond, 3 * time.Second: 3 * time.Second, time.Minute: pollTick, time.Hour: pollTick,
	} {
		if got := pause(in); got != want {
			t.Errorf("pause(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestRunAsksForTheIntervalAgainAndStopsPollingWhenItGrows(t *testing.T) {
	st := testutil.Store(t)

	var interval atomic.Int64
	interval.Store(int64(20 * time.Millisecond))
	var cycles atomic.Int64
	p := &Poller{
		Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IntervalFunc: func() time.Duration { return time.Duration(interval.Load()) },
		Now:          func() time.Time { cycles.Add(1); return time.Now() }, // PollOnce asks for the time once per cycle
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for cycles.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cycles.Load() < 4 {
		t.Fatalf("only %d cycles with an interval of 20ms", cycles.Load())
	}
	interval.Store(int64(time.Hour))
	time.Sleep(60 * time.Millisecond) // a cycle that was already due may still run
	settled := cycles.Load()
	time.Sleep(300 * time.Millisecond)
	if cycles.Load() != settled {
		t.Fatalf("cycles went from %d to %d after the interval grew to an hour", settled, cycles.Load())
	}
}
