package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestNewRunsAreAdhocWithoutAnIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, err := s.CreateRun(ctx, "claude", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleAdhoc || r.IncidentID != nil || r.Output != nil || r.FailureReason != "" {
		t.Fatalf("run = %+v", r)
	}
}

func TestCreateIncidentRunLinksTheIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}

	r, err := s.CreateIncidentRun(ctx, in.ID, "claude", "diagnose it")
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleResponder || r.IncidentID == nil || *r.IncidentID != in.ID || r.Status != run.Queued || r.Prompt != "diagnose it" {
		t.Fatalf("run = %+v", r)
	}
	got, _ := s.GetRun(ctx, r.ID)
	if got.Role != run.RoleResponder || got.IncidentID == nil || *got.IncidentID != in.ID {
		t.Fatalf("stored run = %+v", got)
	}

	if _, err := s.CreateIncidentRun(ctx, 9999, "claude", "x"); err == nil {
		t.Fatal("a run for an unknown incident must be refused")
	}
}

func TestDeletingTheIncidentKeepsItsRuns(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	r, _ := s.CreateIncidentRun(ctx, in.ID, "claude", "diagnose it")

	if err := s.DeleteRepo(ctx, repo.ID); err != nil { // cascades to the incident
		t.Fatal(err)
	}
	got, err := s.GetRun(ctx, r.ID)
	if err != nil || got.IncidentID != nil || got.Role != run.RoleResponder {
		t.Fatalf("run after the incident was deleted = %+v, %v", got, err)
	}
}

func TestFinishRunStoresTheOutputAndTheFailureReason(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	finish := func(prompt string, o run.Outcome) run.Run {
		t.Helper()
		r, err := s.CreateRun(ctx, "claude", prompt)
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := s.ClaimNext(ctx); err != nil || claimed == nil || claimed.ID != r.ID {
			t.Fatalf("ClaimNext = %+v, %v", claimed, err)
		}
		if err := s.FinishRun(ctx, r.ID, o); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetRun(ctx, r.ID)
		return got
	}

	withOutput := finish("one", run.Outcome{Result: "done", Output: json.RawMessage(`{"summary":"x"}`)})
	if string(withOutput.Output) != `{"summary":"x"}` || withOutput.Status != run.Succeeded || withOutput.FailureReason != "" {
		t.Fatalf("run with output = %+v", withOutput)
	}

	timedOut := finish("two", run.Outcome{ExitCode: -1, FailureReason: run.ReasonTimeout})
	if timedOut.Status != run.Failed || timedOut.FailureReason != "timeout" || timedOut.Output != nil {
		t.Fatalf("timed out run = %+v", timedOut)
	}

	withNull := finish("three", run.Outcome{Output: json.RawMessage(`null`)})
	if withNull.Output != nil {
		t.Fatalf("an output of null must be stored as none, got %s", withNull.Output)
	}

	if err := s.FinishRun(ctx, withOutput.ID, run.Outcome{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("finishing a finished run: error = %v", err)
	}
}
