package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// automaticToolRun makes a running responder run of an alert incident that was started without a click.
func (e *gateEnv) automaticToolRun(t *testing.T) run.Run {
	t.Helper()
	ctx := context.Background()
	in, err := e.st.OpenIncident(ctx, store.NewIncident{Source: store.SourceAlertmanager, Key: "A/" + run.NewID(), Title: "t", Conclusion: "firing", AutoDiagnose: true},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.st.StartDiagnosis(ctx, store.StartParams{IncidentID: in.ID, Provider: "claude", Prompt: "p", Automatic: true, Limits: store.DefaultLimits(), Now: time.Now()},
		store.NewActivity{Kind: store.KindDiagnosisStarted, Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return *claimed
}

func (e *gateEnv) rawApprovals(t *testing.T, query string) []map[string]any {
	t.Helper()
	code, body := e.admin(t, http.MethodGet, "/api/approvals"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/approvals%s = %d %s", query, code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return list
}

func TestTheApprovalAPIShowsTheDeadlineOfAnAutomaticRun(t *testing.T) {
	e := newGateEnv(t)
	ctx := context.Background()
	auto := e.automaticToolRun(t)

	c, _, err := e.st.BeginToolCall(ctx, store.NewToolCall{RunID: auto.ID, ToolUseID: "u1", Tool: "cluster_rollout_restart", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`), ExpiresIn: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	list := e.rawApprovals(t, "?status=pending")
	if len(list) != 1 {
		t.Fatalf("pending approvals = %d, want 1: %v", len(list), list)
	}
	raw, ok := list[0]["expiresAt"].(string)
	if !ok {
		t.Fatalf("approval has no expiresAt: %v", list[0])
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(at); d < 14*time.Minute || d > 16*time.Minute {
		t.Errorf("expiresAt = %v, %v from now, want about 15m", at, d)
	}

	// After the expiry the call is in the full list as abandoned, with the reason.
	if ok, err := e.st.ExpireCall(ctx, c.ID, at.Add(time.Second)); !ok || err != nil {
		t.Fatalf("ExpireCall = %v, %v", ok, err)
	}
	if pending := e.rawApprovals(t, "?status=pending"); len(pending) != 0 {
		t.Errorf("pending after the expiry = %v", pending)
	}
	all := e.rawApprovals(t, "?status=all")
	if len(all) != 1 {
		t.Fatalf("all approvals = %d, want 1", len(all))
	}
	got := all[0]
	if got["status"] != "abandoned" || got["decision"] != "abandoned" {
		t.Errorf("expired approval = %v, want status and decision abandoned", got)
	}
	if msg, _ := got["error"].(string); !strings.HasPrefix(msg, "no decision within") {
		t.Errorf("error = %q, want a text starting with \"no decision within\"", msg)
	}
}

func TestTheApprovalAPIHasNoDeadlineForARunThatAClickStarted(t *testing.T) {
	e := newGateEnv(t)
	id, _ := e.toolRun(t)
	if _, _, err := e.st.BeginToolCall(context.Background(), store.NewToolCall{RunID: id, ToolUseID: "u1", Tool: "cluster_rollout_restart", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`), ExpiresIn: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	list := e.rawApprovals(t, "?status=pending")
	if len(list) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(list))
	}
	if _, has := list[0]["expiresAt"]; has {
		t.Errorf("a manual run's approval has expiresAt: %v", list[0])
	}
}
