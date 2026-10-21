package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

type relayStack struct {
	ts     *httptest.Server
	st     *store.Store
	client *http.Client
	app    *app.App
}

func newRelayStack(t *testing.T, relay bool) *relayStack {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Server{
		AdminPassword: password, RunnerToken: runnerToken, MasterKey: key, PollInterval: time.Minute,
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20, LoginRelay: relay,
	}
	a := app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	s := &relayStack{ts: ts, st: st, client: &http.Client{Jar: jar}, app: a}
	if resp, _ := s.admin(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return s
}

func (s *relayStack) admin(t *testing.T, method, path, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, s.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

// runner posts as the runner; a timeout ends a poll that the manager holds (it notes the poll at once).
func (s *relayStack) runner(t *testing.T, path, body string, timeout time.Duration) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func loginFact(t *testing.T, s *relayStack) string {
	t.Helper()
	resp, body := s.admin(t, http.MethodGet, "/api/settings", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/settings = %d %s", resp.StatusCode, body)
	}
	var v struct {
		Facts []struct{ Label, Value string } `json:"facts"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	for _, f := range v.Facts {
		if f.Label == "Login from this page" {
			return f.Value
		}
	}
	t.Fatalf("no fact Login from this page in %s", body)
	return ""
}

func TestTheSetupFactAndActivity(t *testing.T) {
	off := newRelayStack(t, false)
	if got := loginFact(t, off); got != "off" {
		t.Errorf("fact with the relay off = %q, want off", got)
	}
	if off.app.Login != nil {
		t.Error("the relay is off but the app built a manager")
	}
	if resp, _ := off.admin(t, http.MethodGet, "/api/runner/login", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/runner/login with the relay off = %d, want 404", resp.StatusCode)
	}

	on := newRelayStack(t, true)
	if got := loginFact(t, on); got != "on" {
		t.Errorf("fact with the relay on = %q, want on", got)
	}
	if on.app.Login == nil {
		t.Fatal("the relay is on but the app has no manager")
	}
	// A queued run waits for the session: the view counts it.
	if _, err := on.st.CreateRun(context.Background(), "claude", "p"); err != nil {
		t.Fatal(err)
	}
	on.runner(t, "/runner/v1/login/poll", `{"boot":"b1"}`, 300*time.Millisecond)
	resp, body := on.admin(t, http.MethodPost, "/api/runner/login", `{"replace":false}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	var sess struct {
		ID          string `json:"id"`
		WaitingRuns int    `json:"waitingRuns"`
	}
	if err := json.Unmarshal([]byte(body), &sess); err != nil || sess.WaitingRuns != 1 {
		t.Fatalf("session = %s (%v), want waitingRuns 1", body, err)
	}
	if code, raw := on.runner(t, "/runner/v1/login/poll", `{"boot":"b1"}`, 5*time.Second); code != http.StatusOK || !strings.Contains(raw, `"start"`) {
		t.Fatalf("poll = %d %s, want the start", code, raw)
	}
	rep, _ := json.Marshal(map[string]any{"session": sess.ID, "state": "awaiting_code", "attempt": 1, "url": "https://evil-relay.test/x"})
	if code, raw := on.runner(t, "/runner/v1/login/report", string(rep), 5*time.Second); code != http.StatusNoContent {
		t.Fatalf("report = %d %s", code, raw)
	}

	log, err := on.st.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ summary, data string }{
		store.KindLoginStarted: {"The runner's login was started from the Setup page.", `{}`},
		store.KindLoginFailed:  {"The runner's login from the Setup page did not work.", `{"reason":"bad_url"}`},
	}
	seen := 0
	for _, a := range log {
		w, ok := want[a.Kind]
		if !ok {
			continue
		}
		seen++
		if a.Summary != w.summary || string(a.Data) != w.data {
			t.Errorf("%s = %q %s, want %q %s", a.Kind, a.Summary, a.Data, w.summary, w.data)
		}
		if a.RunID != "" || a.IncidentID != 0 || a.RepoID != 0 {
			t.Errorf("%s links to something: %+v", a.Kind, a)
		}
		if strings.Contains(a.Summary+string(a.Data), "evil-relay") || strings.Contains(a.Summary+string(a.Data), sess.ID) {
			t.Errorf("%s holds the address or the session id: %+v", a.Kind, a)
		}
	}
	if seen != 2 {
		t.Errorf("found %d login entries, want 2: %+v", seen, log)
	}
}
