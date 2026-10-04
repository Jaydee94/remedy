package github_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
)

func loggedClient(t *testing.T, level slog.Level, h http.HandlerFunc) (*github.Client, *bytes.Buffer) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	return github.New(ts.URL, secret.NewValue(token), ts.Client(), github.WithLog(log)), &buf
}

func TestDebugLogShowsEveryRequestWithoutQueryOrToken(t *testing.T) {
	c, buf := loggedClient(t, slog.LevelDebug, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.ListOpenPRs(context.Background(), "o/r"); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{"github request", "method=GET", "path=/repos/o/r/pulls", "status=200", "took="} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"per_page", "state=open", token, "Bearer", "Authorization"} {
		if strings.Contains(out, bad) {
			t.Errorf("the log contains %q:\n%s", bad, out)
		}
	}
}

func TestDebugLogNamesTheHostOfARedirectButNotItsPathOrSignature(t *testing.T) {
	blob := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("##[error]boom\n"))
	})
	c, buf := loggedClient(t, slog.LevelDebug, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blob+"/logs/abc?sig=SIGNEDSECRET", http.StatusFound)
	})
	if _, _, err := c.GetJobLogs(context.Background(), "o/r", 1); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "path=/repos/o/r/actions/jobs/1/logs") || !strings.Contains(out, "status=302") {
		t.Errorf("the API request is missing:\n%s", out)
	}
	if !strings.Contains(out, "host=localhost:") || !strings.Contains(out, "status=200") {
		t.Errorf("the request to the other host is missing:\n%s", out)
	}
	for _, bad := range []string{"SIGNEDSECRET", "/logs/abc", "sig="} {
		if strings.Contains(out, bad) {
			t.Errorf("the log contains %q:\n%s", bad, out)
		}
	}
}

func TestInfoLevelLogsNoRequests(t *testing.T) {
	c, buf := loggedClient(t, slog.LevelInfo, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.ListOpenPRs(context.Background(), "o/r"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("the log at info level is not empty:\n%s", buf.String())
	}
}
