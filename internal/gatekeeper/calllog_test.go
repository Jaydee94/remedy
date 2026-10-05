package gatekeeper_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/store"
)

// lockedBuffer is a log destination that a handler goroutine and the test can both touch.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range strings.Split(l.b.String(), "\n") {
		if strings.Contains(line, `msg="tool call"`) {
			out = append(out, line)
		}
	}
	return out
}

func TestEveryToolCallLeavesADebugLineWithoutItsArguments(t *testing.T) {
	var logs lockedBuffer
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return nil }, func(c *gatekeeper.Config) {
		c.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})

	_, _ = resultText(t, e.call(t, "toolu_first", "echo", map[string]any{"text": "argument-that-must-not-be-logged"}))
	_, _ = resultText(t, e.call(t, "toolu_first", "echo", map[string]any{"text": "other-argument-that-must-not-be-logged"})) // the CLI replays a call
	_, _ = resultText(t, e.call(t, "toolu_refused", "echo", map[string]any{}))                                               // refused: no text
	_, _ = resultText(t, e.call(t, "toolu_unknown", "no_such_tool", map[string]any{"secret": "unknown-tool-argument"}))

	got := logs.lines()
	if len(got) != 4 {
		t.Fatalf("%d tool call lines, want 4 (one per call, also a replay and a refusal):\n%s", len(got), strings.Join(got, "\n"))
	}
	want := []struct{ useID, tool, replay, refused string }{
		{"toolu_first", "echo", "replay=false", "refused=false"},
		{"toolu_first", "echo", "replay=true", "refused=false"},
		{"toolu_refused", "echo", "replay=false", "refused=true"},
		{"toolu_unknown", "no_such_tool", "replay=false", "refused=true"},
	}
	for i, w := range want {
		for _, part := range []string{"level=DEBUG", "run=" + e.run.ID, "tool_use_id=" + w.useID, "tool=" + w.tool, w.replay, w.refused, "call="} {
			if !strings.Contains(got[i], part) {
				t.Errorf("line %d lacks %q: %s", i, part, got[i])
			}
		}
	}
	all := strings.Join(got, "\n")
	for _, secret := range []string{"argument-that-must-not-be-logged", "unknown-tool-argument", "text must not be empty"} {
		if strings.Contains(all, secret) {
			t.Errorf("the log shows %q, which came from the agent or from a refusal:\n%s", secret, all)
		}
	}
}

func TestToolCallLinesAreOnlyDebugLines(t *testing.T) {
	var logs lockedBuffer
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return nil }, func(c *gatekeeper.Config) {
		c.Log = slog.New(slog.NewTextHandler(&logs, nil)) // the default level is info
	})
	_, _ = resultText(t, e.call(t, "toolu_1", "echo", map[string]any{"text": "x"}))
	if got := logs.lines(); len(got) != 0 {
		t.Fatalf("an info logger shows the per-call lines: %v", got)
	}
}
