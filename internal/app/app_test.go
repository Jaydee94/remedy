package app_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

const (
	token       = "ghp_WHOLECHAINTOKEN0123456789abcdefghijkl"
	password    = "whole-chain-admin-password"
	runnerToken = "whole-chain-runner-token-24-chars"

	goodDiagnosis = `{"summary":"npm ci fails because the lock file is stale","cause":"package.json wants typescript 7.0.2, the lock file pins 6.0.3.","confidence":"high","category":"dependency_update","affected_files":["web/package.json","web/package-lock.json"],"proposed_fix":"Run npm install in web/ and commit the lock file.","fix_looks_automatable":true}`
)

var bom = string(rune(0xFEFF))

// fakeGitHub is the GitHub API for one repository, octo/hello, with one pull request, number 7. Like the
// real one it serves job logs and archives by a redirect to another host, and it records every request.
type fakeGitHub struct {
	mu         sync.Mutex
	sha        string
	conclusion string
	prOpen     bool
	requests   []string
	blobAuth   []string
	url        string
	blobURL    string
	tarball    []byte
}

func (g *fakeGitHub) set(sha, conclusion string, prOpen bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sha, g.conclusion, g.prOpen = sha, conclusion, prOpen
}

func (g *fakeGitHub) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.requests...)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	sha, conclusion, prOpen := g.sha, g.conclusion, g.prOpen
	auth := "no"
	if r.Header.Get("Authorization") != "" {
		auth = "yes"
	}
	if strings.HasPrefix(r.URL.Path, "/blob/") {
		g.blobAuth = append(g.blobAuth, r.Header.Get("Authorization"))
	} else {
		g.requests = append(g.requests, fmt.Sprintf("%s %s auth=%s", r.Method, r.URL.Path, auth))
	}
	g.mu.Unlock()

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path
	switch {
	case path == "/blob/log":
		_, _ = io.WriteString(w, bom+"2026-10-02T12:16:36.1123671Z ##[group]Run npm ci\n"+
			"2026-10-02T12:16:39.4400532Z npm error Invalid: lock file's typescript@6.0.3 does not satisfy typescript@7.0.2\n"+
			"2026-10-02T12:16:39.4859831Z ##[error]Process completed with exit code 1.\n2026-10-02T12:16:39.5007256Z Post job cleanup.\n")
	case path == "/blob/tarball":
		_, _ = w.Write(g.tarball)
	case path == "/user":
		writeJSON(w, github.User{Login: "octo"})
	case path == "/repos/octo/hello":
		writeJSON(w, github.Repo{FullName: "octo/hello", DefaultBranch: "main"})
	case path == "/repos/octo/hello/pulls":
		prs := []github.PullRequest{}
		if prOpen {
			prs = append(prs, g.pr(sha))
		}
		writeJSON(w, prs)
	case path == "/repos/octo/hello/pulls/7":
		writeJSON(w, g.pr(sha))
	case path == "/repos/octo/hello/pulls/7/files":
		writeJSON(w, []github.PRFile{{Filename: "web/package.json", Status: "modified", Additions: 1, Deletions: 1, Patch: "@@ -1 +1 @@\n-a\n+b"}})
	case strings.HasPrefix(path, "/repos/octo/hello/commits/") && strings.HasSuffix(path, "/check-runs"):
		ref := strings.TrimSuffix(strings.TrimPrefix(path, "/repos/octo/hello/commits/"), "/check-runs")
		runs := []github.CheckRun{}
		switch ref {
		case sha:
			runs = append(runs, github.CheckRun{ID: 11, Name: "go", Status: "completed", Conclusion: conclusion, HeadSHA: sha,
				HTMLURL: "https://github.com/octo/hello/actions/runs/1/job/11"})
		case "main":
			runs = append(runs, github.CheckRun{ID: 12, Name: "go", Status: "completed", Conclusion: "success", HeadSHA: "mainsha1"})
		}
		writeJSON(w, map[string]any{"total_count": len(runs), "check_runs": runs})
	case path == "/repos/octo/hello/actions/jobs/11/logs":
		http.Redirect(w, r, g.blobURL+"/blob/log?sig=SIGNED", http.StatusFound)
	case strings.HasPrefix(path, "/repos/octo/hello/tarball/"):
		http.Redirect(w, r, g.blobURL+"/blob/tarball?sig=SIGNED", http.StatusFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (g *fakeGitHub) pr(sha string) github.PullRequest {
	p := github.PullRequest{Number: 7, Title: "chore(deps): update typescript", Body: "bump typescript", HTMLURL: "https://github.com/octo/hello/pull/7"}
	p.Head.SHA, p.Head.Ref = sha, "bump"
	p.User.Login, p.User.Type = "renovate[bot]", "Bot"
	return p
}

func tarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "octo-hello-aaaaaaa/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range map[string]string{"main.go": "package main\n", "README.md": "# hello\n", ".env": "API_TOKEN=do-not-send\n", "web/package.json": "{}\n"} {
		_ = tw.WriteHeader(&tar.Header{Name: "octo-hello-aaaaaaa/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = io.WriteString(tw, body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

type stack struct {
	t      *testing.T
	gh     *fakeGitHub
	st     *store.Store
	app    *app.App
	ts     *httptest.Server
	client *http.Client
	now    time.Time
	mu     sync.Mutex
}

func (s *stack) clock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *stack) advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

// newStack wires the real control plane over a fake GitHub. With a runner the real runner loop runs against
// it, using a fake claude that answers with structured output.
func newStack(t *testing.T, withRunner bool) *stack {
	t.Helper()
	gh := &fakeGitHub{tarball: tarball(t)}
	ghServer := httptest.NewServer(gh)
	t.Cleanup(ghServer.Close)
	gh.url = ghServer.URL
	// The blob host has another name than the API host, as in the real world, so that a token that is
	// wrongly forwarded after a redirect shows up.
	gh.blobURL = "http://localhost:" + strconv.Itoa(ghServer.Listener.Addr().(*net.TCPAddr).Port)
	gh.set("aaaaaaa", "failure", true)

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Server{
		AdminPassword: password, RunnerToken: runnerToken, MasterKey: key, GitHubAPIURL: gh.url, PollInterval: time.Minute,
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20,
	}
	a := app.New(cfg, st, log, nil)
	s := &stack{t: t, gh: gh, st: st, app: a, now: time.Now()}
	a.Responder.Now = s.clock
	s.ts = httptest.NewServer(a.Handler)
	t.Cleanup(s.ts.Close)

	jar, _ := cookiejar.New(nil)
	s.client = &http.Client{Jar: jar}
	if code, _ := s.admin(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login = %d", code)
	}
	if code, body := s.admin(http.MethodPut, "/api/github/connection", `{"token":"`+token+`"}`); code != http.StatusOK {
		t.Fatalf("connect = %d %s", code, body)
	}
	if code, body := s.admin(http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`); code != http.StatusCreated {
		t.Fatalf("add repo = %d %s", code, body)
	}

	if withRunner {
		loop := &runner.Loop{
			Client:        &runner.Client{BaseURL: s.ts.URL, Token: runnerToken, HTTP: s.ts.Client()},
			Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.FakeClaudeStructured(t, goodDiagnosis)}},
			WorkspaceRoot: t.TempDir(),
			Env:           os.Environ(),
			Log:           log,
			Backoff:       50 * time.Millisecond,
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { loop.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done })
	}
	return s
}

func (s *stack) admin(method, path, body string) (int, string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (s *stack) poll() { s.app.Poller.PollOnce(context.Background()) }

func (s *stack) incident() store.Incident {
	s.t.Helper()
	list, err := s.st.ListIncidents(context.Background(), store.IncidentFilter{State: "all"})
	if err != nil || len(list) == 0 {
		s.t.Fatalf("incidents = %+v, %v", list, err)
	}
	return list[0]
}

// waitFor polls the store until cond holds for the newest incident.
func (s *stack) waitFor(what string, cond func(store.Incident) bool) store.Incident {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if in := s.incident(); cond(in) {
			return in
		}
		time.Sleep(50 * time.Millisecond)
	}
	in := s.incident()
	s.t.Fatalf("timed out waiting for %s; incident = %+v", what, in)
	return in
}

func (s *stack) responderRuns() []run.Run {
	s.t.Helper()
	all, err := s.st.ListRuns(context.Background(), 100)
	if err != nil {
		s.t.Fatal(err)
	}
	var out []run.Run
	for _, r := range all {
		if r.Role == run.RoleResponder {
			out = append(out, r)
		}
	}
	return out
}

func TestFromAFailedCheckToAStoredDiagnosis(t *testing.T) {
	s := newStack(t, true)
	ctx := context.Background()

	// 1. The poll sees the red check, opens the incident and the same cycle starts the diagnosis.
	s.poll()
	in := s.waitFor("the diagnosis", func(in store.Incident) bool { return in.State == store.IncDiagnosed })

	if in.Ref != "pr:7" || in.CheckName != "go" || in.Diagnoses != 1 || in.DiagnosedSHA != "aaaaaaa" || in.RunID == "" {
		t.Fatalf("incident = %+v", in)
	}
	d, err := diagnosis.Parse(in.Diagnosis)
	if err != nil || d.Category != "dependency_update" || d.Confidence != "high" {
		t.Fatalf("diagnosis = %+v, %v", d, err)
	}

	// 2. The run: automatic, responder, successful, with the evidence in its prompt and its answer stored.
	rn, err := s.st.GetRun(ctx, in.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if rn.Role != run.RoleResponder || !rn.Automatic || rn.Status != run.Succeeded || rn.HeadSHA != "aaaaaaa" || len(rn.Output) == 0 {
		t.Fatalf("run = %+v", rn)
	}
	for _, want := range []string{"chore(deps): update typescript", "npm error Invalid: lock file's typescript@6.0.3", "##[error]Process completed with exit code 1.", "web/package.json"} {
		if !strings.Contains(rn.Prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(rn.Prompt, "Post job cleanup") {
		t.Error("the runner cleanup after the error reached the agent")
	}

	// 3. What the agent saw: the schema flag and the snapshot without the secret file.
	var probe struct{ Args, Files string }
	events, _ := s.st.Events(ctx, rn.ID, 0)
	for _, e := range events {
		if e.Kind == "probe" {
			_ = json.Unmarshal(e.Payload, &probe)
		}
	}
	if !strings.Contains(probe.Args, "--json-schema") || !strings.Contains(probe.Args, `"required"`) {
		t.Errorf("the CLI args = %q", probe.Args)
	}
	for _, want := range []string{"main.go", "README.md", "web"} {
		if !strings.Contains(probe.Files, want) {
			t.Errorf("the workspace lacks %q: %q", want, probe.Files)
		}
	}
	if strings.Contains(probe.Files, ".env") {
		t.Errorf("a secret file reached the runner: %q", probe.Files)
	}

	// 4. GitHub only ever saw GET requests, and the token went to the API host only.
	for _, req := range s.gh.seen() {
		if !strings.HasPrefix(req, "GET ") {
			t.Errorf("GitHub saw %q", req)
		}
		if strings.Contains(req, "/blob/") {
			t.Errorf("a blob request was counted as an API request: %q", req)
		}
	}
	seen := strings.Join(s.gh.seen(), "\n")
	for _, want := range []string{"/repos/octo/hello/pulls/7 auth=yes", "/repos/octo/hello/pulls/7/files auth=yes",
		"/repos/octo/hello/actions/jobs/11/logs auth=yes", "/repos/octo/hello/tarball/aaaaaaa auth=yes"} {
		if !strings.Contains(seen, want) {
			t.Errorf("GitHub never saw %q", want)
		}
	}
	s.gh.mu.Lock()
	blobAuth := append([]string(nil), s.gh.blobAuth...)
	s.gh.mu.Unlock()
	if len(blobAuth) < 2 {
		t.Fatalf("the log and the archive were fetched from the blob host %d times, want 2", len(blobAuth))
	}
	for _, a := range blobAuth {
		if a != "" {
			t.Fatalf("the token was sent to the blob host: %q", a)
		}
	}

	// 5. The token is nowhere in what Remedy stored.
	all, _ := s.st.ListRuns(ctx, 100)
	for _, r := range all {
		full, _ := s.st.GetRun(ctx, r.ID)
		evs, _ := s.st.Events(ctx, r.ID, 0)
		text := full.Prompt + full.Result + string(full.Output)
		for _, e := range evs {
			text += string(e.Payload)
		}
		if strings.Contains(text, token) || strings.Contains(text, "do-not-send") {
			t.Fatalf("a secret reached run %s", r.ID)
		}
	}
	acts, _ := s.st.ListActivity(ctx, store.ActivityQuery{Limit: 100})
	for _, a := range acts {
		if strings.Contains(a.Summary+string(a.Data), token) {
			t.Fatalf("the token is in the activity: %+v", a)
		}
	}

	// 6. The poll again, on the same commit, starts nothing.
	s.poll()
	s.poll()
	if runs := s.responderRuns(); len(runs) != 1 {
		t.Fatalf("%d responder runs after repeated polls, want 1", len(runs))
	}

	// 7. A new red commit after the cooldown is diagnosed again.
	s.gh.set("bbbbbbb", "failure", true)
	s.poll() // a recurrence; the cooldown has not passed, so no new diagnosis yet
	if runs := s.responderRuns(); len(runs) != 1 {
		t.Fatalf("%d responder runs inside the cooldown, want 1", len(runs))
	}
	s.advance(16 * time.Minute)
	s.poll()
	in = s.waitFor("the second diagnosis", func(in store.Incident) bool {
		return in.State == store.IncDiagnosed && in.DiagnosedSHA == "bbbbbbb"
	})
	if in.Diagnoses != 2 || in.Occurrences != 2 {
		t.Fatalf("incident = %+v", in)
	}

	// 8. Green resolves the incident and keeps its diagnosis.
	s.gh.set("bbbbbbb", "success", true)
	s.poll()
	in = s.incident()
	if in.State != store.IncResolved || in.ResolvedReason != "green" || len(in.Diagnosis) == 0 {
		t.Fatalf("incident = %+v", in)
	}

	kinds := ""
	acts, _ = s.st.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID, Limit: 100})
	for i := len(acts) - 1; i >= 0; i-- {
		kinds += acts[i].Kind + " "
	}
	want := "incident_opened diagnosis_started diagnosis_finished incident_recurred diagnosis_started diagnosis_finished incident_resolved "
	if kinds != want {
		t.Fatalf("activity:\n got %s\nwant %s", kinds, want)
	}
}

func TestOnlyOneDiagnosisRunsAtATime(t *testing.T) {
	s := newStack(t, false) // no runner: the run stays queued
	s.poll()
	s.poll()
	s.poll()
	if runs := s.responderRuns(); len(runs) != 1 || runs[0].Status != run.Queued {
		t.Fatalf("responder runs = %+v, want exactly one, queued", runs)
	}
	if in := s.incident(); in.State != store.IncDiagnosing || in.Diagnoses != 1 {
		t.Fatalf("incident = %+v", in)
	}
}

func TestAnIgnoredIncidentIsNotDiagnosed(t *testing.T) {
	s := newStack(t, false)
	// The check is red and the maintainer ignores the incident before any diagnosis started: open it with
	// automatic diagnosis switched off, ignore it, switch automatic diagnosis on, poll again.
	s.app.Responder.Limits.MaxPerDay = 0
	s.poll()
	in := s.incident()
	if code, _ := s.admin(http.MethodPost, "/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/ignore", ""); code != http.StatusOK {
		t.Fatalf("ignore = %d", code)
	}
	s.app.Responder.Limits.MaxPerDay = 20
	s.poll()
	if runs := s.responderRuns(); len(runs) != 0 {
		t.Fatalf("an ignored incident was diagnosed: %+v", runs)
	}
}

func TestAStuckDiagnosisIsClosedByTheReaper(t *testing.T) {
	s := newStack(t, false)
	ctx := context.Background()
	s.poll()
	in := s.incident()
	if _, err := s.st.ClaimNext(ctx); err != nil { // a runner took the run and died
		t.Fatal(err)
	}

	later := time.Now().Add(16 * time.Minute)
	s.app.Reaper.Now = func() time.Time { return later }
	s.app.Reaper.MaxAge = reaper.DefaultMaxAge
	if n, err := s.app.Reaper.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}

	got, _ := s.st.GetIncident(ctx, in.ID)
	if got.State != store.IncOpen {
		t.Fatalf("incident = %+v: a diagnosis whose run was reaped must not stay diagnosing", got)
	}
	acts, _ := s.st.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID, Limit: 1})
	if acts[0].Kind != store.KindDiagnosisFailed || !strings.Contains(acts[0].Summary, "timed out") {
		t.Fatalf("activity = %+v", acts[0])
	}
}

func TestADiagnosisCanStillBeStartedByClickWhenAutomaticDiagnosisIsOff(t *testing.T) {
	s := newStack(t, true)
	s.app.Responder.Limits.MaxPerDay = 0 // nothing starts automatically
	s.poll()
	in := s.incident()
	if len(s.responderRuns()) != 0 {
		t.Fatal("a diagnosis started although automatic diagnosis is off")
	}

	code, body := s.admin(http.MethodPost, "/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/diagnose", "")
	if code != http.StatusAccepted {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	got := s.waitFor("the manual diagnosis", func(in store.Incident) bool { return in.State == store.IncDiagnosed })
	if rn, _ := s.st.GetRun(context.Background(), got.RunID); rn.Automatic || got.Diagnoses != 0 {
		t.Fatalf("run %+v, diagnoses %d: a click is not an automatic diagnosis", rn, got.Diagnoses)
	}
}
