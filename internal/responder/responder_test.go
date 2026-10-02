package responder_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	token = "ghp_RESPONDERTOKEN0123456789abcdefghijkl"
	sha   = "913da1edbf28ced7b324b5b99ab3c6c61241acee"

	goodDiagnosis = `{"summary":"npm ci fails because the lock file is stale","cause":"package.json wants typescript 7.0.2, the lock file pins 6.0.3.","confidence":"high","category":"dependency_update","affected_files":["web/package.json","web/package-lock.json"],"proposed_fix":"Run npm install in web/ and commit the lock file.","fix_looks_automatable":true}`
)

var bom = string(rune(0xFEFF))

// fakeSource is GitHub as the responder sees it. It records every call.
type fakeSource struct {
	mu           sync.Mutex
	calls        []string
	pr           github.PullRequest
	prErr        error
	files        []github.PRFile
	checks       []github.CheckRun
	checksErr    error
	log          string
	logTruncated bool
	logErr       error
	tarball      []byte
	tarErr       error
}

func (f *fakeSource) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeSource) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeSource) GetPR(_ context.Context, repo string, n int) (github.PullRequest, error) {
	f.record("GetPR %s %d", repo, n)
	return f.pr, f.prErr
}

func (f *fakeSource) ListPRFiles(_ context.Context, repo string, n int) ([]github.PRFile, error) {
	f.record("ListPRFiles %s %d", repo, n)
	return f.files, nil
}

func (f *fakeSource) ListCheckRuns(_ context.Context, repo, ref string) ([]github.CheckRun, error) {
	f.record("ListCheckRuns %s %s", repo, ref)
	return f.checks, f.checksErr
}

func (f *fakeSource) GetJobLogs(_ context.Context, repo string, id int64) (string, bool, error) {
	f.record("GetJobLogs %s %d", repo, id)
	return f.log, f.logTruncated, f.logErr
}

func (f *fakeSource) GetTarball(_ context.Context, repo, ref string) (io.ReadCloser, error) {
	f.record("GetTarball %s %s", repo, ref)
	if f.tarErr != nil {
		return nil, f.tarErr
	}
	return io.NopCloser(bytes.NewReader(f.tarball)), nil
}

type env struct {
	t      *testing.T
	st     *store.Store
	r      *responder.Responder
	src    *fakeSource
	repo   store.Repo
	now    time.Time
	tokens []string
}

func testKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newEnv(t *testing.T) *env { return newEnvWithKeys(t, testKey(t, 1), testKey(t, 1)) }

func newEnvWithKeys(t *testing.T, sealKey, openKey secret.Key) *env {
	t.Helper()
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
		TokenCiphertext: sealed, TokenHint: "ijkl", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}

	pr := github.PullRequest{Number: 20, Title: "chore(deps): update dependency typescript to v7", Body: "This PR contains the following updates:"}
	pr.User.Login, pr.User.Type = "renovate[bot]", "Bot"
	pr.Head.SHA = sha
	e := &env{
		t: t, st: st, repo: repo, now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		src: &fakeSource{
			pr:    pr,
			files: []github.PRFile{{Filename: "web/package.json", Status: "modified", Additions: 1, Deletions: 1, Patch: "@@ -21,7 +21,7 @@\n-    \"typescript\": \"~6.0.2\",\n+    \"typescript\": \"~7.0.0\","}},
			checks: []github.CheckRun{
				{ID: 110833313460, Name: "go", Status: "completed", Conclusion: "success", HeadSHA: sha},
				{ID: 110833313765, Name: "web", Status: "completed", Conclusion: "failure", HeadSHA: sha},
			},
			log: bom + "2026-10-02T12:16:39.4400532Z npm error Invalid: lock file's typescript@6.0.3 does not satisfy typescript@7.0.2\n" +
				"2026-10-02T12:16:39.4859831Z ##[error]Process completed with exit code 1.\n2026-10-02T12:16:39.5007256Z Post job cleanup.\n",
			tarball: []byte("tarball bytes"),
		},
	}
	e.r = &responder.Responder{
		Store: st, Key: openKey, Provider: "claude", Limits: store.DefaultLimits(),
		NewSource: func(tok secret.Value) responder.Source {
			e.tokens = append(e.tokens, tok.Reveal())
			return e.src
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return e.now },
	}
	return e
}

func (e *env) incident(ref, check, headSHA, conclusion string) store.Incident {
	e.t.Helper()
	in, err := e.st.OpenIncident(context.Background(), store.NewIncident{
		RepoID: e.repo.ID, Ref: ref, RefURL: "https://github.com/octo/hello/pull/20", CheckName: check,
		Conclusion: conclusion, HeadSHA: headSHA, CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: e.repo.ID, Summary: "opened"})
	if err != nil {
		e.t.Fatal(err)
	}
	return in
}

// finish claims the run, applies CheckOutcome like the server does, finishes it and completes the diagnosis.
func (e *env) finish(r run.Run, o run.Outcome) {
	e.t.Helper()
	ctx := context.Background()
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID {
		e.t.Fatalf("ClaimNext = %+v, %v, want %s", claimed, err, r.ID)
	}
	if err := e.st.FinishRun(ctx, r.ID, responder.CheckOutcome(o)); err != nil {
		e.t.Fatal(err)
	}
	e.r.Complete(ctx, r.ID)
}

func (e *env) get(in store.Incident) store.Incident {
	e.t.Helper()
	got, err := e.st.GetIncident(context.Background(), in.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func (e *env) kinds(in store.Incident) []string {
	e.t.Helper()
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID})
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Kind)
	}
	return out
}

func TestStartBuildsThePromptFromGitHubAndCreatesTheRun(t *testing.T) {
	e := newEnv(t)
	in := e.incident("pr:20", "web", sha, "failure")

	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleResponder || r.Status != run.Queued || r.HeadSHA != sha || r.Automatic || r.Provider != "claude" {
		t.Fatalf("run = %+v", r)
	}
	for _, want := range []string{
		"chore(deps): update dependency typescript to v7",
		"This PR contains the following updates:",
		"modified web/package.json (+1 -1)",
		"npm error Invalid: lock file's typescript@6.0.3 does not satisfy typescript@7.0.2",
		"##[error]Process completed with exit code 1.",
		"Failing commit: " + sha,
		"pull request #20",
	} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(r.Prompt, "Post job cleanup") || strings.Contains(r.Prompt, bom) {
		t.Error("log noise reached the prompt")
	}
	if strings.Contains(r.Prompt, token) {
		t.Fatal("the GitHub token is in the prompt")
	}

	want := []string{"GetPR octo/hello 20", "ListPRFiles octo/hello 20", "ListCheckRuns octo/hello " + sha, "GetJobLogs octo/hello 110833313765"}
	if got := e.src.callList(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("GitHub calls = %v, want %v", got, want)
	}
	if len(e.tokens) != 1 || e.tokens[0] != token {
		t.Fatalf("the source was built with %v, want the opened token", e.tokens)
	}
	if got := e.get(in); got.State != store.IncDiagnosing || got.RunID != r.ID {
		t.Fatalf("incident = %+v", got)
	}
}

func TestStartForADefaultBranchSkipsThePullRequest(t *testing.T) {
	e := newEnv(t)
	in := e.incident("branch:main", "web", sha, "timed_out")
	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Prompt, "pull_request_title") || !strings.Contains(r.Prompt, "the default branch") {
		t.Fatalf("prompt:\n%s", r.Prompt)
	}
	for _, c := range e.src.callList() {
		if strings.HasPrefix(c, "GetPR") || strings.HasPrefix(c, "ListPRFiles") {
			t.Errorf("a default-branch incident read a pull request: %s", c)
		}
	}
}

func TestStartUsesTheCheckOutputWhenThereIsNoActionsLog(t *testing.T) {
	e := newEnv(t)
	e.src.logErr = github.ErrNotFound
	e.src.checks[1].Output.Title = "3 problems"
	e.src.checks[1].Output.Text = "file.go:12: unused variable x"
	in := e.incident("pr:20", "web", sha, "failure")

	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Prompt, "file.go:12: unused variable x") || !strings.Contains(r.Prompt, "not a GitHub Actions job") ||
		strings.Contains(r.Prompt, "job_log>>>") {
		t.Fatalf("prompt:\n%s", r.Prompt)
	}
}

func TestStartSaysSoWhenTheLogHasExpired(t *testing.T) {
	e := newEnv(t)
	e.src.logErr = &github.APIError{Status: 410, Message: "Gone"}
	in := e.incident("pr:20", "web", sha, "failure")
	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil || !strings.Contains(r.Prompt, "the log has expired") {
		t.Fatalf("err = %v, prompt:\n%s", err, r.Prompt)
	}
}

func TestStartSaysSoWhenTheCheckRunIsGone(t *testing.T) {
	e := newEnv(t)
	e.src.checks = e.src.checks[:1] // only "go" exists at this commit any more
	in := e.incident("pr:20", "web", sha, "failure")
	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil || !strings.Contains(r.Prompt, "no check run named") {
		t.Fatalf("err = %v, prompt:\n%s", err, r.Prompt)
	}
}

func TestStartNotesATruncatedLog(t *testing.T) {
	e := newEnv(t)
	e.src.logTruncated = true
	in := e.incident("pr:20", "web", sha, "failure")
	r, err := e.r.Start(context.Background(), in.ID)
	if err != nil || !strings.Contains(r.Prompt, "Job log: ") || !strings.Contains(r.Prompt, "cut off") || !strings.Contains(r.Prompt, "job_log>>>") {
		t.Fatalf("err = %v, prompt:\n%s", err, r.Prompt)
	}
}

func TestStartRefusesWhenGitHubCannotBeRead(t *testing.T) {
	for name, mutate := range map[string]func(*fakeSource){
		"the pull request": func(f *fakeSource) { f.prErr = errors.New("boom") },
		"the check runs":   func(f *fakeSource) { f.checksErr = errors.New("boom") },
		"the log":          func(f *fakeSource) { f.logErr = &github.RateLimitError{RetryAfter: time.Minute} },
	} {
		e := newEnv(t)
		mutate(e.src)
		in := e.incident("pr:20", "web", sha, "failure")

		_, err := e.r.Start(context.Background(), in.ID)
		if !errors.Is(err, responder.ErrGitHub) {
			t.Errorf("%s: error = %v, want ErrGitHub", name, err)
		}
		if got := e.get(in); got.State != store.IncOpen || got.RunID != "" {
			t.Errorf("%s: incident = %+v, must be untouched", name, got)
		}
		if busy, _ := e.st.HasActiveRun(context.Background()); busy {
			t.Errorf("%s: a run was created", name)
		}
	}
}

func TestStartNeedsAUsableConnection(t *testing.T) {
	t.Run("a token that does not open", func(t *testing.T) {
		e := newEnvWithKeys(t, testKey(t, 1), testKey(t, 2))
		in := e.incident("pr:20", "web", sha, "failure")
		if _, err := e.r.Start(context.Background(), in.ID); !errors.Is(err, responder.ErrNoConnection) {
			t.Fatalf("error = %v, want ErrNoConnection", err)
		}
		if n := len(e.src.callList()); n != 0 {
			t.Fatalf("%d GitHub calls without a usable token", n)
		}
	})
	t.Run("a rejected token", func(t *testing.T) {
		e := newEnv(t)
		in := e.incident("pr:20", "web", sha, "failure")
		_ = e.st.UpdateConnectionStatus(context.Background(), store.ConnError, "rejected", e.now)
		if _, err := e.r.Start(context.Background(), in.ID); !errors.Is(err, responder.ErrNoConnection) {
			t.Fatalf("error = %v, want ErrNoConnection", err)
		}
	})
	t.Run("GitHub rejects the token during the start", func(t *testing.T) {
		e := newEnv(t)
		e.src.prErr = github.ErrUnauthorized
		in := e.incident("pr:20", "web", sha, "failure")
		if _, err := e.r.Start(context.Background(), in.ID); !errors.Is(err, responder.ErrNoConnection) {
			t.Fatalf("error = %v, want ErrNoConnection", err)
		}
	})
}

func TestStartRefusesWhileARunIsActiveWithoutTouchingGitHub(t *testing.T) {
	e := newEnv(t)
	a := e.incident("pr:20", "web", sha, "failure")
	b := e.incident("pr:21", "web", sha, "failure")
	if _, err := e.r.Start(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	calls := len(e.src.callList())

	if _, err := e.r.Start(context.Background(), b.ID); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("error = %v, want ErrBusy", err)
	}
	if got := len(e.src.callList()); got != calls {
		t.Fatalf("a refused start made %d GitHub calls", got-calls)
	}
}

func TestStartRefusesIncidentsThatCannotBeDiagnosed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ignored := e.incident("pr:1", "web", sha, "failure")
	_ = e.st.IgnoreIncident(ctx, ignored.ID, store.NewActivity{Kind: store.KindIncidentIgnored, Summary: "x"})

	if _, err := e.r.Start(ctx, ignored.ID); !errors.Is(err, store.ErrNotDiagnosable) {
		t.Fatalf("an ignored incident: %v", err)
	}
	if _, err := e.r.Start(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown incident: %v", err)
	}
	if n := len(e.src.callList()); n != 0 {
		t.Fatalf("%d GitHub calls for incidents that cannot be diagnosed", n)
	}
}

func TestAManualStartIgnoresTheLimits(t *testing.T) {
	e := newEnv(t)
	e.r.Limits = store.DiagnosisLimits{Cooldown: time.Hour, MaxPerIncident: 1, MaxPerDay: 1}
	in := e.incident("pr:20", "web", sha, "failure")

	first, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.finish(first, run.Outcome{ExitCode: 1})
	again, err := e.r.Start(context.Background(), in.ID)
	if err != nil {
		t.Fatalf("a second manual diagnosis right away: %v", err)
	}
	if again.Automatic {
		t.Fatal("a click started an automatic run")
	}
}

func TestCheckOutcome(t *testing.T) {
	cases := []struct {
		name       string
		in         run.Outcome
		wantReason string
	}{
		{"a valid answer", run.Outcome{Output: []byte(goodDiagnosis)}, ""},
		{"no answer", run.Outcome{}, run.ReasonInvalidOutput},
		{"an answer of null", run.Outcome{Output: []byte("null")}, run.ReasonInvalidOutput},
		{"an invalid answer", run.Outcome{Output: []byte(`{"summary":"x"}`)}, run.ReasonInvalidOutput},
		{"an answer with a wrong enum", run.Outcome{Output: []byte(strings.Replace(goodDiagnosis, `"high"`, `"certain"`, 1))}, run.ReasonInvalidOutput},
		{"a failed run stays failed", run.Outcome{ExitCode: 1, Result: "boom"}, ""},
		{"a timeout stays a timeout", run.Outcome{ExitCode: -1, FailureReason: run.ReasonTimeout}, run.ReasonTimeout},
	}
	for _, tc := range cases {
		got := responder.CheckOutcome(tc.in)
		if got.FailureReason != tc.wantReason {
			t.Errorf("%s: failure reason = %q, want %q", tc.name, got.FailureReason, tc.wantReason)
		}
		if tc.wantReason == run.ReasonInvalidOutput && got.Result == "" {
			t.Errorf("%s: no explanation in the result", tc.name)
		}
	}
	if got := responder.CheckOutcome(run.Outcome{ExitCode: 1, Result: "boom"}); got.Result != "boom" {
		t.Errorf("the result of a failed run was changed to %q", got.Result)
	}
}

func TestCompleteStoresTheValidatedDiagnosis(t *testing.T) {
	e := newEnv(t)
	in := e.incident("pr:20", "web", sha, "failure")
	r, _ := e.r.Start(context.Background(), in.ID)

	e.finish(r, run.Outcome{Output: []byte("  " + goodDiagnosis + "\n")})

	got := e.get(in)
	if got.State != store.IncDiagnosed || got.DiagnosedSHA != sha || got.RunID != r.ID {
		t.Fatalf("incident = %+v", got)
	}
	if !strings.Contains(string(got.Diagnosis), `"fix_looks_automatable":true`) || strings.Contains(string(got.Diagnosis), "\n") {
		t.Fatalf("the stored diagnosis is not the canonical form: %s", got.Diagnosis)
	}
	if kinds := e.kinds(in); strings.Join(kinds, ",") != "incident_opened,diagnosis_started,diagnosis_finished" {
		t.Fatalf("activity = %v", kinds)
	}
	log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID, Limit: 1})
	if !strings.Contains(log[0].Summary, "npm ci fails because the lock file is stale") || !strings.Contains(log[0].Summary, "web on PR #20 in octo/hello") {
		t.Fatalf("summary = %q", log[0].Summary)
	}
}

func TestCompleteReopensTheIncidentWhenTheAnswerIsInvalid(t *testing.T) {
	e := newEnv(t)
	in := e.incident("pr:20", "web", sha, "failure")
	r, _ := e.r.Start(context.Background(), in.ID)

	e.finish(r, run.Outcome{Output: []byte(`{"summary":"only this"}`)})

	got := e.get(in)
	if got.State != store.IncOpen || got.Diagnosis != nil {
		t.Fatalf("incident = %+v", got)
	}
	finished, _ := e.st.GetRun(context.Background(), r.ID)
	if finished.Status != run.Failed || finished.FailureReason != run.ReasonInvalidOutput {
		t.Fatalf("run = %+v", finished)
	}
	if kinds := e.kinds(in); kinds[len(kinds)-1] != store.KindDiagnosisFailed {
		t.Fatalf("activity = %v", kinds)
	}
}

func TestCompleteHandlesFailedAndTimedOutRuns(t *testing.T) {
	for name, o := range map[string]run.Outcome{
		"exit code":   {ExitCode: 2, Result: "boom"},
		"timeout":     {ExitCode: -1, FailureReason: run.ReasonTimeout},
		"no output":   {},
		"null output": {Output: []byte("null")},
	} {
		e := newEnv(t)
		in := e.incident("pr:20", "web", sha, "failure")
		r, _ := e.r.Start(context.Background(), in.ID)
		e.finish(r, o)
		if got := e.get(in); got.State != store.IncOpen {
			t.Errorf("%s: state = %q, want open", name, got.State)
		}
		log, _ := e.st.ListActivity(context.Background(), store.ActivityQuery{IncidentID: in.ID, Limit: 1})
		if log[0].Kind != store.KindDiagnosisFailed || !strings.Contains(log[0].Summary, "web on PR #20 in octo/hello") {
			t.Errorf("%s: activity = %+v", name, log[0])
		}
	}
}

func TestCompleteIgnoresAdhocRunsAndRunsThatAreNotFinished(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	adhoc, _ := e.st.CreateRun(ctx, "claude", "hello")
	e.r.Complete(ctx, adhoc.ID)
	e.r.Complete(ctx, "no-such-run")

	in := e.incident("pr:20", "web", sha, "failure")
	// The ad-hoc run must finish before a diagnosis can start.
	if _, err := e.st.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	_ = e.st.FinishRun(ctx, adhoc.ID, run.Outcome{})
	r, _ := e.r.Start(ctx, in.ID)
	e.r.Complete(ctx, r.ID) // still queued: nothing may happen
	if got := e.get(in); got.State != store.IncDiagnosing {
		t.Fatalf("state = %q: Complete acted on a run that is not finished", got.State)
	}
}

func TestAutoStartStartsTheOldestEligibleIncidentOnlyOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.incident("pr:1", "web", sha, "cancelled")
	first := e.incident("pr:2", "web", sha, "failure")
	e.incident("pr:3", "web", sha, "failure")

	e.r.AutoStart(ctx)
	got := e.get(first)
	if got.State != store.IncDiagnosing {
		t.Fatalf("the oldest eligible incident is %q, want diagnosing", got.State)
	}
	r, _ := e.st.GetRun(ctx, got.RunID)
	if !r.Automatic || got.Diagnoses != 1 {
		t.Fatalf("run %+v, diagnoses %d", r, got.Diagnoses)
	}

	calls := len(e.src.callList())
	e.r.AutoStart(ctx)
	if len(e.src.callList()) != calls {
		t.Fatal("a second automatic start read GitHub while a run is active")
	}
}

func TestAutoStartHonoursTheCooldownAndTheCap(t *testing.T) {
	e := newEnv(t)
	e.r.Limits = store.DiagnosisLimits{Cooldown: 15 * time.Minute, MaxPerIncident: 2, MaxPerDay: 20}
	ctx := context.Background()
	in := e.incident("pr:20", "web", sha, "failure")

	e.r.AutoStart(ctx)
	first, _ := e.st.GetRun(ctx, e.get(in).RunID)
	e.finish(first, run.Outcome{ExitCode: 1})

	e.now = e.now.Add(5 * time.Minute)
	e.r.AutoStart(ctx)
	if got := e.get(in); got.State != store.IncOpen || got.Diagnoses != 1 {
		t.Fatalf("within the cooldown: %+v", got)
	}

	e.now = e.now.Add(15 * time.Minute)
	e.r.AutoStart(ctx)
	second, _ := e.st.GetRun(ctx, e.get(in).RunID)
	if second.ID == first.ID || e.get(in).Diagnoses != 2 {
		t.Fatalf("after the cooldown the incident must be diagnosed again: %+v", e.get(in))
	}
	e.finish(second, run.Outcome{ExitCode: 1})

	e.now = e.now.Add(time.Hour)
	e.r.AutoStart(ctx)
	if got := e.get(in); got.State != store.IncOpen || got.Diagnoses != 2 {
		t.Fatalf("at the cap: %+v", got)
	}
}

func TestAutoStartDoesNothingWhenSwitchedOffOrOverTheDailyLimit(t *testing.T) {
	ctx := context.Background()
	off := newEnv(t)
	off.r.Limits = store.DiagnosisLimits{Cooldown: time.Minute, MaxPerIncident: 3, MaxPerDay: 0}
	in := off.incident("pr:20", "web", sha, "failure")
	off.r.AutoStart(ctx)
	if got := off.get(in); got.State != store.IncOpen || len(off.src.callList()) != 0 {
		t.Fatalf("switched off: %+v, %d GitHub calls", got, len(off.src.callList()))
	}

	e := newEnv(t)
	e.r.Limits = store.DiagnosisLimits{Cooldown: time.Minute, MaxPerIncident: 3, MaxPerDay: 1}
	a := e.incident("pr:1", "web", sha, "failure")
	b := e.incident("pr:2", "web", sha, "failure")
	e.r.AutoStart(ctx)
	ra, _ := e.st.GetRun(ctx, e.get(a).RunID)
	e.finish(ra, run.Outcome{ExitCode: 1})
	e.now = e.now.Add(time.Hour)
	e.r.AutoStart(ctx)
	if got := e.get(b); got.State != store.IncOpen {
		t.Fatalf("the daily limit of 1 was exceeded: %+v", got)
	}
	// A day later the oldest eligible incident would be a again; ignore it so that b is next.
	if err := e.st.IgnoreIncident(ctx, a.ID, store.NewActivity{Kind: store.KindIncidentIgnored, Summary: "x"}); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(24 * time.Hour)
	e.r.AutoStart(ctx)
	if got := e.get(b); got.State != store.IncDiagnosing {
		t.Fatalf("a day later: %+v", got)
	}
}

func TestAutoStartBacksOffAfterAGitHubFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.src.prErr = errors.New("boom")
	in := e.incident("pr:20", "web", sha, "failure")

	e.r.AutoStart(ctx)
	e.r.AutoStart(ctx)
	e.now = e.now.Add(time.Minute)
	e.r.AutoStart(ctx)
	if n := len(e.src.callList()); n != 1 {
		t.Fatalf("%d GitHub calls, want one attempt and then silence until the cooldown has passed", n)
	}
	if got := e.get(in); got.State != store.IncOpen || got.Diagnoses != 0 {
		t.Fatalf("a failed attempt must not count: %+v", got)
	}

	e.src.prErr = nil
	e.now = e.now.Add(16 * time.Minute)
	e.r.AutoStart(ctx)
	if got := e.get(in); got.State != store.IncDiagnosing {
		t.Fatalf("after the back-off: %+v", got)
	}
}

func TestAutoStartDoesNothingWithoutAConnection(t *testing.T) {
	e := newEnvWithKeys(t, testKey(t, 1), testKey(t, 2))
	in := e.incident("pr:20", "web", sha, "failure")
	e.r.AutoStart(context.Background())
	if got := e.get(in); got.State != store.IncOpen || len(e.src.callList()) != 0 {
		t.Fatalf("incident %+v, %d calls", got, len(e.src.callList()))
	}
}

func TestOpenSnapshotReturnsTheTarballOfTheCommitTheRunIsFor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := e.incident("pr:20", "web", sha, "failure")
	r, _ := e.r.Start(ctx, in.ID)

	if _, err := e.r.OpenSnapshot(ctx, r.ID); !errors.Is(err, responder.ErrNotSnapshotRun) {
		t.Fatalf("a queued run: error = %v, want ErrNotSnapshotRun", err)
	}
	if _, err := e.st.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	// A new commit arrives while the run is going: the snapshot must still be the commit of the prompt.
	if err := e.st.RecordRecurrence(ctx, in.ID, "failure", "fffffff", "", store.NewActivity{Kind: store.KindIncidentRecurred, RepoID: e.repo.ID, Summary: "again"}); err != nil {
		t.Fatal(err)
	}

	rc, err := e.r.OpenSnapshot(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "tarball bytes" {
		t.Fatalf("body = %q", b)
	}
	calls := e.src.callList()
	if last := calls[len(calls)-1]; last != "GetTarball octo/hello "+sha {
		t.Fatalf("last call = %q, want the tarball of %s", last, sha)
	}
}

func TestOpenSnapshotRefusesWhatIsNotARunningResponderRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	adhoc, _ := e.st.CreateRun(ctx, "claude", "hello")
	if _, err := e.st.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.OpenSnapshot(ctx, adhoc.ID); !errors.Is(err, responder.ErrNotSnapshotRun) {
		t.Fatalf("an ad-hoc run: error = %v", err)
	}
	if _, err := e.r.OpenSnapshot(ctx, "no-such-run"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown run: error = %v", err)
	}
	_ = e.st.FinishRun(ctx, adhoc.ID, run.Outcome{})

	in := e.incident("pr:20", "web", sha, "failure")
	r, _ := e.r.Start(ctx, in.ID)
	e.finish(r, run.Outcome{Output: []byte(goodDiagnosis)})
	if _, err := e.r.OpenSnapshot(ctx, r.ID); !errors.Is(err, responder.ErrNotSnapshotRun) {
		t.Fatalf("a finished run: error = %v", err)
	}
}

func TestOpenSnapshotReportsGitHubErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := e.incident("pr:20", "web", sha, "failure")
	r, _ := e.r.Start(ctx, in.ID)
	_, _ = e.st.ClaimNext(ctx)

	e.src.tarErr = github.ErrNotFound
	if rc, err := e.r.OpenSnapshot(ctx, r.ID); !errors.Is(err, responder.ErrGitHub) || rc != nil {
		t.Fatalf("rc = %v, err = %v, want ErrGitHub", rc, err)
	}
}
