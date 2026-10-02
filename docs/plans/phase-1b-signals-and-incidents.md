# Phase 1b: Signals and Incidents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remedy polls the enabled repositories on GitHub, turns failed checks into incidents (and resolves them again), keeps an activity log, and shows the incidents in the UI. Runs learn about roles, incident links, structured output and timeouts, and a reaper fails runs that got stuck.

**Architecture:** The read-only `github` client learns to list open pull requests and check runs, with an ETag cache and rate-limit handling. A new `incident` package holds the state machine (observation in, incident and activity entry out) on top of new `incidents` and `activity` tables. A new `poller` package turns the GitHub answers into observations once a minute. The `server` package gets the incident API, the `runs` table gets its phase 1 columns, the runner stops runs that take too long, and a `reaper` fails runs that no runner ever finished.

**Tech Stack:** Go 1.27 stdlib only (no new Go dependencies), SQLite, React 19, `react-router` 8 and the shadcn/ui components that are already installed.

**Spec:** [`docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`](../specs/2026-10-02-phase-1-detect-and-diagnose-design.md), sections 4 (tables), 5 (polling and incident correlation, the reaper), 8 (incident API), 9 (incident views) and 11 steps 5 and 6.

**Scope note:** Plan 1a landed the connection, the repos and the read-only client. This plan stops before the responder: no run is created for an incident yet, `incidents.diagnosis` stays empty, and nothing writes `diagnosing` or `diagnosed`. Plan 1c adds the context package, the automatic start rules and the diagnosis card; plan 1d the timeline and the real run against GitHub. The activity table is written here and read per incident only; the global feed and its stream come with the timeline.

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, UI copy, commit messages.
- No new Go dependencies and no new web dependencies. The incident views use the shadcn components that exist in `web/src/components/ui` and native `<select>` elements.
- The GitHub client is **read-only**: every exported method name starts with `Get` or `List`, and it only sends `GET`. The existing reflection test keeps enforcing this.
- The GitHub token never appears in an API response, a log line, an error message, an activity entry or the database in plaintext.
- Untrusted text (check names, PR titles, GitHub error messages) is shown escaped in the UI and never interpreted as instructions.
- Polling, incident correlation and the reaper are **idempotent**: running a cycle twice on the same GitHub state changes nothing the second time.
- `web/tsconfig.app.json` keeps `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types.
- Every UI change is checked in a real browser (Playwright) before it is called done.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- `make check` and `go test ./... -race -count=1` must pass at the end of every task.

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/github/client.go`, `internal/github/testdata/*.json` | `ListOpenPRs`, `ListCheckRuns`, ETag cache, `RateLimitError` |
| `internal/store/migrations/003_incidents.sql`, `internal/store/incidents.go`, `internal/store/seal.go` | Incident and activity tables and queries, the shared token context and message |
| `internal/incident/incident.go` | Classification, the state machine, activity wording |
| `internal/poller/poller.go` | One polling cycle per repo, pause on rate limits, repo health |
| `internal/config/config.go` | `REMEDY_POLL_INTERVAL`, `REMEDY_RUN_TIMEOUT` |
| `internal/server/incidents.go`, `activity.go`, `github.go` | Incident API, activity for connection and repo changes |
| `internal/store/migrations/004_run_extension.sql`, `internal/run/run.go`, `internal/store/store.go`, `internal/store/incident_runs.go` | Run role, incident link, output, failure reason |
| `internal/runner/loop.go` | Run timeout |
| `internal/store/reap.go`, `internal/reaper/reaper.go` | Fail runs that stay `running` |
| `web/src/*` | Incident list and detail, run failure reason |

---

### Task 1: GitHub client: pull requests, check runs, ETag cache

The poller needs two read calls and must not waste rate limit. `ListOpenPRs` and `ListCheckRuns` are added to the client, a response with an `ETag` is remembered so the next request is conditional (a `304` does not count against the rate limit), and `403`/`429` answers that mean "slow down" become a `RateLimitError` with the wait time.

The fixtures were captured from the real API (`gh api repos/Jaydee94/remedy/pulls` and `.../commits/main/check-runs`, 2026-10-02) and trimmed to the fields Remedy reads. A real check run for a commit carries `head_sha`; that is where the poller gets the commit of a default-branch observation. A real check run that has not finished has `"conclusion": null`.

**Files:**
- Create: `internal/github/testdata/pulls.json`, `internal/github/testdata/check-runs.json`
- Create: `internal/github/checks_test.go`
- Overwrite: `internal/github/client.go`

**Interfaces:**
- Consumes: the existing `secret.Value`, `validRepoName`.
- Produces (package `github`):
  - `const PageSize = 100`
  - `type PullRequest struct { Number int; Title string; Draft bool; HTMLURL string; Head struct{ SHA, Ref string }; User struct{ Login, Type string } }`
  - `type CheckRun struct { ID int64; Name, Status, Conclusion, HeadSHA, HTMLURL string }` (`Conclusion` is empty until the run completes)
  - `func (*Client) ListOpenPRs(ctx, fullName string) ([]PullRequest, error)`
  - `func (*Client) ListCheckRuns(ctx, fullName, ref string) ([]CheckRun, error)` (`ref` is a commit SHA or a branch name)
  - `var ErrInvalidRef error`
  - `type RateLimitError struct{ RetryAfter time.Duration }`

- [ ] **Step 1: Create the fixtures**

Create `internal/github/testdata/pulls.json`:

```json
[
  {
    "number": 32,
    "state": "closed",
    "draft": false,
    "title": "docs: document the master key, the GitHub token handling and the settings page",
    "html_url": "https://github.com/Jaydee94/remedy/pull/32",
    "head": {
      "ref": "worktree-docs-phase1a",
      "sha": "8f828550083a5a0f38501215805c4d9b899aa6f1"
    },
    "base": {
      "ref": "main"
    },
    "user": {
      "login": "Jaydee94",
      "type": "User"
    }
  },
  {
    "number": 31,
    "state": "closed",
    "draft": false,
    "title": "feat(web): add the settings page for the GitHub connection and repos",
    "html_url": "https://github.com/Jaydee94/remedy/pull/31",
    "head": {
      "ref": "worktree-feat-settings-page",
      "sha": "bf3a1b9349abaa571e96e1f5ecec290798965c5d"
    },
    "base": {
      "ref": "main"
    },
    "user": {
      "login": "Jaydee94",
      "type": "User"
    }
  }
]
```

Create `internal/github/testdata/check-runs.json`:

```json
{
  "total_count": 2,
  "check_runs": [
    {
      "id": 110957236793,
      "name": "web",
      "status": "completed",
      "conclusion": "success",
      "head_sha": "2feb68e76ef56d9721d71a29c955c6c29fd8b5ef",
      "html_url": "https://github.com/Jaydee94/remedy/actions/runs/37042940411/job/110957236793",
      "started_at": "2026-10-02T17:46:05Z",
      "completed_at": "2026-10-02T17:46:24Z"
    },
    {
      "id": 110957236546,
      "name": "go",
      "status": "completed",
      "conclusion": "success",
      "head_sha": "2feb68e76ef56d9721d71a29c955c6c29fd8b5ef",
      "html_url": "https://github.com/Jaydee94/remedy/actions/runs/37042940411/job/110957236546",
      "started_at": "2026-10-02T17:46:06Z",
      "completed_at": "2026-10-02T17:46:28Z"
    }
  ]
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/github/checks_test.go`:

```go
package github_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestListOpenPRsDecodesARealResponse(t *testing.T) {
	body := fixture(t, "pulls.json")
	var method, uri string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, uri = r.Method, r.URL.RequestURI()
		_, _ = w.Write(body)
	})

	prs, err := c.ListOpenPRs(context.Background(), "Jaydee94/remedy")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || uri != "/repos/Jaydee94/remedy/pulls?state=open&per_page=100" {
		t.Errorf("request = %s %s", method, uri)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d pull requests, want 2", len(prs))
	}
	p := prs[0]
	if p.Number != 32 || p.Draft || p.Title == "" || p.HTMLURL != "https://github.com/Jaydee94/remedy/pull/32" ||
		p.Head.SHA != "8f828550083a5a0f38501215805c4d9b899aa6f1" || p.Head.Ref != "worktree-docs-phase1a" ||
		p.User.Login != "Jaydee94" || p.User.Type != "User" {
		t.Errorf("first pull request = %+v", p)
	}
}

func TestListCheckRunsDecodesARealResponse(t *testing.T) {
	body := fixture(t, "check-runs.json")
	var method, uri string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, uri = r.Method, r.URL.RequestURI()
		_, _ = w.Write(body)
	})

	runs, err := c.ListCheckRuns(context.Background(), "Jaydee94/remedy", "main")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || uri != "/repos/Jaydee94/remedy/commits/main/check-runs?filter=latest&per_page=100" {
		t.Errorf("request = %s %s", method, uri)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d check runs, want 2", len(runs))
	}
	r := runs[0]
	if r.ID != 110957236793 || r.Name != "web" || r.Status != "completed" || r.Conclusion != "success" ||
		r.HeadSHA != "2feb68e76ef56d9721d71a29c955c6c29fd8b5ef" ||
		r.HTMLURL != "https://github.com/Jaydee94/remedy/actions/runs/37042940411/job/110957236793" {
		t.Errorf("first check run = %+v", r)
	}
}

func TestAnUnfinishedCheckRunHasNoConclusion(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_count":1,"check_runs":[{"id":7,"name":"go","status":"in_progress","conclusion":null,"head_sha":"abc","html_url":"u"}]}`))
	})

	runs, err := c.ListCheckRuns(context.Background(), "o/r", "abc")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
	if runs[0].Status != "in_progress" || runs[0].Conclusion != "" {
		t.Errorf("check run = %+v", runs[0])
	}
}

func TestABranchNameWithASlashIsAValidRef(t *testing.T) {
	var path string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"total_count":0,"check_runs":[]}`))
	})

	if _, err := c.ListCheckRuns(context.Background(), "o/r", "release/1.0"); err != nil {
		t.Fatal(err)
	}
	if path != "/repos/o/r/commits/release/1.0/check-runs" {
		t.Errorf("path = %q", path)
	}
}

func TestListCallsRejectInvalidInputWithoutARequest(t *testing.T) {
	var requests atomic.Int32
	c := newClient(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	ctx := context.Background()

	if _, err := c.ListOpenPRs(ctx, "../x"); !errors.Is(err, github.ErrInvalidRepoName) {
		t.Errorf("ListOpenPRs(../x) error = %v", err)
	}
	if _, err := c.ListCheckRuns(ctx, "bad", "abc"); !errors.Is(err, github.ErrInvalidRepoName) {
		t.Errorf("ListCheckRuns(bad) error = %v", err)
	}
	for _, ref := range []string{"", "a/../b", "/abc", "abc/", "a//b", "a?b=1", "a b", "a#frag", "./x"} {
		if _, err := c.ListCheckRuns(ctx, "o/r", ref); !errors.Is(err, github.ErrInvalidRef) {
			t.Errorf("ref %q: error = %v, want ErrInvalidRef", ref, err)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests were sent for invalid input", n)
	}
}

func TestConditionalRequestsReuseTheCachedBody(t *testing.T) {
	var (
		mu                sync.Mutex
		etag, body        = `W/"v1"`, `{"total_count":1,"check_runs":[{"id":1,"name":"go","status":"completed","conclusion":"success","head_sha":"abc"}]}`
		requests, cond304 int
	)
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.Header.Get("If-None-Match") == etag {
			cond304++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_, _ = w.Write([]byte(body))
	})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		runs, err := c.ListCheckRuns(ctx, "o/r", "abc")
		if err != nil || len(runs) != 1 || runs[0].Name != "go" {
			t.Fatalf("call %d: runs = %+v, err = %v", i, runs, err)
		}
	}
	mu.Lock()
	if requests != 3 || cond304 != 2 {
		t.Fatalf("requests = %d, answered with 304 = %d; want 3 and 2", requests, cond304)
	}
	// The resource changes: a new ETag and a new body must replace the cached one.
	etag = `W/"v2"`
	body = `{"total_count":1,"check_runs":[{"id":1,"name":"go","status":"completed","conclusion":"failure","head_sha":"def"}]}`
	mu.Unlock()

	runs, err := c.ListCheckRuns(ctx, "o/r", "abc")
	if err != nil || len(runs) != 1 || runs[0].Conclusion != "failure" || runs[0].HeadSHA != "def" {
		t.Fatalf("after the change: runs = %+v, err = %v", runs, err)
	}
}

func TestEachPathHasItsOwnCacheEntry(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("a first request to %s carried If-None-Match", r.URL.Path)
		}
		w.Header().Set("ETag", `W/"`+r.URL.Path+`"`)
		_, _ = w.Write([]byte(`{"total_count":0,"check_runs":[]}`))
	})
	ctx := context.Background()
	for _, ref := range []string{"aaa", "bbb"} {
		if _, err := c.ListCheckRuns(ctx, "o/r", ref); err != nil {
			t.Fatal(err)
		}
	}
}

func TestANotModifiedAnswerWithoutACachedBodyIsAnError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotModified) })

	if _, err := c.ListOpenPRs(context.Background(), "o/r"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRateLimitsBecomeRateLimitErrors(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(45*time.Second).Unix(), 10)
	cases := []struct {
		name     string
		status   int
		headers  map[string]string
		min, max time.Duration
	}{
		{"secondary limit with Retry-After", http.StatusForbidden, map[string]string{"Retry-After": "30"}, 30 * time.Second, 30 * time.Second},
		{"primary limit with a reset time", http.StatusForbidden, map[string]string{"X-Ratelimit-Remaining": "0", "X-Ratelimit-Reset": reset}, 40 * time.Second, 46 * time.Second},
		{"primary limit without a reset time", http.StatusForbidden, map[string]string{"X-Ratelimit-Remaining": "0"}, time.Minute, time.Minute},
		{"429 without hints", http.StatusTooManyRequests, nil, time.Minute, time.Minute},
		{"a huge Retry-After is capped", http.StatusTooManyRequests, map[string]string{"Retry-After": "86400"}, time.Hour, time.Hour},
	}
	for _, tc := range cases {
		c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
			for k, v := range tc.headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"message":"slow down"}`))
		})
		_, err := c.ListOpenPRs(context.Background(), "o/r")
		var rl *github.RateLimitError
		if !errors.As(err, &rl) {
			t.Errorf("%s: error = %v, want a *RateLimitError", tc.name, err)
			continue
		}
		if rl.RetryAfter < tc.min || rl.RetryAfter > tc.max {
			t.Errorf("%s: RetryAfter = %s, want %s to %s", tc.name, rl.RetryAfter, tc.min, tc.max)
		}
	}
}

func TestAPlain403IsNotARateLimit(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	})

	_, err := c.ListOpenPRs(context.Background(), "o/r")
	var rl *github.RateLimitError
	var api *github.APIError
	if errors.As(err, &rl) || !errors.As(err, &api) || api.Status != http.StatusForbidden {
		t.Fatalf("error = %v, want a plain *APIError with status 403", err)
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/github -count=1`
Expected: FAIL to compile with `c.ListOpenPRs undefined`, `c.ListCheckRuns undefined`, `github.ErrInvalidRef undefined`, `github.RateLimitError undefined`.

- [ ] **Step 4: Implement the client changes**

Overwrite `internal/github/client.go`:

```go
// Package github is a read-only client for the parts of the GitHub REST API that Remedy uses.
// It only ever sends GET requests, and its errors never contain the token.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	apiVersion = "2022-11-28"
	userAgent  = "remedy"

	// PageSize is the page size of the list calls. A list that comes back full may be truncated.
	PageSize = 100

	// maxBody bounds a response. A full page of pull requests is a few megabytes of JSON.
	maxBody = 8 << 20

	// The ETag cache keeps response bodies so that a 304 can be answered from memory.
	maxCachedBody   = 2 << 20
	maxCacheEntries = 256

	defaultRateLimitWait = time.Minute
	maxRateLimitWait     = time.Hour
)

var (
	// ErrUnauthorized means GitHub rejected the token.
	ErrUnauthorized = errors.New("github: token rejected")
	// ErrNotFound means the resource does not exist or the token has no access to it. GitHub
	// answers 404 in both cases on purpose.
	ErrNotFound = errors.New("github: not found, or no access")
	// ErrInvalidRepoName means the name is not of the form owner/name.
	ErrInvalidRepoName = errors.New("github: repository name must be owner/name")
	// ErrInvalidRef means the commit SHA or branch name cannot be used in a request path.
	ErrInvalidRef = errors.New("github: invalid commit or branch name")
)

var (
	repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	refName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// validRepoName accepts owner/name. The character class allows dots, so "." and ".." must be
// rejected explicitly: "../x" would otherwise turn into a request to a different path.
func validRepoName(s string) bool {
	if !repoName.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// validRef accepts a commit SHA or a branch name (which may contain slashes) that is safe to put
// into a request path.
func validRef(s string) bool {
	if !refName.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// APIError is any other non-success answer, for example a 403 for a missing permission or a 5xx.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message) }

// RateLimitError means GitHub asked the client to slow down (primary or secondary rate limit).
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("github: rate limited, retry in %s", e.RetryAfter.Round(time.Second))
}

type User struct {
	Login string `json:"login"`
}

type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// PullRequest is the part of a pull request that Remedy reads.
type PullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// CheckRun is one check of a commit. Conclusion is empty until the run has completed.
type CheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
	HTMLURL    string `json:"html_url"`
}

type cacheEntry struct {
	etag string
	body []byte
}

// Client is a read-only GitHub API client. Every exported method is a Get or a List. A Client
// remembers the ETag and the body of its answers, so use one Client per token for as long as the
// token lives.
type Client struct {
	baseURL string
	token   secret.Value
	http    *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry // by request path and query
}

// New returns a client for baseURL (for example https://api.github.com). A nil httpClient gets a
// 20 second timeout.
func New(baseURL string, token secret.Value, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
		cache:   map[string]cacheEntry{},
	}
}

// scrub removes the token from text that may be shown to a user or written to a log.
func (c *Client) scrub(s string) string {
	if t := c.token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

func (c *Client) cached(path string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[path]
	return e, ok
}

func (c *Client) remember(path, etag string, body []byte) {
	if etag == "" || len(body) > maxCachedBody {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= maxCacheEntries {
		clear(c.cache) // a simple bound; the next cycle refills what it needs
	}
	c.cache[path] = cacheEntry{etag: etag, body: body}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+c.token.Reveal())
	cached, hasCached := c.cached(path)
	if hasCached {
		req.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))

	switch resp.StatusCode {
	case http.StatusOK:
		c.remember(path, resp.Header.Get("ETag"), body)
	case http.StatusNotModified:
		if !hasCached {
			return errors.New("github: unexpected 304 without a cached response")
		}
		body = cached.body
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		if wait, ok := rateLimitWait(resp.StatusCode, resp.Header); ok {
			return &RateLimitError{RetryAfter: wait}
		}
		return c.apiError(resp.StatusCode, body)
	default:
		return c.apiError(resp.StatusCode, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("github: unexpected response: %w", err)
	}
	return nil
}

func (c *Client) apiError(status int, body []byte) error {
	msg := string(body)
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return &APIError{Status: status, Message: c.scrub(strings.TrimSpace(msg))}
}

// rateLimitWait reads how long GitHub wants the client to wait. ok is false when the answer is not
// a rate limit: a 403 can also mean a missing permission.
func rateLimitWait(status int, h http.Header) (wait time.Duration, ok bool) {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return clampWait(time.Duration(secs) * time.Second), true
		}
	}
	if h.Get("X-Ratelimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			return clampWait(time.Until(time.Unix(reset, 0))), true
		}
		return defaultRateLimitWait, true
	}
	if status == http.StatusTooManyRequests {
		return defaultRateLimitWait, true
	}
	return 0, false
}

func clampWait(d time.Duration) time.Duration {
	return min(max(d, time.Second), maxRateLimitWait)
}

// GetUser returns the account the token belongs to. It is the cheapest way to check a token.
func (c *Client) GetUser(ctx context.Context) (User, error) {
	var u User
	if err := c.get(ctx, "/user", &u); err != nil {
		return User{}, err
	}
	if u.Login == "" {
		return User{}, errors.New("github: unexpected response: no login")
	}
	return u, nil
}

// GetRepo returns a repository by its full name (owner/name).
func (c *Client) GetRepo(ctx context.Context, fullName string) (Repo, error) {
	if !validRepoName(fullName) {
		return Repo{}, ErrInvalidRepoName
	}
	var r Repo
	if err := c.get(ctx, "/repos/"+fullName, &r); err != nil {
		return Repo{}, err
	}
	return r, nil
}

// ListOpenPRs returns the open pull requests of a repository, newest first, at most PageSize.
func (c *Client) ListOpenPRs(ctx context.Context, fullName string) ([]PullRequest, error) {
	if !validRepoName(fullName) {
		return nil, ErrInvalidRepoName
	}
	var prs []PullRequest
	if err := c.get(ctx, "/repos/"+fullName+"/pulls?state=open&per_page="+strconv.Itoa(PageSize), &prs); err != nil {
		return nil, err
	}
	return prs, nil
}

// ListCheckRuns returns the latest check run per check name for a commit SHA or a branch name, at
// most PageSize.
func (c *Client) ListCheckRuns(ctx context.Context, fullName, ref string) ([]CheckRun, error) {
	if !validRepoName(fullName) {
		return nil, ErrInvalidRepoName
	}
	if !validRef(ref) {
		return nil, ErrInvalidRef
	}
	var page struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	path := "/repos/" + fullName + "/commits/" + ref + "/check-runs?filter=latest&per_page=" + strconv.Itoa(PageSize)
	if err := c.get(ctx, path, &page); err != nil {
		return nil, err
	}
	return page.CheckRuns, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal/github && go vet ./internal/github && go test ./internal/github -race -count=1`
Expected: no gofmt output, vet clean, `ok`. The existing tests (`TestOnlyReadMethodsAreExported`, `TestStatusCodesMapToErrors`, `TestErrorsNeverContainTheToken`) must still pass.

- [ ] **Step 6: Commit**

```bash
git add internal/github
git commit -m "feat(github): list open pull requests and check runs with an ETag cache" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 2: Migration 003 and the incident and activity store

**Files:**
- Create: `internal/store/migrations/003_incidents.sql`
- Create: `internal/store/seal.go`, `internal/store/seal_test.go`
- Create: `internal/store/incidents.go`, `internal/store/incidents_test.go`

**Interfaces:**
- Consumes: `Store`, `ErrNotFound`, `ErrExists`, `scanner`, `formatTS`, `parseTS`, `Repo`, `AddRepo` from plan 1a.
- Produces (package `store`):
  - `func ConnectionAAD() string` (`"github_connection:1"`) and `const UndecryptableDetail string`
  - `type IncidentState string` with `IncOpen`, `IncDiagnosing`, `IncDiagnosed`, `IncResolved`, `IncIgnored`
  - `type Incident struct { ID, RepoID int64; RepoName, Ref, RefURL, CheckName string; State IncidentState; Conclusion, HeadSHA, CheckURL string; Occurrences int; FirstSeen, LastSeen time.Time; ResolvedAt *time.Time; ResolvedReason string }`
  - `type NewIncident struct { RepoID int64; Ref, RefURL, CheckName, Conclusion, HeadSHA, CheckURL string }`
  - `type IncidentFilter struct { State string; RepoID int64; Limit int }` (`State` is `""`, `"all"`, `"active"` (open, diagnosing, diagnosed) or one state)
  - `type NewActivity struct { Kind string; RepoID, IncidentID int64; RunID, Summary string; Data json.RawMessage }` (zero means none; `Data` defaults to `{}`)
  - `type Activity struct { ID int64; At time.Time; Kind string; RepoID, IncidentID int64; RunID, Summary string; Data json.RawMessage }`
  - `type ActivityQuery struct { IncidentID int64; Limit int }`
  - `const KindIncidentOpened`, `KindIncidentRecurred`, `KindIncidentResolved`, `KindIncidentIgnored`, `KindPollFailed`, `KindPollRecovered`, `KindConnectionChanged`, `KindRepoAdded`, `KindRepoRemoved`
  - `func (*Store) OpenIncident(ctx, NewIncident, NewActivity) (Incident, error)` (`ErrExists` if the key has an active incident)
  - `func (*Store) RecordRecurrence(ctx, id int64, conclusion, headSHA, checkURL string, NewActivity) error`
  - `func (*Store) TouchIncident(ctx, id int64) error`
  - `func (*Store) ResolveIncident(ctx, id int64, reason string, NewActivity) error`
  - `func (*Store) IgnoreIncident(ctx, id int64, NewActivity) error`
  - the four mutations return `ErrNotFound` when the incident does not exist or is not in a state the change applies to
  - `func (*Store) GetIncident(ctx, id int64) (Incident, error)`, `FindActiveIncident(ctx, repoID int64, ref, checkName string) (Incident, error)`, `ListActiveIncidents(ctx, repoID int64) ([]Incident, error)`, `ListIncidents(ctx, IncidentFilter) ([]Incident, error)`
  - `func (*Store) AddActivity(ctx, NewActivity) error`, `ListActivity(ctx, ActivityQuery) ([]Activity, error)` (newest first)
  - `func (*Store) MarkRepoPolled(ctx, id int64, at time.Time, lastError string) error`

Every incident mutation writes its activity entry in the same transaction, so the log never disagrees with the state. The key `(repo, ref, check name)` is unique among incidents that are not resolved.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/seal_test.go`:

```go
package store_test

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

// Tokens that are already sealed in a database depend on this exact value.
func TestConnectionAADIsStable(t *testing.T) {
	if got := store.ConnectionAAD(); got != "github_connection:1" {
		t.Fatalf("ConnectionAAD() = %q; changing it makes every stored token undecryptable", got)
	}
}
```

Create `internal/store/incidents_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func seedRepo(t *testing.T, s *store.Store) store.Repo {
	t.Helper()
	ctx := context.Background()
	if err := s.SaveConnection(ctx, connection("octo")); err != nil {
		t.Fatal(err)
	}
	r, err := s.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func failing(repoID int64, ref, check, sha string) store.NewIncident {
	return store.NewIncident{
		RepoID: repoID, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7", CheckName: check,
		Conclusion: "failure", HeadSHA: sha, CheckURL: "https://github.com/octo/hello/runs/1",
	}
}

func entry(kind string, repoID int64) store.NewActivity {
	return store.NewActivity{Kind: kind, RepoID: repoID, Summary: kind + " summary"}
}

func TestOpenIncidentStoresItAndLogsTheActivity(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)

	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	if in.State != store.IncOpen || in.Occurrences != 1 || in.RepoName != "octo/hello" || in.RepoID != repo.ID ||
		in.Ref != "pr:7" || in.CheckName != "go" || in.HeadSHA != "aaa" || in.Conclusion != "failure" ||
		in.RefURL == "" || in.CheckURL == "" || in.ResolvedAt != nil || in.FirstSeen.IsZero() || !in.FirstSeen.Equal(in.LastSeen) {
		t.Fatalf("incident = %+v", in)
	}

	log, err := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if err != nil || len(log) != 1 {
		t.Fatalf("activity = %+v, err = %v", log, err)
	}
	a := log[0]
	if a.Kind != store.KindIncidentOpened || a.IncidentID != in.ID || a.RepoID != repo.ID ||
		a.Summary != "incident_opened summary" || string(a.Data) != "{}" || a.At.IsZero() {
		t.Fatalf("activity entry = %+v", a)
	}
}

func TestOnlyOneActiveIncidentPerKey(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)

	first, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "bbb"), entry(store.KindIncidentOpened, repo.ID)); !errors.Is(err, store.ErrExists) {
		t.Fatalf("second incident for the same key: error = %v, want ErrExists", err)
	}
	if all, _ := s.ListActivity(ctx, store.ActivityQuery{}); len(all) != 1 {
		t.Fatalf("the refused incident left %d activity entries, want 1", len(all))
	}
	// Another check name, another ref: both are different keys.
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "web", "aaa"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, failing(repo.ID, "pr:8", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID)); err != nil {
		t.Fatal(err)
	}

	// A resolved incident no longer blocks the key.
	if err := s.ResolveIncident(ctx, first.ID, "green", entry(store.KindIncidentResolved, repo.ID)); err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "ccc"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil || second.ID == first.ID {
		t.Fatalf("reopening after resolve: %+v, %v", second, err)
	}
	found, err := s.FindActiveIncident(ctx, repo.ID, "pr:7", "go")
	if err != nil || found.ID != second.ID {
		t.Fatalf("FindActiveIncident = %+v, %v, want the new incident %d", found, err, second.ID)
	}
}

func TestRecordRecurrence(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.RecordRecurrence(ctx, in.ID, "timed_out", "bbb", "https://example.test/2", entry(store.KindIncidentRecurred, repo.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.Occurrences != 2 || got.HeadSHA != "bbb" || got.Conclusion != "timed_out" || got.CheckURL != "https://example.test/2" ||
		got.State != store.IncOpen || !got.FirstSeen.Equal(in.FirstSeen) || got.LastSeen.Before(in.LastSeen) {
		t.Fatalf("incident after the recurrence = %+v", got)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 2 || log[0].Kind != store.KindIncidentRecurred || log[1].Kind != store.KindIncidentOpened {
		t.Fatalf("activity = %+v, want recurred then opened (newest first)", log)
	}

	_ = s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	if err := s.RecordRecurrence(ctx, in.ID, "failure", "ccc", "", entry(store.KindIncidentRecurred, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("recurrence on a resolved incident: error = %v, want ErrNotFound", err)
	}
}

func TestTouchIncidentOnlyMovesLastSeen(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.TouchIncident(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.LastSeen.Before(in.LastSeen) || got.Occurrences != 1 || got.HeadSHA != "aaa" {
		t.Fatalf("incident after touch = %+v", got)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID}); len(log) != 1 {
		t.Fatalf("touch wrote an activity entry: %+v", log)
	}
	if err := s.TouchIncident(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("touch of an unknown incident: error = %v", err)
	}
}

func TestResolveIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIncident(ctx, in.ID)
	if got.State != store.IncResolved || got.ResolvedReason != "green" || got.ResolvedAt == nil {
		t.Fatalf("incident after resolve = %+v", got)
	}
	if _, err := s.FindActiveIncident(ctx, repo.ID, "pr:7", "go"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a resolved incident is still active: %v", err)
	}
	if err := s.ResolveIncident(ctx, in.ID, "green", entry(store.KindIncidentResolved, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second resolve: error = %v, want ErrNotFound", err)
	}
	if log, _ := s.ListActivity(ctx, store.ActivityQuery{IncidentID: in.ID}); len(log) != 2 || log[0].Kind != store.KindIncidentResolved {
		t.Fatalf("activity = %+v", log)
	}
}

func TestIgnoreIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetIncident(ctx, in.ID); got.State != store.IncIgnored {
		t.Fatalf("state = %q, want ignored", got.State)
	}
	// An ignored incident still holds its key, so that a new failure does not open a second one.
	if _, err := s.FindActiveIncident(ctx, repo.ID, "pr:7", "go"); err != nil {
		t.Fatalf("FindActiveIncident of an ignored incident: %v", err)
	}
	if err := s.IgnoreIncident(ctx, in.ID, entry(store.KindIncidentIgnored, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignoring twice: error = %v, want ErrNotFound", err)
	}

	other, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:8", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	_ = s.ResolveIncident(ctx, other.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	if err := s.IgnoreIncident(ctx, other.ID, entry(store.KindIncidentIgnored, repo.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignoring a resolved incident: error = %v, want ErrNotFound", err)
	}
}

func TestListIncidentsFiltersAndOrders(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	other, err := s.AddRepo(ctx, store.ConnectionID, "octo/other", "main")
	if err != nil {
		t.Fatal(err)
	}
	open := func(repoID int64, ref, check string) store.Incident {
		in, err := s.OpenIncident(ctx, failing(repoID, ref, check, "aaa"), entry(store.KindIncidentOpened, repoID))
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	a := open(repo.ID, "pr:7", "go")
	b := open(repo.ID, "pr:8", "go")
	c := open(repo.ID, "pr:9", "web")
	d := open(other.ID, "pr:1", "go")
	_ = s.ResolveIncident(ctx, b.ID, "green", entry(store.KindIncidentResolved, repo.ID))
	_ = s.IgnoreIncident(ctx, c.ID, entry(store.KindIncidentIgnored, repo.ID))

	ids := func(f store.IncidentFilter) []int64 {
		list, err := s.ListIncidents(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, in := range list {
			out = append(out, in.ID)
		}
		return out
	}
	same := func(got, want []int64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name   string
		filter store.IncidentFilter
		want   []int64
	}{
		{"all, newest first", store.IncidentFilter{}, []int64{d.ID, c.ID, b.ID, a.ID}},
		{"all, spelled out", store.IncidentFilter{State: "all"}, []int64{d.ID, c.ID, b.ID, a.ID}},
		{"active", store.IncidentFilter{State: "active"}, []int64{d.ID, a.ID}},
		{"resolved", store.IncidentFilter{State: "resolved"}, []int64{b.ID}},
		{"ignored", store.IncidentFilter{State: "ignored"}, []int64{c.ID}},
		{"one repo", store.IncidentFilter{RepoID: repo.ID}, []int64{c.ID, b.ID, a.ID}},
		{"one repo, active", store.IncidentFilter{RepoID: repo.ID, State: "active"}, []int64{a.ID}},
		{"limit", store.IncidentFilter{Limit: 2}, []int64{d.ID, c.ID}},
	}
	for _, tc := range cases {
		if got := ids(tc.filter); !same(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	active, err := s.ListActiveIncidents(ctx, repo.ID)
	if err != nil || len(active) != 2 {
		t.Fatalf("ListActiveIncidents = %+v, %v; want the open and the ignored incident of the repo", active, err)
	}
	if _, err := s.GetIncident(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIncident(unknown) error = %v", err)
	}
}

func TestListActivityIsNewestFirstAndLimited(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	for _, kind := range []string{"a", "b", "c", "d", "e"} {
		if err := s.AddActivity(ctx, store.NewActivity{Kind: kind, Summary: kind}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListActivity(ctx, store.ActivityQuery{Limit: 3})
	if err != nil || len(got) != 3 || got[0].Kind != "e" || got[1].Kind != "d" || got[2].Kind != "c" {
		t.Fatalf("activity = %+v, err = %v", got, err)
	}
	if got[0].RepoID != 0 || got[0].IncidentID != 0 || got[0].RunID != "" {
		t.Fatalf("an entry without links has links: %+v", got[0])
	}
}

func TestAddActivityKeepsValidDataAndRejectsInvalidData(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if err := s.AddActivity(ctx, store.NewActivity{Kind: "k", Summary: "s", Data: []byte(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(ctx, store.NewActivity{Kind: "k", Summary: "s", Data: []byte(`{bad`)}); err == nil {
		t.Fatal("expected an error for data that is not JSON")
	}
	got, _ := s.ListActivity(ctx, store.ActivityQuery{})
	if len(got) != 1 || string(got[0].Data) != `{"a":1}` {
		t.Fatalf("activity = %+v", got)
	}
}

func TestDeletingARepoKeepsItsHistory(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))

	if err := s.DeleteRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIncident(ctx, in.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the incident of a removed repo is still there: %v", err)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{})
	if len(log) != 1 || log[0].IncidentID != 0 || log[0].RepoID != 0 || log[0].Summary == "" {
		t.Fatalf("history after removing the repo = %+v", log)
	}
}

func TestMarkRepoPolled(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	if err := s.MarkRepoPolled(ctx, repo.ID, at, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRepo(ctx, repo.ID)
	if got.LastPolledAt == nil || !got.LastPolledAt.Equal(at) || got.LastError != "boom" {
		t.Fatalf("repo = %+v", got)
	}
	if err := s.MarkRepoPolled(ctx, repo.ID, at.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRepo(ctx, repo.ID); got.LastError != "" {
		t.Fatalf("last error = %q, want it cleared", got.LastError)
	}
	if err := s.MarkRepoPolled(ctx, 9999, at, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown repo: error = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/store -count=1`
Expected: FAIL to compile with `undefined: store.ConnectionAAD`, `store.OpenIncident`, `store.NewIncident` and so on.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/003_incidents.sql`:

```sql
-- One incident per (repo, ref, check name) while it is not resolved. Ignored incidents keep their key
-- so that a new failure does not open a second one; they resolve like the others when the check is green.
-- diagnoses, last_diagnosis_at, diagnosis and run_id are for the responder (plan 1c).
CREATE TABLE incidents (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  repo_id           INTEGER NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
  ref               TEXT NOT NULL,
  ref_url           TEXT NOT NULL DEFAULT '',
  check_name        TEXT NOT NULL,
  state             TEXT NOT NULL CHECK (state IN ('open', 'diagnosing', 'diagnosed', 'resolved', 'ignored')),
  conclusion        TEXT NOT NULL,
  head_sha          TEXT NOT NULL,
  check_url         TEXT NOT NULL DEFAULT '',
  occurrences       INTEGER NOT NULL DEFAULT 1,
  diagnoses         INTEGER NOT NULL DEFAULT 0,
  first_seen        TEXT NOT NULL,
  last_seen         TEXT NOT NULL,
  last_diagnosis_at TEXT,
  resolved_at       TEXT,
  resolved_reason   TEXT NOT NULL DEFAULT '',
  diagnosis         TEXT,
  run_id            TEXT REFERENCES runs (id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX incidents_active_key ON incidents (repo_id, ref, check_name) WHERE state <> 'resolved';
CREATE INDEX incidents_state_seen ON incidents (state, last_seen);

-- Append-only. The links are nulled, not cascaded, when their target is deleted: the history stays and
-- its summary text still names what happened.
CREATE TABLE activity (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  at          TEXT NOT NULL,
  kind        TEXT NOT NULL,
  repo_id     INTEGER REFERENCES repos (id) ON DELETE SET NULL,
  incident_id INTEGER REFERENCES incidents (id) ON DELETE SET NULL,
  run_id      TEXT REFERENCES runs (id) ON DELETE SET NULL,
  summary     TEXT NOT NULL,
  data        TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX activity_incident ON activity (incident_id, id);
```

- [ ] **Step 4: Write the store code**

Create `internal/store/seal.go`:

```go
package store

import "strconv"

// ConnectionAAD binds the sealed GitHub token to its row, so a ciphertext cannot be moved to another
// one. Tokens that are already sealed in a database depend on this exact value: never change it.
func ConnectionAAD() string { return "github_connection:" + strconv.FormatInt(ConnectionID, 10) }

// UndecryptableDetail is what the UI shows when the master key does not open the stored token.
const UndecryptableDetail = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
```

Create `internal/store/incidents.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Activity kinds.
const (
	KindIncidentOpened    = "incident_opened"
	KindIncidentRecurred  = "incident_recurred"
	KindIncidentResolved  = "incident_resolved"
	KindIncidentIgnored   = "incident_ignored"
	KindPollFailed        = "poll_failed"
	KindPollRecovered     = "poll_recovered"
	KindConnectionChanged = "connection_changed"
	KindRepoAdded         = "repo_added"
	KindRepoRemoved       = "repo_removed"
)

type IncidentState string

const (
	IncOpen       IncidentState = "open"
	IncDiagnosing IncidentState = "diagnosing"
	IncDiagnosed  IncidentState = "diagnosed"
	IncResolved   IncidentState = "resolved"
	IncIgnored    IncidentState = "ignored"
)

type Incident struct {
	ID             int64
	RepoID         int64
	RepoName       string
	Ref            string // "pr:<number>" or "branch:<name>"
	RefURL         string
	CheckName      string
	State          IncidentState
	Conclusion     string
	HeadSHA        string
	CheckURL       string
	Occurrences    int
	FirstSeen      time.Time
	LastSeen       time.Time
	ResolvedAt     *time.Time
	ResolvedReason string
}

type NewIncident struct {
	RepoID                                                int64
	Ref, RefURL, CheckName, Conclusion, HeadSHA, CheckURL string
}

// IncidentFilter selects incidents. State is "" or "all" for every incident, "active" for open,
// diagnosing and diagnosed ones, or one state.
type IncidentFilter struct {
	State  string
	RepoID int64
	Limit  int // default 200
}

const (
	incidentCols = `i.id, i.repo_id, r.full_name, i.ref, i.ref_url, i.check_name, i.state, i.conclusion,
		i.head_sha, i.check_url, i.occurrences, i.first_seen, i.last_seen, i.resolved_at, i.resolved_reason`
	incidentFrom = ` FROM incidents i JOIN repos r ON r.id = i.repo_id`
)

func scanIncident(sc scanner) (Incident, error) {
	var (
		in          Incident
		state       string
		first, last string
		resolved    sql.NullString
	)
	if err := sc.Scan(&in.ID, &in.RepoID, &in.RepoName, &in.Ref, &in.RefURL, &in.CheckName, &state, &in.Conclusion,
		&in.HeadSHA, &in.CheckURL, &in.Occurrences, &first, &last, &resolved, &in.ResolvedReason); err != nil {
		return Incident{}, err
	}
	in.State = IncidentState(state)
	var err error
	if in.FirstSeen, err = parseTS(first); err != nil {
		return Incident{}, err
	}
	if in.LastSeen, err = parseTS(last); err != nil {
		return Incident{}, err
	}
	if resolved.Valid {
		t, err := parseTS(resolved.String)
		if err != nil {
			return Incident{}, err
		}
		in.ResolvedAt = &t
	}
	return in, nil
}

func collectIncidents(rows *sql.Rows) ([]Incident, error) {
	defer rows.Close()
	list := []Incident{}
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, in)
	}
	return list, rows.Err()
}

// inTx runs fn in a transaction. The store has a single connection, so fn must only use tx.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// oneRow turns "no row changed" into ErrNotFound.
func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// OpenIncident creates an open incident and logs act in the same transaction. It returns ErrExists
// if the key already has an active incident.
func (s *Store) OpenIncident(ctx context.Context, n NewIncident, act NewActivity) (Incident, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := formatTS(time.Now())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO incidents (repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url,
				occurrences, first_seen, last_seen)
			VALUES (?, ?, ?, ?, 'open', ?, ?, ?, 1, ?, ?)`,
			n.RepoID, n.Ref, n.RefURL, n.CheckName, n.Conclusion, n.HeadSHA, n.CheckURL, now, now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return ErrExists
			}
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		act.RepoID, act.IncidentID = n.RepoID, id
		return insertActivity(ctx, tx, act)
	})
	if err != nil {
		return Incident{}, err
	}
	return s.GetIncident(ctx, id)
}

// RecordRecurrence records a new failing commit of an incident that is not resolved.
func (s *Store) RecordRecurrence(ctx context.Context, id int64, conclusion, headSHA, checkURL string, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET occurrences = occurrences + 1, conclusion = ?, head_sha = ?, check_url = ?, last_seen = ?
			WHERE id = ? AND state <> 'resolved'`, conclusion, headSHA, checkURL, formatTS(time.Now()), id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// TouchIncident records that the incident was seen failing again on the same commit. It writes no
// activity entry.
func (s *Store) TouchIncident(ctx context.Context, id int64) error {
	return oneRow(s.db.ExecContext(ctx,
		`UPDATE incidents SET last_seen = ? WHERE id = ? AND state <> 'resolved'`, formatTS(time.Now()), id))
}

// ResolveIncident resolves an incident that is not resolved yet.
func (s *Store) ResolveIncident(ctx context.Context, id int64, reason string, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'resolved', resolved_at = ?, resolved_reason = ?
			WHERE id = ? AND state <> 'resolved'`, formatTS(time.Now()), reason, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// IgnoreIncident moves an open, diagnosing or diagnosed incident to ignored.
func (s *Store) IgnoreIncident(ctx context.Context, id int64, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'ignored'
			WHERE id = ? AND state IN ('open', 'diagnosing', 'diagnosed')`, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

func (s *Store) GetIncident(ctx context.Context, id int64) (Incident, error) {
	in, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incidentCols+incidentFrom+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return in, err
}

// FindActiveIncident returns the incident of a key that is not resolved (ErrNotFound if there is none).
func (s *Store) FindActiveIncident(ctx context.Context, repoID int64, ref, checkName string) (Incident, error) {
	in, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incidentCols+incidentFrom+
		` WHERE i.repo_id = ? AND i.ref = ? AND i.check_name = ? AND i.state <> 'resolved'`, repoID, ref, checkName))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return in, err
}

// ListActiveIncidents returns the incidents of a repo that are not resolved.
func (s *Store) ListActiveIncidents(ctx context.Context, repoID int64) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+incidentFrom+
		` WHERE i.repo_id = ? AND i.state <> 'resolved' ORDER BY i.id`, repoID)
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}

// ListIncidents returns incidents, the most recently seen first.
func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	var (
		conds []string
		args  []any
	)
	switch f.State {
	case "", "all":
	case "active":
		conds = append(conds, `i.state IN ('open', 'diagnosing', 'diagnosed')`)
	default:
		conds = append(conds, `i.state = ?`)
		args = append(args, f.State)
	}
	if f.RepoID != 0 {
		conds = append(conds, `i.repo_id = ?`)
		args = append(args, f.RepoID)
	}
	query := `SELECT ` + incidentCols + incidentFrom
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	query += ` ORDER BY i.last_seen DESC, i.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}

// NewActivity describes an activity entry. A zero RepoID, IncidentID or RunID means no link, and nil
// Data means {}.
type NewActivity struct {
	Kind       string
	RepoID     int64
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}

type Activity struct {
	ID         int64
	At         time.Time
	Kind       string
	RepoID     int64
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}

// ActivityQuery selects activity entries. IncidentID 0 means all of them; Limit defaults to 100.
type ActivityQuery struct {
	IncidentID int64
	Limit      int
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertActivity(ctx context.Context, q execer, a NewActivity) error {
	data := a.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	if !json.Valid(data) {
		return errors.New("activity data is not valid JSON")
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO activity (at, kind, repo_id, incident_id, run_id, summary, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		formatTS(time.Now()), a.Kind, nullInt(a.RepoID), nullInt(a.IncidentID), nullString(a.RunID), a.Summary, string(data))
	return err
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// AddActivity appends an entry to the activity log.
func (s *Store) AddActivity(ctx context.Context, a NewActivity) error {
	return insertActivity(ctx, s.db, a)
}

// ListActivity returns activity entries, newest first.
func (s *Store) ListActivity(ctx context.Context, q ActivityQuery) ([]Activity, error) {
	query := `SELECT id, at, kind, repo_id, incident_id, run_id, summary, data FROM activity`
	var args []any
	if q.IncidentID != 0 {
		query += ` WHERE incident_id = ?`
		args = append(args, q.IncidentID)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Activity{}
	for rows.Next() {
		var (
			a         Activity
			at, data  string
			repo, inc sql.NullInt64
			runID     sql.NullString
		)
		if err := rows.Scan(&a.ID, &at, &a.Kind, &repo, &inc, &runID, &a.Summary, &data); err != nil {
			return nil, err
		}
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		a.RepoID, a.IncidentID, a.RunID = repo.Int64, inc.Int64, runID.String
		a.Data = json.RawMessage(data)
		list = append(list, a)
	}
	return list, rows.Err()
}

// MarkRepoPolled records the time and the outcome of a polling attempt. An empty lastError means it worked.
func (s *Store) MarkRepoPolled(ctx context.Context, id int64, at time.Time, lastError string) error {
	return oneRow(s.db.ExecContext(ctx,
		`UPDATE repos SET last_polled_at = ?, last_error = ? WHERE id = ?`, formatTS(at), lastError, id))
}
```

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal/store && go vet ./internal/store && go test ./internal/store -race -count=1`
Expected: no gofmt output, vet clean, `ok`. `TestOpenMigratesAnExistingDatabaseAndKeepsItsData` also passes: a database that only knows migration 001 now migrates through 003.

- [ ] **Step 6: Commit**

```bash
git add internal/store
git commit -m "feat(store): add the incident and activity tables" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The incident engine

The state machine from spec section 5. It knows nothing about GitHub: it receives **observations** and decides whether to open, update, resolve or leave an incident, and it words the activity entries.

**Files:**
- Create: `internal/incident/incident.go`
- Test: `internal/incident/incident_test.go`

**Interfaces:**
- Consumes: the store from Task 2.
- Produces (package `incident`):
  - `type Class int` with `Green`, `Pending`, `Bad` (ordered by severity: when several check runs share a key, the highest wins)
  - `func Classify(status, conclusion string) Class`
  - `type Observation struct { RepoID int64; RepoName, Ref, RefURL, CheckName string; Class Class; Conclusion, HeadSHA, URL string }`
  - `type Engine struct { Store *store.Store }`
  - `func (*Engine) Observe(ctx, Observation) error`
  - `func (*Engine) ResolveClosedPRs(ctx, repoID int64, openPRs map[int]bool) error`
  - `func (*Engine) Ignore(ctx, id int64) (store.Incident, error)` (`store.ErrNotFound` for an unknown incident, `ErrNotActive` if it is resolved or ignored)
  - `const ReasonGreen = "green"`, `ReasonPRClosed = "pr_closed"`, `var ErrNotActive error`

Rules, from the spec: a bad result without an active incident opens one; on a new head SHA it is a recurrence; on the same head SHA nothing changes except `last_seen`; `Pending` observations change nothing; `Green` resolves; an ignored incident stays ignored on bad results and resolves on green; a PR that is no longer open resolves its incidents.

- [ ] **Step 1: Write the failing tests**

Create `internal/incident/incident_test.go`:

```go
package incident_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

type env struct {
	e    *incident.Engine
	st   *store.Store
	repo store.Repo
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.SaveConnection(ctx, store.Connection{
		TokenCiphertext: []byte{1}, TokenHint: "1234", Login: "octo", Status: store.ConnOK,
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	return &env{e: &incident.Engine{Store: st}, st: st, repo: repo}
}

func (v *env) obs(class incident.Class, ref, check, sha, conclusion string) incident.Observation {
	return incident.Observation{
		RepoID: v.repo.ID, RepoName: v.repo.FullName, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7",
		CheckName: check, Class: class, Conclusion: conclusion, HeadSHA: sha, URL: "https://github.com/octo/hello/runs/1",
	}
}

func (v *env) observe(t *testing.T, o incident.Observation) {
	t.Helper()
	if err := v.e.Observe(context.Background(), o); err != nil {
		t.Fatalf("Observe(%+v): %v", o, err)
	}
}

func (v *env) incidents(t *testing.T, state string) []store.Incident {
	t.Helper()
	list, err := v.st.ListIncidents(context.Background(), store.IncidentFilter{State: state})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// kinds returns the kinds of all activity entries, oldest first.
func (v *env) kinds(t *testing.T) []string {
	t.Helper()
	log, err := v.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Kind)
	}
	return out
}

func (v *env) summaries(t *testing.T) []string {
	t.Helper()
	log, _ := v.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000})
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Summary)
	}
	return out
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status, conclusion string
		want               incident.Class
	}{
		{"completed", "success", incident.Green},
		{"completed", "neutral", incident.Green},
		{"completed", "skipped", incident.Green},
		{"completed", "failure", incident.Bad},
		{"completed", "timed_out", incident.Bad},
		{"completed", "startup_failure", incident.Bad},
		{"completed", "cancelled", incident.Bad},
		{"completed", "action_required", incident.Bad},
		{"queued", "", incident.Pending},
		{"in_progress", "", incident.Pending},
		{"waiting", "", incident.Pending},
		{"completed", "stale", incident.Pending},
		{"completed", "", incident.Pending},
		{"completed", "something_new", incident.Pending},
	}
	for _, tc := range cases {
		if got := incident.Classify(tc.status, tc.conclusion); got != tc.want {
			t.Errorf("Classify(%q, %q) = %v, want %v", tc.status, tc.conclusion, got, tc.want)
		}
	}
	if !(incident.Green < incident.Pending && incident.Pending < incident.Bad) {
		t.Error("the classes must be ordered by severity")
	}
}

func TestABadResultOpensAnIncident(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaaaaaaaaa", "failure"))

	list := v.incidents(t, "all")
	if len(list) != 1 {
		t.Fatalf("incidents = %+v", list)
	}
	in := list[0]
	if in.State != store.IncOpen || in.Occurrences != 1 || in.Ref != "pr:7" || in.CheckName != "go" ||
		in.HeadSHA != "aaaaaaaaaa" || in.Conclusion != "failure" || in.RepoName != "octo/hello" ||
		in.CheckURL != "https://github.com/octo/hello/runs/1" || in.RefURL != "https://github.com/octo/hello/pull/7" {
		t.Fatalf("incident = %+v", in)
	}
	if got := v.summaries(t); !slices.Equal(got, []string{"go failed on PR #7 in octo/hello"}) {
		t.Fatalf("summaries = %q", got)
	}
}

func TestEveryBadConclusionIsWordedForTheLog(t *testing.T) {
	v := newEnv(t)
	for i, c := range []struct{ conclusion, summary string }{
		{"failure", "go failed on branch main in octo/hello"},
		{"timed_out", "web timed out on branch main in octo/hello"},
		{"startup_failure", "lint failed to start on branch main in octo/hello"},
		{"cancelled", "docs was cancelled on branch main in octo/hello"},
		{"action_required", "deploy needs action on branch main in octo/hello"},
	} {
		check := []string{"go", "web", "lint", "docs", "deploy"}[i]
		v.observe(t, v.obs(incident.Bad, "branch:main", check, "abc", c.conclusion))
		got := v.summaries(t)
		if got[len(got)-1] != c.summary {
			t.Errorf("%s: summary = %q, want %q", c.conclusion, got[len(got)-1], c.summary)
		}
	}
}

func TestTheSameCommitChangesNothingButLastSeen(t *testing.T) {
	v := newEnv(t)
	o := v.obs(incident.Bad, "pr:7", "go", "aaaaaaaaaa", "failure")
	v.observe(t, o)
	before := v.incidents(t, "all")[0]

	v.observe(t, o)
	v.observe(t, o)

	after := v.incidents(t, "all")
	if len(after) != 1 || after[0].Occurrences != 1 || after[0].HeadSHA != "aaaaaaaaaa" || after[0].LastSeen.Before(before.LastSeen) {
		t.Fatalf("incident after identical observations = %+v", after)
	}
	if got := v.kinds(t); !slices.Equal(got, []string{store.KindIncidentOpened}) {
		t.Fatalf("activity = %v, want only the opening", got)
	}
}

func TestANewCommitIsARecurrence(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaaaaaaaaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "bbbbbbbbbb", "timed_out"))

	list := v.incidents(t, "all")
	if len(list) != 1 || list[0].Occurrences != 2 || list[0].HeadSHA != "bbbbbbbbbb" || list[0].Conclusion != "timed_out" {
		t.Fatalf("incidents = %+v", list)
	}
	if got := v.kinds(t); !slices.Equal(got, []string{store.KindIncidentOpened, store.KindIncidentRecurred}) {
		t.Fatalf("activity = %v", got)
	}
	if got := v.summaries(t); got[1] != "go timed out again on PR #7 in octo/hello (commit bbbbbbb)" {
		t.Fatalf("recurrence summary = %q", got[1])
	}
}

func TestPendingChecksChangeNothing(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Pending, "pr:7", "go", "aaa", ""))
	if got := v.incidents(t, "all"); len(got) != 0 {
		t.Fatalf("a pending check opened an incident: %+v", got)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Pending, "pr:7", "go", "bbb", "")) // a re-run is in progress
	list := v.incidents(t, "all")
	if len(list) != 1 || list[0].State != store.IncOpen || list[0].Occurrences != 1 || list[0].HeadSHA != "aaa" {
		t.Fatalf("incident after a pending observation = %+v", list)
	}
	if got := v.kinds(t); len(got) != 1 {
		t.Fatalf("activity = %v", got)
	}
}

func TestGreenResolvesAndAnotherFailureOpensANewIncident(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Green, "pr:7", "go", "aaa", "success"))
	if got := v.incidents(t, "all"); len(got) != 0 || len(v.kinds(t)) != 0 {
		t.Fatalf("green without an incident did something: %+v", got)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	first := v.incidents(t, "all")[0]
	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))

	resolved := v.incidents(t, "resolved")
	if len(resolved) != 1 || resolved[0].ID != first.ID || resolved[0].ResolvedReason != incident.ReasonGreen || resolved[0].ResolvedAt == nil {
		t.Fatalf("resolved = %+v", resolved)
	}
	if got := v.summaries(t); got[len(got)-1] != "go is green again on PR #7 in octo/hello" {
		t.Fatalf("summaries = %q", got)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "ccc", "failure"))
	all := v.incidents(t, "all")
	if len(all) != 2 || all[0].ID == first.ID || all[0].State != store.IncOpen || all[0].Occurrences != 1 {
		t.Fatalf("a failure after the resolution must open a new incident: %+v", all)
	}
}

func TestAnIgnoredIncidentStaysIgnoredUntilGreen(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]
	if _, err := v.e.Ignore(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}

	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "bbb", "failure"))
	list := v.incidents(t, "all")
	if len(list) != 1 || list[0].State != store.IncIgnored || list[0].Occurrences != 1 || list[0].HeadSHA != "aaa" {
		t.Fatalf("an ignored incident changed: %+v", list)
	}

	v.observe(t, v.obs(incident.Green, "pr:7", "go", "ccc", "success"))
	if got := v.incidents(t, "resolved"); len(got) != 1 {
		t.Fatalf("green must resolve an ignored incident: %+v", v.incidents(t, "all"))
	}
}

func TestIncidentsAreKeyedByRepoRefAndCheck(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:7", "web", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:8", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "branch:main", "go", "aaa", "failure"))
	if got := v.incidents(t, "all"); len(got) != 4 {
		t.Fatalf("got %d incidents, want 4", len(got))
	}

	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))
	if got := v.incidents(t, "active"); len(got) != 3 {
		t.Fatalf("resolving one key must leave the other three: %+v", got)
	}
}

func TestResolveClosedPRs(t *testing.T) {
	v := newEnv(t)
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:7", "web", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "pr:8", "go", "aaa", "failure"))
	v.observe(t, v.obs(incident.Bad, "branch:main", "go", "aaa", "failure"))

	if err := v.e.ResolveClosedPRs(context.Background(), v.repo.ID, map[int]bool{8: true}); err != nil {
		t.Fatal(err)
	}

	resolved := v.incidents(t, "resolved")
	if len(resolved) != 2 {
		t.Fatalf("resolved = %+v, want the two incidents of PR 7", resolved)
	}
	for _, in := range resolved {
		if in.Ref != "pr:7" || in.ResolvedReason != incident.ReasonPRClosed {
			t.Errorf("resolved incident = %+v", in)
		}
	}
	if got := v.incidents(t, "active"); len(got) != 2 {
		t.Fatalf("active = %+v, want pr:8 and the branch", got)
	}
	if got := v.summaries(t); got[len(got)-1] != "PR #7 in octo/hello was closed or merged; the incident for web is resolved" &&
		got[len(got)-2] != "PR #7 in octo/hello was closed or merged; the incident for web is resolved" {
		t.Fatalf("summaries = %q", got)
	}

	// Running it again changes nothing.
	if err := v.e.ResolveClosedPRs(context.Background(), v.repo.ID, map[int]bool{8: true}); err != nil {
		t.Fatal(err)
	}
	if got := v.kinds(t); len(got) != 6 {
		t.Fatalf("activity = %v, want 4 openings and 2 resolutions", got)
	}
}

func TestIgnore(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.observe(t, v.obs(incident.Bad, "pr:7", "go", "aaa", "failure"))
	in := v.incidents(t, "all")[0]

	got, err := v.e.Ignore(ctx, in.ID)
	if err != nil || got.State != store.IncIgnored {
		t.Fatalf("Ignore = %+v, %v", got, err)
	}
	if s := v.summaries(t); s[len(s)-1] != "Ignored the incident for go on PR #7 in octo/hello" {
		t.Fatalf("summaries = %q", s)
	}
	if _, err := v.e.Ignore(ctx, in.ID); !errors.Is(err, incident.ErrNotActive) {
		t.Fatalf("ignoring twice: error = %v, want ErrNotActive", err)
	}
	if _, err := v.e.Ignore(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignoring an unknown incident: error = %v, want ErrNotFound", err)
	}

	v.observe(t, v.obs(incident.Green, "pr:7", "go", "bbb", "success"))
	if _, err := v.e.Ignore(ctx, in.ID); !errors.Is(err, incident.ErrNotActive) {
		t.Fatalf("ignoring a resolved incident: error = %v, want ErrNotActive", err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/incident -count=1`
Expected: FAIL to compile with `undefined: incident.Engine`, `incident.Classify` and so on.

- [ ] **Step 3: Implement the engine**

Create `internal/incident/incident.go`:

```go
// Package incident turns observations of CI checks into incidents and keeps their lifecycle. It
// knows nothing about GitHub: the poller hands it observations.
package incident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Jaydee94/remedy/internal/store"
)

// Reasons an incident was resolved.
const (
	ReasonGreen    = "green"
	ReasonPRClosed = "pr_closed"
)

// ErrNotActive is returned when an incident cannot be changed because it is resolved or ignored.
var ErrNotActive = errors.New("incident is already resolved or ignored")

// Class says what a check run means for an incident. The values are ordered by severity: when
// several check runs share a key, the highest wins.
type Class int

const (
	// Green is a finished check that passed (success, neutral, skipped).
	Green Class = iota
	// Pending is a check that is not finished or has a result Remedy does not act on.
	Pending
	// Bad is a finished check that failed (failure, timed_out, startup_failure, cancelled, action_required).
	Bad
)

// Classify maps the status and conclusion of a GitHub check run to a Class. Anything unknown, such
// as "stale" or a conclusion GitHub adds later, is Pending: Remedy waits for a clear result.
func Classify(status, conclusion string) Class {
	if status != "completed" {
		return Pending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return Green
	case "failure", "timed_out", "startup_failure", "cancelled", "action_required":
		return Bad
	}
	return Pending
}

// Observation is the state of one check on one ref, as seen by the poller.
type Observation struct {
	RepoID     int64
	RepoName   string
	Ref        string // "pr:<number>" or "branch:<name>"
	RefURL     string
	CheckName  string
	Class      Class
	Conclusion string
	HeadSHA    string
	URL        string
}

type Engine struct{ Store *store.Store }

// Observe applies one observation. It is idempotent: observing the same state again changes
// nothing except last_seen.
func (e *Engine) Observe(ctx context.Context, o Observation) error {
	if o.Class == Pending {
		return nil
	}
	cur, err := e.Store.FindActiveIncident(ctx, o.RepoID, o.Ref, o.CheckName)
	if errors.Is(err, store.ErrNotFound) {
		if o.Class == Green {
			return nil
		}
		_, err := e.Store.OpenIncident(ctx, store.NewIncident{
			RepoID: o.RepoID, Ref: o.Ref, RefURL: o.RefURL, CheckName: o.CheckName,
			Conclusion: o.Conclusion, HeadSHA: o.HeadSHA, CheckURL: o.URL,
		}, store.NewActivity{
			Kind:    store.KindIncidentOpened,
			Summary: fmt.Sprintf("%s %s on %s in %s", o.CheckName, verb(o.Conclusion), refLabel(o.Ref), o.RepoName),
			Data:    payload(o),
		})
		if errors.Is(err, store.ErrExists) {
			return nil // another writer was faster; the next cycle sees its incident
		}
		return err
	}
	if err != nil {
		return err
	}

	var change error
	switch {
	case o.Class == Green:
		change = e.Store.ResolveIncident(ctx, cur.ID, ReasonGreen, store.NewActivity{
			Kind:    store.KindIncidentResolved,
			RepoID:  cur.RepoID,
			Summary: fmt.Sprintf("%s is green again on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName),
			Data:    payload(o),
		})
	case cur.State == store.IncIgnored:
		return nil
	case cur.HeadSHA == o.HeadSHA:
		change = e.Store.TouchIncident(ctx, cur.ID)
	default:
		change = e.Store.RecordRecurrence(ctx, cur.ID, o.Conclusion, o.HeadSHA, o.URL, store.NewActivity{
			Kind:   store.KindIncidentRecurred,
			RepoID: cur.RepoID,
			Summary: fmt.Sprintf("%s %s again on %s in %s (commit %s)",
				cur.CheckName, verb(o.Conclusion), refLabel(cur.Ref), cur.RepoName, short(o.HeadSHA)),
			Data: payload(o),
		})
	}
	if errors.Is(change, store.ErrNotFound) {
		return nil // resolved by someone else in the meantime
	}
	return change
}

// ResolveClosedPRs resolves the active incidents of pull requests that are no longer open.
// openPRs holds the numbers of the pull requests that are open right now.
func (e *Engine) ResolveClosedPRs(ctx context.Context, repoID int64, openPRs map[int]bool) error {
	active, err := e.Store.ListActiveIncidents(ctx, repoID)
	if err != nil {
		return err
	}
	for _, in := range active {
		rest, ok := strings.CutPrefix(in.Ref, "pr:")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil || openPRs[n] {
			continue
		}
		err = e.Store.ResolveIncident(ctx, in.ID, ReasonPRClosed, store.NewActivity{
			Kind:   store.KindIncidentResolved,
			RepoID: in.RepoID,
			Summary: fmt.Sprintf("%s in %s was closed or merged; the incident for %s is resolved",
				refLabel(in.Ref), in.RepoName, in.CheckName),
			Data: payload(Observation{Ref: in.Ref, CheckName: in.CheckName, Conclusion: in.Conclusion, HeadSHA: in.HeadSHA, URL: in.CheckURL}),
		})
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

// Ignore moves an incident to ignored. It returns store.ErrNotFound for an unknown incident and
// ErrNotActive for one that is resolved or already ignored.
func (e *Engine) Ignore(ctx context.Context, id int64) (store.Incident, error) {
	cur, err := e.Store.GetIncident(ctx, id)
	if err != nil {
		return store.Incident{}, err
	}
	if cur.State == store.IncResolved || cur.State == store.IncIgnored {
		return store.Incident{}, ErrNotActive
	}
	err = e.Store.IgnoreIncident(ctx, id, store.NewActivity{
		Kind:    store.KindIncidentIgnored,
		RepoID:  cur.RepoID,
		Summary: fmt.Sprintf("Ignored the incident for %s on %s in %s", cur.CheckName, refLabel(cur.Ref), cur.RepoName),
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Incident{}, ErrNotActive
	}
	if err != nil {
		return store.Incident{}, err
	}
	return e.Store.GetIncident(ctx, id)
}

func refLabel(ref string) string {
	if n, ok := strings.CutPrefix(ref, "pr:"); ok {
		return "PR #" + n
	}
	if b, ok := strings.CutPrefix(ref, "branch:"); ok {
		return "branch " + b
	}
	return ref
}

func verb(conclusion string) string {
	switch conclusion {
	case "failure":
		return "failed"
	case "timed_out":
		return "timed out"
	case "startup_failure":
		return "failed to start"
	case "cancelled":
		return "was cancelled"
	case "action_required":
		return "needs action"
	}
	return conclusion
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// payload is the machine-readable part of an activity entry. It never contains more than names,
// SHAs and links that GitHub itself shows.
func payload(o Observation) json.RawMessage {
	b, _ := json.Marshal(map[string]string{
		"checkName": o.CheckName, "ref": o.Ref, "conclusion": o.Conclusion, "headSha": o.HeadSHA, "url": o.URL,
	})
	return b
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal/incident && go vet ./internal/incident && go test ./internal/incident -race -count=1`
Expected: no gofmt output, vet clean, `ok`.

- [ ] **Step 5: Mutation check**

The tests must catch a broken rule. Make each change in `internal/incident/incident.go`, run `go test ./internal/incident -count=1`, expect FAIL, then undo it:

1. In `Observe`, change `case cur.HeadSHA == o.HeadSHA:` to `case false:` (every failure becomes a recurrence).
2. Delete the `case cur.State == store.IncIgnored:` line and its `return nil` (ignored incidents change).
3. In `Classify`, change `return Pending` at the end to `return Bad`.

- [ ] **Step 6: Commit**

```bash
git add internal/incident
git commit -m "feat(incident): add the incident state machine" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 4: The poller

Once per interval the poller reads the stored connection, and for every enabled repo it lists the open pull requests, the check runs of each PR head and the check runs of the default branch, turns them into observations and hands them to the engine. It reuses one GitHub client per token so the ETag cache works, pauses when GitHub asks it to, and records per repo whether polling works.

**Files:**
- Create: `internal/poller/poller.go`
- Test: `internal/poller/poller_test.go`
- Modify: `internal/config/config.go` (`REMEDY_POLL_INTERVAL`), `internal/config/poll_test.go` (create)
- Modify: `internal/server/github.go` (use `store.ConnectionAAD` and `store.UndecryptableDetail`)
- Overwrite: `cmd/remedy-server/main.go`

**Interfaces:**
- Consumes: `github.PullRequest`, `github.CheckRun`, `github.PageSize`, `github.RateLimitError`, `github.ErrUnauthorized` (Task 1); `store` (Task 2); `incident.Engine`, `incident.Observation`, `incident.Classify` (Task 3); `secret.Key`.
- Produces (package `poller`):
  - `type Source interface { ListOpenPRs(ctx, fullName string) ([]github.PullRequest, error); ListCheckRuns(ctx, fullName, ref string) ([]github.CheckRun, error) }` (`*github.Client` implements it)
  - `type Poller struct { Store *store.Store; Engine *incident.Engine; Key secret.Key; NewSource func(secret.Value) Source; Interval time.Duration; Log *slog.Logger; Now func() time.Time }`
  - `func (*Poller) Run(ctx)` (polls at once, then every `Interval`, until `ctx` ends) and `func (*Poller) PollOnce(ctx)` (one cycle; not safe to call concurrently)
- Produces (package `config`): `Server.PollInterval time.Duration` from `REMEDY_POLL_INTERVAL` (default `60s`, minimum `10s`).

What a cycle does, in order:

1. Nothing if polling is paused (rate limit), if there is no connection, or if the connection status is not `ok` (a rejected or undecryptable token needs the maintainer; re-entering the token or "Check connection" sets it back).
2. A token that does not open with the master key sets the status `undecryptable` and ends the cycle.
3. Per enabled repo: list open PRs; per PR list the check runs of its head commit (ref `pr:<number>`); list the check runs of the default branch (ref `branch:<name>`). Several check runs with the same name on the same ref (two workflows with a job called `build`) collapse into one observation, the worst one wins, so a green twin cannot resolve a red one.
4. If fewer PRs than a full page came back, incidents of PRs that are no longer open are resolved. With a full page the list may be truncated, so nothing is resolved.
5. The repo's `last_polled_at` and `last_error` are updated. The first failure after a success logs `poll_failed`, the first success after a failure logs `poll_recovered`.
6. A rate limit ends the cycle and pauses polling for the time GitHub named; a rejected token (401) sets the connection status `error` and ends the cycle.

- [ ] **Step 1: Share the token context and message**

In `internal/server/github.go`, replace:

```go
const (
	minTokenLen = 20
	maxTokenLen = 512

	undecryptableDetail = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
)

// connectionAAD binds the sealed token to its row, so a ciphertext cannot be moved to another one.
func connectionAAD() string { return "github_connection:" + strconv.FormatInt(store.ConnectionID, 10) }
```

with:

```go
const (
	minTokenLen = 20
	maxTokenLen = 512
)
```

In `internal/server/github.go`, replace:

```go
	raw, err := s.d.Key.Open(c.TokenCiphertext, connectionAAD())
```

with:

```go
	raw, err := s.d.Key.Open(c.TokenCiphertext, store.ConnectionAAD())
```

In `internal/server/github.go`, replace:

```go
	sealed, err := s.d.Key.Seal([]byte(token), connectionAAD())
```

with:

```go
	sealed, err := s.d.Key.Seal([]byte(token), store.ConnectionAAD())
```

In `internal/server/github.go`, replace:

```go
	_ = s.d.Store.UpdateConnectionStatus(ctx, store.ConnUndecryptable, undecryptableDetail, time.Now())
```

with:

```go
	_ = s.d.Store.UpdateConnectionStatus(ctx, store.ConnUndecryptable, store.UndecryptableDetail, time.Now())
```

In `internal/server/github.go`, replace:

```go
		writeErr(w, http.StatusConflict, undecryptableDetail)
```

with:

```go
		writeErr(w, http.StatusConflict, store.UndecryptableDetail)
```

Run: `go build ./... && go test ./internal/server -race -count=1`
Expected: builds, `ok` (a pure move, the sealed tokens stay readable).

- [ ] **Step 2: Write the failing config tests**

Create `internal/config/poll_test.go`:

```go
package config_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestPollIntervalDefaultsToAMinute(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil || c.PollInterval != time.Minute {
		t.Fatalf("PollInterval = %s, err = %v", c.PollInterval, err)
	}
}

func TestPollIntervalIsConfigurable(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_POLL_INTERVAL": "2m30s"}))
	if err != nil || c.PollInterval != 150*time.Second {
		t.Fatalf("PollInterval = %s, err = %v", c.PollInterval, err)
	}
}

func TestPollIntervalRejectsNonsenseAndTooFastPolling(t *testing.T) {
	for _, bad := range []string{"soon", "60", "5s", "0s", "-1m"} {
		if _, err := config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_POLL_INTERVAL": bad})); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
```

Run: `go test ./internal/config -count=1`
Expected: FAIL to compile with `c.PollInterval undefined`.

- [ ] **Step 3: Implement the setting**

In `internal/config/config.go`, replace:

```go
	"path/filepath"
	"strings"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
	defaultGitHub  = "https://api.github.com"
)

type Server struct {
	Addr          string     // REMEDY_ADDR, default ":8080"
	DBPath        string     // REMEDY_DB, default "remedy.db"
	AdminPassword string     // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken   string     // REMEDY_RUNNER_TOKEN, required, min 24 chars
	MasterKey     secret.Key // REMEDY_MASTER_KEY, required, 32 random bytes in Base64
	GitHubAPIURL  string     // REMEDY_GITHUB_API_URL, default "https://api.github.com"
}
```

with:

```go
	"path/filepath"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
	defaultGitHub  = "https://api.github.com"

	defaultPollInterval = time.Minute
	minPollInterval     = 10 * time.Second
)

type Server struct {
	Addr          string        // REMEDY_ADDR, default ":8080"
	DBPath        string        // REMEDY_DB, default "remedy.db"
	AdminPassword string        // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken   string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	MasterKey     secret.Key    // REMEDY_MASTER_KEY, required, 32 random bytes in Base64
	GitHubAPIURL  string        // REMEDY_GITHUB_API_URL, default "https://api.github.com"
	PollInterval  time.Duration // REMEDY_POLL_INTERVAL, a Go duration, default 60s, at least 10s
}
```

In `internal/config/config.go`, replace:

```go
	c.GitHubAPIURL = strings.TrimRight(c.GitHubAPIURL, "/")
	return c, nil
}
```

with:

```go
	c.GitHubAPIURL = strings.TrimRight(c.GitHubAPIURL, "/")

	c.PollInterval = defaultPollInterval
	if v := get("REMEDY_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minPollInterval {
			return Server{}, errors.New("REMEDY_POLL_INTERVAL must be a duration of at least 10s, for example 60s or 2m")
		}
		c.PollInterval = d
	}
	return c, nil
}
```

Run: `gofmt -l internal/config && go test ./internal/config -race -count=1`
Expected: no gofmt output, `ok`.

- [ ] **Step 4: Write the failing poller tests**

Create `internal/poller/poller_test.go`:

```go
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
```

Run: `go test ./internal/poller -count=1`
Expected: FAIL to compile with `no required module provides package` or `undefined: poller.Poller`.

- [ ] **Step 5: Implement the poller**

Create `internal/poller/poller.go`:

```go
// Package poller watches the enabled repositories on GitHub and feeds what it sees to the incident
// engine. It only reads from GitHub.
package poller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// Source is what the poller needs from GitHub. *github.Client implements it.
type Source interface {
	ListOpenPRs(ctx context.Context, fullName string) ([]github.PullRequest, error)
	ListCheckRuns(ctx context.Context, fullName, ref string) ([]github.CheckRun, error)
}

// Poller polls once per Interval. Run and PollOnce must not be called concurrently.
type Poller struct {
	Store     *store.Store
	Engine    *incident.Engine
	Key       secret.Key
	NewSource func(token secret.Value) Source
	Interval  time.Duration // default one minute
	Log       *slog.Logger
	Now       func() time.Time // default time.Now

	source      Source // reused between cycles so that its ETag cache works
	sealed      []byte // the ciphertext source was built from
	pausedUntil time.Time
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run polls at once and then every Interval until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	interval := p.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	p.PollOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.PollOnce(ctx)
		}
	}
}

// PollOnce runs one cycle over all enabled repos.
func (p *Poller) PollOnce(ctx context.Context) {
	if p.now().Before(p.pausedUntil) {
		return
	}
	conn, err := p.Store.GetConnection(ctx)
	if errors.Is(err, store.ErrNotFound) {
		p.source, p.sealed = nil, nil
		return
	}
	if err != nil {
		p.Log.Error("cannot load the GitHub connection", "err", err)
		return
	}
	if conn.Status != store.ConnOK {
		return // a rejected or undecryptable token needs the maintainer
	}

	src, err := p.sourceFor(conn)
	if errors.Is(err, secret.ErrOpen) {
		_ = p.Store.UpdateConnectionStatus(ctx, store.ConnUndecryptable, store.UndecryptableDetail, p.now())
		p.Log.Warn("the stored GitHub token cannot be decrypted; polling is off")
		return
	}
	if err != nil {
		p.Log.Error("cannot read the GitHub token", "err", err)
		return
	}

	repos, err := p.Store.ListRepos(ctx)
	if err != nil {
		p.Log.Error("cannot list repos", "err", err)
		return
	}
	for _, repo := range repos {
		if !repo.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := p.pollRepo(ctx, src, repo); fatal(err) {
			p.react(ctx, err)
			return
		}
	}
}

// sourceFor returns the GitHub source for the stored token, building a new one only when the token changed.
func (p *Poller) sourceFor(conn store.Connection) (Source, error) {
	if p.source != nil && bytes.Equal(p.sealed, conn.TokenCiphertext) {
		return p.source, nil
	}
	raw, err := p.Key.Open(conn.TokenCiphertext, store.ConnectionAAD())
	if err != nil {
		return nil, err
	}
	p.source, p.sealed = p.NewSource(secret.NewValue(string(raw))), conn.TokenCiphertext
	return p.source, nil
}

// fatal reports errors that make every further request of this cycle pointless.
func fatal(err error) bool {
	var rl *github.RateLimitError
	return errors.As(err, &rl) || errors.Is(err, github.ErrUnauthorized)
}

func (p *Poller) react(ctx context.Context, err error) {
	var rl *github.RateLimitError
	if errors.As(err, &rl) {
		p.pausedUntil = p.now().Add(rl.RetryAfter)
		p.Log.Warn("GitHub rate limit, polling paused", "for", rl.RetryAfter.Round(time.Second))
		return
	}
	_ = p.Store.UpdateConnectionStatus(ctx, store.ConnError, "GitHub rejected the stored token.", p.now())
	p.Log.Warn("GitHub rejected the stored token; polling is off")
}

func (p *Poller) pollRepo(ctx context.Context, src Source, repo store.Repo) error {
	err := p.observeRepo(ctx, src, repo)
	if ctx.Err() != nil {
		return err // shutting down: record nothing
	}
	p.record(ctx, repo, err)
	return err
}

// record stores the outcome on the repo and logs the transitions between working and failing.
func (p *Poller) record(ctx context.Context, repo store.Repo, pollErr error) {
	msg := ""
	if pollErr != nil {
		msg = pollErr.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		p.Log.Warn("polling failed", "repo", repo.FullName, "err", msg)
	}
	if err := p.Store.MarkRepoPolled(ctx, repo.ID, p.now(), msg); err != nil {
		p.Log.Error("cannot record the poll", "repo", repo.FullName, "err", err)
		return
	}
	var act *store.NewActivity
	switch {
	case pollErr != nil && repo.LastError == "":
		act = &store.NewActivity{Kind: store.KindPollFailed, Summary: fmt.Sprintf("Polling %s failed: %s", repo.FullName, msg)}
	case pollErr == nil && repo.LastError != "":
		act = &store.NewActivity{Kind: store.KindPollRecovered, Summary: fmt.Sprintf("Polling %s works again", repo.FullName)}
	}
	if act != nil {
		act.RepoID = repo.ID
		if err := p.Store.AddActivity(ctx, *act); err != nil {
			p.Log.Error("cannot log the poll", "repo", repo.FullName, "err", err)
		}
	}
}

func (p *Poller) observeRepo(ctx context.Context, src Source, repo store.Repo) error {
	prs, err := src.ListOpenPRs(ctx, repo.FullName)
	if err != nil {
		return err
	}

	var (
		agg      aggregate
		firstErr error
		open     = make(map[int]bool, len(prs))
	)
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, pr := range prs {
		open[pr.Number] = true
		runs, err := src.ListCheckRuns(ctx, repo.FullName, pr.Head.SHA)
		if fatal(err) {
			return err
		}
		if err != nil {
			note(fmt.Errorf("check runs of PR #%d: %w", pr.Number, err))
			continue
		}
		agg.add(observations(repo, "pr:"+strconv.Itoa(pr.Number), pr.HTMLURL, runs)...)
	}
	runs, err := src.ListCheckRuns(ctx, repo.FullName, repo.DefaultBranch)
	if fatal(err) {
		return err
	}
	if err != nil {
		note(fmt.Errorf("check runs of %s: %w", repo.DefaultBranch, err))
	} else {
		agg.add(observations(repo, "branch:"+repo.DefaultBranch, "", runs)...)
	}

	for _, o := range agg.list {
		if err := p.Engine.Observe(ctx, o); err != nil {
			note(err)
		}
	}
	if len(prs) < github.PageSize {
		if err := p.Engine.ResolveClosedPRs(ctx, repo.ID, open); err != nil {
			note(err)
		}
	} else {
		p.Log.Warn("a full page of open pull requests may be truncated; closed PRs are not resolved", "repo", repo.FullName)
	}
	return firstErr
}

func observations(repo store.Repo, ref, refURL string, runs []github.CheckRun) []incident.Observation {
	out := make([]incident.Observation, 0, len(runs))
	for _, r := range runs {
		out = append(out, incident.Observation{
			RepoID: repo.ID, RepoName: repo.FullName, Ref: ref, RefURL: refURL, CheckName: r.Name,
			Class: incident.Classify(r.Status, r.Conclusion), Conclusion: r.Conclusion, HeadSHA: r.HeadSHA, URL: r.HTMLURL,
		})
	}
	return out
}

// aggregate keeps one observation per (ref, check name): when several check runs share the key, the
// most severe one wins, so that a green twin cannot resolve a red one. The order of first appearance is kept.
type aggregate struct {
	list  []incident.Observation
	index map[[2]string]int
}

func (a *aggregate) add(obs ...incident.Observation) {
	if a.index == nil {
		a.index = map[[2]string]int{}
	}
	for _, o := range obs {
		k := [2]string{o.Ref, o.CheckName}
		i, ok := a.index[k]
		switch {
		case !ok:
			a.index[k] = len(a.list)
			a.list = append(a.list, o)
		case o.Class > a.list[i].Class:
			a.list[i] = o
		}
	}
}
```

- [ ] **Step 6: Run the tests**

Run: `gofmt -l internal/poller internal/config && go vet ./internal/... && go test ./internal/poller ./internal/config ./internal/server -race -count=1`
Expected: no gofmt output, vet clean, `ok` for all three.

- [ ] **Step 7: Mutation check**

Make each change in `internal/poller/poller.go`, run `go test ./internal/poller -count=1`, expect FAIL, then undo it:

1. In `aggregate.add`, change `case o.Class > a.list[i].Class:` to `case o.Class < a.list[i].Class:` (the best one wins).
2. In `observeRepo`, change `if len(prs) < github.PageSize {` to `if true {`.
3. In `PollOnce`, delete the `if conn.Status != store.ConnOK {` block.
4. In `record`, change `case pollErr != nil && repo.LastError == "":` to `case pollErr != nil:` (a failure is logged every cycle).

- [ ] **Step 8: Wire the poller into the server**

Overwrite `cmd/remedy-server/main.go`:

```go
// Command remedy-server runs the Remedy control plane.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/poller"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.ServerFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("cannot open database", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Background workers stop with ctx. They are waited for before the database closes.
	var workers sync.WaitGroup
	defer workers.Wait()
	background := func(run func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			run(ctx)
		}()
	}

	engine := &incident.Engine{Store: st}
	background((&poller.Poller{
		Store:  st,
		Engine: engine,
		Key:    cfg.MasterKey,
		NewSource: func(token secret.Value) poller.Source {
			return github.New(cfg.GitHubAPIURL, token, nil)
		},
		Interval: cfg.PollInterval,
		Log:      log,
	}).Run)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: server.New(server.Deps{
			Store:       st,
			Auth:        auth.New(cfg.AdminPassword),
			RunnerToken: cfg.RunnerToken,
			Web:         web.FS(),
			Key:         cfg.MasterKey,
			NewGitHub: func(token secret.Value) server.GitHub {
				return github.New(cfg.GitHubAPIURL, token, nil)
			},
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("control plane listening", "addr", cfg.Addr, "db", cfg.DBPath, "pollInterval", cfg.PollInterval)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
```

The `engine` variable is used by the poller now and by the incident API in Task 5, where it is also passed to `server.Deps`.

Run: `make build-go && go vet ./... && go test ./... -race -count=1`
Expected: builds, vet clean, all packages `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal cmd
git commit -m "feat(poller): poll GitHub for failed checks and feed the incident engine" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The incident API and activity for connection and repo changes

**Files:**
- Create: `internal/server/incidents.go`, `internal/server/activity.go`
- Test: `internal/server/incidents_test.go`
- Modify: `internal/server/server.go` (`Deps.Incidents`, routes), `internal/server/github.go` (log connection and repo changes), `internal/server/github_test.go` (`newGHEnv` passes the engine)
- Modify: `cmd/remedy-server/main.go`

**Interfaces:**
- Consumes: `incident.Engine` (Task 3), `store` incident and activity methods (Task 2).
- Produces (admin API, session cookie and `X-Remedy-CSRF` as for every route):
  - `GET /api/incidents?state=&repo=` → `[]incidentView`, most recently seen first, at most 200. `state` is `active` (default when absent is `all`), `all`, or one state; `repo` is a repo id. Anything else is `400`.
  - `GET /api/incidents/{id}` → `{"incident": incidentView, "activity": [activityView]}` (newest activity first, at most 100); `404` for an unknown id.
  - `POST /api/incidents/{id}/ignore` → the updated `incidentView`; `404` for an unknown id, `409` if it is resolved or already ignored.
  - `incidentView`: `id`, `repoId`, `repo`, `ref`, `refUrl`, `checkName`, `state`, `conclusion`, `headSha`, `checkUrl`, `occurrences`, `firstSeen`, `lastSeen`, `resolvedAt`, `resolvedReason` (empty fields omitted where marked in the code).
  - `activityView`: `id`, `at`, `kind`, `summary`.
- The routes exist only when `Deps.Incidents` is set. Saving or removing the connection and adding or removing a repo write `connection_changed`, `repo_added` and `repo_removed` entries.

- [ ] **Step 1: Write the failing tests**

In `internal/server/github_test.go`, replace:

```go
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: key, NewGitHub: gh.factory,
	}))
```

with:

```go
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: key, NewGitHub: gh.factory,
		Incidents: &incident.Engine{Store: st},
	}))
```

In `internal/server/github_test.go`, replace:

```go
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
```

with:

```go
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/secret"
```

Create `internal/server/incidents_test.go`:

```go
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

type incidentJSON struct {
	ID             int64  `json:"id"`
	RepoID         int64  `json:"repoId"`
	Repo           string `json:"repo"`
	Ref            string `json:"ref"`
	RefURL         string `json:"refUrl"`
	CheckName      string `json:"checkName"`
	State          string `json:"state"`
	Conclusion     string `json:"conclusion"`
	HeadSHA        string `json:"headSha"`
	CheckURL       string `json:"checkUrl"`
	Occurrences    int    `json:"occurrences"`
	ResolvedReason string `json:"resolvedReason"`
}

// withRepo connects GitHub and adds octo/hello through the API, so that the activity of both steps exists.
func withRepo(t *testing.T) (*ghEnv, int64) {
	t.Helper()
	e := newGHEnv(t, nil, ghKey(t, 1))
	if code, body := e.putToken(t, ghToken); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	code, body := e.call(t, http.MethodPost, "/api/repos", `{"fullName":"octo/hello"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/repos = %d %s", code, body)
	}
	return e, int64(field(t, body, "id").(float64))
}

func openIncident(t *testing.T, e *ghEnv, repoID int64, ref, check string) store.Incident {
	t.Helper()
	in, err := e.store.OpenIncident(context.Background(), store.NewIncident{
		RepoID: repoID, Ref: ref, RefURL: "https://github.com/octo/hello/pull/7", CheckName: check,
		Conclusion: "failure", HeadSHA: "abc1234", CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: check + " failed on " + ref})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func listIncidents(t *testing.T, e *ghEnv, query string) []incidentJSON {
	t.Helper()
	code, body := e.call(t, http.MethodGet, "/api/incidents"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/incidents%s = %d %s", query, code, body)
	}
	var list []incidentJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return list
}

func TestIncidentRoutesNeedASession(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	for _, path := range []string{"/api/incidents", "/api/incidents/1"} {
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestListIncidentsFiltersByStateAndRepo(t *testing.T) {
	e, repoID := withRepo(t)
	a := openIncident(t, e, repoID, "pr:7", "go")
	b := openIncident(t, e, repoID, "pr:8", "go")
	_ = e.store.ResolveIncident(context.Background(), b.ID, "green", store.NewActivity{Kind: store.KindIncidentResolved, Summary: "green"})

	all := listIncidents(t, e, "")
	if len(all) != 2 || all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatalf("all = %+v, want newest first", all)
	}
	got := all[1]
	if got.Repo != "Octo/Hello" || got.RepoID != repoID || got.Ref != "pr:7" || got.CheckName != "go" || got.State != "open" ||
		got.Conclusion != "failure" || got.HeadSHA != "abc1234" || got.Occurrences != 1 ||
		got.RefURL != "https://github.com/octo/hello/pull/7" || got.CheckURL != "https://github.com/octo/hello/runs/1" {
		t.Fatalf("incident = %+v", got)
	}
	if all[0].ResolvedReason != "green" {
		t.Fatalf("resolved incident = %+v", all[0])
	}

	for query, want := range map[string]int{
		"?state=active":                          1,
		"?state=open":                            1,
		"?state=resolved":                        1,
		"?state=all":                             2,
		"?state=ignored":                         0,
		"?repo=" + strconv.FormatInt(repoID, 10): 2,
		"?repo=9999":                             0,
	} {
		if got := listIncidents(t, e, query); len(got) != want {
			t.Errorf("%s: %d incidents, want %d", query, len(got), want)
		}
	}
	for _, bad := range []string{"?state=bogus", "?repo=abc", "?repo=-1", "?state=" + url.QueryEscape("open' OR 1=1")} {
		if code, _ := e.call(t, http.MethodGet, "/api/incidents"+bad, ""); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
}

func TestGetIncidentReturnsItsHistory(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	_ = e.store.RecordRecurrence(context.Background(), in.ID, "failure", "def5678", "", store.NewActivity{
		Kind: store.KindIncidentRecurred, RepoID: repoID, Summary: "go failed again",
	})

	code, body := e.call(t, http.MethodGet, "/api/incidents/"+strconv.FormatInt(in.ID, 10), "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s", code, body)
	}
	var got struct {
		Incident incidentJSON `json:"incident"`
		Activity []struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
			At      string `json:"at"`
		} `json:"activity"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Incident.ID != in.ID || got.Incident.Occurrences != 2 || got.Incident.HeadSHA != "def5678" {
		t.Fatalf("incident = %+v", got.Incident)
	}
	if len(got.Activity) != 2 || got.Activity[0].Kind != "incident_recurred" || got.Activity[1].Kind != "incident_opened" ||
		got.Activity[0].Summary != "go failed again" || got.Activity[0].At == "" {
		t.Fatalf("activity = %+v, want only this incident's entries, newest first", got.Activity)
	}

	for _, id := range []string{"9999", "abc"} {
		if code, _ := e.call(t, http.MethodGet, "/api/incidents/"+id, ""); code != http.StatusNotFound {
			t.Errorf("GET /api/incidents/%s = %d, want 404", id, code)
		}
	}
}

func TestIgnoreIncident(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	path := "/api/incidents/" + strconv.FormatInt(in.ID, 10) + "/ignore"

	code, body := e.call(t, http.MethodPost, path, "")
	if code != http.StatusOK || field(t, body, "state") != "ignored" {
		t.Fatalf("POST ignore = %d %s", code, body)
	}
	if code, _ := e.call(t, http.MethodPost, path, ""); code != http.StatusConflict {
		t.Errorf("ignoring twice = %d, want 409", code)
	}
	if code, _ := e.call(t, http.MethodPost, "/api/incidents/9999/ignore", ""); code != http.StatusNotFound {
		t.Errorf("ignoring an unknown incident = %d, want 404", code)
	}
	if got := listIncidents(t, e, "?state=ignored"); len(got) != 1 {
		t.Fatalf("ignored = %+v", got)
	}
	log, _ := e.store.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	if len(log) != 2 || log[0].Kind != store.KindIncidentIgnored {
		t.Fatalf("activity = %+v", log)
	}
}

func TestIgnoreNeedsTheCSRFHeader(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")

	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/ignore", strings.NewReader(""))
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without X-Remedy-CSRF = %d, want 403", resp.StatusCode)
	}
	if got, _ := e.store.GetIncident(context.Background(), in.ID); got.State != store.IncOpen {
		t.Fatalf("state = %q: the request without the header changed the incident", got.State)
	}
}

func TestConnectionAndRepoChangesAreLogged(t *testing.T) {
	e, repoID := withRepo(t)
	if code, _ := e.call(t, http.MethodDelete, "/api/repos/"+strconv.FormatInt(repoID, 10), ""); code != http.StatusNoContent {
		t.Fatalf("DELETE repo = %d", code)
	}
	if code, _ := e.call(t, http.MethodDelete, "/api/github/connection", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE connection = %d", code)
	}

	log, err := e.store.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds, summaries []string
	for i := len(log) - 1; i >= 0; i-- {
		kinds = append(kinds, log[i].Kind)
		summaries = append(summaries, log[i].Summary+string(log[i].Data))
	}
	want := []string{store.KindConnectionChanged, store.KindRepoAdded, store.KindRepoRemoved, store.KindConnectionChanged}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("activity kinds = %v, want %v (%v)", kinds, want, summaries)
	}
	if strings.Contains(strings.Join(summaries, " "), "DISTINCTIVE") {
		t.Fatalf("the token leaked into the activity: %v", summaries)
	}
	// The log is newest first: connection removed, repo removed, repo added, connection saved.
	if log[1].Summary != "Removed repository Octo/Hello" || log[2].Summary != "Added repository Octo/Hello" {
		t.Fatalf("summaries = %q and %q", log[2].Summary, log[1].Summary)
	}
	if log[1].RepoID != 0 {
		t.Fatalf("the removal entry links to the deleted repo %d", log[1].RepoID)
	}
}
```

Run: `go test ./internal/server -count=1`
Expected: FAIL to compile with `unknown field Incidents in struct literal`.

- [ ] **Step 2: Implement the API**

Create `internal/server/activity.go`:

```go
package server

import (
	"context"

	"github.com/Jaydee94/remedy/internal/store"
)

// logActivity appends to the activity log. It is best effort: a handler that already did its work
// must not fail because the log entry could not be written.
func (s *srv) logActivity(ctx context.Context, kind string, repoID int64, summary string) {
	_ = s.d.Store.AddActivity(ctx, store.NewActivity{Kind: kind, RepoID: repoID, Summary: summary})
}
```

Create `internal/server/incidents.go`:

```go
package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/store"
)

type incidentView struct {
	ID             int64      `json:"id"`
	RepoID         int64      `json:"repoId"`
	Repo           string     `json:"repo"`
	Ref            string     `json:"ref"`
	RefURL         string     `json:"refUrl,omitempty"`
	CheckName      string     `json:"checkName"`
	State          string     `json:"state"`
	Conclusion     string     `json:"conclusion"`
	HeadSHA        string     `json:"headSha"`
	CheckURL       string     `json:"checkUrl,omitempty"`
	Occurrences    int        `json:"occurrences"`
	FirstSeen      time.Time  `json:"firstSeen"`
	LastSeen       time.Time  `json:"lastSeen"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
	ResolvedReason string     `json:"resolvedReason,omitempty"`
}

func incidentViewOf(in store.Incident) incidentView {
	return incidentView{
		ID: in.ID, RepoID: in.RepoID, Repo: in.RepoName, Ref: in.Ref, RefURL: in.RefURL, CheckName: in.CheckName,
		State: string(in.State), Conclusion: in.Conclusion, HeadSHA: in.HeadSHA, CheckURL: in.CheckURL,
		Occurrences: in.Occurrences, FirstSeen: in.FirstSeen, LastSeen: in.LastSeen,
		ResolvedAt: in.ResolvedAt, ResolvedReason: in.ResolvedReason,
	}
}

type activityView struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
}

func validStateFilter(s string) bool {
	switch s {
	case "", "all", "active", "open", "diagnosing", "diagnosed", "resolved", "ignored":
		return true
	}
	return false
}

// incidentID parses the {id} path value. ok is false for anything that is not a number.
func incidentID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *srv) listIncidents(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !validStateFilter(state) {
		writeErr(w, http.StatusBadRequest, "unknown state")
		return
	}
	var repoID int64
	if v := r.URL.Query().Get("repo"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "repo must be a repository id")
			return
		}
		repoID = id
	}

	list, err := s.d.Store.ListIncidents(r.Context(), store.IncidentFilter{State: state, RepoID: repoID, Limit: 200})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list incidents")
		return
	}
	views := make([]incidentView, 0, len(list))
	for _, in := range list {
		views = append(views, incidentViewOf(in))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) getIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	in, err := s.d.Store.GetIncident(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the incident")
		return
	}
	log, err := s.d.Store.ListActivity(r.Context(), store.ActivityQuery{IncidentID: id, Limit: 100})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the history")
		return
	}
	history := make([]activityView, 0, len(log))
	for _, a := range log {
		history = append(history, activityView{ID: a.ID, At: a.At, Kind: a.Kind, Summary: a.Summary})
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": incidentViewOf(in), "activity": history})
}

func (s *srv) ignoreIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	in, err := s.d.Incidents.Ignore(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "incident not found")
	case errors.Is(err, incident.ErrNotActive):
		writeErr(w, http.StatusConflict, "this incident is already resolved or ignored")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not ignore the incident")
	default:
		writeJSON(w, http.StatusOK, incidentViewOf(in))
	}
}
```

In `internal/server/server.go`, replace:

```go
	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/secret"
```

with:

```go
	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/secret"
```

In `internal/server/server.go`, replace:

```go
	Key       secret.Key
	NewGitHub func(token secret.Value) GitHub
}
```

with:

```go
	Key       secret.Key
	NewGitHub func(token secret.Value) GitHub

	// Incidents changes incidents on behalf of the UI. When it is nil the incident routes are not
	// registered.
	Incidents *incident.Engine
}
```

In `internal/server/server.go`, replace:

```go
	if d.Web != nil {
		mux.Handle("/", spa(d.Web))
	}
```

with:

```go
	if d.Incidents != nil {
		mux.HandleFunc("GET /api/incidents", s.session(s.listIncidents))
		mux.HandleFunc("GET /api/incidents/{id}", s.session(s.getIncident))
		mux.HandleFunc("POST /api/incidents/{id}/ignore", s.session(s.ignoreIncident))
	}

	if d.Web != nil {
		mux.Handle("/", spa(d.Web))
	}
```

In `internal/server/github.go`, replace:

```go
	if err := s.d.Store.SaveConnection(r.Context(), conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(conn))
```

with:

```go
	if err := s.d.Store.SaveConnection(r.Context(), conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the connection")
		return
	}
	s.logActivity(r.Context(), store.KindConnectionChanged, 0, "GitHub connection saved for "+user.Login)
	writeJSON(w, http.StatusOK, viewOf(conn))
```

In `internal/server/github.go`, replace:

```go
		writeErr(w, http.StatusInternalServerError, "could not remove the connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
```

with:

```go
		writeErr(w, http.StatusInternalServerError, "could not remove the connection")
		return
	}
	s.logActivity(r.Context(), store.KindConnectionChanged, 0, "GitHub connection removed")
	w.WriteHeader(http.StatusNoContent)
```

In `internal/server/github.go`, replace:

```go
		writeErr(w, http.StatusInternalServerError, "could not add the repository")
		return
	}
	writeJSON(w, http.StatusCreated, repoViewOf(added))
```

with:

```go
		writeErr(w, http.StatusInternalServerError, "could not add the repository")
		return
	}
	s.logActivity(r.Context(), store.KindRepoAdded, added.ID, "Added repository "+added.FullName)
	writeJSON(w, http.StatusCreated, repoViewOf(added))
```

In `internal/server/github.go`, replace:

```go
	err := s.d.Store.DeleteRepo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
```

with:

```go
	repo, err := s.d.Store.GetRepo(r.Context(), id)
	if err == nil {
		err = s.d.Store.DeleteRepo(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the repository")
		return
	}
	// No repo link: the row is gone, and the summary names it.
	s.logActivity(r.Context(), store.KindRepoRemoved, 0, "Removed repository "+repo.FullName)
	w.WriteHeader(http.StatusNoContent)
```

In `cmd/remedy-server/main.go`, replace:

```go
			NewGitHub: func(token secret.Value) server.GitHub {
				return github.New(cfg.GitHubAPIURL, token, nil)
			},
		}),
```

with:

```go
			NewGitHub: func(token secret.Value) server.GitHub {
				return github.New(cfg.GitHubAPIURL, token, nil)
			},
			Incidents: engine,
		}),
```

- [ ] **Step 3: Run the tests**

Run: `gofmt -l internal cmd && go vet ./... && go test ./... -race -count=1`
Expected: no gofmt output, vet clean, all packages `ok`.

- [ ] **Step 4: Commit**

```bash
git add internal cmd
git commit -m "feat(server): add the incident API and log connection and repo changes" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 6: Run extension and the run timeout

A run learns what the responder needs (spec section 4): a **role** (`adhoc` or `responder`), a link to its **incident**, a structured **output** and a **failure reason**. The runner reports `output` and `failureReason` in `finish` and stops a run that takes longer than its timeout. Nothing creates responder runs yet except a store method plan 1c will call; the provider does not parse structured output yet either (plan 1c, after the `--json-schema` decision of the spike).

**Files:**
- Create: `internal/store/migrations/004_run_extension.sql`, `internal/store/incident_runs.go`, `internal/testutil/slowclaude.go`
- Overwrite: `internal/run/run.go`
- Modify: `internal/store/store.go`, `internal/store/migrate_test.go`, `internal/server/runnerapi.go`, `internal/runner/loop.go`, `internal/runner/loop_test.go`, `internal/config/config.go`, `cmd/remedy-runner/main.go`
- Test: `internal/store/runs_extension_test.go`, `internal/server/finish_test.go`, `internal/runner/timeout_test.go`, `internal/config/runtimeout_test.go`

**Interfaces:**
- Consumes: `store.Incident` and `OpenIncident` (Task 2).
- Produces:
  - `run.Role` with `run.RoleAdhoc`, `run.RoleResponder`; `const run.ReasonTimeout = "timeout"`
  - `run.Run` gains `Role Role`, `IncidentID *int64`, `Output json.RawMessage`, `FailureReason string` (JSON `role`, `incidentId`, `output`, `failureReason`, the last three omitted when empty)
  - `run.Outcome` gains `Output json.RawMessage` and `FailureReason string` (both omitted when empty)
  - `func (*Store) CreateIncidentRun(ctx, incidentID int64, provider, prompt string) (run.Run, error)` creates a queued run with role `responder`
  - `POST /runner/v1/runs/{id}/finish` accepts `output` and `failureReason` (`""` or `"timeout"`, anything else is `400`)
  - `runner.Loop.RunTimeout`, `const runner.DefaultRunTimeout = 10 * time.Minute`
  - `config.Runner.RunTimeout` from `REMEDY_RUN_TIMEOUT` (default `10m`, minimum `10s`)
  - `testutil.SlowClaude(t) string`: a fake `claude` that prints one event and then sleeps for a minute

- [ ] **Step 1: Write the failing store tests**

Create `internal/store/runs_extension_test.go`:

```go
package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestNewRunsAreAdhocWithoutAnIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, err := s.CreateRun(ctx, "claude", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleAdhoc || r.IncidentID != nil || r.Output != nil || r.FailureReason != "" {
		t.Fatalf("run = %+v", r)
	}
}

func TestCreateIncidentRunLinksTheIncident(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, err := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	if err != nil {
		t.Fatal(err)
	}

	r, err := s.CreateIncidentRun(ctx, in.ID, "claude", "diagnose it")
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleResponder || r.IncidentID == nil || *r.IncidentID != in.ID || r.Status != run.Queued || r.Prompt != "diagnose it" {
		t.Fatalf("run = %+v", r)
	}
	got, _ := s.GetRun(ctx, r.ID)
	if got.Role != run.RoleResponder || got.IncidentID == nil || *got.IncidentID != in.ID {
		t.Fatalf("stored run = %+v", got)
	}

	if _, err := s.CreateIncidentRun(ctx, 9999, "claude", "x"); err == nil {
		t.Fatal("a run for an unknown incident must be refused")
	}
}

func TestDeletingTheIncidentKeepsItsRuns(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	in, _ := s.OpenIncident(ctx, failing(repo.ID, "pr:7", "go", "aaa"), entry(store.KindIncidentOpened, repo.ID))
	r, _ := s.CreateIncidentRun(ctx, in.ID, "claude", "diagnose it")

	if err := s.DeleteRepo(ctx, repo.ID); err != nil { // cascades to the incident
		t.Fatal(err)
	}
	got, err := s.GetRun(ctx, r.ID)
	if err != nil || got.IncidentID != nil || got.Role != run.RoleResponder {
		t.Fatalf("run after the incident was deleted = %+v, %v", got, err)
	}
}

func TestFinishRunStoresTheOutputAndTheFailureReason(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	finish := func(prompt string, o run.Outcome) run.Run {
		t.Helper()
		r, err := s.CreateRun(ctx, "claude", prompt)
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := s.ClaimNext(ctx); err != nil || claimed == nil || claimed.ID != r.ID {
			t.Fatalf("ClaimNext = %+v, %v", claimed, err)
		}
		if err := s.FinishRun(ctx, r.ID, o); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetRun(ctx, r.ID)
		return got
	}

	withOutput := finish("one", run.Outcome{Result: "done", Output: json.RawMessage(`{"summary":"x"}`)})
	if string(withOutput.Output) != `{"summary":"x"}` || withOutput.Status != run.Succeeded || withOutput.FailureReason != "" {
		t.Fatalf("run with output = %+v", withOutput)
	}

	timedOut := finish("two", run.Outcome{ExitCode: -1, FailureReason: run.ReasonTimeout})
	if timedOut.Status != run.Failed || timedOut.FailureReason != "timeout" || timedOut.Output != nil {
		t.Fatalf("timed out run = %+v", timedOut)
	}

	withNull := finish("three", run.Outcome{Output: json.RawMessage(`null`)})
	if withNull.Output != nil {
		t.Fatalf("an output of null must be stored as none, got %s", withNull.Output)
	}

	if err := s.FinishRun(ctx, withOutput.ID, run.Outcome{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("finishing a finished run: error = %v", err)
	}
}
```

In `internal/store/migrate_test.go`, replace:

```go
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)
```

with:

```go
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)
```

In `internal/store/migrate_test.go`, replace:

```go
	if got, err := s.GetRun(ctx, "old-run"); err != nil || got.Prompt != "from before" {
		t.Fatalf("existing run = %+v, %v", got, err)
	}
```

with:

```go
	if got, err := s.GetRun(ctx, "old-run"); err != nil || got.Prompt != "from before" || got.Role != run.RoleAdhoc || got.IncidentID != nil {
		t.Fatalf("existing run = %+v, %v", got, err)
	}
```

Run: `go test ./internal/store -count=1`
Expected: FAIL to compile with `undefined: run.RoleAdhoc`, `s.CreateIncidentRun undefined`.

- [ ] **Step 2: Implement the store and domain changes**

Create `internal/store/migrations/004_run_extension.sql`:

```sql
-- role: who the run is for. incident_id: the incident a responder run diagnoses; the link is cleared,
-- not cascaded, when the incident goes away, so the run stays readable. output: the structured
-- result of the run as JSON. failure_reason: why a failed run failed when it was not the agent's
-- own exit code (timeout).
ALTER TABLE runs ADD COLUMN role TEXT NOT NULL DEFAULT 'adhoc' CHECK (role IN ('adhoc', 'responder'));
ALTER TABLE runs ADD COLUMN incident_id INTEGER REFERENCES incidents (id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN output TEXT;
ALTER TABLE runs ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX runs_incident ON runs (incident_id);
```

Overwrite `internal/run/run.go`:

```go
// Package run holds the domain types shared by the control plane and the runner.
package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
)

// Terminal reports whether no further state change is expected.
func (s Status) Terminal() bool { return s == Succeeded || s == Failed }

// Role says who a run is for.
type Role string

const (
	// RoleAdhoc is a run the maintainer started by hand.
	RoleAdhoc Role = "adhoc"
	// RoleResponder is a run that diagnoses an incident.
	RoleResponder Role = "responder"
)

// ReasonTimeout is the failure reason of a run that was stopped for taking too long, by the runner
// or by the control plane's reaper.
const ReasonTimeout = "timeout"

type Run struct {
	ID            string          `json:"id"`
	Provider      string          `json:"provider"`
	Prompt        string          `json:"prompt"`
	Status        Status          `json:"status"`
	ExitCode      *int            `json:"exitCode,omitempty"`
	Result        string          `json:"result"`
	SessionID     string          `json:"sessionId"`
	CostUSD       float64         `json:"costUsd"`
	CreatedAt     time.Time       `json:"createdAt"`
	StartedAt     *time.Time      `json:"startedAt,omitempty"`
	FinishedAt    *time.Time      `json:"finishedAt,omitempty"`
	Role          Role            `json:"role"`
	IncidentID    *int64          `json:"incidentId,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	FailureReason string          `json:"failureReason,omitempty"`
}

type Event struct {
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Outcome is what the runner reports when a run has ended.
type Outcome struct {
	ExitCode      int             `json:"exitCode"`
	Result        string          `json:"result"`
	SessionID     string          `json:"sessionId"`
	CostUSD       float64         `json:"costUsd"`
	Output        json.RawMessage `json:"output,omitempty"`
	FailureReason string          `json:"failureReason,omitempty"`
}

// NewID returns a random 128-bit identifier as 32 hex characters.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

// JSONString encodes s as a JSON string value.
func JSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // marshalling a string cannot fail
	return b
}
```

In `internal/store/store.go`, replace:

```go
const runCols = `id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at`
```

with:

```go
const runCols = `id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at,
	role, incident_id, output, failure_reason`
```

In `internal/store/store.go`, replace:

```go
	var (
		r                 run.Run
		status, created   string
		exit              sql.NullInt64
		started, finished sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.Provider, &r.Prompt, &status, &exit, &r.Result, &r.SessionID,
		&r.CostUSD, &created, &started, &finished); err != nil {
		return run.Run{}, err
	}
	r.Status = run.Status(status)
```

with:

```go
	var (
		r                 run.Run
		status, created   string
		role              string
		exit, incident    sql.NullInt64
		started, finished sql.NullString
		output            sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.Provider, &r.Prompt, &status, &exit, &r.Result, &r.SessionID,
		&r.CostUSD, &created, &started, &finished, &role, &incident, &output, &r.FailureReason); err != nil {
		return run.Run{}, err
	}
	r.Status = run.Status(status)
	r.Role = run.Role(role)
	if incident.Valid {
		r.IncidentID = &incident.Int64
	}
	if output.Valid {
		r.Output = json.RawMessage(output.String)
	}
```

In `internal/store/store.go`, replace:

```go
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, exit_code = ?, result = ?, session_id = ?, cost_usd = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		string(status), o.ExitCode, o.Result, o.SessionID, o.CostUSD, formatTS(time.Now()), id)
```

with:

```go
	var output any // NULL unless the run produced structured output
	if len(o.Output) > 0 && string(o.Output) != "null" {
		output = string(o.Output)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, exit_code = ?, result = ?, session_id = ?, cost_usd = ?, output = ?,
			failure_reason = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		string(status), o.ExitCode, o.Result, o.SessionID, o.CostUSD, output, o.FailureReason, formatTS(time.Now()), id)
```

Create `internal/store/incident_runs.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// CreateIncidentRun queues a responder run for an incident. It fails if the incident does not exist.
func (s *Store) CreateIncidentRun(ctx context.Context, incidentID int64, provider, prompt string) (run.Run, error) {
	id := run.NewID()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (id, provider, prompt, status, role, incident_id, created_at)
		VALUES (?, ?, ?, 'queued', 'responder', ?, ?)`,
		id, provider, prompt, incidentID, formatTS(time.Now()))
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}
```

- [ ] **Step 3: Run the store tests**

Run: `gofmt -l internal && go vet ./internal/... && go test ./internal/store ./internal/run -race -count=1`
Expected: no gofmt output, vet clean, `ok`.

- [ ] **Step 4: Write the failing server test and accept the new fields in `finish`**

Create `internal/server/finish_test.go`:

```go
package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func claimedRun(t *testing.T, e *env, prompt string) string {
	t.Helper()
	ctx := context.Background()
	r, err := e.store.CreateRun(ctx, "claude", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := e.store.ClaimNext(ctx); err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return r.ID
}

func TestFinishStoresTheOutputAndTheFailureReason(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	withOutput := claimedRun(t, e, "one")
	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+withOutput+"/finish",
		`{"exitCode":0,"result":"done","output":{"summary":"x","confidence":"high"}}`, runnerToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("finish with output = %d", resp.StatusCode)
	}
	got, _ := e.store.GetRun(ctx, withOutput)
	if string(got.Output) != `{"summary":"x","confidence":"high"}` || got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}

	timedOut := claimedRun(t, e, "two")
	resp = e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+timedOut+"/finish",
		`{"exitCode":-1,"result":"stopped","failureReason":"timeout"}`, runnerToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("finish with a timeout = %d", resp.StatusCode)
	}
	if got, _ := e.store.GetRun(ctx, timedOut); got.FailureReason != "timeout" || got.Status != run.Failed {
		t.Fatalf("run = %+v", got)
	}
}

func TestFinishRefusesAnUnknownFailureReason(t *testing.T) {
	e := newEnv(t)
	id := claimedRun(t, e, "one")

	resp := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+id+"/finish", `{"exitCode":1,"failureReason":"because"}`, runnerToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got, _ := e.store.GetRun(context.Background(), id); got.Status != run.Running {
		t.Fatalf("status = %q: the refused request must not finish the run", got.Status)
	}
}
```

In `internal/server/runnerapi.go`, replace:

```go
	var out run.Outcome
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&out); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
```

with:

```go
	var out run.Outcome
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&out); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if out.FailureReason != "" && out.FailureReason != run.ReasonTimeout {
		writeErr(w, http.StatusBadRequest, "unknown failure reason")
		return
	}
```

Run: `go test ./internal/server -race -count=1`
Expected: `ok`. (The two tests pass because the store already persists both fields; the second one fails without the handler check, which is what the mutation step below verifies.)

- [ ] **Step 5: Write the failing runner and config tests**

Create `internal/testutil/slowclaude.go`:

```go
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// SlowClaude writes an executable script that mimics a `claude` that hangs: it prints one event and
// then sleeps far longer than any test waits, so that a run timeout has something to stop. It uses
// exec, so that killing the script kills the sleep and closes its output.
func SlowClaude(t testing.TB) string {
	t.Helper()
	script := `#!/bin/sh
cat > /dev/null
echo '{"type":"system","subtype":"init","session_id":"s-slow"}'
exec sleep 60
`
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write slow claude: %v", err)
	}
	return path
}
```

In `internal/runner/loop_test.go`, replace:

```go
func startLoop(t *testing.T, providers map[string]provider.Provider) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
```

with:

```go
func startLoop(t *testing.T, providers map[string]provider.Provider) (*store.Store, *httptest.Server) {
	t.Helper()
	return startLoopWithTimeout(t, providers, 0)
}

// startLoopWithTimeout is startLoop with a run timeout; zero means the default.
func startLoopWithTimeout(t *testing.T, providers map[string]provider.Provider, timeout time.Duration) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
```

In `internal/runner/loop_test.go`, replace:

```go
		Backoff:       50 * time.Millisecond,
	}
```

with:

```go
		Backoff:       50 * time.Millisecond,
		RunTimeout:    timeout,
	}
```

Create `internal/runner/timeout_test.go`:

```go
package runner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestARunThatTakesTooLongIsStopped(t *testing.T) {
	st, _ := startLoopWithTimeout(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.SlowClaude(t)},
	}, 500*time.Millisecond)

	r, err := st.CreateRun(context.Background(), "claude", "hang")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got := waitTerminal(t, st, r.ID)

	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout {
		t.Fatalf("run = %+v, want failed with reason timeout", got)
	}
	if got.ExitCode == nil || *got.ExitCode == 0 || !strings.Contains(got.Result, "stopped") {
		t.Fatalf("exit code = %v, result = %q", got.ExitCode, got.Result)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("the run took %s to stop; the sleep must have been left running", elapsed)
	}
}

func TestARunThatFinishesInTimeIsNotMarkedAsTimedOut(t *testing.T) {
	st, _ := startLoopWithTimeout(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.FakeClaude(t, 0)},
	}, 30*time.Second)

	r, _ := st.CreateRun(context.Background(), "claude", "quick")
	got := waitTerminal(t, st, r.ID)
	if got.Status != run.Succeeded || got.FailureReason != "" {
		t.Fatalf("run = %+v", got)
	}
}
```

Create `internal/config/runtimeout_test.go`:

```go
package config_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
)

func runnerEnv(extra map[string]string) func(string) string {
	m := map[string]string{"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"}
	for k, v := range extra {
		m[k] = v
	}
	return env(m)
}

func TestRunTimeoutDefaultsToTenMinutes(t *testing.T) {
	c, err := config.RunnerFromEnv(runnerEnv(nil))
	if err != nil || c.RunTimeout != 10*time.Minute {
		t.Fatalf("RunTimeout = %s, err = %v", c.RunTimeout, err)
	}
}

func TestRunTimeoutIsConfigurable(t *testing.T) {
	c, err := config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_RUN_TIMEOUT": "90s"}))
	if err != nil || c.RunTimeout != 90*time.Second {
		t.Fatalf("RunTimeout = %s, err = %v", c.RunTimeout, err)
	}
}

func TestRunTimeoutRejectsNonsenseAndTinyValues(t *testing.T) {
	for _, bad := range []string{"later", "600", "1s", "0s", "-5m"} {
		if _, err := config.RunnerFromEnv(runnerEnv(map[string]string{"REMEDY_RUN_TIMEOUT": bad})); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
```

Run: `go test ./internal/runner ./internal/config -count=1`
Expected: FAIL to compile with `unknown field RunTimeout in struct literal of type runner.Loop` and `c.RunTimeout undefined`.

- [ ] **Step 6: Implement the timeout**

In `internal/runner/loop.go`, replace:

```go
import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"
```

with:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
```

In `internal/runner/loop.go`, replace:

```go
// Loop claims runs one at a time, executes them and reports the outcome.
// Known limitation (phase 0): if the runner dies mid-run the run stays "running";
// a reaper for stale runs arrives with phase 1.
type Loop struct {
	Client        *Client
	Providers     map[string]provider.Provider
	WorkspaceRoot string
	Env           []string
	Log           *slog.Logger
	Backoff       time.Duration // wait after a failed claim, default 3s
}
```

with:

```go
// DefaultRunTimeout is how long a run may take before the runner stops it.
const DefaultRunTimeout = 10 * time.Minute

// Loop claims runs one at a time, executes them and reports the outcome. A runner that dies
// mid-run leaves its run "running"; the control plane's reaper fails it after a while.
type Loop struct {
	Client        *Client
	Providers     map[string]provider.Provider
	WorkspaceRoot string
	Env           []string
	Log           *slog.Logger
	Backoff       time.Duration // wait after a failed claim, default 3s
	RunTimeout    time.Duration // longest a run may take, default DefaultRunTimeout
}
```

In `internal/runner/loop.go`, replace:

```go
	out, err := Execute(ctx, p, provider.Spec{Prompt: r.Prompt, Workdir: dir}, l.Env,
		clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	l.finish(log, r.ID, out)
}
```

with:

```go
	timeout := l.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := Execute(execCtx, p, provider.Spec{Prompt: r.Prompt, Workdir: dir}, l.Env,
		clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	// The deadline kills the subprocess. A run that ended by itself is not a timeout, even if the
	// deadline passed a moment later; neither is a run that was cut short by the runner shutting down.
	if ctx.Err() == nil && errors.Is(execCtx.Err(), context.DeadlineExceeded) && out.ExitCode != 0 {
		out.FailureReason = run.ReasonTimeout
		if out.Result == "" {
			out.Result = fmt.Sprintf("The run was stopped after %s.", timeout)
		}
		log.Warn("run timed out", "after", timeout)
	}
	l.finish(log, r.ID, out)
}
```

In `internal/config/config.go`, replace:

```go
	defaultPollInterval = time.Minute
	minPollInterval     = 10 * time.Second
)
```

with:

```go
	defaultPollInterval = time.Minute
	minPollInterval     = 10 * time.Second

	defaultRunTimeout = 10 * time.Minute
	minRunTimeout     = 10 * time.Second
)
```

In `internal/config/config.go`, replace:

```go
type Runner struct {
	ServerURL     string // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string // REMEDY_CLAUDE_BIN, default "claude"
	ClaudeModel   string // REMEDY_CLAUDE_MODEL, empty means the adapter's default
}
```

with:

```go
type Runner struct {
	ServerURL     string        // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string        // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string        // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string        // REMEDY_CLAUDE_BIN, default "claude"
	ClaudeModel   string        // REMEDY_CLAUDE_MODEL, empty means the adapter's default
	RunTimeout    time.Duration // REMEDY_RUN_TIMEOUT, a Go duration, default 10m, at least 10s
}
```

In `internal/config/config.go`, replace:

```go
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	return c, nil
}
```

with:

```go
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}

	c.RunTimeout = defaultRunTimeout
	if v := get("REMEDY_RUN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minRunTimeout {
			return Runner{}, errors.New("REMEDY_RUN_TIMEOUT must be a duration of at least 10s, for example 10m")
		}
		c.RunTimeout = d
	}
	return c, nil
}
```

In `cmd/remedy-runner/main.go`, replace:

```go
		Env:           os.Environ(),
		Log:           log,
	}
```

with:

```go
		Env:           os.Environ(),
		Log:           log,
		RunTimeout:    cfg.RunTimeout,
	}
```

- [ ] **Step 7: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./... -race -count=1`
Expected: no gofmt output, vet clean, all packages `ok`.

- [ ] **Step 8: Mutation check**

Make each change, run the named tests, expect FAIL, then undo it:

1. In `internal/runner/loop.go`, delete the `out.FailureReason = run.ReasonTimeout` line: `go test ./internal/runner -run TestARunThatTakesTooLongIsStopped -count=1`.
2. In `internal/server/runnerapi.go`, delete the new `if out.FailureReason != ""` block: `go test ./internal/server -run TestFinishRefuses -count=1`.
3. In `internal/store/store.go`, change `output, o.FailureReason,` in `FinishRun` to `output, "",`: `go test ./internal/store -run TestFinishRun -count=1`.

- [ ] **Step 9: Commit**

```bash
git add internal cmd
git commit -m "feat(run): add role, incident link, output and failure reason, and a run timeout" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The reaper

A run stays `running` forever if its runner dies. The reaper closes that gap (spec section 5): once a minute it fails runs that have been `running` for more than 15 minutes, with `failure_reason = timeout`. A browser that follows such a run sees it end within the stream's 15 second keep-alive, because the stream re-reads the run status every time it wakes.

**Files:**
- Create: `internal/store/reap.go`, `internal/reaper/reaper.go`
- Test: `internal/store/reap_test.go`, `internal/reaper/reaper_test.go`
- Modify: `cmd/remedy-server/main.go`

**Interfaces:**
- Consumes: `run.ReasonTimeout` (Task 6).
- Produces:
  - `func (*Store) FailStaleRuns(ctx, cutoff time.Time, result string) ([]string, error)`: fails the runs with status `running` and `started_at` before `cutoff` (exit code `-1`, `failure_reason` `timeout`, `result` set to the given text only if it was empty) and returns their IDs
  - package `reaper`: `type Reaper struct { Store *store.Store; MaxAge, Interval time.Duration; Log *slog.Logger; Now func() time.Time }`, `func (*Reaper) Sweep(ctx) (int, error)`, `func (*Reaper) Run(ctx)`, `const DefaultMaxAge = 15 * time.Minute`, `const DefaultInterval = time.Minute`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/reap_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestFailStaleRunsOnlyTouchesRunsThatStartedBeforeTheCutoff(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	stuck, _ := s.CreateRun(ctx, "claude", "stuck")
	if c, _ := s.ClaimNext(ctx); c == nil || c.ID != stuck.ID {
		t.Fatalf("ClaimNext = %+v", c)
	}
	done, _ := s.CreateRun(ctx, "claude", "done")
	_, _ = s.ClaimNext(ctx)
	if err := s.FinishRun(ctx, done.ID, run.Outcome{Result: "fine"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := s.CreateRun(ctx, "claude", "queued")

	ids, err := s.FailStaleRuns(ctx, time.Now().Add(-time.Hour), "gone")
	if err != nil || len(ids) != 0 {
		t.Fatalf("before the cutoff: ids = %v, err = %v", ids, err)
	}

	ids, err = s.FailStaleRuns(ctx, time.Now().Add(time.Minute), "gone")
	if err != nil || len(ids) != 1 || ids[0] != stuck.ID {
		t.Fatalf("ids = %v, err = %v, want only the running run", ids, err)
	}

	got, _ := s.GetRun(ctx, stuck.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout || got.Result != "gone" ||
		got.ExitCode == nil || *got.ExitCode != -1 || got.FinishedAt == nil {
		t.Fatalf("stuck run = %+v", got)
	}
	if got, _ := s.GetRun(ctx, done.ID); got.Status != run.Succeeded || got.Result != "fine" {
		t.Fatalf("finished run = %+v", got)
	}
	if got, _ := s.GetRun(ctx, queued.ID); got.Status != run.Queued {
		t.Fatalf("queued run = %+v", got)
	}
}
```

Create `internal/reaper/reaper_test.go`:

```go
package reaper_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSweepFailsOnlyRunsThatAreRunningForTooLong(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	stuck, _ := st.CreateRun(ctx, "claude", "stuck")
	if c, _ := st.ClaimNext(ctx); c == nil || c.ID != stuck.ID {
		t.Fatalf("ClaimNext = %+v", c)
	}
	done, _ := st.CreateRun(ctx, "claude", "done")
	_, _ = st.ClaimNext(ctx)
	_ = st.FinishRun(ctx, done.ID, run.Outcome{})
	queued, _ := st.CreateRun(ctx, "claude", "queued")

	now := time.Now()
	r := &reaper.Reaper{Store: st, MaxAge: 15 * time.Minute, Log: quiet, Now: func() time.Time { return now }}

	if n, err := r.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("Sweep right after the start = %d, %v; a fresh run must be left alone", n, err)
	}
	now = now.Add(14 * time.Minute)
	if n, _ := r.Sweep(ctx); n != 0 {
		t.Fatalf("Sweep after 14 minutes = %d, want 0", n)
	}

	now = now.Add(2 * time.Minute)
	n, err := r.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Sweep after 16 minutes = %d, %v, want 1", n, err)
	}
	got, _ := st.GetRun(ctx, stuck.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout || got.Result == "" {
		t.Fatalf("stuck run = %+v", got)
	}
	if got, _ := st.GetRun(ctx, done.ID); got.Status != run.Succeeded {
		t.Fatalf("finished run = %+v", got)
	}
	if got, _ := st.GetRun(ctx, queued.ID); got.Status != run.Queued {
		t.Fatalf("queued run = %+v", got)
	}
	if n, _ := r.Sweep(ctx); n != 0 {
		t.Fatalf("a second sweep = %d, want 0 (idempotent)", n)
	}
}

func TestRunSweepsUntilItIsCancelled(t *testing.T) {
	st, ctx := openStore(t), context.Background()
	stuck, _ := st.CreateRun(ctx, "claude", "stuck")
	_, _ = st.ClaimNext(ctx)

	r := &reaper.Reaper{Store: st, MaxAge: time.Millisecond, Interval: 20 * time.Millisecond, Log: quiet}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := st.GetRun(ctx, stuck.ID); got.Status == run.Failed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the reaper did not fail the stuck run")
}
```

Run: `go test ./internal/store ./internal/reaper -count=1`
Expected: FAIL to compile with `s.FailStaleRuns undefined` and `package .../reaper` having no Go files.

- [ ] **Step 2: Implement the reaper**

Create `internal/store/reap.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// FailStaleRuns fails the runs that are still running and started before cutoff: no runner ever
// finished them. It returns their IDs. result becomes the run's result text if it has none.
func (s *Store) FailStaleRuns(ctx context.Context, cutoff time.Time, result string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE runs SET status = 'failed', exit_code = -1, failure_reason = ?, finished_at = ?,
			result = CASE WHEN result = '' THEN ? ELSE result END
		WHERE status = 'running' AND started_at < ?
		RETURNING id`, run.ReasonTimeout, formatTS(time.Now()), result, formatTS(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
```

Create `internal/reaper/reaper.go`:

```go
// Package reaper fails runs that stay "running" because their runner died or lost the connection.
package reaper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const (
	// DefaultMaxAge is longer than the runner's own timeout (10 minutes), so the reaper only acts
	// when the runner could not report.
	DefaultMaxAge   = 15 * time.Minute
	DefaultInterval = time.Minute
)

type Reaper struct {
	Store    *store.Store
	MaxAge   time.Duration // default DefaultMaxAge
	Interval time.Duration // default DefaultInterval
	Log      *slog.Logger
	Now      func() time.Time // default time.Now
}

// Sweep fails the runs that have been running for longer than MaxAge and returns how many it failed.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	maxAge := r.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	ids, err := r.Store.FailStaleRuns(ctx, now.Add(-maxAge),
		fmt.Sprintf("The run did not finish within %s and was failed by the control plane.", maxAge))
	for _, id := range ids {
		r.Log.Warn("failed a stale run", "run", id, "after", maxAge)
	}
	return len(ids), err
}

// Run sweeps every Interval until ctx ends.
func (r *Reaper) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.Log.Error("reaper sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
```

In `cmd/remedy-server/main.go`, replace:

```go
	"github.com/Jaydee94/remedy/internal/poller"
```

with:

```go
	"github.com/Jaydee94/remedy/internal/poller"
	"github.com/Jaydee94/remedy/internal/reaper"
```

In `cmd/remedy-server/main.go`, replace:

```go
	srv := &http.Server{
```

with:

```go
	background((&reaper.Reaper{Store: st, Log: log}).Run)

	srv := &http.Server{
```

- [ ] **Step 3: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./... -race -count=1`
Expected: no gofmt output, vet clean, all packages `ok`.

- [ ] **Step 4: Mutation check**

Make each change, run `go test ./internal/store ./internal/reaper -count=1`, expect FAIL, then undo it:

1. In `internal/store/reap.go`, delete `AND started_at < ?` and its argument `formatTS(cutoff)` (every running run is failed).
2. In `internal/store/reap.go`, change `WHERE status = 'running' AND started_at < ?` to `WHERE started_at < ?` (finished runs are failed too).

- [ ] **Step 5: Commit**

```bash
git add internal cmd
git commit -m "feat(reaper): fail runs that stay running" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 8: The incident views

An **Incidents** page (table with a state filter and a repo filter) and an **incident detail** page (facts, links, history, ignore). The run view shows a timeout and the incident a run belongs to. The index route becomes the incident list until the timeline (plan 1d) takes the home place.

**Files:**
- Create: `web/src/incidents.ts`, `web/src/StateBadge.tsx`, `web/src/RefLink.tsx`, `web/src/IncidentsPage.tsx`, `web/src/IncidentView.tsx`
- Overwrite: `web/src/App.tsx`, `web/src/NotFound.tsx`
- Modify: `web/src/api.ts`, `web/src/RunView.tsx`, `web/src/components/AppLayout.tsx`

**Interfaces:**
- Consumes: the incident API of Task 5 and the run fields of Task 6; shadcn components `alert`, `badge`, `card`, `skeleton`, `table` and `ConfirmButton`.
- Produces: routes `/incidents` and `/incidents/:id`; `api.listIncidents(state, repoId?)`, `api.getIncident(id)`, `api.ignoreIncident(id)`; the types `Incident`, `IncidentState`, `ActivityEntry`, `IncidentDetail`.

There is no web test runner. The logic in `incidents.ts` is small and pure; everything is checked in the browser in Step 6.

- [ ] **Step 1: Types and API calls**

In `web/src/api.ts`, replace:

```ts
  startedAt?: string
  finishedAt?: string
}

export interface RunEvent {
```

with:

```ts
  startedAt?: string
  finishedAt?: string
  role: 'adhoc' | 'responder'
  incidentId?: number
  /** Set when the run was stopped for taking too long. */
  failureReason?: 'timeout'
}

export interface RunEvent {
```

In `web/src/api.ts`, replace:

```ts
export class ApiError extends Error {
```

with:

```ts
export type IncidentState = 'open' | 'diagnosing' | 'diagnosed' | 'resolved' | 'ignored'

export interface Incident {
  id: number
  repoId: number
  repo: string
  /** "pr:<number>" or "branch:<name>". */
  ref: string
  refUrl?: string
  checkName: string
  state: IncidentState
  conclusion: string
  headSha: string
  checkUrl?: string
  occurrences: number
  firstSeen: string
  lastSeen: string
  resolvedAt?: string
  resolvedReason?: string
}

export interface ActivityEntry {
  id: number
  at: string
  kind: string
  summary: string
}

export interface IncidentDetail {
  incident: Incident
  activity: ActivityEntry[]
}

export class ApiError extends Error {
```

In `web/src/api.ts`, replace:

```ts
  deleteRepo: (id: number) => request<void>('DELETE', `/api/repos/${id}`),
}
```

with:

```ts
  deleteRepo: (id: number) => request<void>('DELETE', `/api/repos/${id}`),

  /** state is "active", "all" or one incident state. */
  listIncidents: (state: string, repoId?: number) => {
    const query = new URLSearchParams({ state })
    if (repoId !== undefined) query.set('repo', String(repoId))
    return request<Incident[]>('GET', `/api/incidents?${query.toString()}`)
  },
  getIncident: (id: number) => request<IncidentDetail>('GET', `/api/incidents/${id}`),
  ignoreIncident: (id: number) => request<Incident>('POST', `/api/incidents/${id}/ignore`),
}
```

- [ ] **Step 2: Helpers and small components**

Create `web/src/incidents.ts`:

```ts
import type { IncidentState } from './api.ts'

export const incidentStateColor: Record<IncidentState, string> = {
  open: 'bg-rose-500',
  diagnosing: 'bg-amber-500',
  diagnosed: 'bg-sky-500',
  resolved: 'bg-emerald-500',
  ignored: 'bg-slate-600',
}

/** Links that leave Remedy are underlined, so that they do not read as plain text. */
export const externalLinkClass = 'underline decoration-muted-foreground/50 underline-offset-4 hover:decoration-foreground'

/** "pr:7" becomes "PR #7" and "branch:main" becomes "main". */
export function refLabel(ref: string): string {
  if (ref.startsWith('pr:')) return `PR #${ref.slice(3)}`
  if (ref.startsWith('branch:')) return ref.slice(7)
  return ref
}

const conclusionLabels: Record<string, string> = {
  failure: 'failed',
  timed_out: 'timed out',
  startup_failure: 'failed to start',
  cancelled: 'cancelled',
  action_required: 'needs action',
}

export function conclusionText(conclusion: string): string {
  return conclusionLabels[conclusion] ?? conclusion
}

export function reasonText(reason?: string): string {
  switch (reason) {
    case 'green':
      return 'the check turned green'
    case 'pr_closed':
      return 'the pull request was closed or merged'
    default:
      return reason ?? ''
  }
}

/** Only http(s) links become links. Anything else that comes from GitHub's data is shown as text. */
export function safeUrl(url?: string): string | undefined {
  return url && /^https?:\/\//i.test(url) ? url : undefined
}

export function timeAgo(iso: string, now: number = Date.now()): string {
  const seconds = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000))
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `${hours} h ago`
  return `${Math.floor(hours / 24)} d ago`
}
```

Create `web/src/StateBadge.tsx`:

```tsx
import type { IncidentState } from './api.ts'
import { incidentStateColor } from './incidents.ts'
import { Badge } from '@/components/ui/badge'

export default function StateBadge({ state }: { state: IncidentState }) {
  return (
    <Badge variant="outline" className="gap-2">
      <span className={`h-2 w-2 rounded-full ${incidentStateColor[state]}`} />
      {state}
    </Badge>
  )
}
```

Create `web/src/RefLink.tsx`:

```tsx
import { externalLinkClass, refLabel, safeUrl } from './incidents.ts'

/** The ref of an incident, linked to its pull request when GitHub gave a usable address. */
export default function RefLink({ refName, url }: { refName: string; url?: string }) {
  const href = safeUrl(url)
  const label = refLabel(refName)
  if (!href) return <span>{label}</span>
  return (
    <a href={href} target="_blank" rel="noreferrer" className={externalLinkClass}>
      {label}
    </a>
  )
}
```

- [ ] **Step 3: The incident list**

Create `web/src/IncidentsPage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Incident, Repo } from './api.ts'
import { conclusionText, timeAgo } from './incidents.ts'
import RefLink from './RefLink.tsx'
import StateBadge from './StateBadge.tsx'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

const selectClass =
  'h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30'

const stateOptions = [
  { value: 'active', label: 'Active' },
  { value: 'ignored', label: 'Ignored' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'all', label: 'All' },
]

export default function IncidentsPage() {
  const [state, setState] = useState('active')
  const [repoId, setRepoId] = useState('')
  const [incidents, setIncidents] = useState<Incident[] | null>(null)
  const [repos, setRepos] = useState<Repo[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    api.listRepos().then(setRepos).catch(() => setRepos([]))
  }, [])

  useEffect(() => {
    let active = true
    const load = () =>
      api
        .listIncidents(state, repoId ? Number(repoId) : undefined)
        .then((list) => {
          if (!active) return
          setIncidents(list)
          setError('')
        })
        .catch((e: unknown) => {
          if (active) setError(e instanceof ApiError ? e.message : 'Could not load the incidents')
        })
    void load()
    const timer = setInterval(() => void load(), 5000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [state, repoId])

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Incidents</h1>
        <div className="flex flex-wrap gap-2">
          <select aria-label="State" className={selectClass} value={state} onChange={(e) => setState(e.target.value)}>
            {stateOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <select
            aria-label="Repository"
            className={selectClass}
            value={repoId}
            onChange={(e) => setRepoId(e.target.value)}
          >
            <option value="">All repositories</option>
            {repos.map((r) => (
              <option key={r.id} value={r.id}>
                {r.fullName}
              </option>
            ))}
          </select>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {incidents === null ? (
        !error && <Skeleton className="h-40 w-full" />
      ) : incidents.length === 0 ? (
        <p className="text-muted-foreground">
          {state === 'active'
            ? 'No active incidents. Remedy checks the enabled repositories regularly.'
            : 'No incidents match.'}
        </p>
      ) : (
        <Card>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Check</TableHead>
                  <TableHead>Repository</TableHead>
                  <TableHead>Ref</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Conclusion</TableHead>
                  <TableHead>Occurrences</TableHead>
                  <TableHead>Last seen</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {incidents.map((i) => (
                  <TableRow key={i.id}>
                    <TableCell className="font-medium">
                      <Link to={`/incidents/${i.id}`} className="underline-offset-4 hover:underline">
                        {i.checkName}
                      </Link>
                    </TableCell>
                    <TableCell>{i.repo}</TableCell>
                    <TableCell>
                      <RefLink refName={i.ref} url={i.refUrl} />
                    </TableCell>
                    <TableCell>
                      <StateBadge state={i.state} />
                    </TableCell>
                    <TableCell>{conclusionText(i.conclusion)}</TableCell>
                    <TableCell>{i.occurrences}</TableCell>
                    <TableCell className="text-muted-foreground" title={new Date(i.lastSeen).toLocaleString()}>
                      {timeAgo(i.lastSeen)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
```

- [ ] **Step 4: The incident detail**

Create `web/src/IncidentView.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { IncidentDetail } from './api.ts'
import { conclusionText, externalLinkClass, reasonText, safeUrl } from './incidents.ts'
import RefLink from './RefLink.tsx'
import StateBadge from './StateBadge.tsx'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

const when = (iso: string) => new Date(iso).toLocaleString()

/** Mount with `key={id}` so that switching incidents resets the state. */
export default function IncidentView({ id }: { id: number }) {
  const [detail, setDetail] = useState<IncidentDetail | null>(null)
  const [missing, setMissing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    const load = () =>
      api
        .getIncident(id)
        .then((d) => {
          if (!active) return
          setDetail(d)
          setError('')
        })
        .catch((e: unknown) => {
          if (!active) return
          if (e instanceof ApiError && e.status === 404) setMissing(true)
          else setError(e instanceof ApiError ? e.message : 'Could not load the incident')
        })
    void load()
    const timer = setInterval(() => void load(), 5000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [id])

  async function ignore() {
    setBusy(true)
    setError('')
    try {
      await api.ignoreIncident(id)
      setDetail(await api.getIncident(id))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not ignore the incident')
    } finally {
      setBusy(false)
    }
  }

  if (missing) {
    return (
      <div className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">Incident not found</h1>
        <Link to="/incidents" className="text-sm text-muted-foreground hover:text-foreground">
          Back to incidents
        </Link>
      </div>
    )
  }

  const incident = detail?.incident
  const checkHref = safeUrl(incident?.checkUrl)
  const canIgnore = incident && ['open', 'diagnosing', 'diagnosed'].includes(incident.state)

  return (
    <div className="flex flex-col gap-6">
      <Link to="/incidents" className="text-sm text-muted-foreground hover:text-foreground">
        ← All incidents
      </Link>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!detail || !incident ? (
        !error && <Skeleton className="h-48 w-full" />
      ) : (
        <>
          <header className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-3">
              <h1 className="text-2xl font-semibold tracking-tight break-all">{incident.checkName}</h1>
              <StateBadge state={incident.state} />
            </div>
            <p className="flex flex-wrap items-center gap-x-2 text-muted-foreground">
              <span>{incident.repo}</span>
              <span aria-hidden>·</span>
              <RefLink refName={incident.ref} url={incident.refUrl} />
              {checkHref && (
                <>
                  <span aria-hidden>·</span>
                  <a href={checkHref} target="_blank" rel="noreferrer" className={externalLinkClass}>
                    Open the check run
                  </a>
                </>
              )}
            </p>
          </header>

          <Card>
            <CardHeader>
              <CardTitle>Details</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
                <dt className="text-muted-foreground">Conclusion</dt>
                <dd>{conclusionText(incident.conclusion)}</dd>
                <dt className="text-muted-foreground">Commit</dt>
                <dd className="font-mono">{incident.headSha.slice(0, 7)}</dd>
                <dt className="text-muted-foreground">Occurrences</dt>
                <dd>{incident.occurrences}</dd>
                <dt className="text-muted-foreground">First seen</dt>
                <dd>{when(incident.firstSeen)}</dd>
                <dt className="text-muted-foreground">Last seen</dt>
                <dd>{when(incident.lastSeen)}</dd>
                {incident.resolvedAt && (
                  <>
                    <dt className="text-muted-foreground">Resolved</dt>
                    <dd>
                      {when(incident.resolvedAt)} ({reasonText(incident.resolvedReason)})
                    </dd>
                  </>
                )}
              </dl>
              {canIgnore && (
                <div>
                  <ConfirmButton
                    label="Ignore"
                    confirmLabel="Confirm ignore"
                    disabled={busy}
                    onConfirm={() => void ignore()}
                  />
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>History</CardTitle>
            </CardHeader>
            <CardContent>
              {detail.activity.length === 0 && <p className="text-sm text-muted-foreground">No history yet.</p>}
              <ol className="flex flex-col gap-3">
                {detail.activity.map((a) => (
                  <li key={a.id} className="flex flex-col gap-0.5">
                    <span className="break-words whitespace-pre-wrap">{a.summary}</span>
                    <span className="text-xs text-muted-foreground">{when(a.at)}</span>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Routes, navigation, run view**

Overwrite `web/src/App.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router'
import { api } from './api.ts'
import AppLayout from './components/AppLayout.tsx'
import IncidentsPage from './IncidentsPage.tsx'
import IncidentView from './IncidentView.tsx'
import Login from './Login.tsx'
import NotFound from './NotFound.tsx'
import RunsPage from './RunsPage.tsx'
import RunView from './RunView.tsx'
import SettingsPage from './SettingsPage.tsx'

/** Mounts RunView with key={id} so that switching runs resets its state. */
function RunRoute() {
  const { id } = useParams()
  return id ? <RunView key={id} id={id} /> : <Navigate to="/runs" replace />
}

/** Mounts IncidentView with key={id}; anything that is not a positive whole number is not an incident. */
function IncidentRoute() {
  const id = Number(useParams().id)
  return Number.isInteger(id) && id > 0 ? <IncidentView key={id} id={id} /> : <NotFound />
}

export default function App() {
  const [auth, setAuth] = useState<'loading' | 'in' | 'out'>('loading')

  useEffect(() => {
    api.me().then(() => setAuth('in')).catch(() => setAuth('out'))
  }, [])

  if (auth === 'loading') return null
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />

  async function signOut() {
    await api.logout()
    setAuth('out')
  }

  return (
    <Routes>
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route index element={<Navigate to="/incidents" replace />} />
        <Route path="incidents" element={<IncidentsPage />} />
        <Route path="incidents/:id" element={<IncidentRoute />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:id" element={<RunRoute />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}
```

Overwrite `web/src/NotFound.tsx`:

```tsx
import { Link } from 'react-router'

export default function NotFound() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <Link to="/incidents" className="text-sm text-muted-foreground hover:text-foreground">
        Back to incidents
      </Link>
    </div>
  )
}
```

In `web/src/components/AppLayout.tsx`, replace:

```tsx
import { LogOut, Play, Settings } from 'lucide-react'
```

with:

```tsx
import { LogOut, Play, Settings, TriangleAlert } from 'lucide-react'
```

In `web/src/components/AppLayout.tsx`, replace:

```tsx
const nav = [
  { to: '/runs', label: 'Runs', icon: Play },
```

with:

```tsx
const nav = [
  { to: '/incidents', label: 'Incidents', icon: TriangleAlert },
  { to: '/runs', label: 'Runs', icon: Play },
```

In `web/src/components/AppLayout.tsx`, replace:

```tsx
        <div className="mx-auto max-w-4xl">
```

with:

```tsx
        <div className="mx-auto max-w-5xl">
```

In `web/src/RunView.tsx`, replace:

```tsx
            {run.exitCode !== undefined && <span className="text-sm text-muted-foreground">exit {run.exitCode}</span>}
          </div>
          <p className="whitespace-pre-wrap rounded-lg bg-card p-3">{run.prompt}</p>
```

with:

```tsx
            {run.exitCode !== undefined && <span className="text-sm text-muted-foreground">exit {run.exitCode}</span>}
            {run.failureReason === 'timeout' && <Badge variant="destructive">timed out</Badge>}
            {run.incidentId !== undefined && (
              <Link to={`/incidents/${run.incidentId}`} className="text-sm text-muted-foreground hover:text-foreground">
                Incident #{run.incidentId}
              </Link>
            )}
          </div>
          <p className="whitespace-pre-wrap rounded-lg bg-card p-3">{run.prompt}</p>
          {run.failureReason && run.result && <p className="text-sm text-destructive">{run.result}</p>}
```

Run: `cd web && npm run lint && npm run build`
Expected: oxlint reports no warnings or errors, `tsc -b` and `vite build` succeed. If `TriangleAlert` is not exported by the installed `lucide-react`, `tsc` says so: use `CircleAlert` instead.

- [ ] **Step 6: Check it in a real browser**

Build the UI into the server and start it on a fresh database. The seeded GitHub connection has the status `error`, so the poller stays idle and does not try to reach GitHub.

```bash
make web-install          # a fresh worktree has no node_modules
make build
WS=$(mktemp -d)
export REMEDY_ADMIN_PASSWORD='ui-test-password' REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)" \
  REMEDY_MASTER_KEY="$(openssl rand -base64 32)" REMEDY_DB="$WS/remedy.db" REMEDY_ADDR=127.0.0.1:8080
./bin/remedy-server > "$WS/server.log" 2>&1 &
until curl -sf http://127.0.0.1:8080/healthz > /dev/null; do sleep 0.2; done
```

Seed incidents with `sqlite3 -cmd '.timeout 5000' "$WS/remedy.db"`, feeding it this script. Incident 5 has a hostile check name and `javascript:` addresses on purpose.

```sql
INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
VALUES (1, x'00', 'seed', 'octo', 'error', 'Seeded for the UI check; polling is off.', '2026-10-02T12:00:00.000000000Z');
INSERT INTO repos (id, connection_id, full_name, default_branch, enabled, created_at) VALUES
  (1, 1, 'octo/hello', 'main', 1, '2026-10-02T12:00:00.000000000Z'),
  (2, 1, 'octo/other', 'main', 1, '2026-10-02T12:00:00.000000000Z');
INSERT INTO incidents (id, repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url, occurrences, first_seen, last_seen, resolved_at, resolved_reason) VALUES
  (1, 1, 'pr:7', 'https://github.com/octo/hello/pull/7', 'go', 'open', 'failure', 'abc1234def5678', 'https://github.com/octo/hello/actions/runs/1/job/1', 3, '2026-10-02T10:00:00.000000000Z', '2026-10-02T11:30:00.000000000Z', NULL, ''),
  (2, 1, 'branch:main', '', 'web', 'open', 'timed_out', '2feb68e76ef56d9721d71a29c955c6c29fd8b5ef', 'https://github.com/octo/hello/actions/runs/2/job/2', 1, '2026-10-02T11:00:00.000000000Z', '2026-10-02T11:00:00.000000000Z', NULL, ''),
  (3, 2, 'pr:3', 'https://github.com/octo/other/pull/3', 'lint', 'ignored', 'cancelled', '1111111222222', '', 1, '2026-10-02T09:00:00.000000000Z', '2026-10-02T09:00:00.000000000Z', NULL, ''),
  (4, 1, 'pr:5', 'https://github.com/octo/hello/pull/5', 'go', 'resolved', 'failure', '3333333444444', '', 2, '2026-10-02T08:00:00.000000000Z', '2026-10-02T08:30:00.000000000Z', '2026-10-02T08:45:00.000000000Z', 'green'),
  (5, 2, 'pr:9', 'javascript:alert(1)', '<img src=x onerror=alert(1)> "quoted" & co', 'open', 'action_required', '5555555666666', 'javascript:alert(2)', 1, '2026-10-02T11:45:00.000000000Z', '2026-10-02T11:45:00.000000000Z', NULL, '');
INSERT INTO activity (at, kind, repo_id, incident_id, summary, data) VALUES
  ('2026-10-02T10:00:00.000000000Z', 'incident_opened', 1, 1, 'go failed on PR #7 in octo/hello', '{}'),
  ('2026-10-02T10:40:00.000000000Z', 'incident_recurred', 1, 1, 'go failed again on PR #7 in octo/hello (commit 9999999)', '{}'),
  ('2026-10-02T11:30:00.000000000Z', 'incident_recurred', 1, 1, 'go failed again on PR #7 in octo/hello (commit abc1234)', '{}');
INSERT INTO runs (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at, role, incident_id, failure_reason) VALUES
  ('seededrun', 'claude', 'Diagnose the failed go check', 'failed', -1, 'The run was stopped after 10m0s.', '', 0, '2026-10-02T11:00:00.000000000Z', '2026-10-02T11:00:01.000000000Z', '2026-10-02T11:10:01.000000000Z', 'responder', 1, 'timeout');
```

With the Playwright browser, open <http://127.0.0.1:8080>, sign in with `ui-test-password`, and check each point. Take a screenshot of the list, of the detail page and of the run view.

1. After the login the page is **Incidents** (the index redirects there) and the sidebar highlights it. The table shows three rows, newest `last seen` first: the hostile check name, `go` on PR #7, `web` on `main`. The ignored and the resolved incident are not listed.
2. The hostile name is shown as literal text (`<img src=x ...`), no dialog opens and the browser console has no errors. Its ref cell is plain text `PR #9`, not a link, and has no `javascript:` address anywhere: `document.querySelectorAll('a[href^="javascript:"]').length` is `0`.
3. The state filter: **Ignored** shows only `lint`; **Resolved** shows only the `go` incident of PR #5; **All** shows five rows. With **All** and the repository **octo/other** the rows are `lint` and the hostile one. **Resolved** with **octo/other** shows "No incidents match."
4. The ref `PR #7` links to `https://github.com/octo/hello/pull/7` (`target="_blank"`); the `web` row on `main` shows `main` without a link.
5. Click `go` (PR #7): the detail page shows the badge `open`, `octo/hello · PR #7 · Open the check run`, the details (`failed`, commit `abc1234`, occurrences `3`) and a history of three entries, newest first (`... (commit abc1234)`, `... (commit 9999999)`, `go failed on PR #7 ...`).
6. Click **Ignore**: the button becomes **Confirm ignore**; clicking elsewhere disarms it. Arm it again and confirm: the badge turns to `ignored`, the button is gone and the history gains `Ignored the incident for go on PR #7 in octo/hello` at the top. Back on the list, **Active** no longer contains `go`.
7. Open `/incidents/999` and `/incidents/abc`: both show a not-found page with a link back. Open the resolved incident (PR #5): it shows `Resolved ... (the check turned green)` and no Ignore button.
8. Open `/runs/seededrun`: a red `timed out` badge, the link `Incident #1` (it opens the incident), and the red text `The run was stopped after 10m0s.`
9. The resolved incident (PR #5) has no activity entries in the seed and shows "No history yet." under History.

The layout is desktop-first because of the fixed sidebar from plan 1a; a phone layout is not part of this plan. The page itself must still have no horizontal scrollbar at 1024 px: `document.documentElement.scrollWidth <= document.documentElement.clientWidth`.

Stop the server (`kill %1`) and remove `$WS`. Then run the whole suite.

Run: `make check`
Expected: fmt, vet, Go tests, web lint and web build all pass.

- [ ] **Step 7: Commit**

```bash
git add web
git commit -m "feat(web): add the incident list and the incident detail" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Documentation

**Files:**
- Modify: `README.md`, `CLAUDE.md`, `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`

- [ ] **Step 1: README**

In `README.md`, replace:

```markdown
> Status: phase 1 is in progress. You can start a read-only agent run from the web UI and watch
> its output stream in live, and you can connect GitHub (read-only token, stored encrypted) and add
> repositories under **Settings**. Watching them for failed checks, incidents and the automatic
> diagnosis, the approval gatekeeper and the learning graph come next (see
> [`docs/design.md`](docs/design.md), [`docs/specs/`](docs/specs/) and [`docs/plans/`](docs/plans/)).
```

with:

```markdown
> Status: phase 1 is in progress. You can start a read-only agent run from the web UI and watch
> its output stream in live, connect GitHub (read-only token, stored encrypted), add repositories
> under **Settings**, and see failed checks of their pull requests and default branches as
> **incidents**. The automatic diagnosis, the timeline, the approval gatekeeper and the learning
> graph come next (see [`docs/design.md`](docs/design.md), [`docs/specs/`](docs/specs/) and
> [`docs/plans/`](docs/plans/)).
```

In `README.md`, replace:

```markdown
again under **Settings**. Under **Settings** you also connect GitHub (a fine-grained, read-only personal access
token) and add repositories. `REMEDY_GITHUB_API_URL` (default `https://api.github.com`) exists for tests.
```

with:

```markdown
again under **Settings**. Under **Settings** you also connect GitHub (a fine-grained, read-only personal access
token) and add repositories. `REMEDY_GITHUB_API_URL` (default `https://api.github.com`) exists for tests.

Remedy polls the enabled repositories with that token every 60 seconds (`REMEDY_POLL_INTERVAL`, a Go
duration of at least `10s`) and never writes to GitHub. A failed check becomes an incident under
**Incidents**, and it resolves when the check is green again or the pull request is closed. The runner stops a
run that takes longer than 10 minutes (`REMEDY_RUN_TIMEOUT`, set on the runner); the server fails runs that
stay `running` for more than 15 minutes, for example because the runner died.
```

- [ ] **Step 2: CLAUDE.md**

In `CLAUDE.md`, replace:

```markdown
Phase 0 is done: control plane (SQLite, admin and runner APIs, SSE), runner, UI, Docker image. Phase 1 (GitHub, incidents, responder) is in progress and is built in small plans: the spec is `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, the plans are in `docs/plans/`. Check `git log` and the plan before assuming a package from a later task exists, and build only within the current plan.
```

with:

```markdown
Phase 0 is done: control plane (SQLite, admin and runner APIs, SSE), runner, UI, Docker image. Phase 1 is built in small plans: the spec is `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, the plans are in `docs/plans/`. Plans 1a (GitHub connection and repos) and 1b (poller, incidents, activity log, run extension, reaper) are implemented; the responder (1c) and the timeline with the real run against GitHub (1d) are next. Check `git log` and the plan before assuming a package from a later task exists, and build only within the current plan.
```

In `CLAUDE.md`, replace:

```markdown
The server needs `REMEDY_ADMIN_PASSWORD` (12+ chars), `REMEDY_RUNNER_TOKEN` (24+ chars) and `REMEDY_MASTER_KEY` (`openssl rand -base64 32`). The runner needs the same runner token. See the README quick start.
```

with:

```markdown
The server needs `REMEDY_ADMIN_PASSWORD` (12+ chars), `REMEDY_RUNNER_TOKEN` (24+ chars) and `REMEDY_MASTER_KEY` (`openssl rand -base64 32`). The runner needs the same runner token. Optional: `REMEDY_POLL_INTERVAL` (server, default 60s, at least 10s) and `REMEDY_RUN_TIMEOUT` (runner, default 10m). See the README quick start.
```

In `CLAUDE.md`, replace:

```markdown
- **Control plane** (`cmd/remedy-server`): admin API for the UI, runner API, SSE stream, SQLite, and later the MCP gatekeeper, git/GitHub access and signal ingestion.
```

with:

```markdown
- **Control plane** (`cmd/remedy-server`): admin API for the UI, runner API, SSE stream, SQLite, a GitHub poller that feeds the incident engine (`internal/poller`, `internal/incident`), a reaper for runs that stay `running` (`internal/reaper`), and later the MCP gatekeeper.
```

In `CLAUDE.md`, replace:

```markdown
that ordering is what guarantees a client sees every event before `done`.
```

with:

```markdown
that ordering is what guarantees a client sees every event before `done`.
- **Incidents** are keyed by `(repo, ref, check name)`, and every state change writes its `activity` entry in the same transaction. The poller must stay idempotent (a second cycle on the same GitHub state changes nothing) and must not resolve PR incidents from a full page of 100 PRs, which may be truncated. Inside `Store.inTx` use only the `tx`: the store has one connection, so a call on `s.db` there deadlocks.
```

- [ ] **Step 3: Spec**

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
(steps 0 to 4, implemented); the plans for the remaining steps follow.
```

with:

```markdown
(steps 0 to 4) and [`phase-1b`](../plans/phase-1b-signals-and-incidents.md) (steps 5 and 6), both implemented; the plans for the remaining steps follow.
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
## 4. Data model (migration 002)
```

with:

```markdown
## 4. Data model (migrations 002 to 004)
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
- `incidents`: `id`, `repo_id`, `ref`, `check_name`, `state`
```

with:

```markdown
- `incidents`: `id`, `repo_id`, `ref`, `ref_url`, `check_name`, `state`
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
Bad results are `failure`, `timed_out`, `startup_failure`, `cancelled`, `action_required`.
```

with:

```markdown
Bad results are `failure`, `timed_out`, `startup_failure`, `cancelled`, `action_required`. A finished check with any other conclusion (for example `stale`) counts like an unfinished one. `last_seen` moves on every poll that still sees the failure.

An ignored incident keeps its key: new failures change nothing, and a green check resolves it. Several check runs with the same name on one ref count as one observation, the worst result wins, so a green twin never resolves a red one. A full page of 100 open pull requests may be truncated, so then no incident is resolved as `pr_closed`.
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
Table with a state filter (default: open) and a repo filter.
```

with:

```markdown
Table with a state filter (default: active, that is open, diagnosing and diagnosed) and a repo filter.
```

- [ ] **Step 4: Verify and commit**

Run: `make check`
Expected: all green.

```bash
git add README.md CLAUDE.md docs/specs
git commit -m "docs: document polling, incidents and the run timeouts" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
