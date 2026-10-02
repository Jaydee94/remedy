package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

func newWebServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>shell</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Web: web,
	}))
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestSPAServesExistingFilesAndIndex(t *testing.T) {
	ts := newWebServer(t)

	if code, body := get(t, ts, "/assets/app.js"); code != http.StatusOK || !strings.Contains(body, "console.log") {
		t.Fatalf("asset = %d %q", code, body)
	}
	if code, body := get(t, ts, "/"); code != http.StatusOK || !strings.Contains(body, "shell") {
		t.Fatalf("root = %d %q", code, body)
	}
}

func TestSPAFallsBackToIndexForUnknownPaths(t *testing.T) {
	ts := newWebServer(t)

	code, body := get(t, ts, "/some/client/route")
	if code != http.StatusOK || !strings.Contains(body, "shell") {
		t.Fatalf("unknown route = %d %q, want the index shell", code, body)
	}
}

func TestSPAKeepsUnknownAPIPathsNotFound(t *testing.T) {
	ts := newWebServer(t)

	for _, path := range []string{"/api/nope", "/runner/nope"} {
		code, body := get(t, ts, path)
		if code != http.StatusNotFound || strings.Contains(body, "shell") {
			t.Errorf("%s = %d %q, want 404 JSON and no HTML shell", path, code, body)
		}
	}
}
