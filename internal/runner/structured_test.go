package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

func TestExecuteReportsTheStructuredOutput(t *testing.T) {
	sink := &recordingSink{}
	schema := `{"type":"object"}`
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: testutil.FakeClaudeStructured(t, `{"summary":"s","n":2}`)},
		provider.Spec{Prompt: "x", Workdir: t.TempDir(), Schema: schema}, os.Environ(), sink)
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 0 || string(out.Output) != `{"summary":"s","n":2}` {
		t.Fatalf("outcome = %+v, output %s", out, out.Output)
	}

	var probe struct{ Args string }
	for _, e := range sink.events {
		if e.Kind == "probe" {
			if err := json.Unmarshal([]byte(e.Payload), &probe); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !strings.Contains(probe.Args, "--json-schema "+schema) {
		t.Errorf("the CLI did not get the schema: %q", probe.Args)
	}
}

func TestExecuteWithoutStructuredOutputHasNone(t *testing.T) {
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: testutil.FakeClaude(t, 0)},
		provider.Spec{Prompt: "x", Workdir: t.TempDir()}, os.Environ(), &recordingSink{})
	if err != nil || out.Output != nil {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
}
