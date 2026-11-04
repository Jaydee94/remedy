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

// spend starts and finishes a run of an incident (by hand, so that no limit is in the way) with the given cost and billing.
func spend(t *testing.T, s *store.Store, repo store.Repo, name string, usd float64, source string) {
	t.Helper()
	in := open(t, s, repo, "pr:"+name, name, "aaa", "failure")
	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, r.ID, run.Outcome{ExitCode: 1, CostUSD: usd, AuthSource: source})
	if err := s.FailDiagnosis(context.Background(), r.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "failed"}); err != nil {
		t.Fatal(err)
	}
}

func budgeted(day int) store.DiagnosisLimits {
	return store.DiagnosisLimits{Cooldown: 0, MaxPerIncident: 10, MaxPerDay: 100, EnforceBudget: true, DayBudgetCents: day, RunBudgetCents: 50}
}

// spendKilled finishes a key-billed run that was killed before the CLI printed its cost: it records no cost at all.
func spendKilled(t *testing.T, s *store.Store, repo store.Repo, name, reason string) {
	t.Helper()
	in := open(t, s, repo, "pr:"+name, name, "aaa", "failure")
	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	finishRun(t, s, r.ID, run.Outcome{ExitCode: -1, FailureReason: reason, AuthSource: "api_key"})
	if err := s.FailDiagnosis(context.Background(), r.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "failed"}); err != nil {
		t.Fatal(err)
	}
}

func TestAKilledKeyRunCountsAtTheBudgetOfOneRun(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	spendKilled(t, s, repo, "timed-out", run.ReasonTimeout)
	spendKilled(t, s, repo, "cancelled", run.ReasonCancelled)
	spendKilled(t, s, repo, "lost", run.ReasonRunnerLost)
	got, err := s.APISpendSince(context.Background(), time.Now().Add(-24*time.Hour), 50)
	if err != nil || got != 150 {
		t.Fatalf("APISpendSince = %d, %v, want 150 cents (three killed runs at 50)", got, err)
	}
	next := open(t, s, repo, "pr:next", "next", "aaa", "failure")
	if _, err := start(s, next, true, budgeted(150), time.Now()); !errors.Is(err, store.ErrLimit) {
		t.Fatalf("killed runs did not count against the daily budget: %v", err)
	}
}

func TestAStoppedBillingMismatchIsNotMoneyEvenWithoutACost(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	spendKilled(t, s, repo, "mismatch", run.ReasonBillingMismatch)
	got, err := s.APISpendSince(context.Background(), time.Now().Add(-24*time.Hour), 50)
	if err != nil || got != 0 {
		t.Fatalf("APISpendSince = %d, %v, want 0", got, err)
	}
}

func TestAnAutomaticRunIsRefusedAtOrOverTheDailyBudget(t *testing.T) {
	s, repo := openStore(t), (store.Repo{})
	repo = seedRepo(t, s)
	spend(t, s, repo, "first", 3.20, "api_key")
	next := open(t, s, repo, "pr:next", "next", "aaa", "failure")

	_, err := start(s, next, true, budgeted(300), time.Now())
	var lim *store.LimitError
	if !errors.Is(err, store.ErrLimit) || !errors.As(err, &lim) {
		t.Fatalf("over the budget: error = %v, want a LimitError", err)
	}
	if !strings.Contains(lim.Reason, "$3.20") || !strings.Contains(lim.Reason, "$3.00") {
		t.Errorf("reason = %q, want the spend and the budget in dollars", lim.Reason)
	}
	if _, err := start(s, next, true, budgeted(320), time.Now()); !errors.Is(err, store.ErrLimit) {
		t.Errorf("exactly at the budget: error = %v, want ErrLimit", err)
	}
	if r, err := start(s, next, true, budgeted(321), time.Now()); err != nil {
		t.Fatalf("one cent under the budget: %v", err)
	} else {
		failRun(t, s, r)
	}
}

func TestARunTheMaintainerStartsIsNeverRefusedForTheBudget(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	spend(t, s, repo, "first", 9, "api_key")
	next := open(t, s, repo, "pr:next", "next", "aaa", "failure")
	if _, err := start(s, next, false, budgeted(100), time.Now()); err != nil {
		t.Fatalf("a manual start over the budget: %v", err)
	}
}

func TestOnlyKeyBilledRunsOfTheLastDayCountAndOnlyWhenEnforced(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	spend(t, s, repo, "subscription", 50, "subscription") // a cost the CLI printed for a subscription is not money
	spend(t, s, repo, "unknown", 50, "")
	next := open(t, s, repo, "pr:next", "next", "aaa", "failure")
	if r, err := start(s, next, true, budgeted(100), time.Now()); err != nil {
		t.Fatalf("subscription and unknown runs counted: %v", err)
	} else {
		failRun(t, s, r)
	}
	spend(t, s, repo, "key", 4, "api_key")
	another := open(t, s, repo, "pr:another", "another", "aaa", "failure")
	if _, err := start(s, another, true, budgeted(100), time.Now()); !errors.Is(err, store.ErrLimit) {
		t.Fatalf("a key-billed run did not count: %v", err)
	}
	notEnforced := budgeted(100)
	notEnforced.EnforceBudget = false
	if r, err := start(s, another, true, notEnforced, time.Now()); err != nil {
		t.Fatalf("with EnforceBudget off the budget still refused: %v", err)
	} else {
		failRun(t, s, r)
	}
	if _, err := start(s, open(t, s, repo, "pr:late", "late", "aaa", "failure"), true, budgeted(100), time.Now().Add(25*time.Hour)); err != nil {
		t.Fatalf("spend older than 24 hours counted: %v", err)
	}
}

func TestABudgetOfZeroRefusesEveryAutomaticRunWhileEnforced(t *testing.T) {
	s := openStore(t)
	in := open(t, s, seedRepo(t, s), "pr:7", "go", "aaa", "failure")
	if _, err := start(s, in, true, budgeted(0), time.Now()); !errors.Is(err, store.ErrLimit) {
		t.Fatalf("budget 0: error = %v, want ErrLimit", err)
	}
}

func TestTheSpendOfTheLastDayIsInCents(t *testing.T) {
	s := openStore(t)
	repo := seedRepo(t, s)
	spend(t, s, repo, "a", 1.234, "api_key")
	spend(t, s, repo, "b", 0.5, "api_key")
	spend(t, s, repo, "c", 7, "subscription")
	got, err := s.APISpendSince(context.Background(), time.Now().Add(-24*time.Hour), 50)
	if err != nil || got != 173 {
		t.Fatalf("APISpendSince = %d, %v, want 173 cents", got, err)
	}
}
