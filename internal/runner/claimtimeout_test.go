package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestTheTimeoutOfTheClaimReplacesTheRunnersOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about ten seconds: the smallest run timeout the settings accept")
	}
	st := testutil.Store(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	set := settings.New(context.Background(), st, nil, quiet)
	if _, err := set.Set(context.Background(), settings.RunTimeout, json.RawMessage(`"10s"`)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New("correct horse battery"), RunnerToken: token, Settings: set}))
	t.Cleanup(ts.Close)

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()},
		Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.SlowClaude(t)}},
		WorkspaceRoot: t.TempDir(),
		Env:           os.Environ(),
		Log:           quiet,
		Backoff:       50 * time.Millisecond,
		RunTimeout:    500 * time.Millisecond, // what the runner would use without the claim's number
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	r, err := st.CreateRun(context.Background(), "claude", "hang")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var got run.Run
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got, err = st.GetRun(context.Background(), r.ID); err != nil {
			t.Fatal(err)
		} else if got.Status.Terminal() {
			break
		}
	}
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout {
		t.Fatalf("run = %+v, want failed with reason timeout", got)
	}
	if elapsed := time.Since(started); elapsed < 8*time.Second || elapsed > 30*time.Second {
		t.Fatalf("the run was stopped after %s, want about 10s (the claim's timeout), not the runner's 500ms", elapsed)
	}
}
