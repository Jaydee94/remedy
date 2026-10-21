package app_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// The whole chain of the login relay: the real handlers of app.New behind a recording middleware, the real runner client
// (with a recording transport), the real driver, the login checker and the reporter as main wires them, and the fake CLI.
// The planted code and the planted URL mark are unique strings: the no-leak test looks for them everywhere.
//
// No failure message of this file prints a body, a header, a log line or a URL: a failing no-leak test must not itself
// put the planted values into the test output. Messages name the place and give lengths.

const (
	plantedCode    = "CODE-planted-7c1f90"
	plantedURLMark = "URL-planted-5ab3e1"
	// wrongCode holds the planted code too, so that the rejected code is looked for everywhere as well.
	wrongCode   = plantedCode + "-wrong"
	plantedURL1 = "https://claude.com/cai/oauth/authorize?code=true&client_id=" + plantedURLMark + "-client&response_type=code" +
		"&code_challenge=" + plantedURLMark + "-challenge&code_challenge_method=S256&state=" + plantedURLMark + "-one"
	plantedURL2 = "https://claude.com/cai/oauth/authorize?code=true&client_id=" + plantedURLMark + "-client&response_type=code" +
		"&code_challenge=" + plantedURLMark + "-challenge2&code_challenge_method=S256&state=" + plantedURLMark + "-two"
)

// lockedBuffer is the one log sink of the chain: every logger writes to it at Debug.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.b.Bytes())
}

// exchange is one recorded HTTP call, seen by the server (side "server") or by the runner's transport (side "runner").
type exchange struct {
	Side       string
	Method     string
	Target     string // the request URI (server) or the URL (runner)
	ReqHeader  http.Header
	ReqBody    []byte
	Status     int
	RespHeader http.Header
	RespBody   []byte
}

func (e exchange) path() string {
	u, err := url.Parse(e.Target)
	if err != nil {
		return ""
	}
	return u.Path
}

type recorder struct {
	mu   sync.Mutex
	list []*exchange
}

func (rec *recorder) add(e *exchange) {
	rec.mu.Lock()
	rec.list = append(rec.list, e)
	rec.mu.Unlock()
}

// snapshot copies every exchange as it is now; a stream still open shows what it has written so far.
func (rec *recorder) snapshot() []exchange {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := make([]exchange, 0, len(rec.list))
	for _, e := range rec.list {
		c := *e
		c.ReqHeader, c.RespHeader = e.ReqHeader.Clone(), e.RespHeader.Clone()
		c.ReqBody, c.RespBody = bytes.Clone(e.ReqBody), bytes.Clone(e.RespBody)
		out = append(out, c)
	}
	return out
}

// middleware records method, request URI, headers and body of each request and status, headers and body of its answer.
func (rec *recorder) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		e := &exchange{Side: "server", Method: r.Method, Target: r.RequestURI, ReqHeader: r.Header.Clone(), ReqBody: body}
		rec.add(e)
		rw := &recordingWriter{ResponseWriter: w, rec: rec, e: e}
		next.ServeHTTP(rw, r)
		rw.header(http.StatusOK)
	})
}

type recordingWriter struct {
	http.ResponseWriter
	rec  *recorder
	e    *exchange
	sent bool
}

// header records the status and the headers as they go out.
func (w *recordingWriter) header(status int) {
	w.rec.mu.Lock()
	defer w.rec.mu.Unlock()
	if w.sent {
		return
	}
	w.sent = true
	w.e.Status, w.e.RespHeader = status, w.ResponseWriter.Header().Clone()
}

func (w *recordingWriter) WriteHeader(status int) {
	w.header(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.header(http.StatusOK)
	w.rec.mu.Lock()
	w.e.RespBody = append(w.e.RespBody, p...)
	w.rec.mu.Unlock()
	return w.ResponseWriter.Write(p)
}

func (w *recordingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// recordingTransport records the runner's side of every call.
type recordingTransport struct {
	rec  *recorder
	base http.RoundTripper
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	e := &exchange{Side: "runner", Method: req.Method, Target: req.URL.String(), ReqHeader: req.Header.Clone(), ReqBody: body}
	t.rec.add(e)
	resp, err := t.base.RoundTrip(out)
	if err != nil {
		return nil, err
	}
	rb, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	t.rec.mu.Lock()
	e.Status, e.RespHeader, e.RespBody = resp.StatusCode, resp.Header.Clone(), rb
	t.rec.mu.Unlock()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(rb))
	return resp, nil
}

// chainRunner is the runner side as main wires it with the relay on: the login relay, the login checker with its pause
// and kick, and the status reporter. The checker waits an hour between its own checks, so only the kick can turn the
// login green within seconds; the reporter reports every 300 ms instead of every 15 s.
type chainRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type loginChain struct {
	t      *testing.T
	dbPath string
	st     *store.Store
	cfg    config.Server
	logs   *lockedBuffer
	log    *slog.Logger
	rec    *recorder
	ts     *httptest.Server
	fake   *testutil.Relay
	client *runner.Client
	admin  *http.Client
	adminT *http.Transport

	handler atomic.Pointer[http.Handler]
	app     *app.App
	stopApp func()
	runner  *chainRunner
}

func newLoginChain(t *testing.T, fake testutil.RelayOptions) *loginChain {
	t.Helper()
	c := &loginChain{t: t, logs: &lockedBuffer{}, rec: &recorder{}, adminT: &http.Transport{}}
	t.Cleanup(c.adminT.CloseIdleConnections)
	c.log = slog.New(slog.NewTextHandler(c.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c.dbPath = filepath.Join(t.TempDir(), "remedy.db")
	st, err := store.Open(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c.st = st
	t.Cleanup(func() { _ = st.Close() })
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	c.cfg = config.Server{
		AdminPassword: password, RunnerToken: runnerToken, MasterKey: key, PollInterval: time.Minute,
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20, LoginRelay: true,
	}
	c.startApp()
	c.ts = httptest.NewServer(c.rec.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*c.handler.Load()).ServeHTTP(w, r)
	})))
	t.Cleanup(c.ts.Close)
	t.Cleanup(func() { c.stopApp() })

	c.fake = testutil.FakeClaudeRelay(t, fake)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	c.client = &runner.Client{BaseURL: c.ts.URL, Token: runnerToken, HTTP: &http.Client{Transport: &recordingTransport{rec: c.rec, base: transport}}}
	c.startRunner()
	// Runs first: the runner stops its child, then no process of the fake may be left.
	t.Cleanup(func() {
		c.stopRunner()
		if alive := c.fake.Alive(); len(alive) > 0 {
			t.Errorf("%d processes of the fake CLI are still running after the runner stopped", len(alive))
		}
	})
	c.signIn()
	return c
}

// startApp builds the control plane on the store and starts the manager's loop, as main does.
func (c *loginChain) startApp() {
	a := app.New(c.cfg, c.st, c.log, nil)
	if a.Login == nil {
		c.t.Fatal("the relay is on but the app has no login manager")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Login.Run(ctx)
	}()
	c.app = a
	h := a.Handler
	c.handler.Store(&h)
	var once sync.Once
	c.stopApp = func() { once.Do(func() { cancel(); <-done }) }
}

// restartControlPlane stops the control plane and builds a new one on the same store; the connections of the old one
// are closed, so the runner's held poll fails and its next one reaches the new control plane.
func (c *loginChain) restartControlPlane() {
	c.stopApp()
	c.startApp()
	c.ts.CloseClientConnections()
	c.adminT.CloseIdleConnections() // the admin's kept-alive connection was closed too
	c.signIn()
}

func (c *loginChain) startRunner() {
	status := runner.NewStatus()
	claude := provider.Claude{Binary: c.fake.Path}
	check := func(ctx context.Context) (provider.LoginState, string) { return claude.LoginCheck(ctx, os.Environ()) }
	pause := &runner.LoginPause{}
	checker := &runner.LoginChecker{
		Check:     check,
		Version:   func(ctx context.Context) string { return claude.Version(ctx, os.Environ()) },
		Status:    status,
		OKEvery:   time.Hour,
		ElseEvery: time.Hour,
		Log:       c.log,
		Pause:     pause.Active,
	}
	relay := &runner.LoginRelay{
		Client: c.client, Claude: claude, Check: check, Status: status, Env: os.Environ(),
		Pause: pause, Kick: checker.Kick, Log: c.log,
		Timings: runner.LoginTimings{Backoff: 200 * time.Millisecond},
	}
	reporter := &runner.Reporter{Client: c.client, Status: status, Interval: 300 * time.Millisecond, Log: c.log}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, f := range []func(context.Context){relay.Run, checker.Run, reporter.Run} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(ctx)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	c.runner = &chainRunner{cancel: cancel, done: done}
}

// stopRunner ends the runner as a SIGTERM does: main waits up to 5 s for the relay.
func (c *loginChain) stopRunner() {
	if c.runner == nil {
		return
	}
	c.runner.cancel()
	select {
	case <-c.runner.done:
	case <-time.After(10 * time.Second):
		c.t.Error("the runner did not stop within 10 s")
	}
	c.runner = nil
}

func (c *loginChain) signIn() {
	jar, _ := cookiejar.New(nil)
	c.admin = &http.Client{Jar: jar, Transport: c.adminT}
	if code, _ := c.do(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		c.t.Fatalf("admin login = %d", code)
	}
}

// do is an admin request with the session and the CSRF header. Its body is never printed by a caller.
func (c *loginChain) do(method, path, body string) (int, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := c.admin.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: request failed", method, path)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// relayView is GET /api/runner/login. Never print it: URL may hold the planted address.
type relayView struct {
	Available bool `json:"available"`
	Session   *struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Attempt     int    `json:"attempt"`
		URL         string `json:"url"`
		Reason      string `json:"reason"`
		WaitingRuns int    `json:"waitingRuns"`
	} `json:"session"`
}

// describe is what a failure message may say of a view: no id, no URL, only its length.
func (v relayView) describe() string {
	if v.Session == nil {
		return "no session"
	}
	return fmt.Sprintf("state %s, attempt %d, reason %q, url of %d bytes, %d waiting runs",
		v.Session.State, v.Session.Attempt, v.Session.Reason, len(v.Session.URL), v.Session.WaitingRuns)
}

func (c *loginChain) view() relayView {
	c.t.Helper()
	code, raw := c.do(http.MethodGet, "/api/runner/login", "")
	if code != http.StatusOK {
		c.t.Fatalf("GET /api/runner/login = %d", code)
	}
	var v relayView
	if err := json.Unmarshal(raw, &v); err != nil {
		c.t.Fatalf("GET /api/runner/login: unreadable answer of %d bytes", len(raw))
	}
	return v
}

// waitView polls the page's GET until ok holds (the page polls every 2 s; this polls faster) and returns that view.
func (c *loginChain) waitView(what string, timeout time.Duration, ok func(relayView) bool) relayView {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var v relayView
	for {
		v = c.view()
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s: %s", what, v.describe())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *loginChain) waitState(id, state string) relayView {
	c.t.Helper()
	return c.waitView("the state "+state, 15*time.Second, func(v relayView) bool {
		return v.Session != nil && v.Session.ID == id && v.Session.State == state
	})
}

// waitFor polls f until it holds.
func waitFor(t *testing.T, what string, timeout time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type runnerView struct {
	Connected bool   `json:"connected"`
	Login     string `json:"login"`
}

func (c *loginChain) runnerView() runnerView {
	c.t.Helper()
	code, raw := c.do(http.MethodGet, "/api/runner", "")
	var v runnerView
	if code != http.StatusOK || json.Unmarshal(raw, &v) != nil {
		c.t.Fatalf("GET /api/runner = %d (%d bytes)", code, len(raw))
	}
	return v
}

// waitRunnerLogin waits until the control plane shows the runner connected with this login state.
func (c *loginChain) waitRunnerLogin(login string, timeout time.Duration) {
	c.t.Helper()
	waitFor(c.t, "the runner's login "+login+" in GET /api/runner", timeout, func() bool {
		v := c.runnerView()
		return v.Connected && v.Login == login
	})
}

// start opens a session from the admin API. The runner may not have polled yet right after it started: a refusal
// runner_offline is retried for a few seconds.
func (c *loginChain) start(replace bool) (string, int) {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, raw := c.do(http.MethodPost, "/api/runner/login", fmt.Sprintf(`{"replace":%t}`, replace))
		var v struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			WaitingRuns int    `json:"waitingRuns"`
			Reason      string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &v)
		if code == http.StatusCreated {
			if v.ID == "" || v.State != "starting" {
				c.t.Fatalf("start: state %q, id of %d bytes", v.State, len(v.ID))
			}
			return v.ID, v.WaitingRuns
		}
		if code != http.StatusConflict || v.Reason != "runner_offline" || time.Now().After(deadline) {
			c.t.Fatalf("start = %d, reason %q", code, v.Reason)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// awaitCode waits for awaiting_code at the attempt and returns the URL the page is shown.
func (c *loginChain) awaitCode(id string, attempt int) string {
	c.t.Helper()
	v := c.waitView(fmt.Sprintf("awaiting_code at attempt %d", attempt), 15*time.Second, func(v relayView) bool {
		return v.Session != nil && v.Session.ID == id && v.Session.State == "awaiting_code" && v.Session.Attempt == attempt
	})
	return v.Session.URL
}

func (c *loginChain) submit(id, code string) {
	c.t.Helper()
	body, _ := json.Marshal(map[string]string{"session": id, "code": code})
	if status, _ := c.do(http.MethodPost, "/api/runner/login/code", string(body)); status != http.StatusAccepted {
		c.t.Fatalf("POST /api/runner/login/code = %d", status)
	}
}

// activity is the store's activity log; data is read here, the API leaves it out.
func (c *loginChain) activity() []store.Activity {
	c.t.Helper()
	log, err := c.st.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		c.t.Fatal(err)
	}
	return log
}

// loginEntries are the activity entries of the login, oldest first, as "kind data".
func (c *loginChain) loginEntries() []string {
	var out []string
	log := c.activity()
	for i := len(log) - 1; i >= 0; i-- {
		if a := log[i]; strings.HasPrefix(a.Kind, "login_") {
			out = append(out, a.Kind+" "+string(a.Data))
		}
	}
	return out
}

// loginKinds is loginEntries for a failure message: the kinds and the length of each data, never the data, which on a
// regression could hold a planted value.
func (c *loginChain) loginKinds() string {
	var out []string
	log := c.activity()
	for i := len(log) - 1; i >= 0; i-- {
		if a := log[i]; strings.HasPrefix(a.Kind, "login_") {
			out = append(out, fmt.Sprintf("%s (data of %d bytes)", a.Kind, len(a.Data)))
		}
	}
	return strings.Join(out, ", ")
}

// readDB opens a second, read-only connection to the database file.
func (c *loginChain) readDB() *sql.DB {
	c.t.Helper()
	db, err := sql.Open("sqlite", "file:"+c.dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (c *loginChain) countRuns() int {
	c.t.Helper()
	var n int
	if err := c.readDB().QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
		c.t.Fatal(err)
	}
	return n
}

// loginSucceeds runs a session from the admin API to succeeded with the planted code.
func loginSucceeds(t *testing.T, c *loginChain) {
	t.Helper()
	c.waitRunnerLogin("missing", 10*time.Second)
	id, _ := c.start(false)
	if got := c.awaitCode(id, 1); got != plantedURL1 {
		t.Fatalf("the page is shown a URL of %d bytes, not the planted one", len(got))
	}
	c.submit(id, plantedCode)
	c.waitState(id, "succeeded")
	ended := time.Now()
	// The kick: the checker waits an hour between its own checks, so only the kick at the end of the session turns it.
	c.waitRunnerLogin("ok", 3*time.Second)
	t.Logf("the login turned ok %v after the session ended", time.Since(ended).Round(10*time.Millisecond))
	if !c.fake.LoggedIn() {
		t.Error("the fake CLI is not logged in")
	}
}

func TestALoginSucceedsThroughTheWholeChain(t *testing.T) {
	c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
	loginSucceeds(t, c)

	if got, want := strings.Join(c.loginEntries(), ", "), "login_started {}, login_succeeded {}"; got != want {
		t.Errorf("login activity = %s, want %s", c.loginKinds(), want)
	}
	code, raw := c.do(http.MethodGet, "/api/activity", "")
	if code != http.StatusOK || !bytes.Contains(raw, []byte(`"login_started"`)) || !bytes.Contains(raw, []byte(`"login_succeeded"`)) {
		t.Errorf("GET /api/activity = %d without both login entries", code)
	}
	if n := c.countRuns(); n != 0 {
		t.Errorf("%d run rows exist, want none: a login is not a run", n)
	}
	waitFor(t, "the fake CLI to be gone", 5*time.Second, func() bool { return len(c.fake.Alive()) == 0 })
}

// invalidThenValid sends a wrong code first: the runner starts a fresh sign-in with a new URL, and the right code then
// logs in.
func invalidThenValid(t *testing.T, c *loginChain) {
	t.Helper()
	c.waitRunnerLogin("missing", 10*time.Second)
	id, _ := c.start(false)
	if got := c.awaitCode(id, 1); got != plantedURL1 {
		t.Fatalf("attempt 1 shows a URL of %d bytes, not the first planted one", len(got))
	}
	c.fake.SetURL(t, plantedURL2) // the next `auth login` prints the second URL
	c.submit(id, wrongCode)
	v := c.waitView("attempt 2", 15*time.Second, func(v relayView) bool {
		return v.Session != nil && v.Session.ID == id && v.Session.Attempt == 2
	})
	if s := v.Session.State; s != "starting" && s != "awaiting_code" {
		t.Fatalf("after a wrong code: %s, want starting or awaiting_code at attempt 2", v.describe())
	}
	if got := c.awaitCode(id, 2); got != plantedURL2 {
		t.Fatalf("attempt 2 shows a URL of %d bytes, not the second planted one", len(got))
	}
	c.submit(id, plantedCode)
	c.waitState(id, "succeeded")
	c.waitRunnerLogin("ok", 3*time.Second)
	if n := len(c.fake.PIDs()); n != 2 {
		t.Errorf("the fake CLI was started %d times, want 2", n)
	}
}

func TestAnInvalidCodeStartsAFreshSignInThroughTheChain(t *testing.T) {
	c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
	invalidThenValid(t, c)
	if got, want := strings.Join(c.loginEntries(), ", "), "login_started {}, login_succeeded {}"; got != want {
		t.Errorf("login activity = %s, want %s", c.loginKinds(), want)
	}
	waitFor(t, "the fake CLI to be gone", 5*time.Second, func() bool { return len(c.fake.Alive()) == 0 })
}

// holds names the planted strings b contains.
func holds(b []byte) (code, url bool) {
	return bytes.Contains(b, []byte(plantedCode)), bytes.Contains(b, []byte(plantedURLMark))
}

// expectClean reports a place that holds a planted string it may not hold. It names the place and the length only.
func expectClean(t *testing.T, place string, b []byte, codeOK, urlOK bool) {
	t.Helper()
	code, url := holds(b)
	if code && !codeOK {
		t.Errorf("%s (%d bytes) holds the planted code", place, len(b))
	}
	if url && !urlOK {
		t.Errorf("%s (%d bytes) holds the planted URL", place, len(b))
	}
}

// safePath is a path for a failure message, or its length when it holds a planted string.
func safePath(p string) string {
	if code, url := holds([]byte(p)); code || url {
		return fmt.Sprintf("(a path of %d bytes)", len(p))
	}
	return p
}

func headerBytes(h http.Header) []byte {
	var b bytes.Buffer
	_ = h.Write(&b)
	return b.Bytes()
}

// allowedBodies says which planted string a recorded body may carry: the URL only in the runner's awaiting_code report
// and in GET /api/runner/login while awaiting_code; the code only in the admin code POST and in the poll answer that
// carries it.
func allowedBodies(e exchange) (reqCode, reqURL, respCode, respURL bool) {
	switch p := e.path(); {
	case e.Method == http.MethodPost && p == "/api/runner/login/code":
		reqCode = true
	case e.Method == http.MethodPost && p == "/runner/v1/login/report":
		var rep struct {
			State string `json:"state"`
		}
		reqURL = json.Unmarshal(e.ReqBody, &rep) == nil && rep.State == "awaiting_code"
	case e.Method == http.MethodPost && p == "/runner/v1/login/poll":
		var ans struct {
			Action string `json:"action"`
		}
		respCode = json.Unmarshal(e.RespBody, &ans) == nil && ans.Action == "code"
	case e.Method == http.MethodGet && p == "/api/runner/login":
		var v relayView
		respURL = json.Unmarshal(e.RespBody, &v) == nil && v.Session != nil && v.Session.State == "awaiting_code"
	}
	return
}

// readSSE reads the activity stream from the start for a while and returns what came.
func (c *loginChain) readSSE(entries int) []byte {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.ts.URL+"/api/activity/stream?after=0", nil)
	resp, err := c.admin.Do(req)
	if err != nil {
		c.t.Fatal("GET /api/activity/stream: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET /api/activity/stream = %d", resp.StatusCode)
	}
	var out bytes.Buffer
	seen := 0
	sc := bufio.NewScanner(resp.Body)
	for seen < entries && sc.Scan() {
		out.Write(sc.Bytes())
		out.WriteByte('\n')
		if strings.HasPrefix(sc.Text(), "event: activity") {
			seen++
		}
	}
	if seen < entries {
		c.t.Errorf("the activity stream sent %d entries, want %d", seen, entries)
	}
	return out.Bytes()
}

// checkDB checks every column of every row of every table, through a second read-only connection, and the raw files.
func (c *loginChain) checkDB(t *testing.T) {
	t.Helper()
	db := c.readDB()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) < 10 {
		t.Fatalf("only %d tables found", len(tables))
	}
	cells := 0
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatalf("read table %s: %v", table, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan table %s: %v", table, err)
			}
			for i, v := range vals {
				var b []byte
				switch x := v.(type) {
				case []byte:
					b = x
				case string:
					b = []byte(x)
				default:
					b = []byte(fmt.Sprint(x))
				}
				cells++
				expectClean(t, "the database column "+table+"."+cols[i], b, false, false)
			}
		}
		rows.Close()
	}
	if cells == 0 {
		t.Error("the database dump read no cell")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if b, err := os.ReadFile(c.dbPath + suffix); err == nil {
			expectClean(t, "the database file"+suffix, b, false, false)
		}
	}
}

// checkNoMaterial asserts, after a session ended, that the planted strings are nowhere but in the bodies that must carry
// them, and that they did reach those bodies (a planted value that never entered the chain proves nothing).
func (c *loginChain) checkNoMaterial(t *testing.T) {
	t.Helper()
	// The answers after the session ended.
	v := c.view()
	if v.Session == nil || (v.Session.State != "succeeded" && v.Session.State != "failed" && v.Session.State != "cancelled") {
		t.Fatalf("the session has not ended: %s", v.describe())
	}
	_, raw := c.do(http.MethodGet, "/api/runner/login", "")
	expectClean(t, "GET /api/runner/login after the session", raw, false, false)
	_, raw = c.do(http.MethodGet, "/api/runner", "")
	expectClean(t, "GET /api/runner", raw, false, false)
	code, raw := c.do(http.MethodGet, "/api/activity", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/activity = %d", code)
	}
	expectClean(t, "GET /api/activity", raw, false, false)
	expectClean(t, "the activity stream", c.readSSE(len(c.activity())), false, false)
	c.checkDB(t)

	// Every recorded call: request line and headers never, bodies only where allowed.
	var urlInReport, urlInView, codeInPost, codeInPoll int
	recs := c.rec.snapshot()
	for i, e := range recs {
		place := fmt.Sprintf("record %d (%s, %s %s)", i, e.Side, e.Method, safePath(e.path()))
		expectClean(t, place+" request line", []byte(e.Method+" "+e.Target), false, false)
		expectClean(t, place+" request headers", headerBytes(e.ReqHeader), false, false)
		expectClean(t, place+" response headers", headerBytes(e.RespHeader), false, false)
		reqCode, reqURL, respCode, respURL := allowedBodies(e)
		expectClean(t, place+" request body", e.ReqBody, reqCode, reqURL)
		expectClean(t, place+" response body", e.RespBody, respCode, respURL)
		if e.Side != "server" {
			continue
		}
		code, url := holds(e.ReqBody)
		if reqURL && url {
			urlInReport++
		}
		if reqCode && code {
			codeInPost++
		}
		code, url = holds(e.RespBody)
		if respURL && url {
			urlInView++
		}
		if respCode && code {
			codeInPoll++
		}
	}
	if urlInReport == 0 || urlInView == 0 || codeInPost == 0 || codeInPoll == 0 {
		t.Errorf("the planted values did not travel where they must: URL in %d reports and %d views, code in %d posts and %d poll answers",
			urlInReport, urlInView, codeInPost, codeInPoll)
	}

	// Last, the logs: everything above may have logged.
	logs := c.logs.Bytes()
	// Both sides log into the buffer, the runner at Debug too: otherwise a clean buffer proves nothing.
	for _, want := range []string{`msg="login session"`, `msg="login output"`, `msg="login process ended"`} {
		if !bytes.Contains(logs, []byte(want)) {
			t.Errorf("the shared log buffer has no %s line: the loggers are not wired to it", want)
		}
	}
	expectClean(t, "the shared log buffer", logs, false, false)
	t.Logf("checked %d recorded calls, %d bytes of logs", len(recs), len(c.logs.Bytes()))
}

func TestNoLoginMaterialAnywhere(t *testing.T) {
	t.Run("a login that succeeds", func(t *testing.T) {
		c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
		loginSucceeds(t, c)
		c.checkNoMaterial(t)
	})
	t.Run("a wrong code first", func(t *testing.T) {
		c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
		invalidThenValid(t, c)
		c.checkNoMaterial(t)
	})
	t.Run("the CLI prints the material on stderr after the URL line", func(t *testing.T) {
		c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode, Noisy: true})
		loginSucceeds(t, c)
		c.checkNoMaterial(t)
	})
}

func TestAControlPlaneRestartMidSessionKillsTheChild(t *testing.T) {
	c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
	c.waitRunnerLogin("missing", 10*time.Second)
	id, _ := c.start(false)
	c.awaitCode(id, 1)
	if len(c.fake.Alive()) == 0 {
		t.Fatal("no child of the fake CLI runs while awaiting_code")
	}

	c.restartControlPlane()
	// The new control plane has no session: the runner's next poll, which names the old one, gets a cancel with seq 0.
	waitFor(t, "a cancel with seq 0 for the session", 10*time.Second, func() bool {
		for _, e := range c.rec.snapshot() {
			if e.Side != "runner" || e.path() != "/runner/v1/login/poll" || e.Status != http.StatusOK {
				continue
			}
			var ans struct {
				Seq     int64  `json:"seq"`
				Session string `json:"session"`
				Action  string `json:"action"`
			}
			if json.Unmarshal(e.RespBody, &ans) == nil && ans.Action == "cancel" && ans.Seq == 0 && ans.Session == id {
				return true
			}
		}
		return false
	})
	waitFor(t, "the child to be killed", 10*time.Second, func() bool { return len(c.fake.Alive()) == 0 })
	if v := c.view(); v.Session != nil {
		t.Errorf("the new control plane shows a session: %s", v.describe())
	}
	if c.fake.LoggedIn() {
		t.Error("the fake CLI is logged in without a code")
	}
}

func TestARunnerRestartMidSessionFailsTheSession(t *testing.T) {
	c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
	c.waitRunnerLogin("missing", 10*time.Second)
	id, _ := c.start(false)
	c.awaitCode(id, 1)

	c.stopRunner()
	if alive := c.fake.Alive(); len(alive) != 0 {
		t.Errorf("%d processes of the fake CLI outlived the runner", len(alive))
	}
	c.startRunner() // a new process: a new boot
	v := c.waitState(id, "failed")
	if v.Session.Reason != "runner_restarted" {
		t.Errorf("after the runner restarted: %s, want reason runner_restarted", v.describe())
	}
	if got, want := strings.Join(c.loginEntries(), ", "), `login_started {}, login_failed {"reason":"runner_restarted"}`; got != want {
		t.Errorf("login activity = %s, want %s", c.loginKinds(), want)
	}
}

func TestARunWaitsForASessionAndNoSessionStartsWhileARunRuns(t *testing.T) {
	c := newLoginChain(t, testutil.RelayOptions{Mode: "valid-exits", URL: plantedURL1, Code: plantedCode})
	c.waitRunnerLogin("missing", 10*time.Second)
	queued, err := c.st.CreateRun(context.Background(), "claude", "a question")
	if err != nil {
		t.Fatal(err)
	}
	id, waiting := c.start(false)
	if waiting != 1 {
		t.Errorf("the start counts %d waiting runs, want 1", waiting)
	}
	c.awaitCode(id, 1)
	if v := c.view(); v.Session == nil || v.Session.WaitingRuns != 1 {
		t.Errorf("GET /api/runner/login: %s, want 1 waiting run", v.describe())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	claim, _ := c.client.Claim(ctx) // the claim is held and ends with the context
	cancel()
	if claim != nil {
		t.Fatal("a run was claimed while a login session is open")
	}
	if r, err := c.st.GetRun(context.Background(), queued.ID); err != nil || r.Status != run.Queued {
		t.Fatalf("the run is %q (%v), want queued", r.Status, err)
	}

	body, _ := json.Marshal(map[string]string{"session": id})
	if code, _ := c.do(http.MethodPost, "/api/runner/login/cancel", string(body)); code != http.StatusNoContent {
		t.Fatalf("cancel = %d", code)
	}
	c.waitState(id, "cancelled")
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	claim, err = c.client.Claim(ctx)
	cancel()
	if err != nil || claim == nil || claim.ID != queued.ID {
		t.Fatalf("after the cancel the claim got no run or another one (err %v)", err)
	}
	waitFor(t, "the child to be killed", 10*time.Second, func() bool { return len(c.fake.Alive()) == 0 })

	// The run is running now: no session starts.
	code, raw := c.do(http.MethodPost, "/api/runner/login", `{"replace":false}`)
	var refusal struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &refusal)
	if code != http.StatusConflict || refusal.Reason != "run_active" {
		t.Errorf("start while a run runs = %d, reason %q, want 409 run_active", code, refusal.Reason)
	}
	if got, want := strings.Join(c.loginEntries(), ", "), `login_started {}, login_cancelled {}`; got != want {
		t.Errorf("login activity = %s, want %s", c.loginKinds(), want)
	}
}
