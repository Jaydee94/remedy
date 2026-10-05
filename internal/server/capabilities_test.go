package server_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// capsEnv is a server with the given cluster capabilities and a signed-in admin client.
func capsEnv(t *testing.T, cluster server.Cluster) (*env, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Cluster: cluster}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return e, c
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func TestCapabilitiesSayThereIsNoClusterByDefault(t *testing.T) {
	e, c := capsEnv(t, server.Cluster{})
	resp := e.do(t, c, http.MethodGet, "/api/capabilities", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := bodyOf(t, resp), `{"cluster":{"read":false,"write":false,"namespaces":[]}}`+"\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestCapabilitiesSayWhatIsConfigured(t *testing.T) {
	e, c := capsEnv(t, server.Cluster{Read: true, Write: true, Namespaces: []string{"demo", "staging"}})
	resp := e.do(t, c, http.MethodGet, "/api/capabilities", "", false)
	if got, want := bodyOf(t, resp), `{"cluster":{"read":true,"write":true,"namespaces":["demo","staging"]}}`+"\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestCapabilitiesNeedASession(t *testing.T) {
	e, _ := capsEnv(t, server.Cluster{Read: true})
	anonymous := &http.Client{}
	if resp := e.do(t, anonymous, http.MethodGet, "/api/capabilities", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
