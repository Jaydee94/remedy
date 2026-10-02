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
