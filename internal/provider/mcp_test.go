package provider_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
)

func argAfter(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func TestARunWithToolsDropsSafeModeAndNamesItsConfig(t *testing.T) {
	cmd := provider.Claude{}.Command(context.Background(),
		provider.Spec{Prompt: "p", Workdir: t.TempDir(), MCPConfig: "/runs/x-mcp-1/mcp.json"}, nil)

	if slices.Contains(cmd.Args, "--safe-mode") {
		t.Fatalf("Args = %v: --safe-mode turns MCP off, a run with tools must not have it", cmd.Args)
	}
	for _, flag := range []string{"--restricted", "--strict-mcp-config"} {
		if !slices.Contains(cmd.Args, flag) {
			t.Errorf("Args = %v lacks %s", cmd.Args, flag)
		}
	}
	if got := argAfter(cmd.Args, "--mcp-config"); got != "/runs/x-mcp-1/mcp.json" {
		t.Errorf("--mcp-config = %q", got)
	}
	if got := argAfter(cmd.Args, "--allowedTools"); got != "mcp__"+provider.MCPServerName {
		t.Errorf("--allowedTools = %q, want the tools of the remedy server", got)
	}
	// The rest of the isolation stays.
	if got := argAfter(cmd.Args, "--tools"); got != "Read,Grep,Glob" {
		t.Errorf("--tools = %q", got)
	}
	if got := argAfter(cmd.Args, "--model"); got != "sonnet" {
		t.Errorf("--model = %q", got)
	}
	if got := argAfter(cmd.Args, "--permission-mode"); got != "dontAsk" {
		t.Errorf("--permission-mode = %q", got)
	}
}

func TestARunWithoutToolsKeepsSafeModeAndHasNoMCP(t *testing.T) {
	cmd := provider.Claude{}.Command(context.Background(), provider.Spec{Prompt: "p", Workdir: t.TempDir()}, nil)
	if !slices.Contains(cmd.Args, "--safe-mode") {
		t.Fatalf("Args = %v lacks --safe-mode", cmd.Args)
	}
	for _, flag := range []string{"--mcp-config", "--allowedTools"} {
		if slices.Contains(cmd.Args, flag) {
			t.Fatalf("Args = %v has %s without tools", cmd.Args, flag)
		}
	}
}

func TestTheServerIsCalledRemedy(t *testing.T) {
	if provider.MCPServerName != "remedy" {
		t.Fatalf("MCPServerName = %q: the tools of the gatekeeper are named mcp__remedy__<tool>", provider.MCPServerName)
	}
}
