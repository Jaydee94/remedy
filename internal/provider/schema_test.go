package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
)

func TestClaudeCommandPassesTheSchemaAsOneArgument(t *testing.T) {
	schema := `{"type":"object","properties":{"a":{"type":"string","enum":["x y","z"]}}}`
	cmd := provider.Claude{}.Command(context.Background(), provider.Spec{Prompt: "diagnose", Schema: schema}, nil)

	i := slices.Index(cmd.Args, "--json-schema")
	if i < 0 || i+2 != len(cmd.Args) || cmd.Args[i+1] != schema {
		t.Fatalf("Args = %v, want the schema as the last argument, in one piece", cmd.Args)
	}
	if stdin, _ := io.ReadAll(cmd.Stdin); string(stdin) != "diagnose" {
		t.Fatalf("stdin = %q: the schema must not travel with the prompt", stdin)
	}
	// The isolation does not depend on the schema.
	for _, flag := range []string{"--safe-mode", "--restricted", "--strict-mcp-config"} {
		if !slices.Contains(cmd.Args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
}

func TestClaudeCommandWithoutASchemaHasNoSchemaFlag(t *testing.T) {
	cmd := provider.Claude{}.Command(context.Background(), provider.Spec{Prompt: "x"}, nil)
	if slices.Contains(cmd.Args, "--json-schema") {
		t.Fatalf("Args = %v", cmd.Args)
	}
}

func TestClaudeParseLineReturnsTheStructuredOutput(t *testing.T) {
	c := provider.Claude{}

	res := c.ParseLine([]byte(`{"type":"result","subtype":"success","result":"{\"a\":1}","session_id":"s","total_cost_usd":0.5,"structured_output":{"a": 1,"b":["x"]}}`))
	if res.Final == nil || res.Final.Output == nil {
		t.Fatalf("final = %+v", res.Final)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Final.Output, &got); err != nil || got["a"] != float64(1) {
		t.Fatalf("output = %s, %v", res.Final.Output, err)
	}

	for name, line := range map[string]string{
		"null":   `{"type":"result","result":"x","structured_output":null}`,
		"absent": `{"type":"result","result":"x"}`,
	} {
		if r := c.ParseLine([]byte(line)); r.Final == nil || r.Final.Output != nil {
			t.Errorf("%s: final = %+v, want no output", name, r.Final)
		}
	}

	// Only the end-of-run line carries the answer; the tool call that produced it is just an event.
	call := c.ParseLine([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"StructuredOutput","input":{"a":1}}]}}`))
	if call.Final != nil {
		t.Fatalf("an assistant event is not the end of the run: %+v", call)
	}
}

func TestClaudeParseLineDoesNotRetainTheOutputBuffer(t *testing.T) {
	buf := []byte(`{"type":"result","result":"x","structured_output":{"a":1}}`)
	l := provider.Claude{}.ParseLine(buf)
	copy(buf, `{"type":"result","result":"x","structured_output":{"a":2}}`)
	if string(l.Final.Output) != `{"a":1}` {
		t.Fatalf("the output aliases the caller's buffer: %s", l.Final.Output)
	}
}
