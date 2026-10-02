package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	password    = "correct horse battery"
	runnerToken = "runner-token-with-at-least-24-chars"
)

type env struct {
	ts    *httptest.Server
	store *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken}))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}
}

// adminClient returns a client with a cookie jar, logged in if login is true.
func (e *env) adminClient(t *testing.T, login bool) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if login {
		resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("login status = %d", resp.StatusCode)
		}
	}
	return c
}

func (e *env) do(t *testing.T, c *http.Client, method, path, body string, csrf bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if csrf {
		req.Header.Set("X-Remedy-CSRF", "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (e *env) runnerDo(t *testing.T, method, path, body string, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.DefaultClient, http.MethodGet, "/healthz", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAdminAPIRequiresSession(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, false)
	for _, path := range []string{"/api/me", "/api/runs"} {
		if got := e.do(t, c, http.MethodGet, path, "", false).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("GET %s without session = %d, want 401", path, got)
		}
	}
}

func TestLoginRejectsWrongPasswordAndRateLimits(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, false)
	for i := 0; i < 5; i++ {
		resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"wrong"}`, true)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i, resp.StatusCode)
		}
	}
	// Even the right password is refused while locked.
	resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked login status = %d, want 429", resp.StatusCode)
	}
}

func TestStateChangingRequestsNeedCSRFHeader(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"hi"}`, false)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if e.do(t, http.DefaultClient, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, false).StatusCode != http.StatusForbidden {
		t.Fatal("login without CSRF header should be 403")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.DefaultClient, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true)
	var found bool
	for _, ck := range resp.Cookies() {
		if ck.Name == "remedy_session" {
			found = true
			if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie flags = %+v", ck)
			}
		}
	}
	if !found {
		t.Fatal("no session cookie set")
	}
}

func TestCreateAndListRuns(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, true)

	if e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":""}`, true).StatusCode != http.StatusBadRequest {
		t.Fatal("empty prompt should be 400")
	}
	if e.do(t, c, http.MethodPost, "/api/runs", `{"provider":"gemini","prompt":"x"}`, true).StatusCode != http.StatusBadRequest {
		t.Fatal("unknown provider should be 400")
	}

	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"say pong"}`, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	created := decode[run.Run](t, resp)
	if created.Provider != "claude" || created.Status != run.Queued {
		t.Fatalf("created = %+v", created)
	}

	list := decode[[]run.Run](t, e.do(t, c, http.MethodGet, "/api/runs", "", false))
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	if e.do(t, c, http.MethodGet, "/api/runs/"+created.ID, "", false).StatusCode != http.StatusOK {
		t.Fatal("get run failed")
	}
	if e.do(t, c, http.MethodGet, "/api/runs/missing", "", false).StatusCode != http.StatusNotFound {
		t.Fatal("missing run should be 404")
	}
}

func TestRunnerAPIRequiresToken(t *testing.T) {
	e := newEnv(t)
	for _, token := range []string{"", "wrong-token-wrong-token-wrong"} {
		resp := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", token)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d, want 401", token, resp.StatusCode)
		}
	}
}

func TestRunnerLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	queued, _ := e.store.CreateRun(ctx, "claude", "say pong")

	claim := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if claim.StatusCode != http.StatusOK {
		t.Fatalf("claim status = %d", claim.StatusCode)
	}
	claimed := decode[run.Run](t, claim)
	if claimed.ID != queued.ID || claimed.Status != run.Running {
		t.Fatalf("claimed = %+v", claimed)
	}

	post := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/events",
		`{"kind":"system","payload":{"a":1}}`, runnerToken)
	if post.StatusCode != http.StatusNoContent {
		t.Fatalf("event status = %d", post.StatusCode)
	}
	if e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/events", `{"kind":"","payload":1}`, runnerToken).StatusCode != http.StatusBadRequest {
		t.Fatal("empty kind should be 400")
	}
	if e.runnerDo(t, http.MethodPost, "/runner/v1/runs/missing/events", `{"kind":"x","payload":1}`, runnerToken).StatusCode != http.StatusNotFound {
		t.Fatal("event for a missing run should be 404")
	}

	fin := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/finish",
		`{"exitCode":0,"result":"pong","sessionId":"s-1","costUsd":0.01}`, runnerToken)
	if fin.StatusCode != http.StatusNoContent {
		t.Fatalf("finish status = %d", fin.StatusCode)
	}
	got, _ := e.store.GetRun(ctx, claimed.ID)
	if got.Status != run.Succeeded || got.Result != "pong" {
		t.Fatalf("run = %+v", got)
	}
	if e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/finish", `{"exitCode":0}`, runnerToken).StatusCode != http.StatusNotFound {
		t.Fatal("finishing twice should be 404")
	}
}

func TestStreamEventsDeliversBacklogLiveEventsAndDone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.adminClient(t, true)

	r, _ := e.store.CreateRun(ctx, "claude", "x")
	_, _ = e.store.ClaimNext(ctx)
	_, _ = e.store.AppendEvent(ctx, r.ID, "system", json.RawMessage(`{"n":1}`))

	resp := e.do(t, c, http.MethodGet, "/api/runs/"+r.ID+"/events", "", false)
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+r.ID+"/events", `{"kind":"assistant","payload":{"n":2}}`, runnerToken)
		e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+r.ID+"/finish", `{"exitCode":0,"result":"ok"}`, runnerToken)
	}()

	var names []string
	sc := bufio.NewScanner(resp.Body)
	deadline := time.AfterFunc(5*time.Second, func() { _ = resp.Body.Close() })
	defer deadline.Stop()
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			names = append(names, name)
			if name == "done" {
				break
			}
		}
	}
	if strings.Join(names, ",") != "run_event,run_event,done" {
		t.Fatalf("events = %v", names)
	}
}
