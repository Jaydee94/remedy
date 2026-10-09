package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func TestNextCheckIsLazyWhenLoggedInAndEagerOtherwise(t *testing.T) {
	ok, other := 10*time.Minute, time.Minute
	for state, want := range map[provider.LoginState]time.Duration{
		provider.LoginOK: ok, provider.LoginMissing: other, provider.LoginUnknown: other,
	} {
		if got := runner.NextCheck(state, ok, other); got != want {
			t.Errorf("%s: %s, want %s", state, got, want)
		}
	}
}

type statusServer struct {
	mu      sync.Mutex
	reports []runner.Report
	auths   []string
	status  int // 0 means 204
}

func (s *statusServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var rep runner.Report
	_ = json.NewDecoder(r.Body).Decode(&rep)
	s.mu.Lock()
	s.reports = append(s.reports, rep)
	s.auths = append(s.auths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
	code := s.status
	s.mu.Unlock()
	if code == 0 {
		code = http.StatusNoContent
	}
	w.WriteHeader(code)
}

func (s *statusServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

func (s *statusServer) last() runner.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reports[len(s.reports)-1]
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTheReporterPostsAtOnceAndThenAgainWithWhatChanged(t *testing.T) {
	srv := &statusServer{}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	status := runner.NewStatus()
	rep := &runner.Reporter{
		Client: &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()},
		Status: status, Interval: 20 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rep.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first report", func() bool { return srv.count() >= 1 })
	if first := srv.last(); first.Login != "unknown" {
		t.Fatalf("the first report = %+v, want unknown", first)
	}
	if srv.auths[0] != "POST /runner/v1/status Bearer runner-token-with-at-least-24-chars" {
		t.Fatalf("the request was %q", srv.auths[0])
	}
	status.SetLogin(provider.LoginOK, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	eventually(t, "a report with the login", func() bool { return srv.last().Login == "ok" })
	if !status.Connected() {
		t.Fatal("a report the control plane accepted means the runner is connected")
	}
}

func TestAFailedReportMeansNotConnectedAndASuccessMeansConnected(t *testing.T) {
	srv := &statusServer{status: http.StatusServiceUnavailable}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	status := runner.NewStatus()
	status.SetConnected(true)
	rep := &runner.Reporter{
		Client: &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()},
		Status: status, Interval: 20 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rep.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "not connected after a refused report", func() bool { return srv.count() >= 1 && !status.Connected() })
	srv.mu.Lock()
	srv.status = 0
	srv.mu.Unlock()
	eventually(t, "connected after an accepted one", status.Connected)
}

func TestTheLoginCheckerChecksAtOnceAndKeepsTheVersion(t *testing.T) {
	status := runner.NewStatus()
	calls := make(chan struct{}, 10)
	checker := &runner.LoginChecker{
		Check: func(context.Context) (provider.LoginState, string) {
			calls <- struct{}{}
			return provider.LoginMissing, ""
		},
		Version:   func(context.Context) string { return "2.1.288 (Claude Code)" },
		Status:    status,
		OKEvery:   time.Hour,
		ElseEvery: 20 * time.Millisecond,
		Log:       quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the login state", func() bool { return status.Report().Login == "missing" })
	if r := status.Report(); r.CLIVersion != "2.1.288 (Claude Code)" || r.LoginCheckedAt.IsZero() {
		t.Fatalf("report = %+v", r)
	}
	// Not logged in: it asks again soon, so the page turns green a minute after the login.
	eventually(t, "a second check while not logged in", func() bool { return len(calls) >= 2 })
}

func TestTheLoginCheckerLeavesALoginAloneForAWhile(t *testing.T) {
	status := runner.NewStatus()
	var n int
	var mu sync.Mutex
	checker := &runner.LoginChecker{
		Check: func(context.Context) (provider.LoginState, string) {
			mu.Lock()
			n++
			mu.Unlock()
			return provider.LoginOK, ""
		},
		Status: status, OKEvery: time.Hour, ElseEvery: 10 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "logged in", func() bool { return status.Report().Login == "ok" })
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("%d checks in 150 ms while logged in with an hour between checks, want 1", n)
	}
}

func TestTheCheckerTreatsAnUnknownAnswerAsUnknownNeverAsMissing(t *testing.T) {
	status := runner.NewStatus()
	status.SetLogin(provider.LoginOK, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	calls := make(chan struct{}, 10)
	checker := &runner.LoginChecker{
		Check: func(context.Context) (provider.LoginState, string) {
			calls <- struct{}{}
			return provider.LoginUnknown, "the check timed out"
		},
		Status: status, OKEvery: time.Hour, ElseEvery: 10 * time.Millisecond, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "two checks", func() bool { return len(calls) >= 2 })
	if got := status.Report().Login; got != "unknown" {
		t.Fatalf("login = %q after a check that could not tell, want unknown (never missing)", got)
	}
}

func TestTheCheckerBoundsACheckWithATimeout(t *testing.T) {
	status := runner.NewStatus()
	deadline := make(chan bool, 1)
	checker := &runner.LoginChecker{
		Check: func(ctx context.Context) (provider.LoginState, string) {
			_, ok := ctx.Deadline()
			deadline <- ok
			return provider.LoginUnknown, "x"
		},
		Status: status, OKEvery: time.Hour, ElseEvery: time.Hour, Log: quiet(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { checker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case ok := <-deadline:
		if !ok {
			t.Fatal("a check without a deadline can hang the checker")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no check")
	}
}

// The reporter and the claim loop both write "connected". The reporter must not depend on the loop: while the claim
// is stuck (a long poll, or a run in progress), a failing report alone has to turn connected off.
func TestTheReporterDoesNotNeedTheClaimLoop(t *testing.T) {
	release := make(chan struct{})
	var failing atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /runner/v1/claim", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /runner/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	status := runner.NewStatus()
	client := &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()}
	loop := &runner.Loop{Client: client, Providers: map[string]provider.Provider{}, WorkspaceRoot: t.TempDir(), Log: quiet(), Status: status}
	rep := &runner.Reporter{Client: client, Status: status, Interval: 20 * time.Millisecond, Log: quiet()}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); loop.Run(ctx) }()
	go func() { defer wg.Done(); rep.Run(ctx) }()
	t.Cleanup(func() { cancel(); close(release); wg.Wait() })

	eventually(t, "connected from the report while the claim is stuck", status.Connected)
	failing.Store(true)
	eventually(t, "not connected from the failing report while the claim is still stuck", func() bool { return !status.Connected() })
	failing.Store(false)
	eventually(t, "connected again from the report alone", status.Connected)
}
