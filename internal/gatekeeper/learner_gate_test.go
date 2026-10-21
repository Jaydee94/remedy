package gatekeeper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

// learnerRig has every kind of tool registered (the incident tools, the note tool, the skill tool, the host tools, and the two test tools
// of the package, which have no group), a host, and a run of the role learner for it.
func learnerRig(t *testing.T, fake *fakeHosts) (e *env, hostID int64) {
	t.Helper()
	e = newEnvWith(t, func(st *store.Store) []gatekeeper.Tool {
		tools := append(gatekeeper.IncidentTools(st), gatekeeper.NoteTool(st), gatekeeper.SkillTool(st))
		return append(tools, gatekeeper.HostTools(fake)...)
	}, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
	})
	ctx := context.Background()
	// The environment starts with a run of its own; a learning run starts only when no other run is queued or running.
	if err := e.st.FinishRun(ctx, e.run.ID, run.Outcome{}); err != nil {
		t.Fatal(err)
	}
	h, err := e.st.CreateHost(ctx, store.HostFields{Name: "nas", Address: "127.0.0.1", Port: 22, Account: "remedy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.st.StartLearning(ctx, store.LearnStart{HostID: h.ID, Provider: "claude", Prompt: "learn", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID || claimed.Role != run.RoleLearner {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	if e.token, err = e.st.MintRunToken(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	return e, h.ID
}

func toolNames(t *testing.T, e *env) []string {
	t.Helper()
	_, out := e.rpc(t, "tools/list", map[string]any{})
	result, _ := out["result"].(map[string]any)
	list, _ := result["tools"].([]any)
	var names []string
	for _, item := range list {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	return names
}

func TestALearningRunIsOfferedHostListHostReadAndSkillGetAndNothingElse(t *testing.T) {
	e, _ := learnerRig(t, newFake())
	got := toolNames(t, e)
	if want := []string{"host_list", "host_read", "skill_get"}; !slices.Equal(got, want) {
		t.Fatalf("tools/list of a learning run = %v, want %v", got, want)
	}

	// Another role with the same registry keeps what it had: the incident tools, the note tool, the skill tool and the test tools.
	other := newEnvWith(t, func(st *store.Store) []gatekeeper.Tool {
		tools := append(gatekeeper.IncidentTools(st), gatekeeper.NoteTool(st), gatekeeper.SkillTool(st))
		return append(tools, gatekeeper.HostTools(newFake())...)
	})
	other.token = hostRunToken(t, other, "nas")
	all := toolNames(t, other)
	for _, tool := range []string{"incident_list", "incident_add_note", "echo", "host_change", "skill_get"} {
		if !slices.Contains(all, tool) {
			t.Errorf("a run with hosts lost %s: %v", tool, all)
		}
	}
}

func TestAToolThatIsNotOfferedToALearningRunAnswersLikeOneThatDoesNotExist(t *testing.T) {
	fake := newFake()
	e, _ := learnerRig(t, fake)
	calls := []struct {
		tool string
		args any
	}{
		{"host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-drill"}}},
		{"incident_list", map[string]any{}},
		{"incident_add_note", map[string]any{"incident_id": 1, "note": "x"}},
		{"echo", map[string]any{"text": "hi"}},
		{"cluster_pods", map[string]any{}},
		{"argo_sync", map[string]any{}},
		{"nonsense", map[string]any{}},
	}
	for i, c := range calls {
		text, isErr := askHost(t, e, fmt.Sprintf("u%d", i), c.tool, c.args)
		if !isErr || !strings.Contains(text, "unknown tool "+c.tool) {
			t.Errorf("%s: %q (error %v), want the answer for a tool that does not exist", c.tool, text, isErr)
		}
	}
	if n := fake.execCount(); n != 0 {
		t.Errorf("the host received %d commands for refused calls", n)
	}
	// Nothing was asked of the maintainer.
	if pending, err := e.st.ListApprovals(context.Background(), store.ApprovalFilter{PendingOnly: true}); err != nil || len(pending) != 0 {
		t.Errorf("pending approvals = %+v, %v", pending, err)
	}
}

func TestALearningRunCanReadItsHostAndLoadASkillButNotChangeAnything(t *testing.T) {
	fake := newFake()
	e, hostID := learnerRig(t, fake)
	ctx := context.Background()
	if _, err := e.st.CreateSkill(ctx, hostID, skill.Fields{Name: "paperless", Description: "About paperless.", Body: "Paperless lives here."}); err != nil {
		t.Fatal(err)
	}
	if text, isErr := askHost(t, e, "r1", "host_list", map[string]any{}); isErr || !strings.Contains(text, "nas") {
		t.Errorf("host_list = %q, %v", text, isErr)
	}
	if text, isErr := askHost(t, e, "r2", "host_read", map[string]any{"host": "nas", "argv": []string{"docker", "ps", "-a"}}); isErr || !strings.Contains(text, "remedy-drill") {
		t.Errorf("host_read = %q, %v", text, isErr)
	}
	if text, isErr := askHost(t, e, "r3", "skill_get", map[string]any{"name": "paperless"}); isErr || !strings.Contains(text, "Paperless lives here.") {
		t.Errorf("skill_get = %q, %v", text, isErr)
	}
	// The table of reads still holds for it.
	if text, isErr := askHost(t, e, "r4", "host_read", map[string]any{"host": "nas", "argv": []string{"docker", "rm", "-f", "remedy-drill"}}); !isErr || !strings.Contains(text, "host_read runs only these commands") {
		t.Errorf("host_read of docker rm = %q, %v", text, isErr)
	}
	if n := fake.execCount(); n != 1 {
		t.Errorf("the host received %d commands, want only the docker ps -a", n)
	}
	reads, err := e.st.SucceededReads(ctx, e.currentRunID(t))
	if err != nil || len(reads) != 1 || strings.Join(reads[0], " ") != "docker ps -a" {
		t.Errorf("SucceededReads = %v, %v, want only the read that worked", reads, err)
	}
}

// currentRunID is the id of the run of the token, read from the audit log.
func (e *env) currentRunID(t *testing.T) string {
	t.Helper()
	runs, err := e.st.ListRuns(context.Background(), 5)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	return runs[0].ID
}

func TestAToolWithoutAGroupIsNotOfferedToALearningRun(t *testing.T) {
	// A tool added tomorrow with no group is not offered to a learning run. The test tool echo stands for it.
	e, _ := learnerRig(t, newFake())
	if slices.Contains(toolNames(t, e), "echo") {
		t.Error("a tool without a group is offered to a learning run")
	}
	raw, _ := json.Marshal(toolNames(t, e))
	if string(raw) != `["host_list","host_read","skill_get"]` {
		t.Errorf("tools = %s", raw)
	}
}

// A learning run has no host_change: the refusal of a command outside the table must not point it to one, and must tell it to go on
// with what it can read. Every other run is still pointed to host_change.
func TestTheRefusalOfHostReadNamesHostChangeOnlyToARunThatHasIt(t *testing.T) {
	notInTable := map[string]any{"host": "nas", "argv": []string{"docker", "rm", "-f", "remedy-drill"}}

	e, _ := learnerRig(t, newFake())
	text, isErr := askHost(t, e, "l1", "host_read", notInTable)
	if !isErr || strings.Contains(text, "host_change") || !strings.Contains(text, "host_read runs only these commands") ||
		!strings.Contains(text, "This command is not allowed in this run; go on with what you can read.") {
		t.Errorf("refusal for a learning run = %q (error %v)", text, isErr)
	}

	other := newEnvWith(t, func(st *store.Store) []gatekeeper.Tool { return gatekeeper.HostTools(newFake()) })
	other.token = hostRunToken(t, other, "nas")
	text, isErr = askHost(t, other, "o1", "host_read", notInTable)
	if !isErr || !strings.Contains(text, "For anything else use host_change, which waits for the maintainer's approval.") ||
		strings.Contains(text, "go on with what you can read") {
		t.Errorf("refusal for a run with hosts = %q (error %v)", text, isErr)
	}
}

// The description of host_read, which every run with hosts sees, names no other tool: a learning run sees it too.
func TestTheDescriptionOfHostReadNamesNoOtherTool(t *testing.T) {
	e, _ := learnerRig(t, newFake())
	_, out := e.rpc(t, "tools/list", map[string]any{})
	result, _ := out["result"].(map[string]any)
	list, _ := result["tools"].([]any)
	found := false
	for _, item := range list {
		tool := item.(map[string]any)
		if tool["name"] != "host_read" {
			continue
		}
		found = true
		if d, _ := tool["description"].(string); strings.Contains(d, "host_change") || !strings.Contains(d, "Anything else is refused.") {
			t.Errorf("description of host_read = %q", d)
		}
	}
	if !found {
		t.Error("a learning run is not offered host_read")
	}
}
