package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func mustIgnore(t *testing.T, s *store.Store, in store.Incident) {
	t.Helper()
	if err := s.IgnoreIncident(context.Background(), in.ID, entry(store.KindIncidentIgnored, in.RepoID)); err != nil {
		t.Fatal(err)
	}
}

func unignoreIncident(s *store.Store, in store.Incident) error {
	return s.UnignoreIncident(context.Background(), in.ID, entry(store.KindIncidentUnignored, in.RepoID))
}

func mustState(t *testing.T, s *store.Store, in store.Incident) store.IncidentState {
	t.Helper()
	got, err := s.GetIncident(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.State
}

func TestUnignoreReopensAnIncidentThatWasNeverDiagnosed(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	mustIgnore(t, s, in)

	if err := unignoreIncident(s, in); err != nil {
		t.Fatal(err)
	}
	if got := mustState(t, s, in); got != store.IncOpen {
		t.Fatalf("state = %q, want open", got)
	}
	log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if err != nil || len(log) == 0 || log[0].Kind != store.KindIncidentUnignored {
		t.Fatalf("activity = %+v, %v: the newest entry must be the un-ignore", log, err)
	}
	if err := unignoreIncident(s, in); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("un-ignoring an open incident: error = %v, want ErrNotFound", err)
	}
	if err := unignoreIncident(s, store.Incident{ID: 9999}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("un-ignoring an unknown incident: error = %v, want ErrNotFound", err)
	}
}

func TestUnignoreRestoresADiagnosedIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 0})
	if err := s.CompleteDiagnosis(ctx, r.ID, json.RawMessage(`{"summary":"s"}`), entry(store.KindDiagnosisFinished, repo.ID)); err != nil {
		t.Fatal(err)
	}
	mustIgnore(t, s, in)

	if err := unignoreIncident(s, in); err != nil {
		t.Fatal(err)
	}
	if got := mustState(t, s, in); got != store.IncDiagnosed {
		t.Fatalf("state = %q, want diagnosed", got)
	}
}

func TestUnignoreOfAnIncidentThatIsBeingDiagnosedGoesBackToDiagnosing(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	if _, err := start(s, in, false, limits, t0); err != nil {
		t.Fatal(err)
	}
	mustIgnore(t, s, in)
	if got := mustState(t, s, in); got != store.IncIgnored {
		t.Fatalf("state = %q, want ignored", got)
	}

	if err := unignoreIncident(s, in); err != nil {
		t.Fatal(err)
	}
	if got := mustState(t, s, in); got != store.IncDiagnosing {
		t.Fatalf("state = %q, want diagnosing: its run is still queued", got)
	}
}

func TestADiagnosisThatFinishesWhileTheIncidentIsIgnoredDoesNotLiftIt(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	mustIgnore(t, s, in)
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 0})
	if err := s.CompleteDiagnosis(ctx, r.ID, json.RawMessage(`{"summary":"s"}`), entry(store.KindDiagnosisFinished, repo.ID)); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncIgnored || len(got.Diagnosis) == 0 {
		t.Fatalf("incident = state %q, diagnosis %q: want ignored, with the diagnosis kept", got.State, got.Diagnosis)
	}
	if err := unignoreIncident(s, in); err != nil {
		t.Fatal(err)
	}
	if got := mustState(t, s, in); got != store.IncDiagnosed {
		t.Fatalf("state = %q, want diagnosed", got)
	}
}

func TestADiagnosisThatFailsWhileTheIncidentIsIgnoredDoesNotLiftIt(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	mustIgnore(t, s, in)
	failRun(t, s, r)

	if got := mustState(t, s, in); got != store.IncIgnored {
		t.Fatalf("state = %q, want ignored", got)
	}
	if err := unignoreIncident(s, in); err != nil {
		t.Fatal(err)
	}
	if got := mustState(t, s, in); got != store.IncOpen {
		t.Fatalf("state = %q, want open: the run left no diagnosis", got)
	}
}

func TestAnIncidentThatTurnedGreenWhileIgnoredCannotBeUnignored(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	mustIgnore(t, s, in)
	if err := s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID)); err != nil {
		t.Fatal(err)
	}

	if err := unignoreIncident(s, in); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("un-ignoring a resolved incident: error = %v, want ErrNotFound", err)
	}
	if got := mustState(t, s, in); got != store.IncResolved {
		t.Fatalf("state = %q, want resolved", got)
	}
}
