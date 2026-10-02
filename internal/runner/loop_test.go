package runner_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

const token = "runner-token-with-at-least-24-chars"

func startLoop(t *testing.T, providers map[string]provider.Provider) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New("correct horse battery"), RunnerToken: token}))
	t.Cleanup(ts.Close)

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()},
		Providers:     providers,
		WorkspaceRoot: t.TempDir(),
		Env:           os.Environ(),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:       50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done }) // runs before ts.Close (LIFO)
	return st, ts
}

func waitTerminal(t *testing.T, st *store.Store, id string) run.Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := st.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status.Terminal() {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not finish in time")
	return run.Run{}
}

func TestLoopRunsQueuedRunEndToEnd(t *testing.T) {
	st, _ := startLoop(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.FakeClaude(t, 0)},
	})
	queued, _ := st.CreateRun(context.Background(), "claude", "hello")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Succeeded || got.Result != "done" || got.SessionID != "s-1" {
		t.Fatalf("run = %+v", got)
	}

	events, _ := st.Events(context.Background(), queued.ID, 0)

	// stdout and stderr are read concurrently and pipes have no cross-stream ordering, so a
	// stderr event may land anywhere. Only the order within stdout is guaranteed.
	var stdoutKinds []string
	var stderrCount int
	for _, e := range events {
		if e.Kind == "stderr" {
			stderrCount++
			continue
		}
		stdoutKinds = append(stdoutKinds, e.Kind)
	}
	if want := "system,probe,raw,result"; strings.Join(stdoutKinds, ",") != want {
		t.Fatalf("stdout event kinds = %v, want %s", stdoutKinds, want)
	}
	if stderrCount != 1 {
		t.Fatalf("stderr events = %d, want 1", stderrCount)
	}
}

func TestLoopReportsFailedExit(t *testing.T) {
	st, _ := startLoop(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.FakeClaude(t, 3)},
	})
	queued, _ := st.CreateRun(context.Background(), "claude", "hello")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("run = %+v", got)
	}
}

func TestLoopFailsRunWithUnknownProvider(t *testing.T) {
	st, _ := startLoop(t, map[string]provider.Provider{})
	queued, _ := st.CreateRun(context.Background(), "claude", "hello")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.ExitCode == nil || *got.ExitCode != 127 {
		t.Fatalf("run = %+v", got)
	}
}
