// Package provider adapts the official agent CLIs. An adapter only builds the command
// line and understands the CLI's output. It never touches credentials.
package provider

import (
	"context"
	"encoding/json"
	"os/exec"
)

// Spec describes one agent invocation.
type Spec struct {
	Prompt    string
	Workdir   string
	Schema    string // JSON schema of the answer; empty means free text
	MCPConfig string // MCP config file of a run with gatekeeper access; empty means no tools
}

// Final carries the end-of-run summary some CLIs emit as their last line.
type Final struct {
	Result    string
	SessionID string
	CostUSD   float64
	Output    json.RawMessage // the structured answer, when the run had a schema and the CLI delivered one
}

// Line is one parsed line of CLI output.
type Line struct {
	Kind    string          // event type, or "raw" for anything unparseable
	Payload json.RawMessage // always valid JSON
	Final   *Final          // set only on the end-of-run summary line
}

type Provider interface {
	Name() string
	// Command builds the subprocess. The prompt goes to stdin, never to argv.
	// parentEnv is the runner's own environment and is filtered by the adapter.
	Command(ctx context.Context, spec Spec, parentEnv []string) *exec.Cmd
	// ParseLine must not retain line; callers reuse the buffer.
	ParseLine(line []byte) Line
}

// envAllowlist names the variables a CLI subprocess may inherit in every mode. EnvFor adds the one variable of the mode:
// CLAUDE_CODE_OAUTH_TOKEN for the subscription, ANTHROPIC_API_KEY for the key. ANTHROPIC_API_KEY is absent here on purpose:
// in the subscription mode a key in the environment would make the CLI bill the API instead of the login.
// ANTHROPIC_AUTH_TOKEN and ANTHROPIC_BASE_URL are never allowed.
// USER is required on macOS: the CLI looks up its login in the keychain by user name, and
// without it reports "Not logged in" even though the login exists.
var envAllowlist = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LANG": true, "LC_ALL": true, "TMPDIR": true,
	"XDG_CONFIG_HOME": true, "HTTPS_PROXY": true, "HTTP_PROXY": true, "NO_PROXY": true,
	"CLAUDE_CONFIG_DIR": true,
}

// FilterEnv is EnvFor for the subscription mode.
func FilterEnv(env []string) []string { return EnvFor(ModeSubscription, env) }
