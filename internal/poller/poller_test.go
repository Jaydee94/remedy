package poller_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/poller"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

const token = "ghp_POLLERTEST0123456789abcdefghijklmnopqr"

// fakeGitHub serves the two list endpoints for any number of repos and records how it was used.
type fakeGitHub struct {
	mu          sync.Mutex
	prs         map[string][]github.PullRequest // by lower-case repo name
	checks      map[string][]github.CheckRun    // by "repo|ref"
	missing     map[string]bool                 // repos that answer 404
	unauth      bool
	retryAfter  int // seconds; when set every request answers 403 with Retry-After
	etags       bool
	requests    []string
	notModified int
	writes      int
	tokens      map[string]bool
}

func newFake(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	f := &fakeGitHub{
		prs: map[string][]github.PullRequest{}, checks: map[string][]github.CheckRun{},
		missing: map[string]bool{}, tokens: map[string]bool{},
	}
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return f, ts
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.tokens[r.Header.Get("Authorization")] = true
	if r.Method != http.MethodGet {
		f.writes++
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if f.unauth {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(f.retryAfter))
		w.WriteHeader(http.StatusForbidden)
		return
	}

	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/repos/"), "/", 3)
	if len(parts) != 3 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	repo, rest := strings.ToLower(parts[0]+"/"+parts[1]), "/"+parts[2]
	if f.missing[repo] {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var body any
	switch {
	case rest == "/pulls":
		prs := f.prs[repo]
		if prs == nil {
			prs = []github.PullRequest{}
		}
		body = prs
	case strings.HasPrefix(rest, "/commits/") && strings.HasSuffix(rest, "/check-runs"):
		ref := strings.TrimSuffix(strings.TrimPrefix(rest, "/commits/"), "/check-runs")
		runs := f.checks[repo+"|"+ref]
		if runs == nil {
			runs = []github.CheckRun{}
		}
		body = map[string]any{"total_count": len(runs), "check_runs": runs}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, _ := json.Marshal(body)
	if f.etags {
		etag := fmt.Sprintf(`W/"%x"`, sha256.Sum256(b))
		if r.Header.Get("If-None-Match") == etag {
			f.notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
	}
	_, _ = w.Write(b)
}

func (f *fakeGitHub) set(fn func(f *fakeGitHub)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeGitHub) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func pr(n int, sha string) github.PullRequest {
	p := github.PullRequest{Number: n, Title: "PR " + strconv.Itoa(n), HTMLURL: fmt.Sprintf("https://github.com/octo/hello/pull/%d", n)}
	p.Head.SHA, p.Head.Ref = sha, "feature"
	return p
}

func check(name, status, conclusion, sha string) github.CheckRun {
	return github.CheckRun{
		ID: 1, Name: name, Status: status, Conclusion: conclusion, HeadSHA: sha,
		HTMLURL: "https://github.com/octo/hello/actions/runs/1/job/1",
	}
}

type env struct {
	t    *testing.T
	st   *store.Store
	gh   *fakeGitHub
	p    *poller.Poller
	repo store.Repo
	logs *bytes.Buffer
	now  time.Time
}

func testKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// newEnv wires a real store, a real GitHub client and a poller to the fake. The connection is sealed
// with sealKey; the poller opens it with openKey.
func newEnv(t *testing.T, sealKey, openKey secret.Key) *env {
	t.Helper()
	gh, ts := newFake(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sealed, err := sealKey.Seal([]byte(token), store.ConnectionAAD())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConnection(ctx, store.Connection{
		TokenCiphertext: sealed, TokenHint: token[len(token)-4:], Login: "octo", Status: store.ConnOK, CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}

	e := &env{t: t, st: st, gh: gh, repo: repo, logs: &bytes.Buffer{}, now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	e.p = &poller.Poller{
		Store:     st,
		Engine:    &incident.Engine{Store: st},
		Key:       openKey,
		NewSource: func(tok secret.Value) poller.Source { return github.New(ts.URL, tok, ts.Client()) },
		Interval:  time.Minute,
		Log:       slog.New(slog.NewTextHandler(e.logs, nil)),
		Now:       func() time.Time { return e.now },
	}
	return e
}

func (e *env) poll() { e.p.PollOnce(context.Background()) }

func (e *env) incidents(state string) []store.Incident {
	e.t.Helper()
	list, err := e.st.ListIncidents(context.Background(), store.IncidentFilter{State: state})
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

func (e *env) kinds() []string {
	e.t.Helper()
	log, err := e.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Kind)
	}
	return out
}

func (e *env) repoRow() store.Repo {
	e.t.Helper()
	r, err := e.st.GetRepo(context.Background(), e.repo.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func TestPollOpensRecursAndResolvesAnIncident(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	t.Cleanup(func() {
		if e.gh.writes != 0 {
			t.Errorf("the poller sent %d requests that were not GET", e.gh.writes)
		}
	})

	// A pull request whose "go" check failed, and a green "web" check.
	e.gh.set(func(f *fakeGitHub) {
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "aaa")}
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("go", "completed", "failure", "aaa"), check("web", "completed", "success", "aaa")}
	})
	e.poll()

	list := e.incidents("all")
	if len(list) != 1 {
		t.Fatalf("incidents = %+v, want one for the failed check", list)
	}
	in := list[0]
	if in.Ref != "pr:7" || in.CheckName != "go" || in.State != store.IncOpen || in.HeadSHA != "aaa" ||
		in.RefURL != "https://github.com/octo/hello/pull/7" || in.CheckURL == "" {
		t.Fatalf("incident = %+v", in)
	}
	if got := e.repoRow(); got.LastPolledAt == nil || !got.LastPolledAt.Equal(e.now) || got.LastError != "" {
		t.Fatalf("repo after a good poll = %+v", got)
	}

	// Polling again on the same state changes nothing.
	e.poll()
	if got := e.kinds(); !slices.Equal(got, []string{store.KindIncidentOpened}) || e.incidents("all")[0].Occurrences != 1 {
		t.Fatalf("a second poll on the same state changed something: %v", got)
	}

	// A new commit that is still red is a recurrence.
	e.gh.set(func(f *fakeGitHub) {
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "bbb")}
		f.checks["octo/hello|bbb"] = []github.CheckRun{check("go", "completed", "failure", "bbb")}
	})
	e.poll()
	if got := e.incidents("all")[0]; got.Occurrences != 2 || got.HeadSHA != "bbb" {
		t.Fatalf("incident after a new red commit = %+v", got)
	}

	// The check turns green: the incident resolves.
	e.gh.set(func(f *fakeGitHub) {
		f.checks["octo/hello|bbb"] = []github.CheckRun{check("go", "completed", "success", "bbb")}
	})
	e.poll()
	if got := e.incidents("resolved"); len(got) != 1 || got[0].ResolvedReason != incident.ReasonGreen {
		t.Fatalf("resolved = %+v", got)
	}
	if got := e.kinds(); !slices.Equal(got, []string{store.KindIncidentOpened, store.KindIncidentRecurred, store.KindIncidentResolved}) {
		t.Fatalf("activity = %v", got)
	}
}

func TestAFailureOnTheDefaultBranchIsAnIncident(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) {
		f.checks["octo/hello|main"] = []github.CheckRun{check("go", "completed", "timed_out", "ccc")}
	})
	e.poll()

	list := e.incidents("all")
	if len(list) != 1 || list[0].Ref != "branch:main" || list[0].Conclusion != "timed_out" || list[0].HeadSHA != "ccc" {
		t.Fatalf("incidents = %+v", list)
	}
}

func TestAClosedPRResolvesItsIncident(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) {
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "aaa")}
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("go", "completed", "failure", "aaa")}
	})
	e.poll()

	e.gh.set(func(f *fakeGitHub) { f.prs["octo/hello"] = nil }) // merged or closed
	e.poll()

	got := e.incidents("resolved")
	if len(got) != 1 || got[0].ResolvedReason != incident.ReasonPRClosed {
		t.Fatalf("resolved = %+v, active = %+v", got, e.incidents("active"))
	}
}

func TestAFullPageOfPRsDoesNotResolveAnything(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	ctx := context.Background()
	// An incident of PR 101, which is not on the (truncated) first page.
	if _, err := e.st.OpenIncident(ctx, store.NewIncident{RepoID: e.repo.ID, Ref: "pr:101", CheckName: "go", Conclusion: "failure", HeadSHA: "zzz"},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "seed"}); err != nil {
		t.Fatal(err)
	}
	var page []github.PullRequest
	for n := 1; n <= github.PageSize; n++ {
		page = append(page, pr(n, "sha"+strconv.Itoa(n)))
	}
	e.gh.set(func(f *fakeGitHub) { f.prs["octo/hello"] = page })
	e.poll()

	if got := e.incidents("resolved"); len(got) != 0 {
		t.Fatalf("a full page may be truncated, but %+v was resolved", got)
	}
	if !strings.Contains(e.logs.String(), "closed PRs are not resolved") {
		t.Errorf("the truncation was not logged: %s", e.logs)
	}
}

func TestPendingChecksAreIgnored(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) {
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "aaa")}
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("go", "in_progress", "", "aaa"), check("web", "queued", "", "aaa")}
	})
	e.poll()
	if got := e.incidents("all"); len(got) != 0 {
		t.Fatalf("incidents = %+v", got)
	}
}

func TestTheWorstCheckRunWinsWhenNamesCollide(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	// Two workflows each have a job called "build": one red, one green.
	e.gh.set(func(f *fakeGitHub) {
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "aaa")}
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("build", "completed", "failure", "aaa"), check("build", "completed", "success", "aaa")}
	})
	e.poll()
	e.poll()

	if got := e.incidents("active"); len(got) != 1 {
		t.Fatalf("active = %+v, want the incident to stay open", got)
	}
	if got := e.kinds(); !slices.Equal(got, []string{store.KindIncidentOpened}) {
		t.Fatalf("activity = %v: the green twin must not resolve the red one", got)
	}

	// Red and still running: the incident stays as it is, nothing resolves.
	e.gh.set(func(f *fakeGitHub) {
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("build", "in_progress", "", "aaa"), check("build", "completed", "success", "aaa")}
	})
	e.poll()
	if got := e.incidents("active"); len(got) != 1 {
		t.Fatalf("active = %+v", got)
	}
}

func TestEtagsMakeRepeatedPollsCheap(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) {
		f.etags = true
		f.prs["octo/hello"] = []github.PullRequest{pr(7, "aaa")}
		f.checks["octo/hello|aaa"] = []github.CheckRun{check("go", "completed", "failure", "aaa")}
	})

	e.poll()
	first := e.gh.requestCount()
	e.poll()
	e.poll()

	e.gh.mu.Lock()
	notModified := e.gh.notModified
	e.gh.mu.Unlock()
	if want := 2 * first; notModified != want {
		t.Fatalf("answered with 304 %d times, want %d (every request of the later polls)", notModified, want)
	}
	if got := e.incidents("all"); len(got) != 1 || got[0].Occurrences != 1 {
		t.Fatalf("incidents = %+v", got)
	}
}

func TestARateLimitPausesPolling(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) { f.retryAfter = 120 })

	e.poll()
	if got := e.repoRow(); !strings.Contains(got.LastError, "rate limited") {
		t.Fatalf("last error = %q", got.LastError)
	}
	n := e.gh.requestCount()
	if n != 1 {
		t.Fatalf("%d requests in the limited cycle, want 1", n)
	}

	e.now = e.now.Add(60 * time.Second) // still inside the pause
	e.poll()
	if e.gh.requestCount() != n {
		t.Fatal("polling continued while paused")
	}

	// The pause is over and GitHub answers again.
	e.now = e.now.Add(61 * time.Second)
	e.gh.set(func(f *fakeGitHub) { f.retryAfter = 0 })
	e.poll()
	if e.gh.requestCount() == n {
		t.Fatal("polling did not resume after the pause")
	}
	if got := e.kinds(); !slices.Equal(got, []string{store.KindPollFailed, store.KindPollRecovered}) {
		t.Fatalf("activity = %v", got)
	}
	if e.repoRow().LastError != "" {
		t.Fatalf("last error = %q, want it cleared", e.repoRow().LastError)
	}
}

func TestOneBrokenRepoDoesNotStopTheOthers(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	ctx := context.Background()
	other, err := e.st.AddRepo(ctx, store.ConnectionID, "octo/other", "main")
	if err != nil {
		t.Fatal(err)
	}
	e.gh.set(func(f *fakeGitHub) {
		f.missing["octo/hello"] = true
		f.checks["octo/other|main"] = []github.CheckRun{check("go", "completed", "failure", "ddd")}
	})
	e.poll()
	e.poll() // the failure is logged once, not once per cycle

	if got := e.repoRow(); !strings.Contains(got.LastError, "not found") {
		t.Fatalf("broken repo last error = %q", got.LastError)
	}
	if got := e.incidents("all"); len(got) != 1 || got[0].RepoID != other.ID {
		t.Fatalf("incidents = %+v, want one for the healthy repo", got)
	}
	if got := e.kinds(); !slices.Equal(got, []string{store.KindPollFailed, store.KindIncidentOpened}) {
		t.Fatalf("activity = %v", got)
	}
}

func TestADisabledRepoIsNotPolled(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	if err := e.st.SetRepoEnabled(context.Background(), e.repo.ID, false); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if n := e.gh.requestCount(); n != 0 {
		t.Fatalf("%d requests for a disabled repo", n)
	}
}

func TestARejectedTokenMarksTheConnectionAndStopsPolling(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) { f.unauth = true })

	e.poll()
	conn, _ := e.st.GetConnection(context.Background())
	if conn.Status != store.ConnError || !strings.Contains(conn.StatusDetail, "rejected") {
		t.Fatalf("connection = %+v", conn)
	}
	n := e.gh.requestCount()
	e.poll()
	if e.gh.requestCount() != n {
		t.Fatal("polling continued with a rejected token")
	}
}

func TestAnUndecryptableTokenStopsPollingWithoutARequest(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 2)) // sealed with another key than the poller has
	e.poll()

	conn, _ := e.st.GetConnection(context.Background())
	if conn.Status != store.ConnUndecryptable || conn.StatusDetail != store.UndecryptableDetail {
		t.Fatalf("connection = %+v", conn)
	}
	if n := e.gh.requestCount(); n != 0 {
		t.Fatalf("%d requests with a token that cannot be opened", n)
	}
}

func TestWithoutAConnectionNothingHappens(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	if err := e.st.DeleteConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if n := e.gh.requestCount(); n != 0 {
		t.Fatalf("%d requests without a connection", n)
	}
}

func TestANewTokenIsUsedAfterTheConnectionChanges(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.poll()

	other := "ghp_ANOTHERTOKEN0123456789abcdefghijklmnopq"
	sealed, err := testKey(t, 1).Seal([]byte(other), store.ConnectionAAD())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SaveConnection(context.Background(), store.Connection{
		TokenCiphertext: sealed, TokenHint: "nopq", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	e.poll()

	e.gh.mu.Lock()
	defer e.gh.mu.Unlock()
	if !e.gh.tokens["Bearer "+token] || !e.gh.tokens["Bearer "+other] {
		t.Fatalf("authorization headers seen: %v, want both tokens", e.gh.tokens)
	}
}

func TestTheTokenNeverReachesTheLogOrTheActivity(t *testing.T) {
	e := newEnv(t, testKey(t, 1), testKey(t, 1))
	e.gh.set(func(f *fakeGitHub) { f.retryAfter = 30 })
	e.poll()
	e.gh.set(func(f *fakeGitHub) { f.retryAfter = 0; f.unauth = true })
	e.now = e.now.Add(time.Minute)
	e.poll()

	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{})
	var text strings.Builder
	text.WriteString(e.logs.String())
	for _, a := range log {
		text.WriteString(a.Summary + string(a.Data))
	}
	if r := e.repoRow(); strings.Contains(r.LastError, "POLLERTEST") {
		t.Fatalf("the token is in last_error: %q", r.LastError)
	}
	if strings.Contains(text.String(), "POLLERTEST") {
		t.Fatalf("the token leaked into the log or the activity: %s", text.String())
	}
}
