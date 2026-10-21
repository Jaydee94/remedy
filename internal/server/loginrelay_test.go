package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// relayURL is a sign-in address ValidateURL accepts; its values are fake.
const relayURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=planted-client&response_type=code&redirect_uri=x&scope=x&code_challenge=planted-challenge&state=planted-state"

// relayCode is a code that ValidateCode accepts and that appears nowhere else.
const relayCode = "PLANTEDcode-7f3a91"

type loginEnv struct {
	*env
	admin  *http.Client
	status *server.RunnerStatus
	mgr    *loginrelay.Manager
}

// writeLoginActivity stands in for the app's activity hook (app.loginActivity), which this package cannot reach: one
// entry per event, data a reason word or nothing.
func writeLoginActivity(st *store.Store, ev loginrelay.Event) {
	kind := map[loginrelay.EventKind]string{
		loginrelay.EventStarted: store.KindLoginStarted, loginrelay.EventSucceeded: store.KindLoginSucceeded,
		loginrelay.EventFailed: store.KindLoginFailed, loginrelay.EventCancelled: store.KindLoginCancelled,
	}[ev.Kind]
	var data json.RawMessage
	if ev.Reason != "" {
		data, _ = json.Marshal(map[string]string{"reason": string(ev.Reason)})
	}
	_ = st.AddActivity(context.Background(), store.NewActivity{Kind: kind, Summary: "login " + string(ev.Kind), Data: data})
}

func newLoginEnv(t *testing.T) *loginEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	status := server.NewRunnerStatus()
	mgr := loginrelay.New(loginrelay.Config{
		PollWait: 200 * time.Millisecond,
		RunActive: func(ctx context.Context) (bool, error) {
			running, _, err := st.RunCounts(ctx)
			return running > 0, err
		},
		OnEvent: func(ev loginrelay.Event) { writeLoginActivity(st, ev) },
	})
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, RunnerStatus: status, Login: mgr,
	}))
	t.Cleanup(ts.Close)
	le := &loginEnv{env: &env{ts: ts, store: st}, status: status, mgr: mgr}
	le.admin = le.adminClient(t, true)
	return le
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// call is an admin request with the CSRF header.
func (le *loginEnv) call(t *testing.T, method, path, body string) (*http.Response, string) {
	t.Helper()
	resp := le.do(t, le.admin, method, path, body, true)
	return resp, readAll(t, resp)
}

// rpost plays the runner: a POST with the runner token.
func (le *loginEnv) rpost(t *testing.T, path, body string) (*http.Response, string) {
	t.Helper()
	resp := le.runnerDo(t, http.MethodPost, path, body, runnerToken)
	return resp, readAll(t, resp)
}

// online makes the runner seen: a poll without a session, answered 204 after the short poll wait.
func (le *loginEnv) online(t *testing.T) {
	t.Helper()
	if resp, body := le.rpost(t, "/runner/v1/login/poll", `{"boot":"b1"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("poll without a session = %d %s, want 204", resp.StatusCode, body)
	}
}

// start opens a session and returns its id.
func (le *loginEnv) start(t *testing.T) string {
	t.Helper()
	resp, body := le.call(t, http.MethodPost, "/api/runner/login", `{"replace":false}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start = %d %s, want 201", resp.StatusCode, body)
	}
	var v struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.ID == "" || v.State != "starting" {
		t.Fatalf("start body = %s (%v)", body, err)
	}
	return v.ID
}

type wireCommand struct {
	Seq     int64  `json:"seq"`
	Session string `json:"session"`
	Action  string `json:"action"`
	Code    string `json:"code"`
}

// poll is the runner's poll that must answer a command.
func (le *loginEnv) poll(t *testing.T, body string) wireCommand {
	t.Helper()
	resp, raw := le.rpost(t, "/runner/v1/login/poll", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("poll %s = %d %s, want 200", body, resp.StatusCode, raw)
	}
	var c wireCommand
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("poll answer %s: %v", raw, err)
	}
	return c
}

// awaitCode starts a session and plays the runner up to awaiting_code.
func (le *loginEnv) awaitCode(t *testing.T) string {
	t.Helper()
	le.online(t)
	id := le.start(t)
	if c := le.poll(t, `{"boot":"b1"}`); c.Action != "start" || c.Session != id || c.Seq != 1 {
		t.Fatalf("first command = %+v, want start of %s", c, id)
	}
	rep, _ := json.Marshal(map[string]any{"session": id, "state": "awaiting_code", "attempt": 1, "url": relayURL})
	if resp, body := le.rpost(t, "/runner/v1/login/report", string(rep)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report awaiting_code = %d %s", resp.StatusCode, body)
	}
	return id
}

type loginView struct {
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable"`
	Session     *struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Attempt     int    `json:"attempt"`
		URL         string `json:"url"`
		Reason      string `json:"reason"`
		ExpiresAt   string `json:"expiresAt"`
		WaitingRuns *int   `json:"waitingRuns"`
	} `json:"session"`
}

func (le *loginEnv) view(t *testing.T) (loginView, string) {
	t.Helper()
	resp, body := le.call(t, http.MethodGet, "/api/runner/login", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runner/login = %d %s", resp.StatusCode, body)
	}
	var v loginView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("view %s: %v", body, err)
	}
	return v, body
}

var loginPaths = []struct{ method, path string }{
	{http.MethodGet, "/api/runner/login"},
	{http.MethodPost, "/api/runner/login"},
	{http.MethodPost, "/api/runner/login/code"},
	{http.MethodPost, "/api/runner/login/cancel"},
}

func TestLoginRelayRoutesAbsentWithoutAManager(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, RunnerStatus: server.NewRunnerStatus()}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	admin := e.adminClient(t, true)
	for _, p := range loginPaths {
		if got := e.do(t, admin, p.method, p.path, `{}`, true).StatusCode; got != http.StatusNotFound {
			t.Errorf("%s %s without a manager = %d, want 404", p.method, p.path, got)
		}
	}
	for _, p := range []string{"/runner/v1/login/poll", "/runner/v1/login/report"} {
		if got := e.runnerDo(t, http.MethodPost, p, `{}`, runnerToken).StatusCode; got != http.StatusNotFound {
			t.Errorf("POST %s without a manager = %d, want 404", p, got)
		}
	}
}

func TestLoginAdminRoutesNeedASessionAndCSRF(t *testing.T) {
	le := newLoginEnv(t)
	anon := le.adminClient(t, false)
	for _, p := range loginPaths {
		resp := le.do(t, anon, p.method, p.path, `{}`, true)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a cookie = %d, want 401", p.method, p.path, resp.StatusCode)
		}
		if p.method == http.MethodGet {
			continue
		}
		resp = le.do(t, le.admin, p.method, p.path, `{}`, false)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s without the CSRF header = %d, want 403", p.method, p.path, resp.StatusCode)
		}
	}
	if v, _ := le.view(t); v.Session != nil {
		t.Errorf("a refused request opened a session: %+v", v.Session)
	}
}

func TestLoginAnswersAreNoStore(t *testing.T) {
	le := newLoginEnv(t)
	check := func(what string, resp *http.Response, want int) {
		t.Helper()
		if resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", what, resp.StatusCode, want)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s (%d): Cache-Control = %q, want no-store", what, resp.StatusCode, got)
		}
	}
	anon := le.adminClient(t, false)
	check("GET without a cookie", le.do(t, anon, http.MethodGet, "/api/runner/login", "", false), http.StatusUnauthorized)
	check("start without CSRF", le.do(t, le.admin, http.MethodPost, "/api/runner/login", `{}`, false), http.StatusForbidden)
	check("code with a bad body", le.do(t, le.admin, http.MethodPost, "/api/runner/login/code", `{"x":1}`, true), http.StatusBadRequest)
	check("start while offline", le.do(t, le.admin, http.MethodPost, "/api/runner/login", `{}`, true), http.StatusConflict)
	check("GET", le.do(t, le.admin, http.MethodGet, "/api/runner/login", "", false), http.StatusOK)
	check("runner poll", le.runnerDo(t, http.MethodPost, "/runner/v1/login/poll", `{"boot":"b1"}`, runnerToken), http.StatusNoContent)
	check("runner report with a bad body", le.runnerDo(t, http.MethodPost, "/runner/v1/login/report", `{`, runnerToken), http.StatusBadRequest)
	check("runner report unknown session", le.runnerDo(t, http.MethodPost, "/runner/v1/login/report",
		`{"session":"nope","state":"failed","attempt":1,"reason":"no_url"}`, runnerToken), http.StatusConflict)
	check("runner poll with a wrong token", le.runnerDo(t, http.MethodPost, "/runner/v1/login/poll", `{}`, "wrong"), http.StatusUnauthorized)

	id := ""
	for i := range 5 {
		resp := le.do(t, le.admin, http.MethodPost, "/api/runner/login", `{}`, true)
		check("start "+strconv.Itoa(i+1), resp, http.StatusCreated)
		var v struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&v)
		id = v.ID
		if i < 4 {
			check("cancel", le.do(t, le.admin, http.MethodPost, "/api/runner/login/cancel", `{"session":"`+id+`"}`, true), http.StatusNoContent)
		}
	}
	// Move the open session to awaiting_code, then submit a code.
	le.poll(t, `{"boot":"b1"}`)
	rep, _ := json.Marshal(map[string]any{"session": id, "state": "awaiting_code", "attempt": 1, "url": relayURL})
	check("runner report", le.runnerDo(t, http.MethodPost, "/runner/v1/login/report", string(rep), runnerToken), http.StatusNoContent)
	check("runner poll that acknowledges the start", le.runnerDo(t, http.MethodPost, "/runner/v1/login/poll", `{"boot":"b1","session":"`+id+`","ack":1}`, runnerToken), http.StatusNoContent)
	check("code", le.do(t, le.admin, http.MethodPost, "/api/runner/login/code", `{"session":"`+id+`","code":"`+relayCode+`"}`, true), http.StatusAccepted)
	check("runner poll with the code", le.runnerDo(t, http.MethodPost, "/runner/v1/login/poll", `{"boot":"b1","session":"`+id+`","ack":1}`, runnerToken), http.StatusOK)
	check("cancel", le.do(t, le.admin, http.MethodPost, "/api/runner/login/cancel", `{"session":"`+id+`"}`, true), http.StatusNoContent)
	check("the sixth start", le.do(t, le.admin, http.MethodPost, "/api/runner/login", `{}`, true), http.StatusTooManyRequests)
}

func TestStartRefusals(t *testing.T) {
	sentence := map[string]string{
		"runner_offline":   "The runner is not connected, so it cannot start a login.",
		"run_active":       "A run is running. Start the login when it has ended.",
		"already_active":   "A login is already in progress.",
		"replace_required": "The runner is logged in already. Confirm that you want to log in again.",
		"api_key_mode":     "The runner uses an API key, so there is no login to start.",
	}
	cases := []struct {
		reason  string
		prepare func(t *testing.T, le *loginEnv) (keep string)
	}{
		{"runner_offline", func(*testing.T, *loginEnv) string { return "" }},
		{"run_active", func(t *testing.T, le *loginEnv) string {
			le.online(t)
			if _, err := le.store.CreateRun(context.Background(), "claude", "p"); err != nil {
				t.Fatal(err)
			}
			if r, err := le.store.ClaimNext(context.Background()); err != nil || r == nil {
				t.Fatalf("ClaimNext = %v, %v", r, err)
			}
			return ""
		}},
		{"already_active", func(t *testing.T, le *loginEnv) string {
			le.online(t)
			return le.start(t)
		}},
		{"replace_required", func(t *testing.T, le *loginEnv) string {
			le.online(t)
			if resp, body := le.rpost(t, "/runner/v1/status", `{"login":"ok"}`); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d %s", resp.StatusCode, body)
			}
			return ""
		}},
		{"api_key_mode", func(t *testing.T, le *loginEnv) string {
			le.online(t)
			le.status.SetAuthMode("api_key")
			return ""
		}},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			le := newLoginEnv(t)
			keep := c.prepare(t, le)
			resp, body := le.call(t, http.MethodPost, "/api/runner/login", `{"replace":false}`)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("start = %d %s, want 409", resp.StatusCode, body)
			}
			var got struct {
				Error  string `json:"error"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatal(err)
			}
			if got.Reason != c.reason || got.Error != sentence[c.reason] {
				t.Errorf("body = %+v, want reason %s and %q", got, c.reason, sentence[c.reason])
			}
			v, _ := le.view(t)
			switch {
			case keep == "" && v.Session != nil:
				t.Errorf("a refused start left a session: %+v", v.Session)
			case keep != "" && (v.Session == nil || v.Session.ID != keep):
				t.Errorf("the refused start changed the open session: %+v, want %s", v.Session, keep)
			}
			if c.reason == "api_key_mode" && (v.Available || v.Unavailable != "api_key_mode") {
				t.Errorf("view = %+v, want unavailable api_key_mode", v)
			}
			if c.reason == "replace_required" {
				if resp, body := le.call(t, http.MethodPost, "/api/runner/login", `{"replace":true}`); resp.StatusCode != http.StatusCreated {
					t.Errorf("start with replace = %d %s, want 201", resp.StatusCode, body)
				}
			}
		})
	}
}

func TestTheCodeRoute(t *testing.T) {
	le := newLoginEnv(t)
	id := le.awaitCode(t)

	v, _ := le.view(t)
	if v.Session == nil || v.Session.State != "awaiting_code" || v.Session.URL != relayURL || !v.Available {
		t.Fatalf("view while awaiting_code = %+v", v)
	}

	codePost := func(body string) (*http.Response, string) {
		return le.call(t, http.MethodPost, "/api/runner/login/code", body)
	}
	if resp, body := codePost(`{"session":"` + id + `","code":"BADCODE with-space"}`); resp.StatusCode != http.StatusBadRequest || strings.Contains(body, "BADCODE") || strings.Contains(body, "with-space") {
		t.Errorf("code with a space = %d %s, want 400 without the code", resp.StatusCode, body)
	}
	if resp, body := codePost(`{"session":"` + id + `","code":"` + relayCode + `","x":1}`); resp.StatusCode != http.StatusBadRequest || strings.Contains(body, relayCode) {
		t.Errorf("unknown field = %d %s, want 400", resp.StatusCode, body)
	}
	if resp, _ := codePost(`{"session":"` + id + `","code":"` + strings.Repeat("a", 1100) + `"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a body over 1 KiB = %d, want 400", resp.StatusCode)
	}
	if resp, body := codePost(`{"session":"other","code":"` + relayCode + `"}`); resp.StatusCode != http.StatusConflict || strings.Contains(body, relayCode) {
		t.Errorf("another id = %d %s, want 409", resp.StatusCode, body)
	}

	if resp, body := codePost(`{"session":"` + id + `","code":"  ` + relayCode + `  "}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("code = %d %s, want 202", resp.StatusCode, body)
	}
	// A second tab posts after the first moved the session to submitting.
	if resp, body := codePost(`{"session":"` + id + `","code":"` + relayCode + `"}`); resp.StatusCode != http.StatusConflict || strings.Contains(body, relayCode) {
		t.Errorf("code while submitting = %d %s, want 409", resp.StatusCode, body)
	}
	c := le.poll(t, `{"boot":"b1","session":"`+id+`","ack":1}`)
	if c.Action != "code" || c.Code != relayCode || c.Session != id || c.Seq != 2 {
		t.Errorf("command = %+v, want the trimmed code", c)
	}
	if _, body := le.view(t); strings.Contains(body, relayCode) || strings.Contains(body, "planted-challenge") {
		t.Errorf("the view while submitting holds the code or the URL: %s", body)
	}

	// Ten submissions named the session (the good one, the 409 of the second tab and these eight); the eleventh is refused.
	for i := range 8 {
		if resp, _ := codePost(`{"session":"` + id + `","code":"` + relayCode + `"}`); resp.StatusCode != http.StatusConflict {
			t.Fatalf("submission %d = %d, want 409", i+3, resp.StatusCode)
		}
	}
	resp, body := codePost(`{"session":"` + id + `","code":"` + relayCode + `"}`)
	if resp.StatusCode != http.StatusTooManyRequests || strings.Contains(body, relayCode) {
		t.Fatalf("the eleventh submission = %d %s, want 429", resp.StatusCode, body)
	}
	if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || n <= 0 {
		t.Errorf("Retry-After = %q, want a whole number above 0", resp.Header.Get("Retry-After"))
	}

	rep, _ := json.Marshal(map[string]any{"session": id, "state": "succeeded", "attempt": 1})
	if resp, body := le.rpost(t, "/runner/v1/login/report", string(rep)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report succeeded = %d %s", resp.StatusCode, body)
	}
	v, body = le.view(t)
	if v.Session == nil || v.Session.State != "succeeded" || strings.Contains(body, relayCode) || strings.Contains(body, "planted-challenge") {
		t.Errorf("the view after success = %s", body)
	}
}

func TestCancelRoute(t *testing.T) {
	le := newLoginEnv(t)
	le.online(t)
	id := le.start(t)
	le.poll(t, `{"boot":"b1"}`)
	if resp, body := le.call(t, http.MethodPost, "/api/runner/login/cancel", `{"session":"`+id+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d %s, want 204", resp.StatusCode, body)
	}
	if c := le.poll(t, `{"boot":"b1","session":"`+id+`","ack":1}`); c.Action != "cancel" || c.Session != id {
		t.Errorf("command after cancel = %+v, want cancel", c)
	}
	if v, _ := le.view(t); v.Session == nil || v.Session.State != "cancelled" {
		t.Errorf("view after cancel = %+v", v.Session)
	}
	for _, body := range []string{`{"session":"` + id + `"}`, `{"session":"nope"}`} {
		if resp, raw := le.call(t, http.MethodPost, "/api/runner/login/cancel", body); resp.StatusCode != http.StatusConflict {
			t.Errorf("cancel %s with no open session = %d %s, want 409", body, resp.StatusCode, raw)
		}
	}
	if resp, _ := le.call(t, http.MethodPost, "/api/runner/login/cancel", `{"session":"x","y":1}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("cancel with an unknown field = %d, want 400", resp.StatusCode)
	}
}

func TestRunnerRoutesRefuseAWrongToken(t *testing.T) {
	le := newLoginEnv(t)
	for _, p := range []string{"/runner/v1/login/poll", "/runner/v1/login/report"} {
		for _, tok := range []string{"", "wrong-token-with-at-least-24-chars"} {
			if got := le.runnerDo(t, http.MethodPost, p, `{"boot":"b1"}`, tok).StatusCode; got != http.StatusUnauthorized {
				t.Errorf("POST %s with token %q = %d, want 401", p, tok, got)
			}
		}
	}
}

func TestPollAnswers204OrACommand(t *testing.T) {
	le := newLoginEnv(t)
	le.online(t)
	if resp, _ := le.rpost(t, "/runner/v1/login/poll", `{"boot":"b1","x":1}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("poll with an unknown field = %d, want 400", resp.StatusCode)
	}
	id := le.start(t)
	resp, raw := le.rpost(t, "/runner/v1/login/poll", `{"boot":"b1"}`)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("poll = %d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(raw), &c); err != nil || c["action"] != "start" || c["session"] != id || c["seq"] != float64(1) {
		t.Errorf("poll answer = %s", raw)
	}
	if _, ok := c["code"]; ok {
		t.Errorf("a start carries a code field: %s", raw)
	}
}

func TestReportIsStrict(t *testing.T) {
	le := newLoginEnv(t)
	id := le.awaitCode(t)
	for name, body := range map[string]string{
		"unknown field": `{"session":"` + id + `","state":"failed","attempt":1,"reason":"no_url","x":1}`,
		"trailing data": `{"session":"` + id + `","state":"failed","attempt":1,"reason":"no_url"} {}`,
		"5 KiB":         `{"session":"` + id + `","state":"failed","attempt":1,"reason":"no_url","url":"` + strings.Repeat("a", 5<<10) + `"}`,
	} {
		if resp, raw := le.rpost(t, "/runner/v1/login/report", body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, resp.StatusCode, raw)
		}
	}
	if v, _ := le.view(t); v.Session == nil || v.Session.State != "awaiting_code" {
		t.Errorf("a refused report changed the session: %+v", v.Session)
	}
}

func TestAHostileReportedURLFailsTheSessionAndShowsNothing(t *testing.T) {
	le := newLoginEnv(t)
	le.online(t)
	id := le.start(t)
	le.poll(t, `{"boot":"b1"}`)
	hostile := "https://evil-relay.test/cai/oauth/authorize?code_challenge=x&state=y"
	rep, _ := json.Marshal(map[string]any{"session": id, "state": "awaiting_code", "attempt": 1, "url": hostile})
	if resp, body := le.rpost(t, "/runner/v1/login/report", string(rep)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d %s, want 204", resp.StatusCode, body)
	}
	v, body := le.view(t)
	if v.Session == nil || v.Session.State != "failed" || v.Session.Reason != "bad_url" || v.Session.URL != "" {
		t.Errorf("view = %s, want failed/bad_url without a url", body)
	}
	if strings.Contains(body, "evil-relay") || strings.Contains(body, `"url"`) {
		t.Errorf("the view shows the hostile address: %s", body)
	}
	resp, act := le.call(t, http.MethodGet, "/api/activity", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activity = %d", resp.StatusCode)
	}
	if strings.Contains(act, "evil-relay") {
		t.Errorf("the activity holds the hostile host: %s", act)
	}
	if !strings.Contains(act, `"kind":"login_failed"`) {
		t.Errorf("the activity API has no login_failed: %s", act)
	}
	// The API does not show an entry's data; the store does.
	log, err := le.store.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range log {
		if strings.Contains(a.Summary+string(a.Data), "evil-relay") {
			t.Errorf("an activity row holds the hostile host: %+v", a)
		}
		if a.Kind == store.KindLoginFailed && string(a.Data) == `{"reason":"bad_url"}` {
			found = true
		}
	}
	if !found {
		t.Errorf("no login_failed with bad_url in %+v", log)
	}
}

func TestTheClaimGate(t *testing.T) {
	le := newLoginEnv(t)
	ctx := context.Background()
	queued, err := le.store.CreateRun(ctx, "claude", "p")
	if err != nil {
		t.Fatal(err)
	}
	le.online(t)
	id := le.start(t)

	short := &http.Client{Timeout: 1500 * time.Millisecond}
	req, _ := http.NewRequest(http.MethodPost, le.ts.URL+"/runner/v1/claim", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	if resp, err := short.Do(req); err == nil {
		body := readAll(t, resp)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a claim while a login is open returned a run: %s", body)
		}
	}
	if r, err := le.store.GetRun(ctx, queued.ID); err != nil || r.Status != "queued" {
		t.Fatalf("run = %+v, %v, want queued", r, err)
	}

	if resp, body := le.call(t, http.MethodPost, "/api/runner/login/cancel", `{"session":"`+id+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", resp.StatusCode, body)
	}
	resp, body := le.rpost(t, "/runner/v1/claim", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, queued.ID) {
		t.Fatalf("claim after cancel = %d %s, want the run", resp.StatusCode, body)
	}
	le.online(t)
	resp, body = le.call(t, http.MethodPost, "/api/runner/login", `{}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, `"run_active"`) {
		t.Errorf("start while a run is running = %d %s, want 409 run_active", resp.StatusCode, body)
	}
}

// A start races 50 claims: afterwards a run is running or a session is open, never both.
func TestTheClaimGateUnderARace(t *testing.T) {
	for round := range 3 {
		le := newLoginEnv(t)
		ctx := context.Background()
		if _, err := le.store.CreateRun(ctx, "claude", "p"); err != nil {
			t.Fatal(err)
		}
		le.online(t)
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := &http.Client{Timeout: 700 * time.Millisecond}
				req, _ := http.NewRequest(http.MethodPost, le.ts.URL+"/runner/v1/claim", nil)
				req.Header.Set("Authorization", "Bearer "+runnerToken)
				if resp, err := c.Do(req); err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
		}
		var started int
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, le.ts.URL+"/api/runner/login", strings.NewReader(`{}`))
			req.Header.Set("X-Remedy-CSRF", "1")
			if resp, err := le.admin.Do(req); err == nil {
				started = resp.StatusCode
				_ = resp.Body.Close()
			}
		}()
		wg.Wait()
		running, _, err := le.store.RunCounts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := le.view(t)
		open := v.Session != nil && v.Session.State == "starting"
		if open && running > 0 {
			t.Fatalf("round %d: a run is running while a login is open", round)
		}
		if !open && running == 0 {
			t.Fatalf("round %d: neither a run nor a session (start = %d)", round, started)
		}
	}
}
