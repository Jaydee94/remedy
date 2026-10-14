package gatekeeper_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/store"
)

// lateTool is a mutating tool whose Run ends only after the time allowed for it has run out.
func lateTool(name string, fail bool) gatekeeper.Tool {
	return gatekeeper.Tool{
		Name:        name,
		Description: "Ends late.",
		Mutating:    true,
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Activity: func(json.RawMessage) string { return "Ended late" },
		Run: func(ctx context.Context, _ gatekeeper.Call) (string, error) {
			<-ctx.Done()
			if fail {
				return "", errors.New("late")
			}
			return "late but done", nil
		},
	}
}

// What an approved tool did is recorded also when its time ran out while it was running: the row must not stay "running"
// until the run ends.
func TestTheOutcomeOfAToolThatRanOutOfTimeIsStillRecorded(t *testing.T) {
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool {
		return []gatekeeper.Tool{lateTool("late_fail", true), lateTool("late_ok", false)}
	}, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
		c.ExecuteTimeout = 50 * time.Millisecond
	})
	for name, want := range map[string]string{"late_fail": store.CallFailed, "late_ok": store.CallSucceeded} {
		s := e.open(t, "toolu_"+name, name, map[string]any{})
		call := e.pending(t)
		if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
			t.Fatal(err)
		}
		s.result()
		got, err := e.st.GetToolCall(context.Background(), call.ID)
		if err != nil || got.Status != want {
			t.Errorf("%s: status = %q, %v; want %q", name, got.Status, err, want)
		}
	}
}
