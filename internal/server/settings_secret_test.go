package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/settings"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func keyedSettingsServer(t *testing.T) (*env, *http.Client) {
	t.Helper()
	st := testutil.Store(t)
	k, _ := secret.ParseKey("BQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU=")
	set := settings.New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), settings.WithKey(k))
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Settings: set}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	return e, e.adminClient(t, true)
}

func TestAMaximumLengthTokenFitsAndAHugeBodyIsRefused(t *testing.T) {
	e, c := keyedSettingsServer(t)
	full := e.do(t, c, http.MethodPut, "/api/settings/alertmanager_token", `{"value":"`+strings.Repeat("a", 4096)+`"}`, true)
	if body := string(mustReadAll(t, full)); full.StatusCode != http.StatusOK {
		t.Errorf("a 4096-byte token: %d %s", full.StatusCode, body)
	}
	huge := e.do(t, c, http.MethodPut, "/api/settings/alertmanager_token", `{"value":"`+strings.Repeat("a", 20<<10)+`"}`, true)
	if huge.StatusCode != http.StatusBadRequest {
		t.Errorf("a 20 KB body: %d", huge.StatusCode)
	}
}

func TestTheTokenIsWrittenOnceAndNeverComesBack(t *testing.T) {
	e, c := keyedSettingsServer(t)

	const token = "tok-0123456789abcdef-SECRET"
	resp := e.do(t, c, http.MethodPut, "/api/settings/alertmanager_token", `{"value":"`+token+`"}`, true)
	body := string(mustReadAll(t, resp))
	if resp.StatusCode != http.StatusOK || strings.Contains(body, token) || !strings.Contains(body, `"hint":"…CRET"`) || !strings.Contains(body, `"value":null`) || !strings.Contains(body, `"kind":"secret"`) {
		t.Fatalf("PUT = %d %s", resp.StatusCode, body)
	}
	for _, path := range []string{"/api/settings", "/api/activity"} {
		got := string(mustReadAll(t, e.do(t, c, http.MethodGet, path, "", false)))
		if strings.Contains(got, token) || strings.Contains(got, "0123456789abcdef") {
			t.Errorf("%s shows the token: %s", path, got)
		}
	}
	bad := e.do(t, c, http.MethodPut, "/api/settings/alertmanager_token", `{"value":"bad\nvalue-with-secret-part"}`, true)
	if msg := string(mustReadAll(t, bad)); bad.StatusCode != http.StatusBadRequest || strings.Contains(msg, "secret-part") {
		t.Errorf("a refused token: %d %s", bad.StatusCode, msg)
	}
	if got := e.do(t, c, http.MethodDelete, "/api/settings/alertmanager_token", "", true).StatusCode; got != http.StatusNoContent {
		t.Errorf("DELETE = %d", got)
	}
}
