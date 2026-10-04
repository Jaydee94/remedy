package provider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// DefaultModel is used when Claude.Model is empty.
const DefaultModel = "sonnet"

// MCPServerName is the name the gatekeeper has in the MCP config of a run. The CLI calls its tools
// mcp__remedy__<tool>.
const MCPServerName = "remedy"

// readOnlyTools is everything an agent may use for now. Roles that need more (the fixer) will name
// their own list in a later phase.
const readOnlyTools = "Read,Grep,Glob"

// Claude runs the unmodified `claude` binary in headless mode on the subscription login.
type Claude struct {
	Binary string // defaults to "claude" on PATH
	Model  string // defaults to DefaultModel
}

func (Claude) Name() string { return "claude" }

func (c Claude) Command(ctx context.Context, spec Spec, parentEnv []string) *exec.Cmd {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	model := c.Model
	if model == "" {
		model = DefaultModel
	}
	// Isolation (measured against the real CLI, see docs/research/spike-claude-billing.md):
	//   --safe-mode        disables the user's CLAUDE.md, plugins, hooks, MCP servers and skills. It also turns MCP
	//                      off for a server passed with --mcp-config (docs/research/spike-mcp-blocking.md), so a run
	//                      with gatekeeper access has to go without it; --restricted alone keeps the user's plugins out.
	//   --restricted       ignores user/project/local settings, removes command-running tools,
	//                      confines file tools to the working directory
	//   --strict-mcp-config ignores every MCP server not passed explicitly (none yet)
	//   --tools            an explicit allowlist, so no tool is exposed by accident
	//   --model            must be pinned: --restricted drops the user's default model
	// dontAsk denies whatever would still prompt. --bare is intentionally not used: it ignores
	// the subscription login.
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk"}
	if spec.MCPConfig == "" {
		args = append(args, "--safe-mode")
	}
	args = append(args, "--restricted", "--strict-mcp-config", "--tools", readOnlyTools, "--model", model)
	if spec.MCPConfig != "" {
		// Only the gatekeeper's tools, and all of them: what the agent may do is decided by the gatekeeper's registry
		// and its approvals, not by this flag.
		args = append(args, "--mcp-config", spec.MCPConfig, "--allowedTools", "mcp__"+MCPServerName)
	}
	// With a schema the CLI answers through structured output, and its result event carries the answer in
	// structured_output (docs/research/spike-structured-output.md). The control plane validates it again.
	if spec.Schema != "" {
		args = append(args, "--json-schema", spec.Schema)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = spec.Workdir
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Env = FilterEnv(parentEnv)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (Claude) ParseLine(line []byte) Line {
	var head struct {
		Type             string          `json:"type"`
		Result           string          `json:"result"`
		SessionID        string          `json:"session_id"`
		CostUSD          float64         `json:"total_cost_usd"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		return Line{Kind: "raw", Payload: run.JSONString(string(line))}
	}
	l := Line{Kind: head.Type, Payload: append(json.RawMessage(nil), line...)}
	if head.Type == "result" {
		l.Final = &Final{Result: head.Result, SessionID: head.SessionID, CostUSD: head.CostUSD}
		if len(head.StructuredOutput) > 0 && string(head.StructuredOutput) != "null" {
			l.Final.Output = append(json.RawMessage(nil), head.StructuredOutput...)
		}
	}
	return l
}
