package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestFailStaleRunsOnlyTouchesRunsThatStartedBeforeTheCutoff(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	stuck, _ := s.CreateRun(ctx, "claude", "stuck")
	if c, _ := s.ClaimNext(ctx); c == nil || c.ID != stuck.ID {
		t.Fatalf("ClaimNext = %+v", c)
	}
	done, _ := s.CreateRun(ctx, "claude", "done")
	_, _ = s.ClaimNext(ctx)
	if err := s.FinishRun(ctx, done.ID, run.Outcome{Result: "fine"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := s.CreateRun(ctx, "claude", "queued")

	ids, err := s.FailStaleRuns(ctx, time.Now().Add(-time.Hour), "gone")
	if err != nil || len(ids) != 0 {
		t.Fatalf("before the cutoff: ids = %v, err = %v", ids, err)
	}

	ids, err = s.FailStaleRuns(ctx, time.Now().Add(time.Minute), "gone")
	if err != nil || len(ids) != 1 || ids[0] != stuck.ID {
		t.Fatalf("ids = %v, err = %v, want only the running run", ids, err)
	}

	got, _ := s.GetRun(ctx, stuck.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout || got.Result != "gone" ||
		got.ExitCode == nil || *got.ExitCode != -1 || got.FinishedAt == nil {
		t.Fatalf("stuck run = %+v", got)
	}
	if got, _ := s.GetRun(ctx, done.ID); got.Status != run.Succeeded || got.Result != "fine" {
		t.Fatalf("finished run = %+v", got)
	}
	if got, _ := s.GetRun(ctx, queued.ID); got.Status != run.Queued {
		t.Fatalf("queued run = %+v", got)
	}
}
