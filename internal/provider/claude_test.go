package provider_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
)

func TestFilterEnvDropsAPIKeysAndKeepsAllowlist(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/r",
		"USER=remedy", // the macOS keychain lookup of the CLI login needs it
		"CLAUDE_CONFIG_DIR=/data/claude",
		"CLAUDE_CODE_OAUTH_TOKEN=tok",
		"ANTHROPIC_API_KEY=sk-secret",
		"ANTHROPIC_AUTH_TOKEN=bearer-secret",
		"GITHUB_TOKEN=ghp_secret",
		"REMEDY_RUNNER_TOKEN=runner-secret",
		"malformed-entry",
	}
	got := provider.FilterEnv(in)

	want := []string{"PATH=/usr/bin", "HOME=/home/r", "USER=remedy", "CLAUDE_CONFIG_DIR=/data/claude", "CLAUDE_CODE_OAUTH_TOKEN=tok"}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterEnv = %v, want %v", got, want)
	}
}

func TestClaudeCommand(t *testing.T) {
	c := provider.Claude{Binary: "/opt/claude"}
	cmd := c.Command(context.Background(), provider.Spec{Prompt: "fix it", Workdir: "/work"},
		[]string{"PATH=/bin", "ANTHROPIC_API_KEY=x"})

	wantArgs := []string{"/opt/claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk"}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("Args = %v, want %v", cmd.Args, wantArgs)
	}
	if cmd.Dir != "/work" {
		t.Fatalf("Dir = %q", cmd.Dir)
	}
	if !slices.Equal(cmd.Env, []string{"PATH=/bin"}) {
		t.Fatalf("Env = %v", cmd.Env)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	if string(stdin) != "fix it" {
		t.Fatalf("stdin = %q, want the prompt (never argv)", stdin)
	}
	if slices.Contains(cmd.Args, "--bare") {
		t.Fatal("--bare ignores the subscription login and must not be used")
	}
}

func TestClaudeCommandDefaultBinary(t *testing.T) {
	cmd := provider.Claude{}.Command(context.Background(), provider.Spec{}, nil)
	if !strings.HasSuffix(cmd.Args[0], "claude") {
		t.Fatalf("Args[0] = %q", cmd.Args[0])
	}
}

func TestClaudeParseLine(t *testing.T) {
	c := provider.Claude{}

	init := c.ParseLine([]byte(`{"type":"system","subtype":"init","session_id":"s-1"}`))
	if init.Kind != "system" || init.Final != nil {
		t.Fatalf("init = %+v", init)
	}

	res := c.ParseLine([]byte(`{"type":"result","subtype":"success","result":"pong","session_id":"s-1","total_cost_usd":0.0123}`))
	if res.Kind != "result" || res.Final == nil {
		t.Fatalf("result = %+v", res)
	}
	if res.Final.Result != "pong" || res.Final.SessionID != "s-1" || res.Final.CostUSD != 0.0123 {
		t.Fatalf("final = %+v", res.Final)
	}

	raw := c.ParseLine([]byte("not json at all"))
	if raw.Kind != "raw" || string(raw.Payload) != `"not json at all"` {
		t.Fatalf("raw = %+v", raw)
	}

	untyped := c.ParseLine([]byte(`{"foo":1}`))
	if untyped.Kind != "raw" {
		t.Fatalf("untyped JSON kind = %q, want raw", untyped.Kind)
	}
}

func TestClaudeParseLineDoesNotRetainBuffer(t *testing.T) {
	buf := []byte(`{"type":"assistant","n":1}`)
	l := provider.Claude{}.ParseLine(buf)
	copy(buf, `{"type":"XXXXXXXXX","n":2}`)
	if string(l.Payload) != `{"type":"assistant","n":1}` {
		t.Fatalf("payload aliases the caller's buffer: %s", l.Payload)
	}
}
