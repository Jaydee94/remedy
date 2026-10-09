package server_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// splitRunnerServers starts the two handlers of NewSplit with a RunnerStatus.
func splitRunnerServers(t *testing.T) (public, internal *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pub, in := server.NewSplit(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, RunnerStatus: server.NewRunnerStatus(),
		Web: fstest.MapFS{"index.html": {Data: []byte("<html>shell</html>")}},
	})
	public, internal = httptest.NewServer(pub), httptest.NewServer(in)
	t.Cleanup(public.Close)
	t.Cleanup(internal.Close)
	return public, internal
}

func splitRunnerPost(t *testing.T, url, path, body, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// splitRunnerView signs in on the public listener and reads GET /api/runner there.
func splitRunnerView(t *testing.T, public *httptest.Server) runnerView {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	req, _ := http.NewRequest(http.MethodPost, public.URL+"/api/login", strings.NewReader(`{"password":"`+password+`"}`))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	resp, err = c.Get(public.URL + "/api/runner")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runner on the public listener = %d", resp.StatusCode)
	}
	return decode[runnerView](t, resp)
}

func TestTheStatusReportIsOnTheInternalListenerAndTheViewOnThePublicOne(t *testing.T) {
	public, internal := splitRunnerServers(t)
	body := `{"login":"ok","cliVersion":"2.1.288"}`

	// The public listener does not serve the report route, not even with the right token.
	code, resp := splitRunnerPost(t, public.URL, "/runner/v1/status", body, runnerToken)
	if code != http.StatusNotFound || strings.Contains(resp, "shell") {
		t.Fatalf("POST /runner/v1/status on the public listener = %d %q, want 404", code, resp)
	}
	if v := splitRunnerView(t, public); v.Connected || v.Login != "unknown" {
		t.Fatalf("a report to the public listener changed the view: %+v", v)
	}

	// The internal one does, and the view on the public listener shows it.
	if code, _ := splitRunnerPost(t, internal.URL, "/runner/v1/status", body, runnerToken); code != http.StatusNoContent {
		t.Fatalf("POST /runner/v1/status on the internal listener = %d, want 204", code)
	}
	if v := splitRunnerView(t, public); !v.Connected || v.Login != "ok" || v.CLIVersion != "2.1.288" {
		t.Fatalf("view = %+v", v)
	}

	// GET /api/runner is not on the internal listener.
	if resp, err := http.Get(internal.URL + "/api/runner"); err != nil {
		t.Fatal(err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /api/runner on the internal listener = %d, want 404", resp.StatusCode)
		}
	}
}

func TestAnyAuthenticatedRunnerRequestMakesTheRunnerConnectedButAnUnauthenticatedOneDoesNot(t *testing.T) {
	e := newRunnerEnv(t)
	admin := e.adminClient(t, true)

	// Refused requests (no token, wrong token) do not touch.
	for _, tok := range []string{"", "wrong-token-wrong-token-wrong-t"} {
		_ = e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", tok)
	}
	if v := getRunnerView(t, e, admin); v.Connected || v.LastSeenAt != nil {
		t.Fatalf("an unauthenticated request touched the runner: %+v", v)
	}

	// A claim (with a run queued, so it answers at once) counts, without any status report.
	if _, err := e.store.CreateRun(t.Context(), "claude", "say pong"); err != nil {
		t.Fatal(err)
	}
	if resp := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", runnerToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d", resp.StatusCode)
	}
	v := getRunnerView(t, e, admin)
	if !v.Connected || v.LastSeenAt == nil || time.Since(*v.LastSeenAt) > time.Minute {
		t.Fatalf("an authenticated claim did not make the runner connected: %+v", v)
	}
	if v.Login != "unknown" || v.LoginCheckedAt != nil {
		t.Fatalf("no report was made, yet the view says %+v", v)
	}
}
