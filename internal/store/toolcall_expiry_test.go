package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// automaticRun makes a running responder run with automatic = 1 (an alert incident that may be diagnosed on its own).
func automaticRun(t *testing.T, s *store.Store) run.Run {
	t.Helper()
	ctx := context.Background()
	in, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceAlertmanager, Key: "A/" + run.NewID(), Title: "t", Conclusion: "firing", AutoDiagnose: true},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.StartDiagnosis(ctx, store.StartParams{IncidentID: in.ID, Provider: "claude", Prompt: "p", Automatic: true, Limits: store.DefaultLimits(), Now: time.Now()},
		store.NewActivity{Kind: store.KindDiagnosisStarted, Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return *claimed
}

func manualRun(t *testing.T, s *store.Store) run.Run {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateToolRun(ctx, "claude", "q"); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return *claimed
}

func mutating(runID, useID string, in time.Duration) store.NewToolCall {
	return store.NewToolCall{RunID: runID, ToolUseID: useID, Tool: "cluster_rollout_restart", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`), ExpiresIn: in}
}

func TestAnApprovalOfAnAutomaticRunGetsADeadlineAndOtherCallsDoNot(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	auto := automaticRun(t, s)

	c, _, err := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpiresAt == nil {
		t.Fatal("a mutating call of an automatic run has no deadline")
	}
	if d := c.ExpiresAt.Sub(c.CreatedAt.Add(15 * time.Minute)); d < -time.Second || d > time.Second {
		t.Errorf("deadline = %v, created = %v: not 15m apart", c.ExpiresAt, c.CreatedAt)
	}
	// The deadline is stored, not only returned.
	got, err := s.GetToolCall(ctx, c.ID)
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(*c.ExpiresAt) {
		t.Errorf("stored call = %+v, %v", got, err)
	}

	read, _, err := s.BeginToolCall(ctx, store.NewToolCall{RunID: auto.ID, ToolUseID: "u2", Tool: "incident_get", Kind: store.CallKindRead, ExpiresIn: 15 * time.Minute})
	if err != nil || read.ExpiresAt != nil {
		t.Errorf("a read call has a deadline: %+v, %v", read, err)
	}
	none, _, err := s.BeginToolCall(ctx, mutating(auto.ID, "u3", 0))
	if err != nil || none.ExpiresAt != nil {
		t.Errorf("a call with ExpiresIn 0 has a deadline: %+v, %v", none, err)
	}
	neg, _, err := s.BeginToolCall(ctx, mutating(auto.ID, "u4", -time.Minute))
	if err != nil || neg.ExpiresAt != nil {
		t.Errorf("a call with a negative ExpiresIn has a deadline: %+v, %v", neg, err)
	}
}

func TestAnApprovalOfARunThatAClickStartedHasNoDeadline(t *testing.T) {
	s := openStore(t)
	manual := manualRun(t, s)
	c, _, err := s.BeginToolCall(context.Background(), mutating(manual.ID, "u1", 15*time.Minute))
	if err != nil || c.ExpiresAt != nil {
		t.Errorf("a mutating call of a manual run has a deadline: %+v, %v", c, err)
	}
}

func TestAnApprovalExpiresOnlyAfterItsDeadlineAndOnlyOnce(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	auto := automaticRun(t, s)
	c, _, err := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if ok, err := s.ExpireCall(ctx, c.ID, c.CreatedAt); ok || err != nil {
		t.Fatalf("ExpireCall before the deadline = %v, %v", ok, err)
	}
	if got, _ := s.GetToolCall(ctx, c.ID); got.Status != store.CallWaiting || got.Decision != store.DecisionPending || got.Error != "" || got.FinishedAt != nil {
		t.Fatalf("a call that did not expire changed: %+v", got)
	}

	if ok, err := s.ExpireCall(ctx, c.ID, c.CreatedAt.Add(time.Hour)); !ok || err != nil {
		t.Fatalf("ExpireCall after the deadline = %v, %v", ok, err)
	}
	got, err := s.GetToolCall(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.CallAbandoned || got.Decision != store.DecisionAbandoned || got.FinishedAt == nil ||
		!strings.HasPrefix(got.Error, "no decision within 15m") || !strings.Contains(got.Error, "put it in proposed_action") {
		t.Errorf("expired call = %+v", got)
	}

	if ok, err := s.ExpireCall(ctx, c.ID, c.CreatedAt.Add(2*time.Hour)); ok || err != nil {
		t.Errorf("a second ExpireCall = %v, %v", ok, err)
	}
}

func TestTheExpiryIsAnActivityEntryWrittenWithTheChange(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	auto := automaticRun(t, s)
	c, _, err := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	count := func() (int, string) {
		entries, err := s.ListActivity(ctx, store.ActivityQuery{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		n, summary := 0, ""
		for _, e := range entries {
			if e.Kind == store.KindApprovalExpired {
				n++
				summary = e.Summary
				if e.RunID != auto.ID {
					t.Errorf("entry run = %q, want %q", e.RunID, auto.ID)
				}
			}
		}
		return n, summary
	}

	if _, err := s.ExpireCall(ctx, c.ID, c.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if n, _ := count(); n != 0 {
		t.Fatalf("%d expiry entries before the deadline", n)
	}
	if ok, err := s.ExpireCall(ctx, c.ID, c.CreatedAt.Add(time.Hour)); !ok || err != nil {
		t.Fatal(ok, err)
	}
	n, summary := count()
	if n != 1 || !strings.Contains(summary, "cluster_rollout_restart") || !strings.Contains(summary, "15m") {
		t.Errorf("entries = %d, summary = %q", n, summary)
	}
	if _, err := s.ExpireCall(ctx, c.ID, c.CreatedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := count(); n != 1 {
		t.Errorf("%d expiry entries after a second call, want 1", n)
	}
}

func TestADecisionAndTheExpiryHaveExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	late := func(c store.ToolCall) time.Time { return c.CreatedAt.Add(time.Hour) }

	t.Run("approve first", func(t *testing.T) {
		s := openStore(t)
		auto := automaticRun(t, s)
		c, _, _ := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
		if _, err := s.DecideApproval(ctx, c.ID, true, ""); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.ExpireCall(ctx, c.ID, late(c)); ok || err != nil {
			t.Fatalf("ExpireCall of an approved call = %v, %v", ok, err)
		}
		got, _ := s.GetToolCall(ctx, c.ID)
		if got.Status != store.CallWaiting || got.Decision != store.DecisionApproved {
			t.Fatalf("approved call after the expiry attempt = %+v", got)
		}
		if _, won, err := s.BeginExecution(ctx, c.ID); !won || err != nil {
			t.Errorf("BeginExecution = %v, %v, want a win", won, err)
		}
	})
	t.Run("expire first", func(t *testing.T) {
		s := openStore(t)
		auto := automaticRun(t, s)
		c, _, _ := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
		if ok, err := s.ExpireCall(ctx, c.ID, late(c)); !ok || err != nil {
			t.Fatal(ok, err)
		}
		if _, err := s.DecideApproval(ctx, c.ID, true, ""); !errors.Is(err, store.ErrNotPending) {
			t.Errorf("DecideApproval after the expiry = %v, want ErrNotPending", err)
		}
		if _, won, _ := s.BeginExecution(ctx, c.ID); won {
			t.Error("an expired call can be executed")
		}
	})
	t.Run("deny first", func(t *testing.T) {
		s := openStore(t)
		auto := automaticRun(t, s)
		c, _, _ := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
		if _, err := s.DecideApproval(ctx, c.ID, false, "no"); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.ExpireCall(ctx, c.ID, late(c)); ok || err != nil {
			t.Fatalf("ExpireCall of a denied call = %v, %v", ok, err)
		}
		if got, _ := s.GetToolCall(ctx, c.ID); got.Status != store.CallDenied || got.Decision != store.DecisionDenied {
			t.Errorf("denied call after the expiry attempt = %+v", got)
		}
	})
}

func TestAnExpiredCallIsNotInThePendingListAndTheRunKeepsRunning(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	auto := automaticRun(t, s)
	c, _, _ := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 15*time.Minute))
	if pending, err := s.ListApprovals(ctx, store.ApprovalFilter{PendingOnly: true}); err != nil || len(pending) != 1 {
		t.Fatalf("pending before = %d, %v", len(pending), err)
	}
	if ok, err := s.ExpireCall(ctx, c.ID, c.CreatedAt.Add(time.Hour)); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if pending, err := s.ListApprovals(ctx, store.ApprovalFilter{PendingOnly: true}); err != nil || len(pending) != 0 {
		t.Errorf("pending after = %d, %v", len(pending), err)
	}
	if r, err := s.GetRun(ctx, auto.ID); err != nil || r.Status != run.Running {
		t.Errorf("run after the expiry = %+v, %v", r.Status, err)
	}
}

func TestExpiryOfAnUnknownCallIsFalse(t *testing.T) {
	s := openStore(t)
	if ok, err := s.ExpireCall(context.Background(), 999, time.Now()); ok || err != nil {
		t.Errorf("ExpireCall of an unknown call = %v, %v", ok, err)
	}
}

func TestExpiryOfACallWithoutADeadlineIsFalse(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	manual := manualRun(t, s)
	c, _, _ := s.BeginToolCall(ctx, mutating(manual.ID, "u1", 15*time.Minute))
	if ok, err := s.ExpireCall(ctx, c.ID, c.CreatedAt.Add(24*time.Hour)); ok || err != nil {
		t.Errorf("ExpireCall of a call without a deadline = %v, %v", ok, err)
	}
	if got, _ := s.GetToolCall(ctx, c.ID); got.Status != store.CallWaiting || got.Decision != store.DecisionPending {
		t.Errorf("call = %+v", got)
	}
}
