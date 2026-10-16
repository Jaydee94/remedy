package reaper_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
)

func TestTheThresholdFollowsALongRunTimeout(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		10 * time.Second: 15 * time.Minute, 10 * time.Minute: 15 * time.Minute, 11 * time.Minute: 16 * time.Minute, time.Hour: 65 * time.Minute,
	} {
		if got := reaper.MaxAgeFor(in); got != want {
			t.Errorf("MaxAgeFor(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestSweepAsksForTheThresholdAtEverySweep(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	stuck, _ := st.CreateRun(ctx, "claude", "stuck")
	_, _ = st.ClaimNext(ctx)
	now := time.Now()
	maxAge := time.Hour
	r := &reaper.Reaper{Store: st, MaxAgeFunc: func() time.Duration { return maxAge }, Log: quiet, Now: func() time.Time { return now }}

	now = now.Add(30 * time.Minute)
	if n, _ := r.Sweep(ctx); n != 0 {
		t.Fatalf("with a threshold of an hour a run of 30 minutes was failed (%d)", n)
	}
	maxAge = 20 * time.Minute // shortened in the Setup page
	if n, _ := r.Sweep(ctx); n != 1 {
		t.Fatalf("with a threshold of 20 minutes the run of 30 minutes must be failed (%d)", n)
	}
	if got, _ := st.GetRun(ctx, stuck.ID); got.Status != run.Failed {
		t.Fatalf("run = %+v", got)
	}
}
