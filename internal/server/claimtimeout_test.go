package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/store"
)

// newSettingsEnv is a server with settings over a new store; the settings tests of this package use it.
func newSettingsEnv(t *testing.T, tune func(*server.Deps)) (*env, *settings.Settings) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set}
	if tune != nil {
		tune(&d)
	}
	ts := httptest.NewServer(server.New(d))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}, set
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
