package poller_test

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/store"
)

func counting(e *env) *int {
	n := 0
	e.p.AfterCycle = func(context.Context) { n++ }
	return &n
}

func TestAfterCycleRunsAfterEveryCompletedCycle(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	n := counting(e)
	e.poll()
	e.poll()
	if *n != 2 {
		t.Fatalf("AfterCycle ran %d times, want 2", *n)
	}
}

func TestAfterCycleRunsEvenWhenOneRepoFails(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) { f.missing["octo/hello"] = true })
	n := counting(e)
	e.poll()
	if *n != 1 {
		t.Fatalf("AfterCycle ran %d times, want 1: a broken repo must not stop the diagnosis of the others", *n)
	}
}

func TestAfterCycleDoesNotRunWhenGitHubCannotBeReached(t *testing.T) {
	t.Run("rate limit", func(t *testing.T) {
		e := newEnv(t, testKey(t, 1), testKey(t, 1))
		e.gh.set(func(f *fakeGitHub) { f.retryAfter = 120 })
		n := counting(e)
		e.poll()
		e.poll() // paused
		if *n != 0 {
			t.Fatalf("AfterCycle ran %d times while rate limited", *n)
		}
	})
	t.Run("rejected token", func(t *testing.T) {
		e := newEnv(t, testKey(t, 1), testKey(t, 1))
		e.gh.set(func(f *fakeGitHub) { f.unauth = true })
		n := counting(e)
		e.poll()
		e.poll() // the connection is marked and polling is off
		if *n != 0 {
			t.Fatalf("AfterCycle ran %d times with a rejected token", *n)
		}
	})
	t.Run("undecryptable token", func(t *testing.T) {
		e := newEnv(t, testKey(t, 1), testKey(t, 2))
		n := counting(e)
		e.poll()
		if *n != 0 {
			t.Fatalf("AfterCycle ran %d times", *n)
		}
	})
	t.Run("no connection", func(t *testing.T) {
		e := newEnv(t, testKey(t, 1), testKey(t, 1))
		_ = e.st.DeleteConnection(context.Background())
		n := counting(e)
		e.poll()
		if *n != 0 {
			t.Fatalf("AfterCycle ran %d times", *n)
		}
	})
	t.Run("connection in error", func(t *testing.T) {
		e := newEnv(t, testKey(t, 1), testKey(t, 1))
		_ = e.st.UpdateConnectionStatus(context.Background(), store.ConnError, "rejected", e.now)
		n := counting(e)
		e.poll()
		if *n != 0 {
			t.Fatalf("AfterCycle ran %d times", *n)
		}
	})
}

func TestAfterCycleSeesTheIncidentsOfTheCycle(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) {
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "aaa")}
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("go", "completed", "failure", "aaa")}
	})
	var seen int
	e.p.AfterCycle = func(ctx context.Context) {
		list, _ := e.st.ListIncidents(ctx, store.IncidentFilter{State: "active"})
		seen = len(list)
	}
	e.poll()
	if seen != 1 {
		t.Fatalf("AfterCycle saw %d active incidents, want the one the cycle just opened", seen)
	}
}
