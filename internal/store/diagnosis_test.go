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

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

var limits = store.DiagnosisLimits{Cooldown: 15 * time.Minute, MaxPerIncident: 2, MaxPerDay: 3}

func open(t *testing.T, s *store.Store, repo store.Repo, ref, check, sha, conclusion string) store.Incident {
	t.Helper()
	n := failing(repo.ID, ref, check, sha)
	n.Conclusion = conclusion
	in, err := s.OpenIncident(context.Background(), n, entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func start(s *store.Store, in store.Incident, auto bool, l store.DiagnosisLimits, now time.Time) (run.Run, error) {
	return s.StartDiagnosis(context.Background(), store.StartParams{
		IncidentID: in.ID, Provider: "claude", Prompt: "diagnose " + in.CheckName, HeadSHA: in.HeadSHA,
		Automatic: auto, Limits: l, Now: now,
	}, store.NewActivity{Kind: store.KindDiagnosisStarted, RepoID: in.RepoID, Summary: "started"})
}

// finishRun claims the oldest queued run, which must be id, and finishes it.
func finishRun(t *testing.T, s *store.Store, id string, o run.Outcome) {
	t.Helper()
	ctx := context.Background()
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != id {
		t.Fatalf("ClaimNext = %+v, %v, want run %s", claimed, err, id)
	}
	if err := s.FinishRun(ctx, id, o); err != nil {
		t.Fatal(err)
	}
}

// failRun finishes a started run as failed and closes the diagnosis, like the control plane does.
func failRun(t *testing.T, s *store.Store, r run.Run) {
	t.Helper()
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 1})
	if err := s.FailDiagnosis(context.Background(), r.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "failed"}); err != nil {
		t.Fatal(err)
	}
}

func TestStartDiagnosisCreatesTheRunAndMarksTheIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")

	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleResponder || r.Status != run.Queued || r.IncidentID == nil || *r.IncidentID != in.ID ||
		r.HeadSHA != "aaa" || r.Automatic || r.Prompt != "diagnose go" {
		t.Fatalf("run = %+v", r)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncDiagnosing || got.RunID != r.ID || got.LastDiagnosisAt == nil || !got.LastDiagnosisAt.Equal(t0) || got.Diagnoses != 0 {
		t.Fatalf("incident = %+v: a manual start must not count against the cap", got)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 2 || log[0].Kind != store.KindDiagnosisStarted || log[0].RunID != r.ID || log[0].IncidentID != in.ID {
		t.Fatalf("activity = %+v", log)
	}
}

func TestAnAutomaticStartCountsAndIsMarked(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")

	r, err := start(s, in, true, limits, t0)
	if err != nil || !r.Automatic {
		t.Fatalf("run = %+v, err = %v", r, err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.Diagnoses != 1 {
		t.Fatalf("diagnoses = %d, want 1", got.Diagnoses)
	}
}

func TestOnlyOneRunAtATime(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	a := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	b := open(t, s, repo, "pr:8", "go", "bbb", "failure")

	first, err := start(s, a, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := start(s, b, false, limits, t0); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("a queued run: error = %v, want ErrBusy", err)
	}
	if _, err := s.ClaimNext(ctx); err != nil { // now it is running
		t.Fatal(err)
	}
	if _, err := start(s, b, false, limits, t0); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("a running run: error = %v, want ErrBusy", err)
	}
	if busy, _ := s.HasActiveRun(ctx); !busy {
		t.Fatal("HasActiveRun = false")
	}
	if err := s.FinishRun(ctx, first.ID, run.Outcome{}); err != nil {
		t.Fatal(err)
	}
	if busy, _ := s.HasActiveRun(ctx); busy {
		t.Fatal("HasActiveRun = true after the run finished")
	}
	if _, err := start(s, b, false, limits, t0); err != nil {
		t.Fatalf("after the run finished: %v", err)
	}

	// A hand-started run blocks as well.
	other := open(t, s, repo, "pr:9", "go", "ccc", "failure")
	finishRun(t, s, mustRun(t, s, b), run.Outcome{})
	if _, err := s.CreateRun(ctx, "claude", "adhoc"); err != nil {
		t.Fatal(err)
	}
	if _, err := start(s, other, false, limits, t0); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("an ad-hoc run: error = %v, want ErrBusy", err)
	}
}

// mustRun returns the latest run of an incident.
func mustRun(t *testing.T, s *store.Store, in store.Incident) string {
	t.Helper()
	got, err := s.GetIncident(context.Background(), in.ID)
	if err != nil || got.RunID == "" {
		t.Fatalf("incident = %+v, %v", got, err)
	}
	return got.RunID
}

func TestAnIncidentMustBeOpenOrDiagnosedToBeDiagnosed(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	resolved := open(t, s, repo, "pr:1", "go", "aaa", "failure")
	_ = s.ResolveIncident(ctx, resolved.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	ignored := open(t, s, repo, "pr:2", "go", "aaa", "failure")
	_ = s.IgnoreIncident(ctx, ignored.ID, entry(store.KindIncidentIgnored, repo.ID))
	running := open(t, s, repo, "pr:3", "go", "aaa", "failure")
	r, _ := start(s, running, false, limits, t0)
	finishRun(t, s, r.ID, run.Outcome{})

	for name, in := range map[string]store.Incident{"resolved": resolved, "ignored": ignored, "already diagnosing": running} {
		if _, err := start(s, in, false, limits, t0); !errors.Is(err, store.ErrNotDiagnosable) {
			t.Errorf("%s: error = %v, want ErrNotDiagnosable", name, err)
		}
	}
	if _, err := start(s, store.Incident{ID: 9999}, false, limits, t0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown incident: error = %v, want ErrNotFound", err)
	}
}

func TestAutomaticStartsRespectTheLimits(t *testing.T) {
	t.Run("cooldown", func(t *testing.T) {
		s := openStore(t)
		in := open(t, s, seedRepo(t, s), "pr:7", "go", "aaa", "failure")
		r, _ := start(s, in, true, limits, t0)
		failRun(t, s, r)

		_, err := start(s, in, true, limits, t0.Add(10*time.Minute))
		var le *store.LimitError
		if !errors.Is(err, store.ErrLimit) || !errors.As(err, &le) || !strings.Contains(le.Reason, "cooldown") {
			t.Fatalf("within the cooldown: error = %v", err)
		}
		if _, err := start(s, in, true, limits, t0.Add(16*time.Minute)); err != nil {
			t.Fatalf("after the cooldown: %v", err)
		}
	})

	t.Run("cap per incident", func(t *testing.T) {
		s := openStore(t)
		in := open(t, s, seedRepo(t, s), "pr:7", "go", "aaa", "failure")
		for i := 0; i < 2; i++ {
			r, err := start(s, in, true, limits, t0.Add(time.Duration(i)*20*time.Minute))
			if err != nil {
				t.Fatalf("start %d: %v", i, err)
			}
			failRun(t, s, r)
		}
		if _, err := start(s, in, true, limits, t0.Add(2*time.Hour)); !errors.Is(err, store.ErrLimit) {
			t.Fatalf("a third automatic diagnosis: error = %v, want ErrLimit", err)
		}
		// A person can always ask again.
		if _, err := start(s, in, false, limits, t0.Add(2*time.Hour)); err != nil {
			t.Fatalf("a manual diagnosis after the cap: %v", err)
		}
	})

	t.Run("per rolling 24 hours", func(t *testing.T) {
		s := openStore(t)
		repo := seedRepo(t, s)
		for i := 0; i < 3; i++ {
			in := open(t, s, repo, "pr:"+string(rune('1'+i)), "go", "aaa", "failure")
			r, err := start(s, in, true, limits, t0.Add(time.Duration(i)*time.Minute))
			if err != nil {
				t.Fatalf("start %d: %v", i, err)
			}
			failRun(t, s, r)
		}
		fourth := open(t, s, repo, "pr:9", "go", "aaa", "failure")
		if _, err := start(s, fourth, true, limits, t0.Add(time.Hour)); !errors.Is(err, store.ErrLimit) {
			t.Fatalf("the fourth automatic run in a day: error = %v, want ErrLimit", err)
		}
		if _, err := start(s, fourth, true, limits, t0.Add(25*time.Hour)); err != nil {
			t.Fatalf("a day later: %v", err)
		}
	})

	t.Run("a daily limit of zero turns automatic diagnosis off", func(t *testing.T) {
		s := openStore(t)
		in := open(t, s, seedRepo(t, s), "pr:7", "go", "aaa", "failure")
		off := store.DiagnosisLimits{Cooldown: time.Minute, MaxPerIncident: 3, MaxPerDay: 0}
		if _, err := start(s, in, true, off, t0); !errors.Is(err, store.ErrLimit) {
			t.Fatalf("error = %v, want ErrLimit", err)
		}
	})

	t.Run("only real failures are diagnosed automatically", func(t *testing.T) {
		s := openStore(t)
		repo := seedRepo(t, s)
		for _, c := range []string{"cancelled", "action_required"} {
			in := open(t, s, repo, "pr:"+c, "go", "aaa", c)
			if _, err := start(s, in, true, limits, t0); !errors.Is(err, store.ErrLimit) {
				t.Errorf("%s: error = %v, want ErrLimit", c, err)
			}
			r, err := start(s, in, false, limits, t0)
			if err != nil {
				t.Fatalf("%s manually: %v", c, err)
			}
			failRun(t, s, r)
		}
		for _, c := range []string{"failure", "timed_out", "startup_failure"} {
			in := open(t, s, repo, "pr:"+c, "go", "aaa", c)
			r, err := start(s, in, true, store.DiagnosisLimits{Cooldown: time.Minute, MaxPerIncident: 3, MaxPerDay: 10}, t0)
			if err != nil {
				t.Errorf("%s: %v", c, err)
				continue
			}
			failRun(t, s, r)
		}
	})
}

func TestADiagnosedIncidentIsDiagnosedAgainOnlyForANewCommit(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, _ := start(s, in, true, limits, t0)
	finishRun(t, s, r.ID, run.Outcome{})
	if err := s.CompleteDiagnosis(ctx, r.ID, []byte(`{"summary":"s"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, Summary: "done"}); err != nil {
		t.Fatal(err)
	}

	later := t0.Add(time.Hour)
	if _, err := start(s, in, true, limits, later); !errors.Is(err, store.ErrLimit) {
		t.Fatalf("the same commit again: error = %v, want ErrLimit", err)
	}
	// A new red commit makes it eligible again.
	if err := s.RecordRecurrence(ctx, in.ID, "failure", "bbb", "", entry(store.KindIncidentRecurred, repo.ID)); err != nil {
		t.Fatal(err)
	}
	fresh, _ := s.GetIncident(ctx, in.ID)
	if _, err := start(s, fresh, true, limits, later); err != nil {
		t.Fatalf("a new commit: %v", err)
	}
}

func TestCompleteDiagnosisStoresItOnTheIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, _ := start(s, in, false, limits, t0)
	finishRun(t, s, r.ID, run.Outcome{})

	err := s.CompleteDiagnosis(ctx, r.ID, []byte(`{"summary":"the lock file is stale"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, RepoID: repo.ID, Summary: "diagnosed"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncDiagnosed || string(got.Diagnosis) != `{"summary":"the lock file is stale"}` || got.DiagnosedSHA != "aaa" || got.RunID != r.ID {
		t.Fatalf("incident = %+v", got)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if log[0].Kind != store.KindDiagnosisFinished || log[0].IncidentID != in.ID {
		t.Fatalf("activity = %+v", log)
	}
}

func TestCompleteDiagnosisKeepsAnIgnoredOrResolvedState(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, _ := start(s, in, false, limits, t0)
	if err := s.IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID)); err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, r.ID, run.Outcome{})

	if err := s.CompleteDiagnosis(ctx, r.ID, []byte(`{"summary":"s"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, Summary: "d"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncIgnored || string(got.Diagnosis) != `{"summary":"s"}` {
		t.Fatalf("incident = %+v: the diagnosis is kept, the state is not touched", got)
	}
}

func TestOnlyTheLatestRunOfAnIncidentCanCloseItsDiagnosis(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	first, _ := start(s, in, false, limits, t0)
	finishRun(t, s, first.ID, run.Outcome{ExitCode: 1})
	_ = s.FailDiagnosis(ctx, first.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "x"})
	second, _ := start(s, in, false, limits, t0.Add(time.Minute))

	if err := s.CompleteDiagnosis(ctx, first.ID, []byte(`{"summary":"stale"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, Summary: "d"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a stale run completed the diagnosis: %v", err)
	}
	if err := s.FailDiagnosis(ctx, first.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a stale run failed the diagnosis: %v", err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.State != store.IncDiagnosing || got.RunID != second.ID {
		t.Fatalf("incident = %+v", got)
	}
}

func TestFailDiagnosisReopensTheIncidentOrFallsBackToItsOldDiagnosis(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)

	fresh := open(t, s, repo, "pr:1", "go", "aaa", "failure")
	r, _ := start(s, fresh, false, limits, t0)
	failRun(t, s, r)
	if got, _ := s.GetIncident(ctx, fresh.ID); got.State != store.IncOpen {
		t.Fatalf("an incident without a diagnosis is %q after a failed run, want open", got.State)
	}

	old := open(t, s, repo, "pr:2", "go", "bbb", "failure")
	r1, _ := start(s, old, false, limits, t0)
	finishRun(t, s, r1.ID, run.Outcome{})
	_ = s.CompleteDiagnosis(ctx, r1.ID, []byte(`{"summary":"first"}`), store.NewActivity{Kind: store.KindDiagnosisFinished, Summary: "d"})
	r2, err := start(s, old, false, limits, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failRun(t, s, r2)
	got, _ := s.GetIncident(ctx, old.ID)
	if got.State != store.IncDiagnosed || string(got.Diagnosis) != `{"summary":"first"}` {
		t.Fatalf("incident = %+v: a failed repeat keeps the earlier diagnosis", got)
	}
}

// DiagnosedAt is when the stored diagnosis was written. Starting a diagnosis and failing one move LastDiagnosisAt only, so a
// failed repeat leaves the old diagnosis with its old time; a new diagnosis moves it.
func TestDiagnosedAtFollowsTheStoredDiagnosisNotTheAttempts(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	diag := func(summary string) []byte { return []byte(`{"summary":"` + summary + `"}`) }
	done := store.NewActivity{Kind: store.KindDiagnosisFinished, RepoID: repo.ID, Summary: "diagnosed"}
	get := func() store.Incident {
		t.Helper()
		got, err := s.GetIncident(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := get(); got.DiagnosedAt != nil {
		t.Fatalf("a new incident has DiagnosedAt = %v, want nil", got.DiagnosedAt)
	}

	// The first diagnosis. Starting does not set it, completing does, to about now.
	r1, _ := start(s, in, false, limits, t0)
	if got := get(); got.DiagnosedAt != nil {
		t.Fatalf("a started diagnosis set DiagnosedAt = %v", got.DiagnosedAt)
	}
	finishRun(t, s, r1.ID, run.Outcome{})
	before := time.Now().Add(-time.Second)
	if err := s.CompleteDiagnosis(ctx, r1.ID, diag("first"), done); err != nil {
		t.Fatal(err)
	}
	first := get()
	if first.DiagnosedAt == nil || first.DiagnosedAt.Before(before) || first.DiagnosedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("DiagnosedAt = %v after CompleteDiagnosis, want about now", first.DiagnosedAt)
	}
	if first.LastDiagnosisAt == nil || !first.LastDiagnosisAt.Equal(t0) {
		t.Fatalf("LastDiagnosisAt = %v, want the start %v", first.LastDiagnosisAt, t0)
	}

	// A repeat that is started: diagnosing, the old DiagnosedAt, a newer LastDiagnosisAt. One that fails changes neither.
	later := time.Now().Add(time.Hour)
	r2, err := start(s, in, false, limits, later)
	if err != nil {
		t.Fatal(err)
	}
	got := get()
	if got.State != store.IncDiagnosing || got.DiagnosedAt == nil || !got.DiagnosedAt.Equal(*first.DiagnosedAt) ||
		got.LastDiagnosisAt == nil || !got.LastDiagnosisAt.Equal(later) {
		t.Fatalf("after a second start: %+v, want diagnosing, the old DiagnosedAt %v, LastDiagnosisAt %v", got, first.DiagnosedAt, later)
	}
	failRun(t, s, r2)
	got = get()
	if got.State != store.IncDiagnosed || got.DiagnosedAt == nil || !got.DiagnosedAt.Equal(*first.DiagnosedAt) ||
		got.LastDiagnosisAt == nil || !got.LastDiagnosisAt.Equal(later) || string(got.Diagnosis) != string(diag("first")) {
		t.Fatalf("after a failed repeat: %+v, want diagnosed with the old diagnosis and the old DiagnosedAt %v", got, first.DiagnosedAt)
	}

	// A repeat that succeeds moves DiagnosedAt forward. Timestamps have nine digits, so a short wait is enough.
	time.Sleep(5 * time.Millisecond)
	r3, err := start(s, in, false, limits, later.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, r3.ID, run.Outcome{})
	if err := s.CompleteDiagnosis(ctx, r3.ID, diag("second"), done); err != nil {
		t.Fatal(err)
	}
	got = get()
	if got.DiagnosedAt == nil || !got.DiagnosedAt.After(*first.DiagnosedAt) || string(got.Diagnosis) != string(diag("second")) {
		t.Fatalf("after a second diagnosis: DiagnosedAt = %v, want after %v", got.DiagnosedAt, first.DiagnosedAt)
	}
}

func TestListAutoCandidates(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	off, err := s.AddRepo(ctx, store.ConnectionID, "octo/off", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoEnabled(ctx, off.ID, false); err != nil {
		t.Fatal(err)
	}

	eligible := open(t, s, repo, "pr:1", "go", "aaa", "failure")
	timedOut := open(t, s, repo, "pr:2", "go", "aaa", "timed_out")
	open(t, s, repo, "pr:3", "go", "aaa", "cancelled")
	ignored := open(t, s, repo, "pr:4", "go", "aaa", "failure")
	_ = s.IgnoreIncident(ctx, ignored.ID, entry(store.KindIncidentIgnored, repo.ID))
	resolved := open(t, s, repo, "pr:5", "go", "aaa", "failure")
	_ = s.ResolveIncident(ctx, resolved.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	open(t, s, off, "pr:6", "go", "aaa", "failure")

	capped := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	for i := 0; i < 2; i++ {
		r, err := start(s, capped, true, limits, t0.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		failRun(t, s, r)
	}
	cooling := open(t, s, repo, "pr:8", "go", "aaa", "failure")
	r, _ := start(s, cooling, true, limits, t0.Add(3*time.Hour))
	failRun(t, s, r)

	now := t0.Add(3*time.Hour + 5*time.Minute)
	got, err := s.ListAutoCandidates(ctx, now, limits)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, in := range got {
		ids = append(ids, in.ID)
	}
	if len(ids) != 2 || ids[0] != eligible.ID || ids[1] != timedOut.ID {
		t.Fatalf("candidates = %v, want [%d %d] (oldest first; not cancelled, ignored, resolved, capped, cooling down or in a disabled repo)", ids, eligible.ID, timedOut.ID)
	}

	later, _ := s.ListAutoCandidates(ctx, t0.Add(3*time.Hour+20*time.Minute), limits)
	if len(later) != 3 {
		t.Fatalf("after the cooldown there are %d candidates, want 3", len(later))
	}
}

func TestAutoRunsSinceCountsOnlyAutomaticResponderRuns(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	manual := open(t, s, repo, "pr:1", "go", "aaa", "failure")
	auto := open(t, s, repo, "pr:2", "go", "aaa", "failure")

	rm, _ := start(s, manual, false, limits, t0)
	failRun(t, s, rm)
	ra, _ := start(s, auto, true, limits, t0.Add(time.Minute))
	failRun(t, s, ra)
	if _, err := s.CreateRun(ctx, "claude", "adhoc"); err != nil {
		t.Fatal(err)
	}

	if n, err := s.AutoRunsSince(ctx, t0.Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("AutoRunsSince = %d, %v, want 1", n, err)
	}
	if n, _ := s.AutoRunsSince(ctx, t0.Add(time.Hour)); n != 0 {
		t.Fatalf("AutoRunsSince after the runs = %d", n)
	}
}

func TestARunWithAFailureReasonIsFailedEvenWithExitCodeZero(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, _ := s.CreateRun(ctx, "claude", "x")
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, r.ID, run.Outcome{ExitCode: 0, FailureReason: run.ReasonInvalidOutput, Result: "no schema match"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(ctx, r.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("run = %+v", got)
	}
}

func TestTheRunListTruncatesPromptsButTheRunKeepsItsOwn(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	long := strings.Repeat("p", 5000)
	r, _ := s.CreateRun(ctx, "claude", long)

	list, err := s.ListRuns(ctx, 10)
	if err != nil || len(list) != 1 || len(list[0].Prompt) != 300 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if got, _ := s.GetRun(ctx, r.ID); got.Prompt != long {
		t.Fatalf("GetRun returned %d prompt bytes, want 5000", len(got.Prompt))
	}
}
