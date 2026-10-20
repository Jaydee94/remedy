package responder_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

func (e *env) skill(host int64, name, body string) store.Skill {
	e.t.Helper()
	k, err := e.st.CreateSkill(context.Background(), host, skill.Fields{Name: name, Description: "About " + name + ".", Body: body})
	if err != nil {
		e.t.Fatal(err)
	}
	return k
}

// finishAlertRun ends the run of an alert with a valid outage answer, so that the next Start is not refused because a run is active.
func (e *env) finishAlertRun(r run.Run) {
	e.t.Helper()
	e.finish(r, run.Outcome{ExitCode: 0, Result: "ok", Output: json.RawMessage(goodOutage)})
}

func TestAnAlertRunListsTheActiveSkillsOfItsHostAndTheGlobalOnesAndNothingElse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas, alpha := e.host("nas", true), e.host("alpha", true)
	e.skill(0, "lab-notes", "b")
	e.skill(nas.ID, "paperless", "b")
	e.skill(alpha.ID, "alpha-only", "b")
	off := e.skill(nas.ID, "nas-off", "b")
	if _, err := e.st.UpdateSkill(ctx, off.ID, skill.Fields{Name: "nas-off", Description: "d", Body: "b"}, store.SkillOff); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{{Fields: skill.Fields{Name: "nas-proposal", Description: "d", Body: "b"}}}}); err != nil {
		t.Fatal(err)
	}
	in := e.openAlert("A/1", "Pod web-1 crashes", nas.ID, true)

	r, err := e.r.Start(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"available_skills", "lab-notes (global) \u2014 About lab-notes.", "paperless (host: nas) \u2014 About paperless.", "skill_get"} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, r.Prompt)
		}
	}
	for _, hidden := range []string{"alpha-only", "nas-off", "nas-proposal"} {
		if strings.Contains(r.Prompt, hidden) {
			t.Errorf("the prompt lists %s:\n%s", hidden, r.Prompt)
		}
	}
	stored, _ := e.st.GetRun(ctx, r.ID)
	if stored.Prompt != r.Prompt || !strings.Contains(stored.Prompt, "paperless (host: nas)") {
		t.Error("the list is part of what is stored")
	}
}

func TestAnAlertRunWithoutAUsableHostListsTheGlobalSkillsOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	disabled := e.host("nas", false)
	e.skill(0, "lab-notes", "b")
	e.skill(disabled.ID, "paperless", "b")

	for name, hostID := range map[string]int64{"no host": 0, "a disabled host": disabled.ID} {
		in := e.openAlert("A/"+name, "Pod crashes "+name, hostID, true)
		r, err := e.r.Start(ctx, in.ID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(r.Prompt, "lab-notes (global)") || strings.Contains(r.Prompt, "paperless") {
			t.Errorf("%s: the prompt =\n%s", name, r.Prompt)
		}
		e.finishAlertRun(r)
	}
}

func TestAnAlertRunWithoutSkillsHasNoSectionAndTheGitHubRunNeverHasOne(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := e.openAlert("A/1", "Pod web-1 crashes", 0, true)
	r, err := e.r.Start(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Prompt, "available_skills") || strings.Contains(r.Prompt, "skill_get") {
		t.Errorf("a prompt without skills mentions them:\n%s", r.Prompt)
	}
	e.finishAlertRun(r)

	e.skill(0, "lab-notes", "b")
	gh := e.incident("pr:20", "web", sha, "failure")
	g, err := e.r.Start(ctx, gh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.MCP || strings.Contains(g.Prompt, "available_skills") || strings.Contains(g.Prompt, "skill_get") || strings.Contains(g.Prompt, "lab-notes") {
		t.Errorf("the GitHub responder has no tools and gets no skills: mcp %v\n%s", g.MCP, g.Prompt)
	}
}
