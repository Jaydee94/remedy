package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// newSettingsEnv is a server with settings over a new store; the settings tests of this package use it.
func newSettingsEnv(t *testing.T, tune func(*server.Deps)) (*env, *settings.Settings) {
	t.Helper()
	st := testutil.Store(t)
	set := settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set}
	if tune != nil {
		tune(&d)
	}
	ts := httptest.NewServer(server.New(d))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}, set
}

func claimTimeoutSeconds(t *testing.T, e *env) (int, bool) {
	t.Helper()
	if _, err := e.store.CreateRun(context.Background(), "claude", "hello"); err != nil {
		t.Fatal(err)
	}
	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d", resp.StatusCode)
	}
	got := decode[map[string]any](t, resp)
	v, ok := got["run_timeout_seconds"]
	if !ok {
		return 0, false
	}
	return int(v.(float64)), true
}

// A runner whose own REMEDY_RUN_TIMEOUT is set must keep it while the control plane has nothing set for it.
func TestTheClaimLeavesTheRunTimeoutOutWhileTheSettingIsTheDefault(t *testing.T) {
	e, _ := newSettingsEnv(t, nil)
	if secs, ok := claimTimeoutSeconds(t, e); ok {
		t.Fatalf("run_timeout_seconds = %d, want the field left out", secs)
	}
}

func TestTheClaimCarriesARunTimeoutFromTheEnvironmentLayer(t *testing.T) {
	st := testutil.Store(t)
	set := settings.New(context.Background(), st, settings.Layer{settings.RunTimeout: {Dur: 20 * time.Minute}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	secs, ok := claimTimeoutSeconds(t, e)
	if !ok || secs != 1200 {
		t.Fatalf("run_timeout_seconds = %d (present %v), want 1200", secs, ok)
	}
}

func TestTheClaimCarriesTheRunTimeoutOfTheSettings(t *testing.T) {
	e, set := newSettingsEnv(t, nil)
	if _, err := set.Set(context.Background(), settings.RunTimeout, json.RawMessage(`"25m"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateRun(context.Background(), "claude", "hello"); err != nil {
		t.Fatal(err)
	}
	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d", resp.StatusCode)
	}
	got := decode[struct {
		Seconds int `json:"run_timeout_seconds"`
	}](t, resp)
	if want := int((25 * time.Minute).Seconds()); got.Seconds != want {
		t.Fatalf("run_timeout_seconds = %d, want %d", got.Seconds, want)
	}
}
