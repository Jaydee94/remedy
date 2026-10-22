package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// UnignoreIncident and ReopenIncident derive the state from the incident's responder runs. At READ COMMITTED a statement
// that waits for the incident's row lock is re-checked on the new row, but its subqueries keep the snapshot of when the
// statement started: a run that finished meanwhile would still look running and the incident would stay diagnosing for
// good (no later CompleteDiagnosis moves it). These tests hold the row lock the way CompleteDiagnosis does, finish the run
// while the un-ignore or reopen waits, and require the state of the finished run.
func TestUnignoreThatWaitsForTheIncidentSeesTheRunThatFinishedMeanwhile(t *testing.T) {
	stateAfterWaiting(t, func(s *store.Store, in store.Incident) {
		mustIgnore(t, s, in)
	}, func(s *store.Store, in store.Incident) error {
		return unignoreIncident(s, in)
	})
}

func TestReopenThatWaitsForTheIncidentSeesTheRunThatFinishedMeanwhile(t *testing.T) {
	stateAfterWaiting(t, func(s *store.Store, in store.Incident) {
		if err := s.ResolveIncident(context.Background(), in.ID, "passed", entry(store.KindIncidentResolved, in.RepoID)); err != nil {
			t.Fatal(err)
		}
	}, func(s *store.Store, in store.Incident) error {
		return s.ReopenIncident(context.Background(), in.ID, store.Reopen{Title: "t", Severity: "none"}, entry(store.KindIncidentReopened, in.RepoID))
	})
}

func stateAfterWaiting(t *testing.T, park func(*store.Store, store.Incident), lift func(*store.Store, store.Incident) error) {
	t.Helper()
	s, dsn := openStoreAt(t)
	ctx := context.Background()
	repo := seedRepo(t, s)
	in := open(t, s, repo, "pr:7", "go", "aaa", "failure")
	r, err := start(s, in, false, limits, t0)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	park(s, in)

	// The holder: a transaction that has the incident's row lock, like CompleteDiagnosis between its UPDATE and its commit.
	holderDB := rawDB(t, dsn)
	holder, err := holderDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	var holderPID int
	if err := holder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `SELECT 1 FROM incidents WHERE id = $1 FOR NO KEY UPDATE`, in.ID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- lift(s, in) }()

	// Wait until the un-ignore or reopen is blocked by the holder: its statement (and so its snapshot) has started.
	watch := rawDB(t, dsn)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := watch.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, holderPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the change did not wait for the incident's row lock (err %v)", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the change never waited for the incident's row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The run ends now, after the waiting statement started, and the holder stores the diagnosis, as CompleteDiagnosis does.
	if err := s.FinishRun(ctx, r.ID, run.Outcome{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `UPDATE incidents SET diagnosis = '{"summary":"s"}' WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := mustState(t, s, in); got != store.IncDiagnosed {
		t.Fatalf("state = %q, want diagnosed: the run had finished and the diagnosis was stored", got)
	}
}
