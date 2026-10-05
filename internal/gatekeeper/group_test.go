package gatekeeper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
)

// clusterProbe is a read tool of the cluster group that counts how often it ran.
func clusterProbe(runs *atomic.Int32) gatekeeper.Tool {
	return gatekeeper.Tool{
		Name:        "cluster_probe",
		Description: "A tool of the cluster group.",
		Group:       gatekeeper.GroupCluster,
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Run: func(context.Context, gatekeeper.Call) (string, error) {
			runs.Add(1)
			return "the cluster answers", nil
		},
	}
}

// clusterRunToken makes a run with cluster tools, lets a runner claim it and returns its token. The environment's
// own run has gatekeeper access but no cluster tools.
func clusterRunToken(t *testing.T, e *env) string {
	t.Helper()
	ctx := context.Background()
	if _, err := e.st.CreateClusterRun(ctx, "claude", "look at the cluster"); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil || !claimed.Cluster {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	token, err := e.st.MintRunToken(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// listedTools returns the names tools/list offers to the token.
func listedTools(t *testing.T, e *env, token string) []string {
	t.Helper()
	status, out := e.post(t, token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list = %d %v", status, out)
	}
	res, _ := out["result"].(map[string]any)
	var names []string
	for _, tool := range res["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func callWith(t *testing.T, e *env, token, useID, tool string) (string, bool) {
	t.Helper()
	status, out := e.post(t, token, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":{},"_meta":{"claudecode/toolUseId":%q}}}`, tool, useID))
	if status != http.StatusOK {
		t.Fatalf("tools/call = %d %v", status, out)
	}
	return resultText(t, out)
}

func TestARunWithoutClusterToolsDoesNotSeeThem(t *testing.T) {
	var probes atomic.Int32
	e := newEnv(t, clusterProbe(&probes))
	if names := listedTools(t, e, e.token); has(names, "cluster_probe") || !has(names, "echo") {
		t.Fatalf("a run without cluster tools is offered %v", names)
	}
	if names := listedTools(t, e, clusterRunToken(t, e)); !has(names, "cluster_probe") || !has(names, "echo") {
		t.Fatalf("a run with cluster tools is offered %v", names)
	}
}

func TestARunWithoutClusterToolsCannotCallThemAndLearnsNothingFromTheRefusal(t *testing.T) {
	var probes atomic.Int32
	e := newEnv(t, clusterProbe(&probes))

	text, isErr := callWith(t, e, e.token, "toolu_1", "cluster_probe")
	if !isErr || text != "unknown tool cluster_probe" {
		t.Fatalf("answer = %q, isError = %v", text, isErr)
	}
	other, _ := callWith(t, e, e.token, "toolu_2", "no_such_tool")
	if other != "unknown tool no_such_tool" {
		t.Fatalf("an unknown tool is answered with %q: the refusal must look the same", other)
	}
	if n := probes.Load(); n != 0 {
		t.Fatalf("the cluster tool ran %d times for a run without cluster tools", n)
	}
	calls, err := e.st.ListToolCalls(context.Background(), e.run.ID)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v, %v: a refused call is still in the audit log", calls, err)
	}
}

func TestARunWithClusterToolsCanCallThem(t *testing.T) {
	var probes atomic.Int32
	e := newEnv(t, clusterProbe(&probes))
	token := clusterRunToken(t, e)
	text, isErr := callWith(t, e, token, "toolu_1", "cluster_probe")
	if isErr || text != "the cluster answers" || probes.Load() != 1 {
		t.Fatalf("answer = %q, isError = %v, runs = %d", text, isErr, probes.Load())
	}
}

func TestAToolOfAnUnknownGroupIsRefusedAtStartUp(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a tool of a group nobody knows was accepted: it would never be offered")
		}
	}()
	tool := clusterProbe(&atomic.Int32{})
	tool.Group = "clustre"
	gatekeeper.New(gatekeeper.Config{Tools: []gatekeeper.Tool{tool}})
}
