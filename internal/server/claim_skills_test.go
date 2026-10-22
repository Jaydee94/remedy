package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func runOK() run.Outcome { return run.Outcome{ExitCode: 0, Result: "ok"} }

func mkSkill(t *testing.T, st *store.Store, host int64, name string) store.Skill {
	t.Helper()
	k, err := st.CreateSkill(context.Background(), host, skill.Fields{Name: name, Description: "About " + name + ".", Body: "Body of " + name + "."})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestAQuestionAboutAnIncidentIsClaimedWithTheListBeforeTheQuestionAndTheStoredPromptStaysBare(t *testing.T) {
	e, repoID := withRepo(t)
	in := openIncident(t, e, repoID, "pr:7", "go")
	ctx := context.Background()
	nas, err := e.store.CreateHost(ctx, store.HostFields{Name: "nas", Address: "10.0.0.5", Port: 22, Account: "remedy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	alpha, _ := e.store.CreateHost(ctx, store.HostFields{Name: "alpha", Address: "10.0.0.6", Port: 22, Account: "remedy", Enabled: true})
	q, err := e.store.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "why did it fail twice?", IncidentID: in.ID, HostIDs: []int64{nas.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// Created after the run: the list is read when the run is claimed.
	mkSkill(t, e.store, 0, "lab-notes")
	mkSkill(t, e.store, nas.ID, "paperless")
	mkSkill(t, e.store, alpha.ID, "alpha-only")
	off := mkSkill(t, e.store, nas.ID, "nas-off")
	if _, err := e.store.UpdateSkill(ctx, off.ID, skill.Fields{Name: "nas-off", Description: off.Description, Body: off.Body}, store.SkillOff); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{{Fields: skill.Fields{Name: "nas-proposal", Description: "d", Body: "b"}}}}); err != nil {
		t.Fatal(err)
	}

	c := claimOne(t, e.ts.URL)
	if c.ID != q.ID {
		t.Fatalf("claimed %s, want %s", c.ID, q.ID)
	}
	for _, want := range []string{"incident_get", "available_skills", "lab-notes (global) \u2014 About lab-notes.", "paperless (host: nas) \u2014 About paperless."} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("the claimed prompt lacks %q:\n%s", want, c.Prompt)
		}
	}
	for _, hidden := range []string{"alpha-only", "nas-proposal", "nas-off", "Body of"} {
		if strings.Contains(c.Prompt, hidden) {
			t.Errorf("the claimed prompt shows %q:\n%s", hidden, c.Prompt)
		}
	}
	if !strings.HasSuffix(c.Prompt, "\nQuestion:\n\nwhy did it fail twice?\n") || strings.Index(c.Prompt, "available_skills") > strings.Index(c.Prompt, "Question:") {
		t.Errorf("the question must come last, after the list:\n%s", c.Prompt)
	}
	if stored, _ := e.store.GetRun(ctx, q.ID); stored.Prompt != "why did it fail twice?" {
		t.Errorf("the stored prompt = %q, want the bare question", stored.Prompt)
	}
}

func TestAnAdHocRunWithToolsGetsTheListInFrontOfItsTaskAndOneWithoutToolsDoesNot(t *testing.T) {
	e, _ := withRepo(t)
	ctx := context.Background()
	mkSkill(t, e.store, 0, "lab-notes")

	if _, err := e.store.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "list the incidents", Tools: true}); err != nil {
		t.Fatal(err)
	}
	c := claimOne(t, e.ts.URL)
	if !strings.HasPrefix(c.Prompt, "Skills.") || !strings.Contains(c.Prompt, "lab-notes (global)") || !strings.HasSuffix(c.Prompt, "The maintainer's task:\n\nlist the incidents\n") {
		t.Errorf("claimed prompt:\n%s", c.Prompt)
	}
	if stored, _ := e.store.GetRun(ctx, c.ID); stored.Prompt != "list the incidents" {
		t.Errorf("the stored prompt = %q", stored.Prompt)
	}
	if err := e.store.FinishRun(ctx, c.ID, runOK()); err != nil {
		t.Fatal(err)
	}

	if _, err := e.store.CreateRun(ctx, "claude", "just chat"); err != nil {
		t.Fatal(err)
	}
	if c := claimOne(t, e.ts.URL); c.Prompt != "just chat" || c.MCPToken != "" {
		t.Errorf("a run without gatekeeper access: prompt %q, token set %v", c.Prompt, c.MCPToken != "")
	}
}

func TestARunWithToolsAndNoSkillsIsClaimedWithItsBarePrompt(t *testing.T) {
	e, _ := withRepo(t)
	if _, err := e.store.CreateRunWith(context.Background(), store.RunOptions{Provider: "claude", Prompt: "list the incidents", Tools: true}); err != nil {
		t.Fatal(err)
	}
	if c := claimOne(t, e.ts.URL); c.Prompt != "list the incidents" {
		t.Errorf("prompt = %q, want the bare prompt", c.Prompt)
	}
}

func TestASkillTurnedOffBetweenTheQueueAndTheClaimIsNotListed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas, err := e.store.CreateHost(ctx, store.HostFields{Name: "nas", Address: "10.0.0.5", Port: 22, Account: "remedy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	keep, soon := mkSkill(t, e.store, nas.ID, "paperless"), mkSkill(t, e.store, nas.ID, "turned-off-later")
	if _, err := e.store.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "check the nas", Tools: true, HostIDs: []int64{nas.ID}}); err != nil {
		t.Fatal(err)
	}
	// The run is queued with both skills active; the maintainer turns one off before a runner claims it.
	if _, err := e.store.UpdateSkill(ctx, soon.ID, skill.Fields{Name: soon.Name, Description: soon.Description, Body: soon.Body}, store.SkillOff); err != nil {
		t.Fatal(err)
	}
	c := claimOne(t, e.ts.URL)
	if !strings.Contains(c.Prompt, keep.Name+" (host: nas)") || strings.Contains(c.Prompt, "turned-off-later") {
		t.Errorf("the claimed prompt must list what is active at the claim only:\n%s", c.Prompt)
	}
}

// If the skills cannot be read, the claim must not hand out a prompt without the list (or with a wrong one): it answers 500 and the run,
// which is claimed already, is failed so that it does not sit in "running" until the reaper comes.
func TestAClaimWhoseSkillsCannotBeReadAnswers500AndFailsTheRun(t *testing.T) {
	st, dsn := testutil.StoreDSN(t)
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken}))
	t.Cleanup(ts.Close)

	ctx := context.Background()
	k := mkSkill(t, st, 0, "lab-notes")
	created, err := st.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "list the incidents", Tools: true})
	if err != nil {
		t.Fatal(err)
	}
	// A timestamp that cannot be read makes the scan of the skill fail, while the run is still claimable.
	testutil.SQL(t, dsn, `UPDATE skills SET updated_at = 'not a time' WHERE id = ?`, k.ID)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/runner/v1/claim", nil)
	req.Header.Set("Authorization", "Bearer "+runnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("claim = %d, want 500", resp.StatusCode)
	}
	got, err := st.GetRun(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != run.Failed {
		t.Errorf("run = %+v, want it failed, not left running", got)
	}
}
