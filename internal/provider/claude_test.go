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

	wantArgs := []string{
		"/opt/claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk",
		"--safe-mode", "--restricted", "--strict-mcp-config", "--tools", "Read,Grep,Glob", "--model", "sonnet",
	}
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

// The CLI must not inherit the maintainer's global setup (MCP servers, hooks, plugins, settings) and
// must only get read-only tools. Measured against the real CLI: this cut 138 tools and 5 MCP servers
// down to 3 tools and none, and a trivial run from ~23k to ~4k tokens.
func TestClaudeCommandIsolatesTheCLIAndGrantsOnlyReadOnlyTools(t *testing.T) {
	cmd := provider.Claude{}.Command(context.Background(), provider.Spec{Prompt: "x"}, nil)

	for _, flag := range []string{"--safe-mode", "--restricted", "--strict-mcp-config"} {
		if !slices.Contains(cmd.Args, flag) {
			t.Errorf("missing isolation flag %s in %v", flag, cmd.Args)
		}
	}

	i := slices.Index(cmd.Args, "--tools")
	if i < 0 || i+1 >= len(cmd.Args) {
		t.Fatalf("--tools is not set: %v", cmd.Args)
	}
	for _, tool := range strings.Split(cmd.Args[i+1], ",") {
		if !slices.Contains([]string{"Read", "Grep", "Glob"}, tool) {
			t.Errorf("tool %q is not read-only", tool)
		}
	}
}

// --restricted ignores the user's settings, which also drops their default model, and the CLI then
// falls back to a different (more expensive) one. The model must therefore always be pinned.
func TestClaudeCommandPinsTheModel(t *testing.T) {
	model := func(c provider.Claude) string {
		args := c.Command(context.Background(), provider.Spec{}, nil).Args
		i := slices.Index(args, "--model")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("--model is not set: %v", args)
		}
		return args[i+1]
	}

	if got := model(provider.Claude{}); got != "sonnet" {
		t.Errorf("default model = %q, want sonnet", got)
	}
	if got := model(provider.Claude{Model: "opus"}); got != "opus" {
		t.Errorf("configured model = %q, want opus", got)
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
