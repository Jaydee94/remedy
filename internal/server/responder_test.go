package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	respToken = "ghp_SERVERRESPONDER0123456789abcdefghijkl"
	respSHA   = "913da1edbf28ced7b324b5b99ab3c6c61241acee"

	goodDiagnosis = `{"summary":"npm ci fails because the lock file is stale","cause":"package.json wants typescript 7.0.2, the lock file pins 6.0.3.","confidence":"high","category":"dependency_update","affected_files":["web/package.json"],"proposed_fix":"Run npm install in web/.","fix_looks_automatable":true}`
)

// fakeRead is GitHub as the responder reads it.
type fakeRead struct {
	prErr   error
	tarball []byte
	tarErr  error
}

func (f *fakeRead) GetPR(context.Context, string, int) (github.PullRequest, error) {
	pr := github.PullRequest{Number: 20, Title: "update typescript", Body: "bump"}
	pr.Head.SHA = respSHA
	return pr, f.prErr
}

func (f *fakeRead) ListPRFiles(context.Context, string, int) ([]github.PRFile, error) {
	return []github.PRFile{{Filename: "web/package.json", Status: "modified", Additions: 1, Deletions: 1}}, nil
}

func (f *fakeRead) ListCheckRuns(context.Context, string, string) ([]github.CheckRun, error) {
	return []github.CheckRun{{ID: 7, Name: "web", Status: "completed", Conclusion: "failure", HeadSHA: respSHA}}, nil
}

func (f *fakeRead) GetJobLogs(context.Context, string, int64) (string, bool, error) {
	return "2026-10-02T12:00:00.1Z npm error boom\n2026-10-02T12:00:01.1Z ##[error]Process completed with exit code 1.\n", false, nil
}

func (f *fakeRead) GetTarball(context.Context, string, string) (io.ReadCloser, error) {
	if f.tarErr != nil {
		return nil, f.tarErr
	}
	return io.NopCloser(bytes.NewReader(f.tarball)), nil
}

type respEnv struct {
	ts     *httptest.Server
	st     *store.Store
	src    *fakeRead
	repo   store.Repo
	client *http.Client
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "Octo-hello-913da1e/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: "Octo-hello-913da1e/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = io.WriteString(tw, body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func newRespEnv(t *testing.T, openKey secret.Key) *respEnv {
	t.Helper()
	sealKey, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sealed, _ := sealKey.Seal([]byte(respToken), store.ConnectionAAD())
	if err := st.SaveConnection(ctx, store.Connection{TokenCiphertext: sealed, TokenHint: "ijkl", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}

	src := &fakeRead{tarball: tarball(t, map[string]string{"main.go": "package main\n", ".env": "TOKEN=x", "web/package.json": "{}"})}
	resp := &responder.Responder{
		Store: st, Key: openKey, Limits: store.DefaultLimits(),
		NewSource: func(secret.Value) responder.Source { return src },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Key: sealKey,
		Incidents: &incident.Engine{Store: st}, Responder: resp, PollInterval: 90 * time.Second,
	}))
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	e := &respEnv{ts: ts, st: st, src: src, repo: repo, client: &http.Client{Jar: jar}}
	if code, _ := e.admin(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	return e
}

func (e *respEnv) admin(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *respEnv) runner(t *testing.T, method, path, body, token string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (e *respEnv) incident(t *testing.T, ref, conclusion string) store.Incident {
	t.Helper()
	in, err := e.st.OpenIncident(context.Background(), store.NewIncident{
		RepoID: e.repo.ID, Ref: ref, CheckName: "web", Conclusion: conclusion, HeadSHA: respSHA,
		RefURL: "https://github.com/octo/hello/pull/20", CheckURL: "https://github.com/octo/hello/runs/7",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: e.repo.ID, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (e *respEnv) diagnose(t *testing.T, in store.Incident) (int, string) {
	t.Helper()
	return e.admin(t, http.MethodPost, "/api/incidents/"+strconv.FormatInt(in.ID, 10)+"/diagnose", "")
}

// startRun diagnoses the incident and claims the run, so that it is running.
func (e *respEnv) startRun(t *testing.T, in store.Incident) string {
	t.Helper()
	code, body := e.diagnose(t, in)
	if code != http.StatusAccepted {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	var out struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	if claimed, err := e.st.ClaimNext(context.Background()); err != nil || claimed == nil || claimed.ID != out.RunID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return out.RunID
}

func goodKey(t *testing.T) secret.Key {
	t.Helper()
	k, _ := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	return k
}

func TestDiagnoseStartsAResponderRun(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")

	code, body := e.diagnose(t, in)
	if code != http.StatusAccepted {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	runID, _ := field(t, body, "runId").(string)
	r, err := e.st.GetRun(context.Background(), runID)
	if err != nil || r.Role != run.RoleResponder || r.Status != run.Queued || r.Automatic || r.HeadSHA != respSHA ||
		!strings.Contains(r.Prompt, "update typescript") || strings.Contains(r.Prompt, respToken) {
		t.Fatalf("run = %+v, %v", r, err)
	}

	code, body = e.admin(t, http.MethodGet, "/api/incidents/"+strconv.FormatInt(in.ID, 10), "")
	if code != http.StatusOK {
		t.Fatalf("GET incident = %d", code)
	}
	var got struct {
		Incident struct {
			State string `json:"state"`
			RunID string `json:"runId"`
		} `json:"incident"`
		Activity []struct{ Kind string } `json:"activity"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	if got.Incident.State != "diagnosing" || got.Incident.RunID != runID || got.Activity[0].Kind != "diagnosis_started" {
		t.Fatalf("incident = %+v", got)
	}
}

func TestDiagnoseRefusesWhatCannotBeDiagnosed(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	ignored := e.incident(t, "pr:1", "failure")
	if code, _ := e.admin(t, http.MethodPost, "/api/incidents/"+strconv.FormatInt(ignored.ID, 10)+"/ignore", ""); code != http.StatusOK {
		t.Fatalf("ignore = %d", code)
	}
	if code, _ := e.diagnose(t, ignored); code != http.StatusConflict {
		t.Errorf("an ignored incident: %d, want 409", code)
	}
	if code, _ := e.admin(t, http.MethodPost, "/api/incidents/9999/diagnose", ""); code != http.StatusNotFound {
		t.Errorf("an unknown incident: %d, want 404", code)
	}
	if code, _ := e.admin(t, http.MethodPost, "/api/incidents/abc/diagnose", ""); code != http.StatusNotFound {
		t.Errorf("a bad id: %d, want 404", code)
	}

	first := e.incident(t, "pr:2", "failure")
	if code, _ := e.diagnose(t, first); code != http.StatusAccepted {
		t.Fatalf("first diagnose = %d", code)
	}
	if code, _ := e.diagnose(t, first); code != http.StatusConflict {
		t.Errorf("an incident that is being diagnosed: %d, want 409", code)
	}
	other := e.incident(t, "pr:3", "failure")
	code, body := e.diagnose(t, other)
	if code != http.StatusConflict || !strings.Contains(body, "another run") {
		t.Errorf("while another run is active: %d %s, want 409 and a hint", code, body)
	}
}

func TestDiagnoseReportsGitHubProblemsWithoutDetails(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	e.src.prErr = errors.New("secret internal detail " + respToken)
	in := e.incident(t, "pr:20", "failure")

	code, body := e.diagnose(t, in)
	if code != http.StatusBadGateway || strings.Contains(body, "secret internal detail") || strings.Contains(body, respToken) {
		t.Fatalf("diagnose = %d %s", code, body)
	}
	if got, _ := e.st.GetIncident(context.Background(), in.ID); got.State != store.IncOpen {
		t.Fatalf("the incident is %q after a failed start", got.State)
	}
}

func TestDiagnoseNeedsAUsableToken(t *testing.T) {
	other, _ := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)))
	e := newRespEnv(t, other)
	in := e.incident(t, "pr:20", "failure")
	if code, body := e.diagnose(t, in); code != http.StatusConflict || !strings.Contains(body, "GitHub") {
		t.Fatalf("diagnose = %d %s", code, body)
	}
}

func TestDiagnoseNeedsASessionAndTheCSRFHeader(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	path := e.ts.URL + "/api/incidents/" + strconv.FormatInt(in.ID, 10) + "/diagnose"

	resp, _ := http.Post(path, "application/json", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a session: %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, path, nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("without X-Remedy-CSRF: %d, want 403", resp.StatusCode)
	}
	if got, _ := e.st.GetIncident(context.Background(), in.ID); got.State != store.IncOpen {
		t.Fatalf("a refused request changed the incident to %q", got.State)
	}
}

func TestTheClaimOfAResponderRunCarriesTheSchemaAndTheSnapshotFlag(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	if code, _ := e.diagnose(t, in); code != http.StatusAccepted {
		t.Fatal("diagnose failed")
	}

	resp, body := e.runner(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d", resp.StatusCode)
	}
	var claim run.Claim
	if err := json.Unmarshal(body, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Role != run.RoleResponder || !claim.Snapshot || claim.Status != run.Running {
		t.Fatalf("claim = %+v", claim)
	}
	var got, want any
	if err := json.Unmarshal(claim.Schema, &got); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	_ = json.Unmarshal([]byte(diagnosis.Schema), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema = %s", claim.Schema)
	}
}

func TestTheClaimOfAnAdhocRunHasNoSchemaAndNoSnapshot(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	if _, err := e.st.CreateRun(context.Background(), "claude", "hello"); err != nil {
		t.Fatal(err)
	}
	_, body := e.runner(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	if _, ok := raw["schema"]; ok || raw["snapshot"] != nil || raw["role"] != "adhoc" {
		t.Fatalf("claim = %s", body)
	}
}

func TestTheSnapshotIsFilteredOnItsWayToTheRunner(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	runID := e.startRun(t, in)

	resp, body := e.runner(t, http.MethodGet, "/runner/v1/runs/"+runID+"/snapshot", "", runnerToken)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("snapshot = %d, %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tr := tar.NewReader(zr); ; {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "web/package.json") || strings.Contains(joined, ".env") {
		t.Fatalf("entries = %v, want the tree without the secret file", names)
	}
}

func TestTheSnapshotEndpointIsForTheRunnerAndForRunningResponderRunsOnly(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	if code, _ := e.diagnose(t, in); code != http.StatusAccepted {
		t.Fatal("diagnose failed")
	}
	queued, _ := e.st.GetIncident(context.Background(), in.ID)

	path := "/runner/v1/runs/" + queued.RunID + "/snapshot"
	for name, token := range map[string]string{"no token": "", "wrong token": "not-the-runner-token-at-all-0123456789"} {
		if resp, _ := e.runner(t, http.MethodGet, path, "", token); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, resp.StatusCode)
		}
	}
	if resp, _ := e.runner(t, http.MethodGet, path, "", runnerToken); resp.StatusCode != http.StatusConflict {
		t.Errorf("a run that is still queued: %d, want 409", resp.StatusCode)
	}
	if resp, _ := e.runner(t, http.MethodGet, "/runner/v1/runs/nope/snapshot", "", runnerToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown run: %d, want 404", resp.StatusCode)
	}

	// An ad-hoc run that is running has no snapshot either.
	_, _ = e.st.ClaimNext(context.Background())
	_ = e.st.FinishRun(context.Background(), queued.RunID, run.Outcome{})
	adhoc, _ := e.st.CreateRun(context.Background(), "claude", "hello")
	_, _ = e.st.ClaimNext(context.Background())
	if resp, _ := e.runner(t, http.MethodGet, "/runner/v1/runs/"+adhoc.ID+"/snapshot", "", runnerToken); resp.StatusCode != http.StatusConflict {
		t.Errorf("an ad-hoc run: %d, want 409", resp.StatusCode)
	}
}

func TestTheSnapshotEndpointReportsGitHubProblemsAndBadArchives(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	runID := e.startRun(t, in)
	path := "/runner/v1/runs/" + runID + "/snapshot"

	e.src.tarErr = github.ErrNotFound
	if resp, body := e.runner(t, http.MethodGet, path, "", runnerToken); resp.StatusCode != http.StatusBadGateway || strings.Contains(string(body), "ghp_") {
		t.Fatalf("a GitHub error: %d %s", resp.StatusCode, body)
	}

	// An archive that is not one: the filter fails after the headers, so the connection is aborted.
	e.src.tarErr, e.src.tarball = nil, []byte("this is not a gzip stream")
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("the runner got no error for an archive the filter rejected")
	}
}

func (e *respEnv) finish(t *testing.T, runID, body string) int {
	t.Helper()
	resp, _ := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/finish", body, runnerToken)
	return resp.StatusCode
}

func (e *respEnv) detail(t *testing.T, in store.Incident) map[string]any {
	t.Helper()
	code, body := e.admin(t, http.MethodGet, "/api/incidents/"+strconv.FormatInt(in.ID, 10), "")
	if code != http.StatusOK {
		t.Fatalf("GET incident = %d", code)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	return out
}

func TestFinishWithAValidAnswerStoresTheDiagnosis(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	runID := e.startRun(t, in)

	if code := e.finish(t, runID, `{"exitCode":0,"result":"ok","output":`+goodDiagnosis+`}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d", code)
	}
	r, _ := e.st.GetRun(context.Background(), runID)
	if r.Status != run.Succeeded || r.FailureReason != "" {
		t.Fatalf("run = %+v", r)
	}
	d := e.detail(t, in)
	inc := d["incident"].(map[string]any)
	diag, _ := inc["diagnosis"].(map[string]any)
	if inc["state"] != "diagnosed" || inc["diagnosedSha"] != respSHA || inc["runId"] != runID ||
		diag["confidence"] != "high" || diag["category"] != "dependency_update" || diag["fix_looks_automatable"] != true {
		t.Fatalf("incident = %v", inc)
	}
	acts := d["activity"].([]any)
	if first := acts[0].(map[string]any); first["kind"] != "diagnosis_finished" ||
		!strings.Contains(first["summary"].(string), "npm ci fails because the lock file is stale") {
		t.Fatalf("activity = %v", acts)
	}
}

func TestFinishWithAnInvalidOrMissingAnswerFailsTheRun(t *testing.T) {
	for name, body := range map[string]string{
		"missing members":       `{"exitCode":0,"result":"ok","output":{"summary":"only this"}}`,
		"no output":             `{"exitCode":0,"result":"ok"}`,
		"output is null":        `{"exitCode":0,"result":"ok","output":null}`,
		"a wrong enum":          `{"exitCode":0,"result":"ok","output":` + strings.Replace(goodDiagnosis, `"high"`, `"certain"`, 1) + `}`,
		"an extra member":       `{"exitCode":0,"result":"ok","output":` + strings.Replace(goodDiagnosis, `"summary"`, `"surprise":1,"summary"`, 1) + `}`,
		"a text, not an object": `{"exitCode":0,"result":"ok","output":"it is the lock file"}`,
	} {
		e := newRespEnv(t, goodKey(t))
		in := e.incident(t, "pr:20", "failure")
		runID := e.startRun(t, in)

		if code := e.finish(t, runID, body); code != http.StatusNoContent {
			t.Fatalf("%s: finish = %d", name, code)
		}
		r, _ := e.st.GetRun(context.Background(), runID)
		if r.Status != run.Failed || r.FailureReason != run.ReasonInvalidOutput || r.Result == "" {
			t.Errorf("%s: run = %+v", name, r)
		}
		inc := e.detail(t, in)["incident"].(map[string]any)
		if inc["state"] != "open" || inc["diagnosis"] != nil {
			t.Errorf("%s: incident = %v", name, inc)
		}
	}
}

func TestFinishWithAFailedOrTimedOutRunReopensTheIncident(t *testing.T) {
	for name, body := range map[string]string{
		"exit code": `{"exitCode":2,"result":"boom"}`,
		"timeout":   `{"exitCode":-1,"result":"stopped","failureReason":"timeout"}`,
	} {
		e := newRespEnv(t, goodKey(t))
		in := e.incident(t, "pr:20", "failure")
		runID := e.startRun(t, in)
		if code := e.finish(t, runID, body); code != http.StatusNoContent {
			t.Fatalf("%s: finish = %d", name, code)
		}
		d := e.detail(t, in)
		if d["incident"].(map[string]any)["state"] != "open" || d["activity"].([]any)[0].(map[string]any)["kind"] != "diagnosis_failed" {
			t.Errorf("%s: %v", name, d)
		}
	}
}

func TestARunnerCannotReportTheInvalidOutputReasonItself(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	in := e.incident(t, "pr:20", "failure")
	runID := e.startRun(t, in)
	if code := e.finish(t, runID, `{"exitCode":0,"output":`+goodDiagnosis+`,"failureReason":"invalid_output"}`); code != http.StatusBadRequest {
		t.Fatalf("finish = %d, want 400", code)
	}
}

func TestFinishOfAnAdhocRunIsNotJudged(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	adhoc, _ := e.st.CreateRun(context.Background(), "claude", "hello")
	_, _ = e.st.ClaimNext(context.Background())
	if code := e.finish(t, adhoc.ID, `{"exitCode":0,"result":"ok","output":{"anything":1}}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d", code)
	}
	r, _ := e.st.GetRun(context.Background(), adhoc.ID)
	if r.Status != run.Succeeded || r.FailureReason != "" || string(r.Output) != `{"anything":1}` {
		t.Fatalf("run = %+v", r)
	}
}

func TestTheLimitsEndpointShowsTheConfiguration(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	code, body := e.admin(t, http.MethodGet, "/api/limits", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/limits = %d", code)
	}
	var got map[string]float64
	_ = json.Unmarshal([]byte(body), &got)
	want := map[string]float64{
		"pollIntervalSeconds": 90, "diagnoseCooldownSeconds": 900, "diagnoseMaxPerIncident": 3, "diagnoseMaxPerDay": 20, "staleRunMinutes": 15, "diagnosesLast24h": 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("limits = %v, want %v", got, want)
	}
	resp, _ := http.Get(e.ts.URL + "/api/limits")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a session: %d, want 401", resp.StatusCode)
	}
}

func TestTheLimitsEndpointCountsTheAutomaticDiagnosesOfTheLastDay(t *testing.T) {
	e := newRespEnv(t, goodKey(t))
	ctx := context.Background()
	now := time.Now()
	var last store.Incident
	// Two automatic diagnoses inside the last 24 hours and one outside.
	for i, at := range []time.Time{now.Add(-time.Hour), now.Add(-2 * time.Hour), now.Add(-25 * time.Hour)} {
		in, err := e.st.OpenIncident(ctx, store.NewIncident{
			RepoID: e.repo.ID, Ref: "pr:" + strconv.Itoa(i+1), CheckName: "go", Conclusion: "failure", HeadSHA: "abc1234",
		}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: e.repo.ID, Summary: "go failed"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := e.st.StartDiagnosis(ctx, store.StartParams{
			IncidentID: in.ID, Provider: "claude", Prompt: "p", HeadSHA: in.HeadSHA, Automatic: true, Limits: store.DefaultLimits(), Now: at,
		}, store.NewActivity{Kind: store.KindDiagnosisStarted, RepoID: in.RepoID, Summary: "started"})
		if err != nil {
			t.Fatal(err)
		}
		// The runner is sequential: let the run end, so that the next diagnosis may start.
		claimed, err := e.st.ClaimNext(ctx)
		if err != nil || claimed == nil || claimed.ID != r.ID {
			t.Fatalf("ClaimNext = %+v, %v, want run %s", claimed, err, r.ID)
		}
		if err := e.st.FinishRun(ctx, r.ID, run.Outcome{ExitCode: 0}); err != nil {
			t.Fatal(err)
		}
		last = in
	}
	// A question about an incident is not a diagnosis and does not count.
	if _, err := e.st.CreateQuestionRun(ctx, "claude", "why?", last.ID, false); err != nil {
		t.Fatal(err)
	}

	code, body := e.admin(t, http.MethodGet, "/api/limits", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/limits = %d", code)
	}
	if got := field(t, body, "diagnosesLast24h"); got != float64(2) {
		t.Fatalf("diagnosesLast24h = %v in %s, want 2", got, body)
	}
}

func TestWithoutAResponderThereAreNoDiagnosisRoutes(t *testing.T) {
	e := newGHEnv(t, nil, ghKey(t, 1))
	if code, _ := e.call(t, http.MethodPost, "/api/incidents/1/diagnose", ""); code != http.StatusNotFound {
		t.Errorf("diagnose: %d, want 404", code)
	}
	if code, _ := e.call(t, http.MethodGet, "/api/limits", ""); code != http.StatusNotFound {
		t.Errorf("limits: %d, want 404", code)
	}
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/runner/v1/runs/x/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("snapshot: %d, want 404", resp.StatusCode)
	}
}
