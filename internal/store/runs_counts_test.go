package store_test

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestRunCounts(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if running, queued, err := s.RunCounts(ctx); err != nil || running != 0 || queued != 0 {
		t.Fatalf("empty store: RunCounts = (%d, %d, %v), want (0, 0, nil)", running, queued, err)
	}
	for range 3 {
		if _, err := s.CreateRun(ctx, "claude", "p"); err != nil {
			t.Fatal(err)
		}
	}
	finished, err := s.ClaimNext(ctx)
	if err != nil || finished == nil {
		t.Fatalf("ClaimNext = %v, %v", finished, err)
	}
	if err := s.FinishRun(ctx, finished.ID, run.Outcome{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ClaimNext(ctx); err != nil || r == nil {
		t.Fatalf("ClaimNext = %v, %v", r, err)
	}
	running, queued, err := s.RunCounts(ctx)
	if err != nil || running != 1 || queued != 1 {
		t.Fatalf("RunCounts = (%d, %d, %v), want (1, 1, nil)", running, queued, err)
	}
}
