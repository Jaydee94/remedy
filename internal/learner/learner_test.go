package learner_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/learner"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

type env struct {
	t  *testing.T
	st *store.Store
	l  *learner.Learner
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &env{t: t, st: st, l: &learner.Learner{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
}

// host makes a host that a learning run may use: enabled, with a login, a pinned key and a successful test.
func (e *env) host(name string) store.Host {
	e.t.Helper()
	ctx := context.Background()
	h, err := e.st.CreateHost(ctx, store.HostFields{Name: name, Address: "10.0.0.5", Port: 22, Account: "remedy", Notes: "the NAS", DockerPattern: "remedy-*", Enabled: true})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{1}, Hint: "set"}); err != nil {
		e.t.Fatal(err)
	}
	const line = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOhostkeyhostkeyhostkeyhostkeyhostkeyhostkeyhost"
	if err := e.st.PinHostKey(ctx, h.ID, line, time.Now()); err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SaveHostTest(ctx, h.ID, line, store.HostOK, "", "", time.Now()); err != nil {
		e.t.Fatal(err)
	}
	return h
}

const goodAnswer = `{"profile":{"os":"Debian 12","role":"NAS with Paperless","services":["docker","sshd"],"containers":["remedy-paperless"],"notes":"Backups run at night."},
"skills":[{"name":"paperless","description":"Paperless keeps its data in /volume1/docker/paperless.","body":"The consume folder is /volume1/docker/paperless/consume.","evidence":[["docker","ps","-a"],["cat","/etc/hostname"]]}]}`

// ran claims the oldest queued run, records the successful host_read calls and ends the run with the output.
func (e *env) ran(r run.Run, output string, exit int, reason string, reads ...[]string) run.Run {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.st.ClaimNext(ctx); err != nil {
		e.t.Fatal(err)
	}
	for i, argv := range reads {
		raw, _ := json.Marshal(map[string]any{"host": "nas", "argv": argv})
		c, _, err := e.st.BeginToolCall(ctx, store.NewToolCall{RunID: r.ID, ToolUseID: "toolu_" + string(rune('a'+i)), Tool: "host_read", Kind: store.CallKindRead, Arguments: raw})
		if err != nil {
			e.t.Fatal(err)
		}
		if err := e.st.FinishToolCall(ctx, c.ID, store.CallSucceeded, "out", ""); err != nil {
			e.t.Fatal(err)
		}
	}
	out := run.Outcome{ExitCode: exit, Result: "done", FailureReason: reason}
	if output != "" {
		out.Output = json.RawMessage(output)
	}
	if err := e.st.FinishRun(ctx, r.ID, out); err != nil {
		e.t.Fatal(err)
	}
	got, err := e.st.GetRun(ctx, r.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func (e *env) proposals() []store.Skill {
	e.t.Helper()
	list, err := e.st.ListSkills(context.Background(), store.SkillFilter{State: store.SkillProposed})
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

func (e *env) activity(kind string) []string {
	e.t.Helper()
	log, err := e.st.ListActivity(context.Background(), store.ActivityQuery{Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		if a.Kind == kind {
			out = append(out, a.Summary)
		}
	}
	return out
}

func TestStartQueuesALearningRunWhosePromptNamesTheHostAndItsSkillsButNoProposal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	if _, err := e.st.CreateSkill(ctx, nas.ID, skill.Fields{Name: "backup", Description: "Backups run at night.", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: []store.Proposal{{Fields: skill.Fields{Name: "secret-proposal", Description: "Not yet.", Body: "b"}}}}); err != nil {
		t.Fatal(err)
	}

	r, err := e.l.Start(ctx, nas.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != run.RoleLearner || !r.MCP || r.Cluster || r.Automatic || r.HostCount != 1 || r.Status != run.Queued {
		t.Fatalf("run = %+v", r)
	}
	for _, want := range []string{"host_name", "nas", "remedy-*", "backup — Backups run at night.", "existing_skills"} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, r.Prompt)
		}
	}
	if strings.Contains(r.Prompt, "secret-proposal") {
		t.Errorf("a proposal is in the prompt:\n%s", r.Prompt)
	}
}

func TestStartRefusesAHostThatIsNotReadyWithTheReason(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.l.Start(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown host = %v", err)
	}
	disabled := e.host("disabled")
	if _, _, err := e.st.UpdateHost(ctx, disabled.ID, store.HostFields{Name: "disabled", Address: "10.0.0.5", Port: 22, Account: "remedy", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	bare, err := e.st.CreateHost(ctx, store.HostFields{Name: "bare", Address: "10.0.0.6", Port: 22, Account: "remedy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	untested := e.host("untested")
	if err := e.st.SaveHostCredential(ctx, store.HostCredential{HostID: untested.ID, Kind: "password", Ciphertext: []byte{2}, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id     int64
		reason string
	}{
		{disabled.ID, "it is disabled"},
		{bare.ID, "it has no login"},
		{untested.ID, "its last connection test did not succeed; test the connection first"},
	} {
		_, err := e.l.Start(ctx, c.id)
		var nr *learner.NotReadyError
		if !errors.As(err, &nr) || nr.Reason != c.reason {
			t.Errorf("host %d: err = %v, want a *NotReadyError with %q", c.id, err, c.reason)
		}
	}
	if list, _ := e.st.ListRuns(ctx, 10); len(list) != 0 {
		t.Errorf("%d runs after the refusals", len(list))
	}
	if err := e.st.SaveHostCredential(ctx, store.HostCredential{HostID: untested.ID, Kind: "password", Ciphertext: []byte{3}, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
	// Pinned and tested, then the pin goes: the reason is the key.
	if err := e.st.UnpinHostKey(ctx, untested.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.Start(ctx, untested.ID); err == nil || !strings.Contains(err.Error(), "its key is not pinned") {
		t.Errorf("an unpinned host = %v", err)
	}
}

func TestCompleteStoresTheProfileAndTheSkillsAsProposalsWithCheckedEvidence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, err := e.l.Start(ctx, nas.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := e.ran(r, goodAnswer, 0, "", []string{"uname", "-a"}, []string{"docker", "ps", "-a"})
	if out := learner.CheckRun(run.Outcome{ExitCode: 0, Output: json.RawMessage(goodAnswer)}); out.FailureReason != "" {
		t.Fatalf("CheckRun refused a good answer: %+v", out)
	}

	e.l.Complete(ctx, done.ID)

	got := e.proposals()
	if len(got) != 2 {
		t.Fatalf("%d proposals, want the profile and one skill: %+v", len(got), got)
	}
	byName := map[string]store.Skill{}
	for _, k := range got {
		byName[k.Name] = k
		if k.State != store.SkillProposed || k.Origin != store.SkillOriginProposed || k.HostID != nas.ID || k.RunID != r.ID {
			t.Errorf("proposal %s = %+v", k.Name, k)
		}
	}
	profile := byName[skill.ProfileName]
	wantBody := "## Operating system\n\nDebian 12\n\n## Role\n\nNAS with Paperless\n\n## Services\n\n- docker\n- sshd\n\n## Containers\n\n- remedy-paperless\n\n## Notes\n\n> Backups run at night."
	if profile.Body != wantBody {
		t.Errorf("profile body:\n%q\nwant\n%q", profile.Body, wantBody)
	}
	if profile.Description != "What this host is: Debian 12; NAS with Paperless" {
		t.Errorf("profile description = %q", profile.Description)
	}
	if len(profile.Evidence) != 2 || !profile.Evidence[0].Ran || strings.Join(profile.Evidence[0].Argv, " ") != "uname -a" {
		t.Errorf("profile evidence = %+v, want the commands that ran", profile.Evidence)
	}
	paperless := byName["paperless"]
	if len(paperless.Evidence) != 2 || !paperless.Evidence[0].Ran || paperless.Evidence[1].Ran || strings.Join(paperless.Evidence[1].Argv, " ") != "cat /etc/hostname" {
		t.Errorf("paperless evidence = %+v, want docker ps -a ran and cat /etc/hostname not", paperless.Evidence)
	}
	if entries := e.activity(store.KindSkillsProposed); len(entries) != 1 || entries[0] != "Learned about nas: 2 proposals" {
		t.Errorf("skills_proposed entries = %v", entries)
	}
	// Nothing is visible to a run before the maintainer confirms it.
	if seen, _ := e.st.VisibleSkills(ctx, []int64{nas.ID}); len(seen) != 0 {
		t.Errorf("a proposal is visible: %+v", seen)
	}
}

func TestACitedCommandRanOnlyWhenTheWordsAreExactlyThoseOfASuccessfulRead(t *testing.T) {
	answer := diagnosis.Learn{
		Profile: diagnosis.LearnProfile{OS: "o", Role: "r", Services: []string{}, Containers: []string{}},
		Skills: []diagnosis.LearnSkill{{Name: "a", Description: "d", Body: "b", Evidence: [][]string{
			{"docker", "ps", "-a"}, {"docker", "ps"}, {"docker", "ps", "-a", "-q"}, {"DOCKER", "ps", "-a"}, {"ps", "docker", "-a"}, {"uname", "-a"},
		}}},
	}
	reads := [][]string{{"docker", "ps", "-a"}, {"uname", "-a"}, {"uname", "-a"}}
	got := learner.Proposals(answer, reads)
	if len(got) != 2 || got[0].Name != skill.ProfileName || got[1].Name != "a" {
		t.Fatalf("proposals = %+v", got)
	}
	var ran []bool
	for _, ev := range got[1].Evidence {
		ran = append(ran, ev.Ran)
	}
	want := []bool{true, false, false, false, false, true}
	for i := range want {
		if ran[i] != want[i] {
			t.Errorf("evidence %d ran = %v, want %v (%v)", i, ran[i], want[i], got[1].Evidence[i].Argv)
		}
	}
	if len(got[0].Evidence) != 2 {
		t.Errorf("the profile cites %d commands, want the 2 different ones that ran", len(got[0].Evidence))
	}
	// An empty list of reads: nothing ran.
	for _, ev := range learner.Proposals(answer, nil)[1].Evidence {
		if ev.Ran {
			t.Errorf("%v ran although nothing did", ev.Argv)
		}
	}
}

func TestACitedCommandOfAFailedOrRefusedCallDidNotRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, _ := e.l.Start(ctx, nas.ID)
	if _, err := e.st.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"host": "nas", "argv": []string{"docker", "ps", "-a"}})
	c, _, err := e.st.BeginToolCall(ctx, store.NewToolCall{RunID: r.ID, ToolUseID: "toolu_x", Tool: "host_read", Kind: store.CallKindRead, Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.FinishToolCall(ctx, c.ID, store.CallFailed, "", "the host did not answer in time"); err != nil {
		t.Fatal(err)
	}
	// The second cited command was refused (a command outside the table of reads is stored as a failed call).
	raw, _ = json.Marshal(map[string]any{"host": "nas", "argv": []string{"cat", "/etc/hostname"}})
	c, _, err = e.st.BeginToolCall(ctx, store.NewToolCall{RunID: r.ID, ToolUseID: "toolu_y", Tool: "host_read", Kind: store.CallKindRead, Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.FinishToolCall(ctx, c.ID, store.CallFailed, "", "the command is not one of the reads"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.FinishRun(ctx, r.ID, run.Outcome{ExitCode: 0, Output: json.RawMessage(goodAnswer)}); err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, r.ID)
	got := e.proposals()
	if len(got) != 2 {
		t.Fatalf("%d proposals, want the profile and paperless: %+v", len(got), got)
	}
	for _, k := range got {
		if k.Name == "paperless" && len(k.Evidence) != 2 {
			t.Errorf("paperless cites %d commands, want 2", len(k.Evidence))
		}
		if k.Name == skill.ProfileName && len(k.Evidence) != 0 {
			t.Errorf("the profile cites %v, but nothing ran", k.Evidence)
		}
		for _, ev := range k.Evidence {
			if ev.Ran {
				t.Errorf("%s: %v ran although its call failed", k.Name, ev.Argv)
			}
		}
	}
}

func TestACitationOfANameThatTheHostHasMakesAReplacingProposal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	old, err := e.st.CreateSkill(ctx, nas.ID, skill.Fields{Name: "paperless", Description: "Old.", Body: "old body"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.l.Start(ctx, nas.ID)
	done := e.ran(r, goodAnswer, 0, "")
	e.l.Complete(ctx, done.ID)
	found := false
	for _, k := range e.proposals() {
		if k.Name == "paperless" {
			found = true
		}
		if k.Name == "paperless" && k.ReplacesID != old.ID {
			t.Errorf("the proposal for paperless replaces %d, want %d", k.ReplacesID, old.ID)
		}
		if k.Name == skill.ProfileName && k.ReplacesID != 0 {
			t.Errorf("the profile replaces %d", k.ReplacesID)
		}
	}
	if !found || old.ID == 0 {
		t.Errorf("no proposal for paperless (old id %d): %+v", old.ID, e.proposals())
	}
	if still, _ := e.st.GetSkill(ctx, old.ID); still.Body != "old body" || still.State != store.SkillActive {
		t.Errorf("the existing skill changed before any confirmation: %+v", still)
	}
}

func TestAnythingButAGoodAnswerLeavesNothingAndLogsTheFailure(t *testing.T) {
	cases := map[string]struct {
		output string
		exit   int
		reason string
		want   string
	}{
		"a garbage answer":     {output: `{"profile":1}`, want: "Learning about nas failed: the answer was not valid"},
		"no answer":            {output: "", want: "Learning about nas failed: the answer was not valid"},
		"a timeout":            {output: goodAnswer, exit: -1, reason: run.ReasonTimeout, want: "Learning about nas failed: the run timed out"},
		"a cancel":             {output: goodAnswer, exit: -1, reason: run.ReasonCancelled, want: "Learning about nas failed: the run was cancelled"},
		"a lost runner":        {output: "", exit: -1, reason: run.ReasonRunnerLost, want: "Learning about nas failed: the runner was lost"},
		"an exit code":         {output: goodAnswer, exit: 2, want: "Learning about nas failed: the agent exited with code 2"},
		"the profile as skill": {output: strings.Replace(goodAnswer, `"name":"paperless"`, `"name":"profile"`, 1), want: "Learning about nas failed: the answer was not valid"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			nas := e.host("nas")
			r, _ := e.l.Start(ctx, nas.ID)
			done := e.ran(r, c.output, c.exit, c.reason)
			e.l.Complete(ctx, done.ID)
			if n := len(e.proposals()); n != 0 {
				t.Errorf("%d proposals were stored", n)
			}
			if got := e.activity(store.KindLearnFailed); len(got) != 1 || got[0] != c.want {
				t.Errorf("learn_failed entries = %v, want %q", got, c.want)
			}
			if got := e.activity(store.KindSkillsProposed); len(got) != 0 {
				t.Errorf("skills_proposed entries = %v", got)
			}
		})
	}
}

func TestCheckRunFailsTheRunsOfAnInvalidAnswerOnly(t *testing.T) {
	good := learner.CheckRun(run.Outcome{ExitCode: 0, Output: json.RawMessage(goodAnswer)})
	if good.FailureReason != "" {
		t.Errorf("a good answer: %+v", good)
	}
	for name, o := range map[string]run.Outcome{
		"no output":     {ExitCode: 0},
		"null":          {ExitCode: 0, Output: json.RawMessage("null")},
		"invalid":       {ExitCode: 0, Output: json.RawMessage(`{"profile":{}}`)},
		"a free answer": {ExitCode: 0, Output: json.RawMessage(`"I looked"`)},
	} {
		if got := learner.CheckRun(o); got.FailureReason != run.ReasonInvalidOutput || got.Result == "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	// A run that failed already keeps its own reason.
	if got := learner.CheckRun(run.Outcome{ExitCode: -1, FailureReason: run.ReasonTimeout}); got.FailureReason != run.ReasonTimeout {
		t.Errorf("a timeout became %+v", got)
	}
	if got := learner.CheckRun(run.Outcome{ExitCode: 3}); got.FailureReason != "" {
		t.Errorf("a failed exit code got a reason: %+v", got)
	}
}

func TestCompleteDoesNothingForOtherRolesRunsThatHaveNotEndedAndDeletedHosts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	adhoc, err := e.st.CreateRun(ctx, "claude", "a question")
	if err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, adhoc.ID) // queued
	if _, err := e.st.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.st.FinishRun(ctx, adhoc.ID, run.Outcome{Output: json.RawMessage(goodAnswer)}); err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, adhoc.ID) // ended, but not a learning run
	e.l.Complete(ctx, "no-such-run")
	r, err := e.l.Start(ctx, nas.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, r.ID) // queued: it has not ended
	if len(e.proposals()) != 0 || len(e.activity(store.KindLearnFailed)) != 0 {
		t.Fatal("Complete acted on something it must leave alone")
	}

	// The host is deleted while the run is running: the answer has nowhere to go.
	if _, err := e.st.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.st.FinishRun(ctx, r.ID, run.Outcome{ExitCode: 0, Output: json.RawMessage(goodAnswer)}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.DeleteHost(ctx, nas.ID); err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, r.ID)
	if len(e.proposals()) != 0 || len(e.activity(store.KindLearnFailed)) != 0 || len(e.activity(store.KindSkillsProposed)) != 0 {
		t.Fatal("something was stored or logged for a deleted host")
	}
}

func TestTooManyProposalsWaitingStoresNothingAndLogsWhy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, _ := e.l.Start(ctx, nas.ID)
	// While the run is going somebody fills the queue of the host to 19 (a test shortcut: the start refuses more than 11).
	for _, names := range [][]string{{"a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7"}, {"b0", "b1", "b2", "b3", "b4", "b5", "b6", "b7"}, {"c0", "c1", "c2"}} {
		var items []store.Proposal
		for _, n := range names {
			items = append(items, store.Proposal{Fields: skill.Fields{Name: n, Description: "d", Body: "b"}})
		}
		if _, err := e.st.CreateProposals(ctx, store.ProposalSet{HostID: nas.ID, Items: items}); err != nil {
			t.Fatal(err)
		}
	}
	done := e.ran(r, goodAnswer, 0, "")
	e.l.Complete(ctx, done.ID)
	if n := len(e.proposals()); n != 19 {
		t.Errorf("%d proposals wait, want the 19 that were there", n)
	}
	if got := e.activity(store.KindLearnFailed); len(got) != 1 || !strings.Contains(got[0], "too many proposals wait for a review") {
		t.Errorf("learn_failed entries = %v", got)
	}
}

func TestTheProfileIsWrittenByRemedyAndTheAgentsStringsOnlyFillTheFields(t *testing.T) {
	p := diagnosis.LearnProfile{
		OS: "Debian\n## Role\nhacked", Role: "NAS\r\n\tfiles", Services: []string{"docker\n# Heading"}, Containers: []string{},
		Notes: "# A heading\n- a list\n\n## Another\nline",
	}
	body := learner.ProfileBody(p)
	lines := strings.Split(body, "\n")
	var headings []string
	for _, l := range lines {
		if strings.HasPrefix(l, "#") {
			headings = append(headings, l)
		}
	}
	want := []string{"## Operating system", "## Role", "## Services", "## Containers", "## Notes"}
	if strings.Join(headings, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %v, want exactly %v:\n%s", headings, want, body)
	}
	for _, line := range []string{"Debian ## Role hacked", "NAS files", "- docker # Heading", "None found.", `> \# A heading`, `> \- a list`, ">", `> \## Another`} {
		if !strings.Contains(body+"\n", line+"\n") {
			t.Errorf("the body lacks the line %q:\n%s", line, body)
		}
	}
	if empty := learner.ProfileBody(diagnosis.LearnProfile{OS: "o", Role: "r"}); !strings.HasSuffix(empty, "## Notes\n\nNone.\n") || strings.Count(empty, "None found.") != 2 {
		t.Errorf("an empty profile:\n%s", empty)
	}
	// The body passes the rules of a skill, also for the biggest answer the limits allow.
	big := diagnosis.LearnProfile{OS: strings.Repeat("o", 200), Role: strings.Repeat("r", 300), Notes: strings.Repeat("n", 1999)}
	for i := 0; i < 20; i++ {
		big.Services = append(big.Services, strings.Repeat("s", 100))
		big.Containers = append(big.Containers, strings.Repeat("c", 100))
	}
	if _, err := (skill.Fields{Name: skill.ProfileName, Description: "d", Body: learner.ProfileBody(big)}).Validate(); err != nil {
		t.Errorf("the biggest profile is refused as a skill: %v", err)
	}
}

// headingLines returns the lines of a Markdown text that a renderer shows as a heading of the second level.
func headingLines(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "## ") {
			out = append(out, l)
		}
	}
	return out
}

func TestAFieldThatStartsWithABlockMarkerIsEscaped(t *testing.T) {
	p := diagnosis.LearnProfile{
		OS: "## Services", Role: "  # x", Services: []string{"## Notes", "1. first", "2) second", "plain"}, Containers: []string{"- x", "> q", "`code`"},
		Notes: "## Another\n---\n1. item\n+ plus\n* star\n= eq\n_ under\n| pipe\n~~~ tilde\nfine",
	}
	body := learner.ProfileBody(p)
	want := []string{"## Operating system", "## Role", "## Services", "## Containers", "## Notes"}
	if got := headingLines(body); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %v, want exactly %v:\n%s", got, want, body)
	}
	for _, line := range []string{
		`\## Services`, `\# x`, `- \## Notes`, `- \1. first`, `- \2) second`, "- plain", `- \- x`, `- \> q`, "- \\`code`",
		`> \## Another`, `> \---`, `> \1. item`, `> \+ plus`, `> \* star`, `> \= eq`, `> \_ under`, `> \| pipe`, `> \~~~ tilde`, "> fine",
	} {
		if !strings.Contains(body+"\n", "\n"+line+"\n") {
			t.Errorf("the body lacks the line %q:\n%s", line, body)
		}
	}
	// A number that is not a list marker stays as it is.
	if b := learner.ProfileBody(diagnosis.LearnProfile{OS: "12 bookworm", Role: "r"}); !strings.Contains(b, "\n12 bookworm\n") {
		t.Errorf("a plain number was escaped:\n%s", b)
	}
}

func TestEveryLineBreakInTheNotesStaysInsideTheBlockQuote(t *testing.T) {
	for name, sep := range map[string]string{"CR": "\r", "CRLF": "\r\n", "NEL": string(rune(0x85)), "LS": string(rune(0x2028)), "PS": string(rune(0x2029)), "VT": "\v", "FF": "\f"} {
		body := learner.ProfileBody(diagnosis.LearnProfile{OS: "o", Role: "r", Notes: "first" + sep + "## x"})
		notes := body[strings.Index(body, "## Notes\n\n")+len("## Notes\n\n"):]
		if notes != "> first\n> \\## x\n" {
			t.Errorf("%s: the notes are %q", name, notes)
		}
		if got := headingLines(body); len(got) != 5 {
			t.Errorf("%s: headings = %v", name, got)
		}
	}
}

func TestCompleteTwiceForASucceededRunStoresOneSet(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, _ := e.l.Start(ctx, nas.ID)
	done := e.ran(r, goodAnswer, 0, "", []string{"docker", "ps", "-a"})
	e.l.Complete(ctx, done.ID)
	first := e.proposals()
	if len(first) != 2 {
		t.Fatalf("%d proposals after the first call", len(first))
	}
	var rejected, profile int64
	for _, k := range first {
		switch k.Name {
		case "paperless":
			rejected = k.ID
		case skill.ProfileName:
			profile = k.ID
		}
	}
	if err := e.st.RejectSkill(ctx, rejected); err != nil {
		t.Fatal(err)
	}

	e.l.Complete(ctx, done.ID) // the reaper after the finish handler, say

	again := e.proposals()
	if len(again) != 1 || again[0].ID != profile {
		t.Errorf("proposals after the second call = %+v, want only the first profile", again)
	}
	if got := e.activity(store.KindSkillsProposed); len(got) != 1 {
		t.Errorf("skills_proposed entries = %v, want one", got)
	}
	if got := e.activity(store.KindLearnFailed); len(got) != 0 {
		t.Errorf("learn_failed entries = %v", got)
	}
}

func TestCompleteTwiceForAFailedRunLogsOneFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, _ := e.l.Start(ctx, nas.ID)
	done := e.ran(r, "", -1, run.ReasonTimeout)
	e.l.Complete(ctx, done.ID)
	e.l.Complete(ctx, done.ID)
	if got := e.activity(store.KindLearnFailed); len(got) != 1 {
		t.Errorf("learn_failed entries = %v, want one", got)
	}
}

func TestLearnOutcomeRecordedSeesOnlyTheOutcomeOfThatRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, _ := e.l.Start(ctx, nas.ID)
	if ok, err := e.st.LearnOutcomeRecorded(ctx, r.ID); err != nil || ok {
		t.Fatalf("a started run = %v, %v (learn_started is not an outcome)", ok, err)
	}
	other, err := e.st.CreateRun(ctx, "claude", "a question")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddActivity(ctx, store.NewActivity{Kind: store.KindLearnFailed, RunID: other.ID, Summary: "x"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.st.LearnOutcomeRecorded(ctx, r.ID); ok {
		t.Error("the outcome of another run counts")
	}
	if err := e.st.AddActivity(ctx, store.NewActivity{Kind: store.KindSkillsProposed, RunID: r.ID, Summary: "x"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.st.LearnOutcomeRecorded(ctx, r.ID); err != nil || !ok {
		t.Errorf("after skills_proposed = %v, %v", ok, err)
	}
}

func TestASecretInTheAnswerNeverReachesAProposal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, _ := e.l.Start(ctx, nas.ID)
	answer := strings.Replace(goodAnswer, "Backups run at night.", "login with password=hunter2hunter2 on the NAS", 1)
	answer = strings.Replace(answer, "The consume folder", "export API_TOKEN=abcdef123456 and AKIAIOSFODNN7EXAMPLE; the consume folder", 1)
	done := e.ran(r, answer, 0, "")
	e.l.Complete(ctx, done.ID)
	if len(e.proposals()) != 2 {
		t.Fatalf("%d proposals", len(e.proposals()))
	}
	for _, k := range e.proposals() {
		for _, secret := range []string{"hunter2hunter2", "abcdef123456", "AKIAIOSFODNN7EXAMPLE"} {
			if strings.Contains(k.Body, secret) || strings.Contains(k.Description, secret) {
				t.Errorf("%s still holds %s: %q", k.Name, secret, k.Body)
			}
		}
	}
}

func TestALaterLearningRunProposesAProfileThatReplacesTheConfirmedOne(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	first, err := e.l.Start(ctx, nas.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, e.ran(first, goodAnswer, 0, "").ID)
	var profile store.Skill
	for _, k := range e.proposals() {
		if k.Name == skill.ProfileName {
			profile = k
		}
	}
	confirmed, err := e.st.ConfirmSkill(ctx, profile.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	second, err := e.l.Start(ctx, nas.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.l.Complete(ctx, e.ran(second, strings.Replace(goodAnswer, "Debian 12", "Debian 13", 1), 0, "").ID)
	var next store.Skill
	for _, k := range e.proposals() {
		if k.Name == skill.ProfileName {
			next = k
		}
	}
	if next.ID == 0 || next.ReplacesID != confirmed.ID {
		t.Fatalf("the new profile = %+v, want a proposal that replaces %d", next, confirmed.ID)
	}
	if still, _ := e.st.GetSkill(ctx, confirmed.ID); !strings.Contains(still.Body, "Debian 12") {
		t.Errorf("the confirmed profile changed before a confirmation: %q", still.Body)
	}
	if _, err := e.st.ConfirmSkill(ctx, next.ID, nil); err != nil {
		t.Fatal(err)
	}
	all, _ := e.st.ListSkills(ctx, store.SkillFilter{HostID: nas.ID})
	profiles := 0
	for _, k := range all {
		if k.Name == skill.ProfileName {
			profiles++
			if k.ID != confirmed.ID || !strings.Contains(k.Body, "Debian 13") || k.State != store.SkillActive {
				t.Errorf("profile after the second confirmation = %+v", k)
			}
		}
	}
	if profiles != 1 {
		t.Errorf("%d skills named profile, want 1", profiles)
	}
}

// checkProfile asserts what every profile body keeps: it fits a skill, is valid UTF-8 and has the five headings once each.
func checkProfile(t *testing.T, body string) {
	t.Helper()
	if len(body) > skill.MaxBodyBytes {
		t.Errorf("the body has %d bytes, at most %d", len(body), skill.MaxBodyBytes)
	}
	if !utf8.ValidString(body) {
		t.Errorf("the body is not valid UTF-8")
	}
	if _, err := (skill.Fields{Name: skill.ProfileName, Description: "d", Body: body}).Validate(); err != nil {
		t.Errorf("the body is refused as a skill: %v", err)
	}
	want := []string{"## Operating system", "## Role", "## Services", "## Containers", "## Notes"}
	if got := headingLines(body); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("headings = %v, want exactly %v", got, want)
	}
}

func TestAProfileOfManyLongListItemsIsCutToFitAndSaysHowManyAreNotShown(t *testing.T) {
	item := strings.Repeat(string(rune(0x1F600)), 100)
	p := diagnosis.LearnProfile{OS: "Debian 12", Role: "NAS", Notes: "Backups run at night."}
	for i := 0; i < 40; i++ {
		p.Services = append(p.Services, item)
	}
	body := learner.ProfileBody(p)
	checkProfile(t, body)
	for _, line := range []string{"Debian 12", "NAS", "> (cut: too long)"} {
		if !strings.Contains(body+"\n", "\n"+line+"\n") {
			t.Errorf("the body lacks the line %q:\n%s", line, body)
		}
	}
	shown := strings.Count(body, "- "+item+"\n")
	if shown == 0 || shown == 40 {
		t.Fatalf("%d of 40 services shown", shown)
	}
	more := "\n- " + string(rune(0x2026)) + " (" + strconv.Itoa(40-shown) + " more not shown)\n"
	if !strings.Contains(body, more) {
		t.Errorf("the body lacks %q:\n%s", more, body)
	}
	if strings.Contains(body, "Backups run at night.") {
		t.Errorf("the notes were kept although the services were cut:\n%s", body)
	}
}

func TestTheBiggestAnswerTheLimitsAllowStoresTheSkillsAndTheProfile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nas := e.host("nas")
	r, err := e.l.Start(ctx, nas.ID)
	if err != nil {
		t.Fatal(err)
	}
	wide := string(rune(0x1F600))
	profile := map[string]any{
		"os": strings.Repeat(wide, diagnosis.MaxLearnOS), "role": strings.Repeat(wide, diagnosis.MaxLearnRole),
		"notes": strings.Repeat(wide, diagnosis.MaxLearnNotes),
	}
	var services, containers []string
	for i := 0; i < diagnosis.MaxLearnList; i++ {
		services = append(services, strings.Repeat(wide, diagnosis.MaxLearnListItem))
		containers = append(containers, strings.Repeat(wide, diagnosis.MaxLearnListItem))
	}
	profile["services"], profile["containers"] = services, containers
	raw, err := json.Marshal(map[string]any{"profile": profile, "skills": []any{map[string]any{
		"name": "paperless", "description": "Paperless keeps its data in /volume1.", "body": "b", "evidence": [][]string{{"docker", "ps", "-a"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := diagnosis.ParseLearn(raw); err != nil {
		t.Fatalf("the answer does not pass the limits: %v", err)
	}
	e.l.Complete(ctx, e.ran(r, string(raw), 0, "").ID)
	if failed := e.activity(store.KindLearnFailed); len(failed) != 0 {
		t.Fatalf("the run failed: %v", failed)
	}
	got := e.proposals()
	if len(got) != 2 {
		t.Fatalf("%d proposals, want the profile and one skill", len(got))
	}
	for _, k := range got {
		if k.Name == skill.ProfileName {
			checkProfile(t, k.Body)
		}
	}
}

func TestNotesOfManyLinesAreCutAtALineAndStayABlockQuote(t *testing.T) {
	p := diagnosis.LearnProfile{OS: "o", Role: "r", Services: []string{"docker"}, Containers: []string{"web"}, Notes: strings.Repeat("line of notes\n", 1000)}
	body := learner.ProfileBody(p)
	checkProfile(t, body)
	notes := body[strings.Index(body, "## Notes\n\n")+len("## Notes\n\n"):]
	lines := strings.Split(strings.TrimSuffix(notes, "\n"), "\n")
	for _, l := range lines {
		if !strings.HasPrefix(l, ">") {
			t.Errorf("a notes line outside the quote: %q", l)
		}
	}
	if last := lines[len(lines)-1]; last != "> (cut: too long)" {
		t.Errorf("the last notes line = %q", last)
	}
	if len(lines) < 100 || lines[len(lines)-2] != "> line of notes" {
		t.Errorf("the notes were not cut at a line: %d lines, before the cut %q", len(lines), lines[len(lines)-2])
	}
	if !strings.Contains(body, "\n- docker\n") || !strings.Contains(body, "\n- web\n") {
		t.Errorf("the lists were cut although the notes sufficed:\n%s", body[:200])
	}
	// One long line is cut at a rune boundary.
	one := learner.ProfileBody(diagnosis.LearnProfile{OS: "o", Role: "r", Notes: strings.Repeat(string(rune(0xE9)), 5000)})
	checkProfile(t, one)
	if !strings.HasSuffix(one, "\n> (cut: too long)\n") || !strings.Contains(one, "\n> "+string(rune(0xE9))) {
		t.Errorf("one long line of notes:\n%s", one[len(one)-100:])
	}
}

func TestABodyOfExactlyTheLimitIsNotCut(t *testing.T) {
	p := diagnosis.LearnProfile{OS: "o", Role: "r", Notes: "x"}
	for i := 0; i < 20; i++ {
		p.Services = append(p.Services, strings.Repeat("s", 100))
		p.Containers = append(p.Containers, strings.Repeat("c", 100))
	}
	base := len(learner.ProfileBody(p))
	p.Notes = strings.Repeat("x", skill.MaxBodyBytes-base+1)
	body := learner.ProfileBody(p)
	if len(body) != skill.MaxBodyBytes || strings.Contains(body, "(cut: too long)") || !strings.HasSuffix(body, "\n> "+p.Notes+"\n") {
		t.Errorf("a body of exactly the limit (%d bytes) was changed", len(body))
	}
	checkProfile(t, body)
	p.Notes += "x"
	if over := learner.ProfileBody(p); !strings.HasSuffix(over, "\n> (cut: too long)\n") || len(over) > skill.MaxBodyBytes {
		t.Errorf("one byte more was not cut: %d bytes", len(over))
	}
}
