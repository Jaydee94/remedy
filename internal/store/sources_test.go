package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func openAlert(t *testing.T, s *store.Store, key string, auto bool) store.Incident {
	t.Helper()
	in, err := s.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceAlertmanager, Key: key, Title: "alert " + key, Severity: "warning", AutoDiagnose: auto, Conclusion: "firing",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened " + key})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// An incident of another source has no repository. Everything that used to read the repository of an incident must cope
// with that: a diagnosis, a note, the history.
func TestAnIncidentOfAnotherSourceNeedsNoRepository(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := openAlert(t, s, "a/1", false)

	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatalf("StartDiagnosis of an alert: %v", err)
	}
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 0})
	if err := s.CompleteDiagnosis(ctx, r.ID, json.RawMessage(`{"summary":"s"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, Summary: "done"}); err != nil {
		t.Fatalf("CompleteDiagnosis: %v", err)
	}
	if err := s.AddNote(ctx, in.ID, r.ID, "looked at it"); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	got, err := s.GetIncident(ctx, in.ID)
	if err != nil || got.State != store.IncDiagnosed || got.RepoID != 0 || got.RepoName != "" {
		t.Fatalf("incident = %+v, %v", got, err)
	}
	log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if err != nil || len(log) != 4 { // opened, started, finished, note
		t.Fatalf("activity = %d entries, %v, want 4", len(log), err)
	}
	for _, a := range log {
		if a.RepoID != 0 || a.RepoName != "" {
			t.Errorf("an entry of an incident without a repository names one: %+v", a)
		}
	}
	if err := s.ResolveIncident(ctx, in.ID, "cleared", entry(store.KindIncidentResolved, 0)); err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}
}

func TestTheResponderStartsOnItsOwnOnlyForGitHubIncidentsThatAreMarkedForIt(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	red := open(t, s, repo, "pr:1", "go", "aaa", "failure")
	open(t, s, repo, "pr:2", "go", "aaa", "cancelled")
	openAlert(t, s, "a/1", true) // marked for it, but the responder does not handle this source yet (plan 2d-3)

	got, err := s.ListAutoCandidates(ctx, t0, limits)
	if err != nil || len(got) != 1 || got[0].ID != red.ID {
		t.Fatalf("candidates = %+v, %v, want only incident %d", got, err, red.ID)
	}

	// The mark decides an automatic start, also for a source other than GitHub.
	unmarked := openAlert(t, s, "a/2", false)
	_, err = start(s, unmarked, true, limits, t0)
	var limit *store.LimitError
	if !errors.As(err, &limit) {
		t.Fatalf("an automatic start of an incident that is not marked: %v, want a LimitError", err)
	}
	if _, err := start(s, openAlert(t, s, "a/3", true), true, limits, t0); err != nil {
		t.Fatalf("an automatic start of a marked incident: %v", err)
	}
}

// A GitHub incident is marked from its conclusion when it opens, and again when a new commit changes the conclusion.
func TestAGitHubIncidentIsMarkedForTheResponderByItsConclusion(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:3", "go", "aaa", "cancelled")
	if in.AutoDiagnose {
		t.Fatal("a cancelled check is marked for the responder")
	}
	for _, step := range []struct {
		conclusion string
		want       bool
	}{{"failure", true}, {"action_required", false}, {"timed_out", true}, {"startup_failure", true}, {"cancelled", false}} {
		if err := s.RecordRecurrence(ctx, in.ID, step.conclusion, "sha-"+step.conclusion, "", entry(store.KindIncidentRecurred, repo.ID)); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetIncident(ctx, in.ID)
		if got.AutoDiagnose != step.want || got.Conclusion != step.conclusion {
			t.Errorf("after %s: conclusion %q, marked %v, want %v", step.conclusion, got.Conclusion, got.AutoDiagnose, step.want)
		}
	}
}
