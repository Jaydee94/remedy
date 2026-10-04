package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestHeartbeatSaysWhetherTheRunWaitsOrWasCancelled(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)

	beat, err := s.Heartbeat(ctx, r.ID, time.Now())
	if err != nil || beat.Waiting || beat.Cancel {
		t.Fatalf("a quiet run: %+v, %v", beat, err)
	}

	first := waitingCall(t, s, r, "w1")
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); !beat.Waiting {
		t.Fatalf("a run with a pending approval: %+v, want Waiting", beat)
	}
	if _, err := s.DecideApproval(ctx, first.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); beat.Waiting {
		t.Fatalf("after the decision: %+v, want it no longer waiting", beat)
	}

	// An approved call that has not run yet is not waiting for the maintainer any more.
	second := waitingCall(t, s, r, "w2")
	if _, err := s.DecideApproval(ctx, second.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); beat.Waiting {
		t.Fatalf("an approved call: %+v, want it no longer waiting", beat)
	}

	waitingCall(t, s, r, "w3")
	if _, err := s.CancelRun(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); !beat.Cancel || beat.Waiting {
		t.Fatalf("a cancelled run: %+v, want Cancel and no waiting (its call was abandoned)", beat)
	}
}

func TestHeartbeatOfARunThatIsNotRunning(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	queued, _ := s.CreateToolRun(ctx, "claude", "x")
	if _, err := s.Heartbeat(ctx, queued.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a queued run: %v, want ErrNotFound", err)
	}
	if _, err := s.Heartbeat(ctx, "no-such-run", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown run: %v, want ErrNotFound", err)
	}
	claimed, _ := s.ClaimNext(ctx)
	if err := s.FinishRun(ctx, claimed.ID, run.Outcome{Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(ctx, claimed.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a finished run: %v, want ErrNotFound", err)
	}
}

func TestFailLostRunsFailsAGatekeeperRunThatStoppedBeating(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	token, err := s.MintRunToken(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := waitingCall(t, s, r, "w")
	base := time.Now()

	// It started just now: not lost.
	if ids, err := s.FailLostRuns(ctx, base.Add(-time.Minute), "lost"); err != nil || len(ids) != 0 {
		t.Fatalf("a fresh run: %v, %v", ids, err)
	}
	// A heartbeat later than the cutoff keeps it alive, whatever its start was.
	if _, err := s.Heartbeat(ctx, r.ID, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.FailLostRuns(ctx, base.Add(time.Hour), "lost"); len(ids) != 0 {
		t.Fatalf("a run that heartbeats: %v, want none", ids)
	}

	ids, err := s.FailLostRuns(ctx, base.Add(3*time.Hour), "the runner is gone")
	if err != nil || len(ids) != 1 || ids[0] != r.ID {
		t.Fatalf("a silent run: %v, %v", ids, err)
	}
	got, _ := s.GetRun(ctx, r.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonRunnerLost || got.Result != "the runner is gone" || got.FinishedAt == nil {
		t.Fatalf("lost run = %+v", got)
	}
	if _, err := s.RunForToken(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the token of a lost run: %v", err)
	}
	if c, _ := s.GetToolCall(ctx, call.ID); c.Status != store.CallAbandoned {
		t.Fatalf("the waiting call of a lost run = %+v", c)
	}
}

func TestOnlyTheHeartbeatRuleAppliesToAGatekeeperRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	tool := claimedToolRun(t, s)
	if _, err := s.CreateRun(ctx, "claude", "plain"); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.ClaimNext(ctx)
	future := time.Now().Add(10 * time.Hour)

	lost, err := s.FailLostRuns(ctx, future, "lost")
	if err != nil || len(lost) != 1 || lost[0] != tool.ID {
		t.Fatalf("FailLostRuns = %v, %v, want only the gatekeeper run", lost, err)
	}

	// The stale rule is the other way round.
	again, _ := s.CreateToolRun(ctx, "claude", "again")
	_, _ = s.ClaimNext(ctx)
	stale, err := s.FailStaleRuns(ctx, future, "stale")
	if err != nil || len(stale) != 1 || stale[0] != plain.ID {
		t.Fatalf("FailStaleRuns = %v, %v, want only the plain run (not %s)", stale, err, again.ID)
	}
}
