package github_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
)

func TestGetPRDecodesARealResponse(t *testing.T) {
	body := fixture(t, "pr-20.json")
	var method, uri string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, uri = r.Method, r.URL.RequestURI()
		_, _ = w.Write(body)
	})

	pr, err := c.GetPR(context.Background(), "Jaydee94/remedy", 20)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || uri != "/repos/Jaydee94/remedy/pulls/20" {
		t.Errorf("request = %s %s", method, uri)
	}
	if pr.Number != 20 || pr.Title != "chore(deps): update dependency typescript to v7" ||
		!strings.HasPrefix(pr.Body, "This PR contains the following updates:") ||
		pr.Head.SHA != "913da1edbf28ced7b324b5b99ab3c6c61241acee" || pr.User.Login != "renovate[bot]" || pr.User.Type != "Bot" {
		t.Errorf("pull request = %+v", pr)
	}
}

func TestAPullRequestWithoutADescriptionHasAnEmptyBody(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"number":3,"title":"t","body":null,"head":{"sha":"abc","ref":"x"}}`))
	})
	pr, err := c.GetPR(context.Background(), "o/r", 3)
	if err != nil || pr.Body != "" {
		t.Fatalf("pull request = %+v, %v", pr, err)
	}
}

func TestListPRFilesDecodesARealResponse(t *testing.T) {
	body := fixture(t, "pr-20-files.json")
	var uri string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		uri = r.URL.RequestURI()
		_, _ = w.Write(body)
	})

	files, err := c.ListPRFiles(context.Background(), "Jaydee94/remedy", 20)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/Jaydee94/remedy/pulls/20/files?per_page=100" {
		t.Errorf("request = %s", uri)
	}
	if len(files) != 1 || files[0].Filename != "web/package.json" || files[0].Status != "modified" ||
		files[0].Additions != 1 || files[0].Deletions != 1 || !strings.HasPrefix(files[0].Patch, "@@ -21,7 +21,7 @@") {
		t.Errorf("files = %+v", files)
	}
}

func TestCheckRunsCarryTheirOutput(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_count":2,"check_runs":[
			{"id":1,"name":"go","status":"completed","conclusion":"failure","head_sha":"abc","html_url":"u","output":{"title":null,"summary":null,"text":null}},
			{"id":2,"name":"lint","status":"completed","conclusion":"failure","head_sha":"abc","html_url":"u","output":{"title":"3 problems","summary":"see below","text":"file.go:1: bad"}}]}`))
	})
	runs, err := c.ListCheckRuns(context.Background(), "o/r", "abc")
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if runs[0].Output.Title != "" || runs[0].Output.Text != "" {
		t.Errorf("an Actions job has no output: %+v", runs[0].Output)
	}
	if runs[1].Output.Title != "3 problems" || runs[1].Output.Summary != "see below" || runs[1].Output.Text != "file.go:1: bad" {
		t.Errorf("output = %+v", runs[1].Output)
	}
}

func TestResponderReadsRejectInvalidInputWithoutARequest(t *testing.T) {
	var requests atomic.Int32
	c := newClient(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	ctx := context.Background()

	if _, err := c.GetPR(ctx, "../x", 1); !errors.Is(err, github.ErrInvalidRepoName) {
		t.Errorf("GetPR(../x) error = %v", err)
	}
	if _, err := c.GetPR(ctx, "o/r", 0); err == nil {
		t.Error("GetPR accepted number 0")
	}
	if _, err := c.ListPRFiles(ctx, "o/r", -4); err == nil {
		t.Error("ListPRFiles accepted a negative number")
	}
	if _, _, err := c.GetJobLogs(ctx, "bad", 1); !errors.Is(err, github.ErrInvalidRepoName) {
		t.Errorf("GetJobLogs(bad) error = %v", err)
	}
	if _, _, err := c.GetJobLogs(ctx, "o/r", 0); err == nil {
		t.Error("GetJobLogs accepted job 0")
	}
	if _, err := c.GetTarball(ctx, "o/r", "a/../b"); !errors.Is(err, github.ErrInvalidRef) {
		t.Errorf("GetTarball(a/../b) error = %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests were sent for invalid input", n)
	}
}

// bom is the byte order mark that GitHub puts in front of every job log.
var bom = string(rune(0xFEFF))

// blobServer plays the host that serves the log after GitHub's redirect. It answers on "localhost" while
// the API server is on 127.0.0.1, so the redirect goes to another host.
func blobServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return "http://localhost:" + strconv.Itoa(ts.Listener.Addr().(*net.TCPAddr).Port)
}

func TestGetJobLogsFollowsTheRedirectWithoutSendingTheToken(t *testing.T) {
	var blobAuth, blobHits atomic.Value
	blob := blobServer(t, func(w http.ResponseWriter, r *http.Request) {
		blobAuth.Store(r.Header.Get("Authorization"))
		blobHits.Store(r.URL.Path)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(bom + "2026-10-02T12:16:39.4859831Z ##[error]Process completed with exit code 1.\n"))
	})
	var apiPath, apiAuth string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		apiPath, apiAuth = r.URL.Path, r.Header.Get("Authorization")
		http.Redirect(w, r, blob+"/logs/abc?sig=SIGNEDSECRET", http.StatusFound)
	})

	text, truncated, err := c.GetJobLogs(context.Background(), "Jaydee94/remedy", 110833313765)
	if err != nil {
		t.Fatal(err)
	}
	if apiPath != "/repos/Jaydee94/remedy/actions/jobs/110833313765/logs" || apiAuth != "Bearer "+token {
		t.Errorf("API request: path %q, Authorization %q", apiPath, apiAuth)
	}
	if got := blobAuth.Load(); got != "" {
		t.Fatalf("the token was sent to the second host: %v", got)
	}
	if blobHits.Load() != "/logs/abc" {
		t.Errorf("the blob host saw %v", blobHits.Load())
	}
	if truncated || !strings.HasSuffix(text, "exit code 1.\n") || !strings.HasPrefix(text, bom+"2026-10-02") {
		t.Errorf("text = %q, truncated = %v", text, truncated)
	}
}

func TestGetJobLogsNeverLeaksTheSignedURL(t *testing.T) {
	// The blob host is gone: the transport error would normally contain the whole signed URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadPort := dead.Listener.Addr().(*net.TCPAddr).Port
	dead.Close()
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:"+strconv.Itoa(deadPort)+"/logs?sig=SIGNEDSECRET", http.StatusFound)
	})

	_, _, err := c.GetJobLogs(context.Background(), "o/r", 7)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SIGNEDSECRET") || strings.Contains(err.Error(), "sig=") {
		t.Fatalf("the signed URL leaked into the error: %v", err)
	}
}

func TestGetJobLogsBoundsTheDownload(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", github.MaxLogBytes+10))
	})
	text, truncated, err := c.GetJobLogs(context.Background(), "o/r", 7)
	if err != nil || !truncated || len(text) != github.MaxLogBytes {
		t.Fatalf("len = %d, truncated = %v, err = %v", len(text), truncated, err)
	}
}

func TestGetJobLogsMapsErrors(t *testing.T) {
	for status, check := range map[int]func(error) bool{
		http.StatusNotFound: func(err error) bool { return errors.Is(err, github.ErrNotFound) },
		http.StatusGone: func(err error) bool {
			var api *github.APIError
			return errors.As(err, &api) && api.Status == http.StatusGone
		},
		http.StatusUnauthorized: func(err error) bool { return errors.Is(err, github.ErrUnauthorized) },
		http.StatusTooManyRequests: func(err error) bool {
			var rl *github.RateLimitError
			return errors.As(err, &rl)
		},
	} {
		c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
		if _, _, err := c.GetJobLogs(context.Background(), "o/r", 7); err == nil || !check(err) {
			t.Errorf("status %d: error = %v", status, err)
		}
	}
}

func TestGetTarballStreamsTheArchive(t *testing.T) {
	var method, path string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write([]byte("archive bytes"))
	})

	rc, err := c.GetTarball(context.Background(), "Jaydee94/remedy", "913da1edbf28ced7b324b5b99ab3c6c61241acee")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "archive bytes" || method != http.MethodGet ||
		path != "/repos/Jaydee94/remedy/tarball/913da1edbf28ced7b324b5b99ab3c6c61241acee" {
		t.Errorf("body %q, request %s %s", b, method, path)
	}
}

func TestGetTarballReportsErrorsWithoutABody(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	rc, err := c.GetTarball(context.Background(), "o/r", "main")
	if !errors.Is(err, github.ErrNotFound) || rc != nil {
		t.Fatalf("rc = %v, err = %v", rc, err)
	}
}

func TestDownloadErrorsNeverContainTheToken(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"echo ` + token + ` back"}`))
	})
	_, _, err := c.GetJobLogs(context.Background(), "o/r", 7)
	if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("error = %v", err)
	}
}
