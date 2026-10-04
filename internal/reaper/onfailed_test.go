package reaper_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
)

func TestOnFailedReceivesTheIDsOfTheFailedRuns(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	a, _ := st.CreateRun(ctx, "claude", "a")
	if c, _ := st.ClaimNext(ctx); c == nil || c.ID != a.ID {
		t.Fatalf("ClaimNext = %+v", c)
	}
	b, _ := st.CreateRun(ctx, "claude", "b")
	_, _ = st.ClaimNext(ctx)

	var got []string
	now := time.Now().Add(time.Hour)
	r := &reaper.Reaper{
		Store: st, MaxAge: 15 * time.Minute, Log: quiet, Now: func() time.Time { return now },
		OnFailed: func(_ context.Context, ids []string) { got = append(got, ids...) },
	}
	if n, err := r.Sweep(ctx); err != nil || n != 2 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	slices.Sort(got)
	want := []string{a.ID, b.ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("OnFailed got %v, want %v", got, want)
	}

	got = nil
	if n, _ := r.Sweep(ctx); n != 0 || got != nil {
		t.Fatalf("a sweep that fails nothing called OnFailed: %d, %v", n, got)
	}
}
