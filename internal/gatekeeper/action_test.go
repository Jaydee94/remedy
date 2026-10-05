package gatekeeper_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/store"
)

// actionTool is a mutating tool of the cluster group that does nothing but remember what it was told: the arguments, and
// when the approval was asked for. fail makes it return an error the agent can fix.
type actionTool struct {
	mu        sync.Mutex
	requested time.Time
	runs      int
	fail      bool
}

func (a *actionTool) tool(withActivity bool) gatekeeper.Tool {
	t := gatekeeper.Tool{
		Name:        "cluster_probe_action",
		Description: "A mutating tool of the cluster group.",
		Mutating:    true,
		Group:       gatekeeper.GroupCluster,
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Run: func(_ context.Context, c gatekeeper.Call) (string, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.requested, a.runs = c.RequestedAt, a.runs+1
			if a.fail {
				return "", gatekeeper.ArgumentError("the pod is gone")
			}
			return "done", nil
		},
	}
	if withActivity {
		t.Activity = func(json.RawMessage) string { return "Did the probe action" }
	}
	return t
}

// clusterActionEnv is an environment whose token is the token of a run with cluster tools, with the probe tool.
func clusterActionEnv(t *testing.T, a *actionTool, withActivity bool) *env {
	t.Helper()
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return []gatekeeper.Tool{a.tool(withActivity)} }, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
	})
	e.token = clusterRunToken(t, e)
	return e
}

func activityOf(t *testing.T, st *store.Store, kind string) []store.Activity {
	t.Helper()
	log, err := st.ListActivity(context.Background(), store.ActivityQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Activity
	for _, a := range log {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func TestAnApprovedActionKnowsWhenItWasAskedForAndIsLoggedInTheActivityLog(t *testing.T) {
	var a actionTool
	e := clusterActionEnv(t, &a, true)
	s := e.open(t, "toolu_1", "cluster_probe_action", map[string]any{})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); isErr || text != "done" {
		t.Fatalf("result = %q (%v)", text, isErr)
	}
	a.mu.Lock()
	requested, runs := a.requested, a.runs
	a.mu.Unlock()
	if runs != 1 || requested.IsZero() || !requested.Equal(call.CreatedAt) {
		t.Fatalf("the tool ran %d times and was told the approval was asked for at %v, want %v", runs, requested, call.CreatedAt)
	}
	acts := activityOf(t, e.st, store.KindClusterAction)
	if len(acts) != 1 || acts[0].Summary != "Did the probe action" || acts[0].RunID == "" {
		t.Fatalf("activity = %+v", acts)
	}
	done, _ := e.st.GetToolCall(context.Background(), call.ID)
	if done.Status != store.CallSucceeded || done.Result != "done" {
		t.Fatalf("audit row = %+v", done)
	}
}

func TestAnActionThatFailsLogsNoActivity(t *testing.T) {
	a := actionTool{fail: true}
	e := clusterActionEnv(t, &a, true)
	s := e.open(t, "toolu_1", "cluster_probe_action", map[string]any{})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); !isErr || text != "the pod is gone" {
		t.Fatalf("result = %q (%v)", text, isErr)
	}
	if n := len(activityOf(t, e.st, store.KindClusterAction)); n != 0 {
		t.Fatalf("%d cluster actions were logged for an action that failed", n)
	}
}

func TestAMutatingToolWithoutAnActivityTextLogsNoActionEntry(t *testing.T) {
	var a actionTool
	e := clusterActionEnv(t, &a, false)
	s := e.open(t, "toolu_1", "cluster_probe_action", map[string]any{})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); isErr || text != "done" {
		t.Fatalf("result = %q (%v)", text, isErr)
	}
	if n := len(activityOf(t, e.st, store.KindClusterAction)); n != 0 {
		t.Fatalf("%d cluster actions were logged", n)
	}
}

func TestADeniedActionDoesNotRunAndLogsNoAction(t *testing.T) {
	var a actionTool
	e := clusterActionEnv(t, &a, true)
	s := e.open(t, "toolu_1", "cluster_probe_action", map[string]any{})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, false, "not now"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); !isErr || text != "denied: not now" {
		t.Fatalf("result = %q (%v)", text, isErr)
	}
	a.mu.Lock()
	runs := a.runs
	a.mu.Unlock()
	if runs != 0 || len(activityOf(t, e.st, store.KindClusterAction)) != 0 {
		t.Fatalf("a denied action ran %d times", runs)
	}
}
