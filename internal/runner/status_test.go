package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestStatusStartsUnknownAndNotConnected(t *testing.T) {
	s := runner.NewStatus()
	if s.Connected() {
		t.Fatal("a new status must not be connected")
	}
	r := s.Report()
	if r.Login != "unknown" || !r.LoginCheckedAt.IsZero() || r.CLIVersion != "" {
		t.Fatalf("report = %+v", r)
	}
}

func TestStatusKeepsWhatItIsTold(t *testing.T) {
	s := runner.NewStatus()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.SetConnected(true)
	s.SetLogin(provider.LoginMissing, at)
	s.SetCLIVersion("2.1.288 (Claude Code)")
	if !s.Connected() {
		t.Fatal("not connected")
	}
	r := s.Report()
	if r.Login != "missing" || !r.LoginCheckedAt.Equal(at) || r.CLIVersion != "2.1.288 (Claude Code)" {
		t.Fatalf("report = %+v", r)
	}
	raw, _ := json.Marshal(r)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	if back["login"] != "missing" || back["loginCheckedAt"] == nil || back["cliVersion"] != "2.1.288 (Claude Code)" {
		t.Fatalf("json = %s", raw)
	}
}

func TestANilStatusDoesNothing(t *testing.T) {
	var s *runner.Status
	s.SetConnected(true)
	s.SetLogin(provider.LoginOK, time.Now())
	s.SetCLIVersion("x")
	if s.Connected() {
		t.Fatal("a nil status is never connected")
	}
	if s.Report().Login != "unknown" {
		t.Fatal("a nil status reports unknown")
	}
}

func get(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestTheProbesLiveAlwaysAndReadyWhenConnectedWhateverTheLogin(t *testing.T) {
	s := runner.NewStatus()
	h := runner.StatusHandler(s)
	if got := get(t, h, "/livez"); got != http.StatusOK {
		t.Fatalf("/livez = %d, want 200", got)
	}
	if got := get(t, h, "/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("/readyz before the control plane answered = %d, want 503", got)
	}
	s.SetConnected(true)
	s.SetLogin(provider.LoginMissing, time.Now())
	if got := get(t, h, "/readyz"); got != http.StatusOK {
		t.Fatalf("/readyz connected but not logged in = %d, want 200: the login must never gate readiness", got)
	}
	s.SetConnected(false)
	if got := get(t, h, "/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("/readyz after a failure = %d, want 503", got)
	}
	if got := get(t, h, "/livez"); got != http.StatusOK {
		t.Fatalf("/livez after a failure = %d, want 200", got)
	}
	if got := get(t, h, "/other"); got != http.StatusNotFound {
		t.Fatalf("/other = %d, want 404", got)
	}
}

func TestTheLoopTracksWhetherTheControlPlaneAnswers(t *testing.T) {
	var up atomic.Bool
	up.Store(true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Millisecond) // a real claim long-polls; do not spin
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	status := runner.NewStatus()
	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: "runner-token-with-at-least-24-chars", HTTP: ts.Client()},
		WorkspaceRoot: t.TempDir(),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:       20 * time.Millisecond,
		Status:        status,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "connected after an answer", status.Connected)
	up.Store(false)
	eventually(t, "not connected after a failed claim", func() bool { return !status.Connected() })
	up.Store(true)
	eventually(t, "connected again", status.Connected)
}
