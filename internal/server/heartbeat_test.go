package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestHeartbeatAnswersWhetherTheRunWaitsOrWasCancelled(t *testing.T) {
	e := newGateEnv(t)
	runID, _ := e.toolRun(t)
	path := "/runner/v1/runs/" + runID + "/heartbeat"

	// Only the runner token opens it.
	resp, err := http.Post(e.ts.URL+path, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a heartbeat without the runner token = %d, want 401", resp.StatusCode)
	}

	beat := func() (waiting, cancel bool) {
		code, body := e.runner(t, http.MethodPost, path, "")
		var got struct {
			Waiting bool `json:"waiting"`
			Cancel  bool `json:"cancel"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
			t.Fatalf("heartbeat = %d %s", code, body)
		}
		return got.Waiting, got.Cancel
	}
	if waiting, cancel := beat(); waiting || cancel {
		t.Fatalf("a quiet run: waiting %v, cancel %v", waiting, cancel)
	}

	_, _, err = e.st.BeginToolCall(context.Background(), store.NewToolCall{
		RunID: runID, ToolUseID: "toolu_1", Tool: "incident_add_note", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if waiting, _ := beat(); !waiting {
		t.Fatal("a run with a pending approval is not reported as waiting")
	}

	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+runID+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("cancel = %d", code)
	}
	if _, cancel := beat(); !cancel {
		t.Fatal("a cancelled run is not reported as cancelled")
	}
}

func TestHeartbeatOfARunThatIsGoneIs404(t *testing.T) {
	e := newGateEnv(t)
	runID, _ := e.toolRun(t)
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/no-such-run/heartbeat", ""); code != http.StatusNotFound {
		t.Fatalf("an unknown run = %d %s, want 404", code, body)
	}
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/finish", `{"exitCode":0,"result":"done"}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d %s", code, body)
	}
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/heartbeat", ""); code != http.StatusNotFound {
		t.Fatalf("a finished run = %d %s, want 404", code, body)
	}
}
