package store_test

// The guarantees SQLite's single connection gave for free, tested with parallel callers on a pool (plan task 6,
// docs/research/postgres-concurrency-audit.md). Each test opens several Stores on one schema, so that its goroutines really run
// side by side on separate connections, releases them together and repeats the race often enough to hit a bad interleaving.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

// concurrentStores opens n Stores on one fresh schema. The first one is the one to set up and check with.
func concurrentStores(t *testing.T, n int) ([]*store.Store, string) {
	t.Helper()
	first, dsn := openStoreAt(t)
	out := []*store.Store{first}
	for len(out) < n {
		out = append(out, openStoreOn(t, dsn))
	}
	return out, dsn
}

// race runs fn(i) for i in [0, n) on n goroutines that are released together, and waits for all of them.
func race(n int, fn func(i int)) {
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			fn(i)
		}()
	}
	close(gate)
	wg.Wait()
}

// errs collects the unexpected errors of goroutines, which must not call t.Fatal.
type errs struct {
	mu   sync.Mutex
	list []string
}

func (e *errs) add(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, fmt.Sprintf(format, args...))
}

func (e *errs) check(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.list) > 0 {
		t.Fatalf("%d unexpected outcomes, the first: %s", len(e.list), strings.Join(e.list[:min(5, len(e.list))], "; "))
	}
}

// until bounds a goroutine's retry loop.
func until(d time.Duration) func() bool {
	end := time.Now().Add(d)
	return func() bool { return time.Now().Before(end) }
}

// drain claims a queued run, if there is one, and finishes it; with fail it also closes the diagnosis of a responder run.
func drain(ctx context.Context, s *store.Store, fail bool) {
	c, err := s.ClaimNext(ctx)
	if err != nil || c == nil {
		time.Sleep(time.Millisecond)
		return
	}
	code := 0
	if fail {
		code = 1
	}
	_ = s.FinishRun(ctx, c.ID, run.Outcome{ExitCode: code})
	if fail {
		_ = s.FailDiagnosis(ctx, c.ID, store.NewActivity{Kind: store.KindDiagnosisFailed, Summary: "failed"})
	}
}

func autoStart(s *store.Store, incidentID int64, l store.DiagnosisLimits) error {
	_, err := s.StartDiagnosis(context.Background(), store.StartParams{
		IncidentID: incidentID, Provider: "claude", Prompt: "diagnose", HeadSHA: "aaa", Automatic: true, Limits: l, Now: time.Now(),
	}, store.NewActivity{Kind: store.KindDiagnosisStarted, Summary: "started"})
	return err
}

func TestConcurrencyTheDailyLimitHolds(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	const goroutines, perDay = 20, 5
	incidents := make([]int64, goroutines)
	for i := range incidents {
		in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", fmt.Sprintf("check-%d", i), "aaa"), entry(store.KindIncidentOpened, repo.ID))
		if err != nil {
			t.Fatal(err)
		}
		incidents[i] = in.ID
	}
	l := store.DiagnosisLimits{Cooldown: 0, MaxPerIncident: 10, MaxPerDay: perDay}

	var started atomic.Int32
	var bad errs
	race(goroutines, func(i int) {
		st := ss[i%len(ss)]
		for more := until(20 * time.Second); more(); {
			err := autoStart(st, incidents[i], l)
			switch {
			case err == nil:
				started.Add(1)
				return
			case errors.Is(err, store.ErrBusy):
				drain(ctx, st, false) // the runner is sequential: free the slot and try again
			case errors.Is(err, store.ErrLimit):
				return
			default:
				bad.add("incident %d: %v", incidents[i], err)
				return
			}
		}
		bad.add("incident %d: no outcome in time", incidents[i])
	})
	bad.check(t)
	n, err := s.AutoRunsSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if started.Load() != perDay || n != perDay {
		t.Fatalf("%d automatic diagnoses started, %d automatic runs stored; the daily limit is %d", started.Load(), n, perDay)
	}
}

func TestConcurrencyThePerIncidentCapHolds(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	const capPerIncident = 3
	l := store.DiagnosisLimits{Cooldown: 0, MaxPerIncident: capPerIncident, MaxPerDay: 1000}

	var started atomic.Int32
	var bad errs
	race(20, func(i int) {
		st := ss[i%len(ss)]
		for more := until(20 * time.Second); more(); {
			err := autoStart(st, in.ID, l)
			switch {
			case err == nil:
				started.Add(1)
			case errors.Is(err, store.ErrBusy), errors.Is(err, store.ErrNotDiagnosable):
				drain(ctx, st, true) // fail the diagnosis, so the incident is open again
			case errors.Is(err, store.ErrLimit):
				return
			default:
				bad.add("%v", err)
				return
			}
		}
		bad.add("no outcome in time")
	})
	bad.check(t)
	got, err := s.GetIncident(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListIncidentRuns(ctx, in.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if started.Load() != capPerIncident || got.Diagnoses != capPerIncident || len(runs) != capPerIncident {
		t.Fatalf("%d starts, diagnoses = %d, %d runs; the cap is %d", started.Load(), got.Diagnoses, len(runs), capPerIncident)
	}
}

func TestConcurrencyOneRunAtATime(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	for round := range 5 {
		var incidents []int64
		for i := range 20 {
			in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", fmt.Sprintf("r%d-%d", round, i), "aaa"), entry(store.KindIncidentOpened, repo.ID))
			if err != nil {
				t.Fatal(err)
			}
			incidents = append(incidents, in.ID)
		}
		var started atomic.Int32
		var bad errs
		race(len(incidents), func(i int) {
			_, err := start(ss[i%len(ss)], store.Incident{ID: incidents[i], HeadSHA: "aaa"}, false, store.DefaultLimits(), time.Now())
			switch {
			case err == nil:
				started.Add(1)
			case errors.Is(err, store.ErrBusy):
			default:
				bad.add("%v", err)
			}
		})
		bad.check(t)
		if started.Load() != 1 {
			t.Fatalf("round %d: %d runs started at once; one may be queued or running", round, started.Load())
		}
		drain(ctx, s, false)
	}
}

func TestConcurrencyStartDiagnosisAndIgnore(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	for i := range 60 {
		in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", fmt.Sprintf("check-%d", i), "aaa"), entry(store.KindIncidentOpened, repo.ID))
		if err != nil {
			t.Fatal(err)
		}
		resolve := i%2 == 1
		var startErr, otherErr error
		race(2, func(g int) {
			if g == 0 {
				_, startErr = start(ss[0], in, false, store.DefaultLimits(), time.Now())
				return
			}
			if resolve {
				otherErr = ss[1].ResolveIncident(ctx, in.ID, "passed", entry(store.KindIncidentResolved, repo.ID))
			} else {
				otherErr = ss[1].IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID))
			}
		})
		if startErr != nil && !errors.Is(startErr, store.ErrNotDiagnosable) || otherErr != nil {
			t.Fatalf("round %d: start %v, ignore or resolve %v", i, startErr, otherErr)
		}
		got, err := s.GetIncident(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := store.IncIgnored
		if resolve {
			want = store.IncResolved
		}
		if got.State != want {
			t.Fatalf("round %d: the incident is %s after the start (%v) and the ignore or resolve; want %s", i, got.State, startErr, want)
		}
		if startErr == nil {
			drain(ctx, s, false)
		}
	}
}

func TestConcurrencyClaimNextHandsOutEachRunOnce(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 30 {
		var want []string
		for range 3 {
			r, err := s.CreateRun(ctx, "claude", "p")
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, r.ID)
		}
		var mu sync.Mutex
		var got []string
		var bad errs
		race(10, func(i int) {
			r, err := ss[i%len(ss)].ClaimNext(ctx)
			if err != nil {
				bad.add("%v", err)
				return
			}
			if r != nil {
				mu.Lock()
				got = append(got, r.ID)
				mu.Unlock()
			}
		})
		bad.check(t)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("round %d: claimed %v, want each of %v once", round, got, want)
		}
		for _, id := range got {
			if err := s.FinishRun(ctx, id, run.Outcome{}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// approvedCall makes a waiting mutating call of r and approves it.
func approvedCall(t *testing.T, s *store.Store, r run.Run, useID string) store.ToolCall {
	t.Helper()
	c := waitingCall(t, s, r, useID)
	if _, err := s.DecideApproval(context.Background(), c.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConcurrencyOneExecutionPerApproval(t *testing.T) {
	// BeginExecution is one guarded UPDATE: this test pins that it stays one (it passes on the unhardened port too).
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	var r run.Run
	for round := range 40 {
		if round%20 == 0 {
			r = claimedToolRun(t, s)
		}
		c := approvedCall(t, s, r, fmt.Sprintf("toolu_%d", round))
		var won atomic.Int32
		var bad errs
		race(10, func(i int) {
			_, ok, err := ss[i%len(ss)].BeginExecution(ctx, c.ID)
			if err != nil {
				bad.add("%v", err)
			}
			if ok {
				won.Add(1)
			}
		})
		bad.check(t)
		if won.Load() != 1 {
			t.Fatalf("round %d: %d callers may execute the approved call", round, won.Load())
		}
		if err := s.FinishToolCall(ctx, c.ID, store.CallSucceeded, "done", ""); err != nil {
			t.Fatal(err)
		}
	}
}

// automaticToolRun is a claimed tool run that counts as automatic, so its mutating calls get a deadline.
func automaticToolRun(t *testing.T, s *store.Store, dsn string) run.Run {
	t.Helper()
	r := claimedToolRun(t, s)
	sqlOn(t, dsn, `UPDATE runs SET automatic = 1 WHERE id = ?`, r.ID)
	return r
}

func expiringCall(t *testing.T, s *store.Store, r run.Run, useID string) store.ToolCall {
	t.Helper()
	n := newCall(r.ID, useID, "incident_add_note", store.CallKindMutating)
	n.ExpiresIn = time.Minute
	c, existed, err := s.BeginToolCall(context.Background(), n)
	if err != nil || existed || c.ExpiresAt == nil {
		t.Fatalf("BeginToolCall = %+v, existed %v, err %v", c, existed, err)
	}
	return c
}

func TestConcurrencyExpireOrDecide(t *testing.T) {
	ss, dsn := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	var r run.Run
	var decided, expired int
	for round := range 200 {
		if round%50 == 0 {
			r = automaticToolRun(t, s, dsn)
		}
		c := expiringCall(t, s, r, fmt.Sprintf("toolu_%d", round))
		approve := round%2 == 0
		var out store.ToolCall
		var decideErr, expireErr error
		var didExpire bool
		race(2, func(g int) {
			if g == 0 {
				out, decideErr = ss[0].DecideApproval(ctx, c.ID, approve, "")
				return
			}
			// The decision has more statements before its write than the expiry: a pause that grows with the round lets
			// either side win.
			time.Sleep(time.Duration(round%8) * 250 * time.Microsecond)
			didExpire, expireErr = ss[1].ExpireCall(ctx, c.ID, time.Now().Add(time.Hour))
		})
		if expireErr != nil || decideErr != nil && !errors.Is(decideErr, store.ErrNotPending) {
			t.Fatalf("round %d: decide %v, expire %v", round, decideErr, expireErr)
		}
		if (decideErr == nil) == didExpire {
			t.Fatalf("round %d: decided %v, expired %v: exactly one may win", round, decideErr == nil, didExpire)
		}
		final, err := s.GetToolCall(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case didExpire:
			expired++
			if final.Status != store.CallAbandoned || final.Decision != store.DecisionAbandoned {
				t.Fatalf("round %d: an expired call is %s/%s", round, final.Status, final.Decision)
			}
		case approve:
			decided++
			if out.Status != store.CallWaiting || out.Decision != store.DecisionApproved || final.Status != store.CallWaiting || final.Decision != store.DecisionApproved {
				t.Fatalf("round %d: an approved call is %s/%s (answer %s/%s)", round, final.Status, final.Decision, out.Status, out.Decision)
			}
			if _, err := s.AbandonCall(ctx, c.ID); err != nil { // the run may have 5 waiting calls at most
				t.Fatal(err)
			}
		default:
			decided++
			if out.Status != store.CallDenied || final.Status != store.CallDenied || final.Decision != store.DecisionDenied {
				t.Fatalf("round %d: a denied call is %s/%s (answer %s/%s)", round, final.Status, final.Decision, out.Status, out.Decision)
			}
		}
	}
	t.Logf("decided %d, expired %d", decided, expired)
}

func TestConcurrencyDecideOrAbandon(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	var r run.Run
	for round := range 100 {
		if round%50 == 0 {
			r = claimedToolRun(t, s)
		}
		c := waitingCall(t, s, r, fmt.Sprintf("toolu_%d", round))
		var out store.ToolCall
		var decideErr, abandonErr error
		race(2, func(g int) {
			if g == 0 {
				out, decideErr = ss[0].DecideApproval(ctx, c.ID, true, "")
				return
			}
			_, abandonErr = ss[1].AbandonCall(ctx, c.ID)
		})
		if abandonErr != nil || decideErr != nil && !errors.Is(decideErr, store.ErrNotPending) {
			t.Fatalf("round %d: decide %v, abandon %v", round, decideErr, abandonErr)
		}
		if decideErr == nil && (out.Status != store.CallWaiting || out.Decision != store.DecisionApproved) {
			t.Fatalf("round %d: the approval went through on a call that is %s/%s", round, out.Status, out.Decision)
		}
		final, err := s.GetToolCall(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status != store.CallAbandoned || (decideErr == nil) != (final.Decision == store.DecisionApproved) {
			t.Fatalf("round %d: the call ends %s/%s, decide %v", round, final.Status, final.Decision, decideErr)
		}
	}
}

func TestConcurrencyDecideOrTheEndOfTheRun(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 60 {
		r := claimedToolRun(t, s)
		c := waitingCall(t, s, r, "toolu_1")
		var out store.ToolCall
		var decideErr, finishErr error
		race(2, func(g int) {
			if g == 0 {
				out, decideErr = ss[0].DecideApproval(ctx, c.ID, true, "")
				return
			}
			finishErr = ss[1].FinishRun(ctx, r.ID, run.Outcome{})
		})
		if finishErr != nil || decideErr != nil && !errors.Is(decideErr, store.ErrNotPending) {
			t.Fatalf("round %d: decide %v, finish %v", round, decideErr, finishErr)
		}
		if decideErr == nil && (out.Status != store.CallWaiting || out.Decision != store.DecisionApproved) {
			t.Fatalf("round %d: the approval went through on a call that is %s/%s", round, out.Status, out.Decision)
		}
		final, err := s.GetToolCall(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status != store.CallAbandoned {
			t.Fatalf("round %d: a call of a finished run is %s", round, final.Status)
		}
	}
}

func TestConcurrencyReopenOnce(t *testing.T) {
	// ReopenIncident is one guarded UPDATE: this test pins it (it passes on the unhardened port too).
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	for round := range 50 {
		in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", fmt.Sprintf("check-%d", round), "aaa"), entry(store.KindIncidentOpened, repo.ID))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ResolveIncident(ctx, in.ID, "passed", entry(store.KindIncidentResolved, repo.ID)); err != nil {
			t.Fatal(err)
		}
		results := make([]error, 2)
		race(2, func(g int) {
			results[g] = ss[g].ReopenIncident(ctx, in.ID, store.Reopen{Title: "t", Severity: "none"}, entry(store.KindIncidentReopened, repo.ID))
		})
		ok := 0
		for _, err := range results {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, store.ErrNotFound):
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d reopens went through, results %v", round, ok, results)
		}
	}
}

func TestConcurrencyOpenIncidentOnce(t *testing.T) {
	// The partial unique index decides: this test pins that its violation is ErrExists (it passes on the unhardened port too).
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	for round := range 30 {
		n := failing(repo.ID, "pr:7", fmt.Sprintf("check-%d", round), "aaa")
		var opened atomic.Int32
		var bad errs
		race(8, func(i int) {
			_, err := ss[i%len(ss)].OpenIncident(ctx, n, entry(store.KindIncidentOpened, repo.ID))
			switch {
			case err == nil:
				opened.Add(1)
			case errors.Is(err, store.ErrExists):
			default:
				bad.add("%v", err)
			}
		})
		bad.check(t)
		if opened.Load() != 1 {
			t.Fatalf("round %d: %d incidents opened for one key", round, opened.Load())
		}
	}
}

func TestConcurrencyResolveAndIgnore(t *testing.T) {
	// Two guarded UPDATEs whose SET is recomputed on the newest row: this test pins it (it passes on the unhardened port too).
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	repo := seedRepo(t, s)
	for round := range 60 {
		in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", fmt.Sprintf("check-%d", round), "aaa"), entry(store.KindIncidentOpened, repo.ID))
		if err != nil {
			t.Fatal(err)
		}
		var resolveErr, ignoreErr error
		race(2, func(g int) {
			if g == 0 {
				resolveErr = ss[0].ResolveIncident(ctx, in.ID, "passed", entry(store.KindIncidentResolved, repo.ID))
				return
			}
			ignoreErr = ss[1].IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID))
		})
		if resolveErr != nil || ignoreErr != nil && !errors.Is(ignoreErr, store.ErrNotFound) {
			t.Fatalf("round %d: resolve %v, ignore %v", round, resolveErr, ignoreErr)
		}
		// A reopen shows resolved_while_ignored: it leaves the incident ignored exactly when the ignore came first.
		if err := s.ReopenIncident(ctx, in.ID, store.Reopen{Title: "t", Severity: "none"}, entry(store.KindIncidentReopened, repo.ID)); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetIncident(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (got.State == store.IncIgnored) != (ignoreErr == nil) {
			t.Fatalf("round %d: reopened as %s, ignore %v", round, got.State, ignoreErr)
		}
		if err := s.ResolveIncident(ctx, in.ID, "done", entry(store.KindIncidentResolved, repo.ID)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrencyARepeatedToolCallIsRecordedOnce(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	var r run.Run
	for round := range 40 {
		if round%40 == 0 {
			r = claimedToolRun(t, s)
		}
		n := newCall(r.ID, fmt.Sprintf("toolu_%d", round), "incident_list", store.CallKindRead)
		var mu sync.Mutex
		ids := map[int64]int{}
		var firsts atomic.Int32
		var bad errs
		race(10, func(i int) {
			c, existed, err := ss[i%len(ss)].BeginToolCall(ctx, n)
			if err != nil {
				bad.add("%v", err)
				return
			}
			if !existed {
				firsts.Add(1)
			}
			mu.Lock()
			ids[c.ID]++
			mu.Unlock()
		})
		bad.check(t)
		if firsts.Load() != 1 || len(ids) != 1 {
			t.Fatalf("round %d: %d callers recorded the call, ids %v", round, firsts.Load(), ids)
		}
	}
	calls, err := s.ListToolCalls(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 40 {
		t.Fatalf("%d calls stored, want 40", len(calls))
	}
}

func TestConcurrencyThePendingLimitOfARunHolds(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 10 {
		r := claimedToolRun(t, s)
		var waiting atomic.Int32
		var bad errs
		race(20, func(i int) {
			_, _, err := ss[i%len(ss)].BeginToolCall(ctx, newCall(r.ID, fmt.Sprintf("toolu_%d", i), "incident_add_note", store.CallKindMutating))
			switch {
			case err == nil:
				waiting.Add(1)
			case errors.Is(err, store.ErrCallLimit):
			default:
				bad.add("%v", err)
			}
		})
		bad.check(t)
		if waiting.Load() != store.MaxPendingPerRun {
			t.Fatalf("round %d: %d calls wait for a decision, at most %d may", round, waiting.Load(), store.MaxPendingPerRun)
		}
		if err := s.FinishRun(ctx, r.ID, run.Outcome{}); err != nil {
			t.Fatal(err)
		}
	}
}

// noWaitingCalls fails when a call of the run still waits.
func noWaitingCalls(t *testing.T, s *store.Store, runID string, round int) {
	t.Helper()
	calls, err := s.ListToolCalls(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.Status == store.CallWaiting {
			t.Fatalf("round %d: call %d waits for a decision on a run that ended or was cancelled", round, c.ID)
		}
	}
}

func TestConcurrencyBeginToolCallAndCancelRun(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 80 {
		r := claimedToolRun(t, s)
		var beginErr, cancelErr error
		race(2, func(g int) {
			if g == 0 {
				_, _, beginErr = ss[0].BeginToolCall(ctx, newCall(r.ID, "toolu_1", "incident_add_note", store.CallKindMutating))
				return
			}
			_, cancelErr = ss[1].CancelRun(ctx, r.ID)
		})
		if cancelErr != nil || beginErr != nil && !errors.Is(beginErr, store.ErrNotFound) {
			t.Fatalf("round %d: begin %v, cancel %v", round, beginErr, cancelErr)
		}
		noWaitingCalls(t, s, r.ID, round)
		if err := s.FinishRun(ctx, r.ID, run.Outcome{FailureReason: run.ReasonCancelled}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrencyBeginToolCallAndTheEndOfItsRun(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 80 {
		r := claimedToolRun(t, s)
		var beginErr, finishErr error
		race(2, func(g int) {
			if g == 0 {
				_, _, beginErr = ss[0].BeginToolCall(ctx, newCall(r.ID, "toolu_1", "incident_add_note", store.CallKindMutating))
				return
			}
			finishErr = ss[1].FinishRun(ctx, r.ID, run.Outcome{})
		})
		if finishErr != nil || beginErr != nil && !errors.Is(beginErr, store.ErrNotFound) {
			t.Fatalf("round %d: begin %v, finish %v", round, beginErr, finishErr)
		}
		noWaitingCalls(t, s, r.ID, round)
	}
}

func TestConcurrencyCancelOrClaim(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 80 {
		r, err := s.CreateToolRun(ctx, "claude", "p")
		if err != nil {
			t.Fatal(err)
		}
		var claimed *run.Run
		var cancelled run.Run
		var claimErr, cancelErr error
		race(2, func(g int) {
			if g == 0 {
				// The claim is one statement and the cancel two: a pause that grows with the round lands the claim
				// between the cancel's read and its write in some rounds.
				time.Sleep(time.Duration(round%8) * 100 * time.Microsecond)
				claimed, claimErr = ss[0].ClaimNext(ctx)
				return
			}
			cancelled, cancelErr = ss[1].CancelRun(ctx, r.ID)
		})
		if claimErr != nil || cancelErr != nil {
			t.Fatalf("round %d: claim %v, cancel %v", round, claimErr, cancelErr)
		}
		switch {
		case claimed == nil: // cancelled before the claim
			if cancelled.Status != run.Failed || cancelled.FailureReason != run.ReasonCancelled {
				t.Fatalf("round %d: not claimed, but the cancel left the run %s %q", round, cancelled.Status, cancelled.FailureReason)
			}
		default: // claimed first: the cancel asks the running run to stop
			if claimed.ID != r.ID || cancelled.Status != run.Running || !cancelled.CancelRequested {
				t.Fatalf("round %d: claimed %s, the cancel answered %s, cancel requested %v", round, claimed.ID, cancelled.Status, cancelled.CancelRequested)
			}
			if err := s.FinishRun(ctx, r.ID, run.Outcome{FailureReason: run.ReasonCancelled}); err != nil {
				t.Fatal(err)
			}
		}
		if n := countKind(t, s, store.KindRunCancelled); n != round+1 {
			t.Fatalf("round %d: %d run_cancelled entries", round, n)
		}
	}
}

func TestConcurrencyAppendEventNumbersWithoutGaps(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 20 {
		r, err := s.CreateRun(ctx, "claude", "p")
		if err != nil {
			t.Fatal(err)
		}
		var bad errs
		race(20, func(i int) {
			if _, err := ss[i%len(ss)].AppendEvent(ctx, r.ID, "stdout", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))); err != nil {
				bad.add("%v", err)
			}
		})
		bad.check(t)
		events, err := s.Events(ctx, r.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 20 {
			t.Fatalf("round %d: %d events stored, want 20", round, len(events))
		}
		for i, e := range events {
			if e.Seq != i+1 {
				t.Fatalf("round %d: event %d has seq %d", round, i, e.Seq)
			}
		}
	}
}

func TestConcurrencyStartLearningOnce(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	h := hostNamed(t, s, "nas")
	for round := range 10 {
		var started atomic.Int32
		var bad errs
		race(20, func(i int) {
			_, err := ss[i%len(ss)].StartLearning(ctx, learnStart(h.ID))
			switch {
			case err == nil:
				started.Add(1)
			case errors.Is(err, store.ErrLearning), errors.Is(err, store.ErrBusy):
			default:
				bad.add("%v", err)
			}
		})
		bad.check(t)
		if started.Load() != 1 {
			t.Fatalf("round %d: %d learning runs started for one host", round, started.Load())
		}
		finish(t, s)
	}
}

func TestConcurrencyTheScopeLimitHolds(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 5 {
		h := hostNamed(t, s, fmt.Sprintf("host%d", round))
		for i := range skill.MaxPerScope - 3 {
			if _, err := s.CreateSkill(ctx, h.ID, skillFields(fmt.Sprintf("old-%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		proposals := append(propose(t, s, h.ID, names("prop-", 0, 5)...), propose(t, s, h.ID, names("prop-", 5, 10)...)...)
		var made atomic.Int32
		var bad errs
		race(20, func(i int) {
			st := ss[i%len(ss)]
			var err error
			if i < 10 {
				_, err = st.CreateSkill(ctx, h.ID, skillFields(fmt.Sprintf("new-%d", i)))
			} else {
				_, err = st.ConfirmSkill(ctx, proposals[i-10].ID, nil)
			}
			switch {
			case err == nil:
				made.Add(1)
			case errors.Is(err, store.ErrScopeFull):
			default:
				bad.add("%v", err)
			}
		})
		bad.check(t)
		active, err := s.ListSkills(ctx, store.SkillFilter{HostID: h.ID, State: store.SkillActive})
		if err != nil {
			t.Fatal(err)
		}
		if made.Load() != 3 || len(active) != skill.MaxPerScope {
			t.Fatalf("round %d: %d skills added, %d active; a scope may have %d", round, made.Load(), len(active), skill.MaxPerScope)
		}
	}
}

func TestConcurrencyThePendingProposalLimitHolds(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 5 {
		h := hostNamed(t, s, fmt.Sprintf("host%d", round))
		for from := 0; from < skill.MaxPendingPerHost-3; from += skill.MaxProposalsPerRun {
			pend(t, s, h.ID, names("old-", from, min(from+skill.MaxProposalsPerRun, skill.MaxPendingPerHost-3))...)
		}
		var stored atomic.Int32
		var bad errs
		race(20, func(i int) {
			_, err := ss[i%len(ss)].CreateProposals(ctx, store.ProposalSet{HostID: h.ID, Items: []store.Proposal{
				{Fields: skillFields(fmt.Sprintf("new-%d", i)), Evidence: dockerPS},
			}})
			switch {
			case err == nil:
				stored.Add(1)
			case errors.Is(err, store.ErrTooManyProposals):
			default:
				bad.add("%v", err)
			}
		})
		bad.check(t)
		n, err := s.CountProposals(ctx, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Load() != 3 || n != skill.MaxPendingPerHost {
			t.Fatalf("round %d: %d sets stored, %d proposals wait; at most %d may", round, stored.Load(), n, skill.MaxPendingPerHost)
		}
	}
}

func TestConcurrencyConfirmOnce(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	h := hostNamed(t, s, "nas")
	for round := range 40 {
		p := propose(t, s, h.ID, fmt.Sprintf("p-%d", round))[0]
		results := make([]error, 2)
		race(2, func(g int) {
			if g == 0 || round%2 == 0 {
				_, results[g] = ss[g].ConfirmSkill(ctx, p.ID, nil)
				return
			}
			results[g] = ss[g].RejectSkill(ctx, p.ID)
		})
		ok := 0
		for _, err := range results {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, store.ErrSkillState), errors.Is(err, store.ErrNotFound):
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of the confirm and the confirm or reject went through: %v", round, ok, results)
		}
	}
	if n := countKind(t, s, store.KindSkillConfirmed) + countKind(t, s, store.KindSkillRejected); n != 40 {
		t.Fatalf("%d confirm and reject entries for 40 proposals", n)
	}
}

func TestConcurrencyUpdateAndDeleteSkill(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	h := hostNamed(t, s, "nas")
	updated, deleted := 0, 0
	for round := range 40 {
		k, err := s.CreateSkill(ctx, h.ID, skillFields(fmt.Sprintf("s-%d", round)))
		if err != nil {
			t.Fatal(err)
		}
		results := make([]error, 2)
		race(2, func(g int) {
			if g == 0 && round%2 == 0 {
				f := skillFields(k.Name)
				f.Body = "Changed."
				_, results[g] = ss[g].UpdateSkill(ctx, k.ID, f, store.SkillActive)
				return
			}
			results[g] = ss[g].DeleteSkill(ctx, k.ID)
		})
		for g, err := range results {
			switch {
			case err == nil && g == 0 && round%2 == 0:
				updated++
			case err == nil:
				deleted++
			case errors.Is(err, store.ErrNotFound):
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
	if deleted != 40 {
		t.Fatalf("%d skills deleted, want 40 (two deletes of one skill both went through, or none did)", deleted)
	}
	if got := countKind(t, s, store.KindSkillUpdated); got != updated {
		t.Fatalf("%d skill_updated entries for %d updates", got, updated)
	}
	if got := countKind(t, s, store.KindSkillDeleted); got != deleted {
		t.Fatalf("%d skill_deleted entries for %d deletes", got, deleted)
	}
}

func TestConcurrencyUpdateHostAndPinHostKey(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 60 {
		name := fmt.Sprintf("h%d", round)
		h := hostNamed(t, s, name)
		moved := hostFields(name)
		moved.Address = "192.168.178.200"
		var updateErr, pinErr error
		race(2, func(g int) {
			if g == 0 {
				_, _, updateErr = ss[0].UpdateHost(ctx, h.ID, moved)
				return
			}
			pinErr = ss[1].PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now())
		})
		if updateErr != nil || pinErr != nil {
			t.Fatalf("round %d: update %v, pin %v", round, updateErr, pinErr)
		}
		got, err := s.GetHost(ctx, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		// The log says which came first. A pin made before the move must be gone: it belongs to the old address.
		log, err := s.ListActivity(ctx, store.ActivityQuery{Kinds: []string{store.KindHostChanged}, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		pinnedLast := strings.HasPrefix(log[0].Summary, "Host key of")
		if pinnedLast != (got.Host.PinnedKey != "") {
			t.Fatalf("round %d: the log ends with %q, %q, and the host's pin is %q", round, log[1].Summary, log[0].Summary, got.Host.PinnedKey)
		}
	}
}

func TestConcurrencyCreateRunWithAndDeleteHost(t *testing.T) {
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 60 {
		h := hostNamed(t, s, fmt.Sprintf("h%d", round))
		var createErr, deleteErr error
		race(2, func(g int) {
			if g == 0 {
				_, createErr = ss[0].CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", HostIDs: []int64{h.ID}})
				return
			}
			deleteErr = ss[1].DeleteHost(ctx, h.ID)
		})
		if deleteErr != nil || createErr != nil && !errors.Is(createErr, store.ErrNoSuchHost) {
			t.Fatalf("round %d: create %v, delete %v", round, createErr, deleteErr)
		}
	}
}

func TestConcurrencyPinHostKeyAndDeleteHost(t *testing.T) {
	// A pin reads the host's name with FOR KEY SHARE and then updates the row, a delete locks it FOR UPDATE: neither may
	// deadlock the other, and a pin after the delete is ErrNotFound.
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	for round := range 60 {
		h := hostNamed(t, s, fmt.Sprintf("h%d", round))
		var pinErr, deleteErr error
		race(2, func(g int) {
			if g == 0 {
				pinErr = ss[0].PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now())
				return
			}
			deleteErr = ss[1].DeleteHost(ctx, h.ID)
		})
		if deleteErr != nil || pinErr != nil && !errors.Is(pinErr, store.ErrNotFound) {
			t.Fatalf("round %d: pin %v, delete %v", round, pinErr, deleteErr)
		}
	}
}

func TestConcurrencyDeleteRepoAndActivityWriters(t *testing.T) {
	// DeleteRepo cascades into the repository's incidents while other transactions change those incidents and log activity
	// that points at the repository. The activity flush takes its foreign key locks before the activity lock, so the
	// delete cannot close a wait cycle through that lock. An entry for a repository that is gone fails on its foreign key,
	// and a change of an incident that is gone is ErrNotFound; nothing else may happen (above all no deadlock, 40P01).
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 15 {
		if err := s.SaveConnection(ctx, connection("octo")); err != nil {
			t.Fatal(err)
		}
		repo, err := s.AddRepo(ctx, store.ConnectionID, fmt.Sprintf("octo/r%d", round), "main")
		if err != nil {
			t.Fatal(err)
		}
		var incidents []int64
		for i := range 6 {
			in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", fmt.Sprintf("check-%d", i), "aaa"), entry(store.KindIncidentOpened, repo.ID))
			if err != nil {
				t.Fatal(err)
			}
			incidents = append(incidents, in.ID)
		}
		var bad errs
		race(13, func(i int) {
			st := ss[i%len(ss)]
			var err error
			switch {
			case i == 0:
				err = st.DeleteRepo(ctx, repo.ID)
			case i <= 6:
				err = st.ResolveIncident(ctx, incidents[i-1], "passed", entry(store.KindIncidentResolved, repo.ID))
			default:
				err = st.AddActivity(ctx, entry(store.KindPollFailed, repo.ID))
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) && !strings.Contains(err.Error(), "23503") {
				bad.add("goroutine %d: %v", i, err)
			}
		})
		bad.check(t)
	}
}

func TestConcurrencyDeleteHostAndItsWriters(t *testing.T) {
	// DeleteHost cascades into the host's skills and sets the host of its incidents to NULL, while other transactions change
	// those incidents (naming the host again) and add skills and logins. Allowed outcomes: success, ErrNotFound, and a foreign
	// key error (23503) for a write that names the host after it went; never a deadlock (40P01).
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()
	for round := range 15 {
		h := hostNamed(t, s, fmt.Sprintf("h%d", round))
		var incidents []int64
		for i := range 4 {
			in, err := s.OpenIncident(ctx, store.NewIncident{
				Source: store.SourceAlertmanager, Key: fmt.Sprintf("r%d-a%d", round, i), Title: "down", Conclusion: "firing", HostID: h.ID,
			}, entry(store.KindIncidentOpened, 0))
			if err != nil {
				t.Fatal(err)
			}
			incidents = append(incidents, in.ID)
		}
		var bad errs
		race(11, func(i int) {
			st := ss[i%len(ss)]
			var err error
			switch {
			case i == 0:
				err = st.DeleteHost(ctx, h.ID)
			case i <= 4:
				err = st.TouchIncidentSignal(ctx, incidents[i-1], h.ID, json.RawMessage(`{"n":1}`))
			case i <= 7:
				_, err = st.CreateSkill(ctx, h.ID, skillFields(fmt.Sprintf("k-%d", i)))
			case i <= 9:
				err = st.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now())
			default:
				err = st.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{1}, Hint: "set"})
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) && !strings.Contains(err.Error(), "23503") {
				bad.add("goroutine %d: %v", i, err)
			}
		})
		bad.check(t)
	}
}

func TestConcurrencyDeleteSkillAndAProposalThatReplacesIt(t *testing.T) {
	// A new proposal deletes the waiting proposal of its name (which points at the skill) and then locks the skill it replaces;
	// DeleteSkill locks the skill and then unlinks the proposals that point at it. Neither may deadlock the other.
	ss, _ := concurrentStores(t, 2)
	s, ctx := ss[0], context.Background()
	h := hostNamed(t, s, "nas")
	for round := range 40 {
		name := fmt.Sprintf("s-%d", round)
		k, err := s.CreateSkill(ctx, h.ID, skillFields(name))
		if err != nil {
			t.Fatal(err)
		}
		propose(t, s, h.ID, name)
		var deleteErr, proposeErr error
		race(2, func(g int) {
			if g == 0 {
				deleteErr = ss[0].DeleteSkill(ctx, k.ID)
				return
			}
			_, proposeErr = ss[1].CreateProposals(ctx, store.ProposalSet{HostID: h.ID, Items: []store.Proposal{{Fields: skillFields(name), Evidence: dockerPS}}})
		})
		if deleteErr != nil || proposeErr != nil {
			t.Fatalf("round %d: delete %v, propose %v", round, deleteErr, proposeErr)
		}
		for _, p := range proposals(t, s) {
			if err := s.RejectSkill(ctx, p.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestConcurrencyTheActivityFeedMissesNothing(t *testing.T) {
	ss, _ := concurrentStores(t, 4)
	s, ctx := ss[0], context.Background()

	// Long writers: AbandonAllWaiting writes one entry per waiting call in one transaction, so its first id is drawn well before
	// its commit. Short writers: AddActivity, one entry each.
	// The runs ask about an incident, so each entry of an abandoned call also reads the incident's repository.
	repo := seedRepo(t, s)
	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	var runs []run.Run
	for range 4 {
		if _, err := s.CreateQuestionRun(ctx, "claude", "why?", in.ID, false); err != nil {
			t.Fatal(err)
		}
		r, err := s.ClaimNext(ctx)
		if err != nil || r == nil {
			t.Fatalf("ClaimNext = %v, %v", r, err)
		}
		runs = append(runs, *r)
	}
	fill := func(round int) {
		for _, r := range runs {
			for j := range store.MaxPendingPerRun {
				waitingCall(t, s, r, fmt.Sprintf("toolu_%d_%d", round, j))
			}
		}
	}

	var stop atomic.Bool
	seen := map[int64]int{}
	var order []int64
	var readErr error
	readerDone := make(chan struct{})
	go func() { // the SSE handler's loop: everything above the last id sent
		defer close(readerDone)
		var last int64
		for {
			finalPass := stop.Load()
			entries, err := s.ListActivitySince(ctx, last, 100)
			if err != nil {
				readErr = err
				return
			}
			for _, a := range entries {
				seen[a.ID]++
				order = append(order, a.ID)
				last = a.ID
			}
			if finalPass && len(entries) == 0 {
				return
			}
		}
	}()

	var bad errs
	for round := range 8 {
		fill(round)
		race(12, func(i int) {
			st := ss[i%len(ss)]
			if i < 2 {
				if _, err := st.AbandonAllWaiting(ctx); err != nil {
					bad.add("%v", err)
				}
				return
			}
			for j := range 10 {
				if err := st.AddActivity(ctx, store.NewActivity{Kind: store.KindPollFailed, Summary: fmt.Sprintf("%d-%d-%d", round, i, j)}); err != nil {
					bad.add("%v", err)
				}
			}
		})
	}
	stop.Store(true)
	<-readerDone
	bad.check(t)
	if readErr != nil {
		t.Fatal(readErr)
	}

	all, err := s.ListActivity(ctx, store.ActivityQuery{Ascending: true, Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	var missed []int64
	for _, a := range all {
		if seen[a.ID] != 1 {
			missed = append(missed, a.ID)
		}
	}
	if len(missed) > 0 || len(seen) != len(all) || !slices.IsSorted(order) {
		t.Fatalf("the feed has %d entries, the reader saw %d; not seen exactly once: %v", len(all), len(seen), missed[:min(10, len(missed))])
	}
}
