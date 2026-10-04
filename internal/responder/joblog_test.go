package responder_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestJobLogReturnsTheLogOfTheCheckBehindTheIncident(t *testing.T) {
	e := newEnv(t)
	in := e.incident("pr:20", "web", sha, "failure")

	log, note, err := e.r.JobLog(context.Background(), in.ID)
	if err != nil || note != "" {
		t.Fatalf("JobLog = %q, %q, %v", log, note, err)
	}
	if !strings.Contains(log, "npm error Invalid: lock file's typescript@6.0.3") {
		t.Fatalf("log = %q", log)
	}
	calls := strings.Join(e.src.callList(), "\n")
	if !strings.Contains(calls, "ListCheckRuns octo/hello "+sha) || !strings.Contains(calls, "GetJobLogs octo/hello 110833313765") {
		t.Fatalf("GitHub calls:\n%s\nwant the check runs of the commit and the log of the web job", calls)
	}
}

func TestJobLogSaysWhyThereIsNoLog(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(e *env)
		note  string
	}{
		"expired":            {func(e *env) { e.src.logErr = &github.APIError{Status: 410, Message: "Gone"} }, "expired"},
		"not an Actions job": {func(e *env) { e.src.logErr = github.ErrNotFound }, "not a GitHub Actions job"},
		"check gone":         {func(e *env) { e.src.checks = e.src.checks[:1] }, "no check run named"},
		"cut off":            {func(e *env) { e.src.logTruncated = true }, "cut off"},
		"rejected token":     {func(e *env) { e.src.logErr = github.ErrUnauthorized }, "not connected"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			tc.setup(e)
			in := e.incident("pr:20", "web", sha, "failure")
			_, note, err := e.r.JobLog(context.Background(), in.ID)
			if err != nil || !strings.Contains(note, tc.note) {
				t.Fatalf("note = %q, err = %v, want a note with %q", note, err, tc.note)
			}
		})
	}
}

func TestJobLogWithoutAUsableConnectionReadsNothing(t *testing.T) {
	e := newEnvWithKeys(t, testKey(t, 1), testKey(t, 2)) // the stored token does not open
	in := e.incident("pr:20", "web", sha, "failure")
	log, note, err := e.r.JobLog(context.Background(), in.ID)
	if err != nil || log != "" || !strings.Contains(note, "not connected") {
		t.Fatalf("JobLog = %q, %q, %v", log, note, err)
	}
	if n := len(e.src.callList()); n != 0 {
		t.Fatalf("%d GitHub calls without a usable token", n)
	}
}

func TestJobLogErrors(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.r.JobLog(context.Background(), 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown incident: %v, want ErrNotFound", err)
	}
	e.src.checksErr = errors.New("boom")
	in := e.incident("pr:20", "web", sha, "failure")
	if _, _, err := e.r.JobLog(context.Background(), in.ID); !errors.Is(err, responder.ErrGitHub) {
		t.Fatalf("a failed GitHub read: %v, want ErrGitHub", err)
	}
}
