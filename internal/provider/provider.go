// Package provider adapts the official agent CLIs. An adapter only builds the command
// line and understands the CLI's output. It never touches credentials.
package provider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// Spec describes one agent invocation.
type Spec struct {
	Prompt  string
	Workdir string
}

// Final carries the end-of-run summary some CLIs emit as their last line.
type Final struct {
	Result    string
	SessionID string
	CostUSD   float64
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

// envAllowlist names the only variables a CLI subprocess may inherit.
// ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN are deliberately absent: with them set the CLI
// would bill the API instead of the subscription login.
var envAllowlist = map[string]bool{
	"PATH": true, "HOME": true, "LANG": true, "LC_ALL": true, "TMPDIR": true,
	"XDG_CONFIG_HOME": true, "HTTPS_PROXY": true, "HTTP_PROXY": true, "NO_PROXY": true,
	"CLAUDE_CONFIG_DIR": true, "CLAUDE_CODE_OAUTH_TOKEN": true,
}

// FilterEnv keeps only allowlisted KEY=value entries, preserving order.
func FilterEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && envAllowlist[key] {
			out = append(out, kv)
		}
	}
	return out
}
