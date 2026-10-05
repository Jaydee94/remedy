package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestCreateQuestionRunLinksTheIncidentAndGivesTools(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")

	q, err := s.CreateQuestionRun(ctx, "claude", "why did it fail twice?", in.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if q.Role != run.RoleAdhoc || q.Status != run.Queued || !q.MCP || q.Cluster || q.Prompt != "why did it fail twice?" ||
		q.IncidentID == nil || *q.IncidentID != in.ID || q.Automatic {
		t.Fatalf("run = %+v", q)
	}

	c, err := s.CreateQuestionRun(ctx, "claude", "and in the cluster?", in.ID, true)
	if err != nil || !c.Cluster || !c.MCP {
		t.Fatalf("cluster run = %+v, %v", c, err)
	}

	if _, err := s.CreateQuestionRun(ctx, "claude", "x", 9999, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a question about an unknown incident: error = %v, want ErrNotFound", err)
	}
	if runs, _ := s.ListRuns(ctx, 50); len(runs) != 2 {
		t.Fatalf("%d runs, want 2: the refused question must not leave a run", len(runs))
	}
}

func TestAQuestionRunDoesNotCountAsAnAutomaticDiagnosis(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	if _, err := s.CreateQuestionRun(ctx, "claude", "why?", in.ID, false); err != nil {
		t.Fatal(err)
	}
	if n, err := s.AutoRunsSince(ctx, t0.Add(-24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("AutoRunsSince = %d, %v, want 0", n, err)
	}
}

func TestListIncidentRunsReturnsTheRunsOfOneIncidentNewestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	other := open(t, s, repo, "pr:8", "go", "bbb", "failure")

	long := strings.Repeat("x", 1000)
	responder, err := s.StartDiagnosis(ctx, store.StartParams{
		IncidentID: in.ID, Provider: "claude", Prompt: long, HeadSHA: in.HeadSHA, Limits: limits, Now: t0,
	}, store.NewActivity{Kind: store.KindDiagnosisStarted, RepoID: in.RepoID, Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, responder.ID, run.Outcome{ExitCode: 0})
	question := strings.Repeat("q", 600)
	q, err := s.CreateQuestionRun(ctx, "claude", question, in.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateQuestionRun(ctx, "claude", "about the other one", other.ID, false); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListIncidentRuns(ctx, in.ID, 50)
	if err != nil || len(got) != 2 || got[0].ID != q.ID || got[1].ID != responder.ID {
		t.Fatalf("runs = %+v, %v, want the question, then the responder run", got, err)
	}
	if got[0].Prompt != question {
		t.Errorf("the question was cut to %d characters, want 600: it is the maintainer's own text", len(got[0].Prompt))
	}
	if len(got[1].Prompt) != 300 {
		t.Errorf("the responder prompt has %d characters, want 300: it can be 200 KB", len(got[1].Prompt))
	}

	none, err := s.ListIncidentRuns(ctx, 9999, 50)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("runs of an unknown incident = %#v, %v, want an empty, non-nil list", none, err)
	}
}
