package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/learner"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
	"github.com/Jaydee94/remedy/internal/store"
)

const learnAnswer = `{"profile":{"os":"Debian 12","role":"NAS","services":["docker"],"containers":["remedy-drill"],"notes":""},` +
	`"skills":[{"name":"drill","description":"The drill container is safe to restart.","body":"remedy-drill restarts cleanly.","evidence":[["docker","ps","-a"],["uname","-a"]]}]}`

type learnEnv struct {
	*hostEnv
	st *store.Store
}

func newLearnEnv(t *testing.T) *learnEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	he := newHostEnv(t, st, dbPath, secret.Key{}, sshtest.Start(t, sshtest.Config{}), func(d *server.Deps) {
		d.Learner = &learner.Learner{Store: st}
	})
	return &learnEnv{hostEnv: he, st: st}
}

// readyHost makes a host a learning run may use: a login, a pinned key and a successful test.
func (e *learnEnv) readyHost(t *testing.T, name string) store.Host {
	t.Helper()
	ctx := context.Background()
	h, err := e.st.CreateHost(ctx, store.HostFields{Name: name, Address: "10.0.0.5", Port: 22, Account: "remedy", DockerPattern: "remedy-*", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const line = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOhostkeyhostkeyhostkeyhostkeyhostkeyhostkeyhost"
	if err := e.st.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{1}, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.PinHostKey(ctx, h.ID, line, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SaveHostTest(ctx, h.ID, line, store.HostOK, "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	return h
}

func (e *learnEnv) learn(t *testing.T, id int64) (int, string) {
	t.Helper()
	return e.do(t, http.MethodPost, "/api/hosts/"+itoa(id)+"/learn", "")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestLearnNeedsASessionAndTheCSRFHeader(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/hosts/"+itoa(h.ID)+"/learn", nil)
	req.Header.Set("X-Remedy-CSRF", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a session = %d, want 401", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, e.ts.URL+"/api/hosts/"+itoa(h.ID)+"/learn", nil)
	resp, err = e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("without the CSRF header = %d, want 403", resp.StatusCode)
	}
	if list, _ := e.st.ListRuns(context.Background(), 5); len(list) != 0 {
		t.Errorf("%d runs were created by refused requests", len(list))
	}
}

func TestLearnStartsOneRunForTheHostAndAnswers202(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	code, body := e.learn(t, h.ID)
	if code != http.StatusAccepted {
		t.Fatalf("learn = %d %s", code, body)
	}
	var started struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
		t.Fatalf("body = %s", body)
	}
	r, err := e.st.GetRun(context.Background(), started.RunID)
	if err != nil || r.Role != run.RoleLearner || !r.MCP || r.Cluster || r.Automatic || r.HostCount != 1 {
		t.Fatalf("run = %+v, %v", r, err)
	}
}

func TestLearnRefusesWithAFixedSentence(t *testing.T) {
	e := newLearnEnv(t)
	ctx := context.Background()
	h := e.readyHost(t, "nas")
	bare, err := e.st.CreateHost(ctx, store.HostFields{Name: "bare", Address: "10.0.0.6", Port: 22, Account: "remedy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	if code, _ := e.learn(t, 999); code != http.StatusNotFound {
		t.Errorf("an unknown host = %d, want 404", code)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/hosts/x/learn", ""); code != http.StatusNotFound {
		t.Errorf("a host id that is not a number = %d, want 404", code)
	}
	if code, body := e.learn(t, bare.ID); code != http.StatusConflict || !strings.Contains(body, "the host bare is not ready: it has no login") {
		t.Errorf("a host without a login = %d %s", code, body)
	}
	if code, body := e.learn(t, h.ID); code != http.StatusAccepted {
		t.Fatalf("learn = %d %s", code, body)
	}
	if code, body := e.learn(t, h.ID); code != http.StatusConflict || !strings.Contains(body, "a learning run for this host is already queued or running") {
		t.Errorf("a second run for the host = %d %s", code, body)
	}
	other := e.readyHost(t, "alpha")
	if code, body := e.learn(t, other.ID); code != http.StatusConflict || !strings.Contains(body, "another run is queued or running") {
		t.Errorf("a run for another host while one is queued = %d %s", code, body)
	}
	if list, _ := e.st.ListRuns(ctx, 10); len(list) != 1 {
		t.Errorf("%d runs after the refusals, want the one that started", len(list))
	}
}

func TestLearnIsRefusedWhenTooManyProposalsWait(t *testing.T) {
	e := newLearnEnv(t)
	ctx := context.Background()
	h := e.readyHost(t, "nas")
	for _, set := range [][]string{{"a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7"}, {"b0", "b1", "b2", "b3"}} {
		var items []store.Proposal
		for _, n := range set {
			items = append(items, store.Proposal{Fields: skill.Fields{Name: n, Description: "d", Body: "b"}})
		}
		if _, err := e.st.CreateProposals(ctx, store.ProposalSet{HostID: h.ID, Items: items}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := e.st.CountProposals(ctx, h.ID); n != 12 {
		t.Fatalf("%d proposals wait, want 12", n)
	}
	if code, body := e.learn(t, h.ID); code != http.StatusConflict || !strings.Contains(body, "too many proposals of this host wait for your review") {
		t.Errorf("learn with 12 waiting = %d %s", code, body)
	}
}

// claimLearner claims the queued learning run the way the runner does and returns the claim.
func claimLearner(t *testing.T, e *learnEnv) run.Claim {
	t.Helper()
	c := claimOne(t, e.ts.URL)
	if c.Role != run.RoleLearner {
		t.Fatalf("claimed a %s run", c.Role)
	}
	return c
}

func (e *learnEnv) finishRun(t *testing.T, id string, o run.Outcome) {
	t.Helper()
	b, _ := json.Marshal(o)
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/runner/v1/runs/"+id+"/finish", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("finish = %d", resp.StatusCode)
	}
}

func TestALearningRunIsClaimedWithItsOwnPromptTheLearnSchemaAndNoSnapshot(t *testing.T) {
	e := newLearnEnv(t)
	ctx := context.Background()
	h := e.readyHost(t, "nas")
	if _, err := e.st.CreateSkill(ctx, 0, skill.Fields{Name: "lab-notes", Description: "Global note.", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if code, body := e.learn(t, h.ID); code != http.StatusAccepted {
		t.Fatalf("learn = %d %s", code, body)
	}
	c := claimLearner(t, e)
	if string(c.Schema) != diagnosis.LearnSchema || c.Snapshot || c.MCPToken == "" {
		t.Errorf("claim: schema ok %v, snapshot %v, token set %v", string(c.Schema) == diagnosis.LearnSchema, c.Snapshot, c.MCPToken != "")
	}
	stored, _ := e.st.GetRun(ctx, c.ID)
	if c.Prompt != stored.Prompt || !strings.Contains(c.Prompt, "Remedy's learner") || strings.Contains(c.Prompt, "available_skills") || strings.Contains(c.Prompt, "lab-notes") {
		t.Errorf("the claimed prompt is not the stored learner prompt:\n%s", c.Prompt)
	}
}

func TestTheAnswerOfALearningRunIsCheckedAndStoredAsProposals(t *testing.T) {
	e := newLearnEnv(t)
	ctx := context.Background()
	h := e.readyHost(t, "nas")
	_, _ = e.learn(t, h.ID)
	c := claimLearner(t, e)
	e.finishRun(t, c.ID, run.Outcome{ExitCode: 0, Result: "done", Output: json.RawMessage(learnAnswer)})

	list, err := e.st.ListSkills(ctx, store.SkillFilter{State: store.SkillProposed})
	if err != nil || len(list) != 2 {
		t.Fatalf("proposals = %+v, %v", list, err)
	}
	if got, _ := e.st.GetRun(ctx, c.ID); got.Status != run.Succeeded {
		t.Errorf("run = %+v", got)
	}
	// Nothing is visible before a confirmation.
	if seen, _ := e.st.VisibleSkills(ctx, []int64{h.ID}); len(seen) != 0 {
		t.Errorf("a proposal is visible: %+v", seen)
	}
}

func TestAnInvalidAnswerFailsTheLearningRunAndStoresNothing(t *testing.T) {
	e := newLearnEnv(t)
	ctx := context.Background()
	h := e.readyHost(t, "nas")
	_, _ = e.learn(t, h.ID)
	c := claimLearner(t, e)
	e.finishRun(t, c.ID, run.Outcome{ExitCode: 0, Result: "done", Output: json.RawMessage(`{"profile":{"os":"x"},"skills":[]}`)})
	got, _ := e.st.GetRun(ctx, c.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonInvalidOutput {
		t.Errorf("run = %+v, want failed with invalid_output", got)
	}
	if list, _ := e.st.ListSkills(ctx, store.SkillFilter{}); len(list) != 0 {
		t.Errorf("%d skills were stored for an invalid answer", len(list))
	}
}

func TestTheRunListNamesTheHostOfALearningRunAndHidesItsPromptAndAnswer(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	_, _ = e.learn(t, h.ID)
	c := claimLearner(t, e)
	e.finishRun(t, c.ID, run.Outcome{ExitCode: 0, Result: "all done", Output: json.RawMessage(learnAnswer)})

	code, body := e.do(t, http.MethodGet, "/api/runs", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	var list []struct {
		ID     string          `json:"id"`
		Role   string          `json:"role"`
		Prompt string          `json:"prompt"`
		Result string          `json:"result"`
		Output json.RawMessage `json:"output"`
		Hosts  []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s (%v)", body, err)
	}
	got := list[0]
	if got.Role != "learner" || got.Prompt != "" || got.Result != "" || len(got.Output) != 0 || len(got.Hosts) != 1 || got.Hosts[0].Name != "nas" {
		t.Errorf("list entry = %+v", got)
	}
	// The detail has everything.
	_, detail := e.do(t, http.MethodGet, "/api/runs/"+c.ID, "")
	if !strings.Contains(detail, "Remedy's learner") || !strings.Contains(detail, `"name":"nas"`) || !strings.Contains(detail, "remedy-drill") {
		t.Errorf("run detail = %s", detail)
	}
}

func TestTheHostShowsItsNewestLearningRun(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	_, body := e.do(t, http.MethodGet, "/api/hosts/"+itoa(h.ID), "")
	if strings.Contains(body, `"learning"`) {
		t.Errorf("a host that never learned has a learning member: %s", body)
	}
	_, started := e.learn(t, h.ID)
	var s struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal([]byte(started), &s)
	_, body = e.do(t, http.MethodGet, "/api/hosts/"+itoa(h.ID), "")
	var view struct {
		Learning struct {
			RunID  string `json:"runId"`
			Status string `json:"status"`
		} `json:"learning"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil || view.Learning.RunID != s.RunID || view.Learning.Status != "queued" {
		t.Fatalf("host = %s", body)
	}
	c := claimLearner(t, e)
	e.finishRun(t, c.ID, run.Outcome{ExitCode: -1, FailureReason: run.ReasonTimeout, Result: "too slow"})
	_, body = e.do(t, http.MethodGet, "/api/hosts/"+itoa(h.ID), "")
	if !strings.Contains(body, `"status":"failed"`) || !strings.Contains(body, `"failureReason":"timeout"`) {
		t.Errorf("host after a timeout = %s", body)
	}
}

func TestALearningRunCanBeCancelledThroughTheAPIBeforeItStarts(t *testing.T) {
	e := newLearnEnv(t)
	h := e.readyHost(t, "nas")
	_, started := e.learn(t, h.ID)
	var s struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal([]byte(started), &s)
	if code, body := e.do(t, http.MethodPost, "/api/runs/"+s.RunID+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", code, body)
	}
	if code, _ := e.learn(t, h.ID); code != http.StatusAccepted {
		t.Errorf("a new run after the cancel = %d, want 202", code)
	}
}
