package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

type recordedEvent struct {
	Kind    string
	Payload string
}

type recordingSink struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (s *recordingSink) Event(_ context.Context, kind string, payload json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, recordedEvent{kind, string(payload)})
	return nil
}

func (s *recordingSink) stdoutKinds() []string {
	var kinds []string
	for _, e := range s.events {
		if e.Kind != "stderr" {
			kinds = append(kinds, e.Kind)
		}
	}
	return kinds
}

func TestExecuteForwardsLinesAndReportsOutcome(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-leak")
	sink := &recordingSink{}
	p := provider.Claude{Binary: testutil.FakeClaude(t, 3)}

	out, err := runner.Execute(context.Background(), p,
		provider.Spec{Prompt: "hello", Workdir: t.TempDir()}, os.Environ(), sink)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if out.ExitCode != 3 || out.Result != "done" || out.SessionID != "s-1" || out.CostUSD != 0.0123 {
		t.Fatalf("outcome = %+v", out)
	}

	want := []string{"system", "probe", "raw", "result"}
	got := sink.stdoutKinds()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stdout kinds = %v, want %v", got, want)
	}

	var probe, stderr string
	var stderrCount int
	for _, e := range sink.events {
		switch e.Kind {
		case "probe":
			probe = e.Payload
		case "stderr":
			stderr = e.Payload
			stderrCount++
		}
	}
	if !strings.Contains(probe, `"prompt":"hello"`) {
		t.Errorf("prompt did not arrive on stdin: %s", probe)
	}
	if !strings.Contains(probe, `"api_key":"unset"`) {
		t.Errorf("ANTHROPIC_API_KEY leaked into the subprocess: %s", probe)
	}
	if stderrCount != 1 || stderr != `"warn: something"` {
		t.Errorf("stderr events = %d (%s)", stderrCount, stderr)
	}
}

func TestExecuteSuccessExitCode(t *testing.T) {
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: testutil.FakeClaude(t, 0)},
		provider.Spec{Prompt: "x", Workdir: t.TempDir()}, os.Environ(), &recordingSink{})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestExecuteStartFailure(t *testing.T) {
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: "/nonexistent/claude"},
		provider.Spec{Prompt: "x", Workdir: t.TempDir()}, os.Environ(), &recordingSink{})
	if err == nil {
		t.Fatal("expected a start error")
	}
	if out.ExitCode != 127 || !strings.Contains(out.Result, "failed to start") {
		t.Fatalf("outcome = %+v", out)
	}
}
