package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

type tripFunc func(*http.Request) (*http.Response, error)

func (f tripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// startToolLoop runs a loop with a heartbeat every 30 ms. wrap, when given, wraps the transport the runner uses.
func startToolLoop(t *testing.T, p provider.Provider, timeout time.Duration, wrap func(http.RoundTripper) http.RoundTripper) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New("correct horse battery"), RunnerToken: token}))
	t.Cleanup(ts.Close)

	httpClient := ts.Client()
	if wrap != nil {
		httpClient = &http.Client{Transport: wrap(http.DefaultTransport)}
	}
	loop := &runner.Loop{
		Client:            &runner.Client{BaseURL: ts.URL, Token: token, HTTP: httpClient},
		Providers:         map[string]provider.Provider{"claude": p},
		WorkspaceRoot:     t.TempDir(),
		Env:               os.Environ(),
		Log:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:           50 * time.Millisecond,
		RunTimeout:        timeout,
		HeartbeatInterval: 30 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return st, ts
}

func waitRunning(t *testing.T, st *store.Store, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := st.GetRun(context.Background(), id); err == nil && r.Status == run.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the run did not start")
}

func TestAWaitingRunIsNotTimedOutUntilTheWaitIsOver(t *testing.T) {
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.SlowClaude(t)}, 400*time.Millisecond, nil)
	ctx := context.Background()
	queued, _ := st.CreateToolRun(ctx, "claude", "use the tools")
	waitRunning(t, st, queued.ID)

	call, _, err := st.BeginToolCall(ctx, store.NewToolCall{
		RunID: queued.ID, ToolUseID: "toolu_1", Tool: "incident_add_note", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Three times the budget, waiting for an approval: the run is still there.
	time.Sleep(1200 * time.Millisecond)
	if got, _ := st.GetRun(ctx, queued.ID); got.Status != run.Running {
		t.Fatalf("a run that waits for an approval was stopped: %+v", got)
	}

	// The decision ends the wait; the clock runs again with what was left, and the run is stopped.
	if _, err := st.DecideApproval(ctx, call.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout {
		t.Fatalf("run = %+v, want a timeout once nobody waits any more", got)
	}
}

func TestCancellingARunStopsTheAgent(t *testing.T) {
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.SlowClaude(t)}, 0, nil) // the agent would sleep for a minute
	ctx := context.Background()
	queued, _ := st.CreateToolRun(ctx, "claude", "use the tools")
	waitRunning(t, st, queued.ID)

	started := time.Now()
	if _, err := st.CancelRun(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonCancelled || got.Result != "The run was cancelled." {
		t.Fatalf("run = %+v", got)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("it took %s to stop the agent", time.Since(started))
	}
}

func TestARunTheControlPlaneNoLongerHasIsStopped(t *testing.T) {
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.SlowClaude(t)}, 0, nil)
	ctx := context.Background()
	lost, _ := st.CreateToolRun(ctx, "claude", "use the tools")
	waitRunning(t, st, lost.ID)
	next, _ := st.CreateRun(ctx, "claude", "the next run")

	// The reaper gave up on the run: the runner's next heartbeat is a 404 and it stops the agent, which frees the
	// loop for the run that waits behind it.
	if ids, err := st.FailLostRuns(ctx, time.Now().Add(time.Hour), "lost"); err != nil || len(ids) != 1 {
		t.Fatalf("FailLostRuns = %v, %v", ids, err)
	}
	waitRunning(t, st, next.ID)
}

func TestAFailingHeartbeatDoesNotStopTheRun(t *testing.T) {
	var beats atomic.Int32
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.FakeClaudeAfter(t, "0.5")}, 0, func(rt http.RoundTripper) http.RoundTripper {
		return tripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/heartbeat") {
				beats.Add(1)
				return nil, errors.New("the control plane is unreachable")
			}
			return rt.RoundTrip(r)
		})
	})
	queued, _ := st.CreateToolRun(context.Background(), "claude", "use the tools")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Succeeded || got.Result != "done" {
		t.Fatalf("run = %+v, want it to finish although every heartbeat failed", got)
	}
	if beats.Load() < 2 {
		t.Fatalf("%d heartbeats were attempted, want several", beats.Load())
	}
}

func TestARunWithoutToolsSendsNoHeartbeat(t *testing.T) {
	var beats atomic.Int32
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.FakeClaudeAfter(t, "0.4")}, 0, func(rt http.RoundTripper) http.RoundTripper {
		return tripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/heartbeat") {
				beats.Add(1)
			}
			return rt.RoundTrip(r)
		})
	})
	queued, _ := st.CreateRun(context.Background(), "claude", "plain")
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}
	if beats.Load() != 0 {
		t.Fatalf("%d heartbeats of a run without tools", beats.Load())
	}
}
