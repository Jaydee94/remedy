package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestACallWithoutAnIncidentTakesTheOneOfItsRun(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	auto := automaticRun(t, s)
	r, err := s.GetRun(ctx, auto.ID)
	if err != nil || r.IncidentID == nil {
		t.Fatalf("run = %+v, %v", r, err)
	}

	c, _, err := s.BeginToolCall(ctx, mutating(auto.ID, "u1", 0))
	if err != nil {
		t.Fatal(err)
	}
	if c.IncidentID != *r.IncidentID {
		t.Fatalf("call.IncidentID = %d, the run's incident is %d", c.IncidentID, *r.IncidentID)
	}
	got, err := s.GetToolCall(ctx, c.ID)
	if err != nil || got.IncidentID != *r.IncidentID {
		t.Fatalf("stored call = %+v, %v", got, err)
	}

	acts, err := s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.Kind == store.KindApprovalRequested {
			found = true
			if a.IncidentID != *r.IncidentID {
				t.Errorf("approval_requested carries incident %d, want %d", a.IncidentID, *r.IncidentID)
			}
		}
	}
	if !found {
		t.Fatal("no approval_requested entry")
	}
}

func TestACallThatNamesAnIncidentKeepsIt(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	auto := automaticRun(t, s)
	other, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceAlertmanager, Key: "other", Title: "other", Conclusion: "firing"},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	n := mutating(auto.ID, "u1", 0)
	n.IncidentID = other.ID
	c, _, err := s.BeginToolCall(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if c.IncidentID != other.ID {
		t.Fatalf("call.IncidentID = %d, want %d (the one it names)", c.IncidentID, other.ID)
	}
}

func TestACallOfARunWithoutAnIncidentHasNone(t *testing.T) {
	s := openStore(t)
	manual := manualRun(t, s)
	c, _, err := s.BeginToolCall(context.Background(), mutating(manual.ID, "u1", 0))
	if err != nil {
		t.Fatal(err)
	}
	if c.IncidentID != 0 {
		t.Fatalf("call.IncidentID = %d, want 0", c.IncidentID)
	}
}

func TestAnIncidentThatWasDeletedIsNotAnError(t *testing.T) {
	s, dsn := openStoreAt(t)
	ctx := context.Background()
	auto := automaticRun(t, s)
	r, err := s.GetRun(ctx, auto.ID)
	if err != nil || r.IncidentID == nil {
		t.Fatalf("run = %+v, %v", r, err)
	}

	sqlOn(t, dsn, `DELETE FROM incidents WHERE id = ?`, *r.IncidentID)

	c, _, err := s.BeginToolCall(ctx, store.NewToolCall{RunID: auto.ID, ToolUseID: "u1", Tool: "cluster_rollout_restart", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("BeginToolCall: %v", err)
	}
	if c.IncidentID != 0 {
		t.Fatalf("call.IncidentID = %d, want 0 for a deleted incident", c.IncidentID)
	}
}
