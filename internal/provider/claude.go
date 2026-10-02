package provider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// Claude runs the unmodified `claude` binary in headless mode on the subscription login.
type Claude struct {
	Binary string // defaults to "claude" on PATH
}

func (Claude) Name() string { return "claude" }

func (c Claude) Command(ctx context.Context, spec Spec, parentEnv []string) *exec.Cmd {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	// dontAsk denies everything that would prompt, so without --allowedTools the agent is read-only.
	// --bare is intentionally not used: it ignores the subscription login.
	cmd := exec.CommandContext(ctx, bin,
		"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk")
	cmd.Dir = spec.Workdir
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Env = FilterEnv(parentEnv)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (Claude) ParseLine(line []byte) Line {
	var head struct {
		Type      string  `json:"type"`
		Result    string  `json:"result"`
		SessionID string  `json:"session_id"`
		CostUSD   float64 `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		return Line{Kind: "raw", Payload: run.JSONString(string(line))}
	}
	l := Line{Kind: head.Type, Payload: append(json.RawMessage(nil), line...)}
	if head.Type == "result" {
		l.Final = &Final{Result: head.Result, SessionID: head.SessionID, CostUSD: head.CostUSD}
	}
	return l
}
