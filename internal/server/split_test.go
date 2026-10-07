package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// splitServers starts the two handlers of NewSplit on two test servers: the public one with a UI, the internal one.
func splitServers(t *testing.T) (public, internal *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gate := gatekeeper.New(gatekeeper.Config{
		Store: st,
		Tools: gatekeeper.IncidentTools(st),
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	pub, in := server.NewSplit(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Gatekeeper: gate,
		Web: fstest.MapFS{"index.html": {Data: []byte("<html>shell</html>")}},
	})
	public, internal = httptest.NewServer(pub), httptest.NewServer(in)
	t.Cleanup(public.Close)
	t.Cleanup(internal.Close)
	return public, internal
}

func send(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestThePublicHandlerServesTheUIAndTheAdminAPI(t *testing.T) {
	public, _ := splitServers(t)
	if code, _ := send(t, http.MethodGet, public.URL+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	if code, body := send(t, http.MethodGet, public.URL+"/"); code != http.StatusOK || !strings.Contains(body, "shell") {
		t.Fatalf("/ = %d %q", code, body)
	}
	if code, _ := send(t, http.MethodGet, public.URL+"/api/me"); code != http.StatusUnauthorized {
		t.Fatalf("/api/me without a session = %d, want 401", code)
	}
}

func TestThePublicHandlerDoesNotServeTheRunnerOrGatekeeperRoutes(t *testing.T) {
	public, _ := splitServers(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/runner/v1/claim"},
		{http.MethodPost, "/runner/v1/runs/x/events"},
		{http.MethodGet, "/runner/v1/runs/x/snapshot"},
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/mcp"},
		{http.MethodPost, "/mcp/anything"},
	} {
		code, body := send(t, c.method, public.URL+c.path)
		if code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, code)
		}
		if strings.Contains(body, "shell") {
			t.Errorf("%s %s returned the UI's index", c.method, c.path)
		}
	}
}

func TestTheInternalHandlerServesOnlyTheRunnerAndGatekeeperRoutes(t *testing.T) {
	_, internal := splitServers(t)
	if code, _ := send(t, http.MethodGet, internal.URL+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	// The routes are there: they answer for a missing credential, not for a missing route.
	if code, _ := send(t, http.MethodPost, internal.URL+"/runner/v1/claim"); code != http.StatusUnauthorized {
		t.Fatalf("/runner/v1/claim without the token = %d, want 401", code)
	}
	if code, _ := send(t, http.MethodPost, internal.URL+"/mcp"); code != http.StatusUnauthorized {
		t.Fatalf("/mcp without a run token = %d, want 401", code)
	}
	for _, path := range []string{"/api/me", "/api/runs", "/api/approvals", "/"} {
		if code, _ := send(t, http.MethodGet, internal.URL+path); code != http.StatusNotFound {
			t.Errorf("GET %s on the internal port = %d, want 404", path, code)
		}
	}
}
