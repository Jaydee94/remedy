package gatekeeper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/redact"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

// MaxResultBytes is the most a tool result may hold, in the audit row and in the answer.
const MaxResultBytes = 32 << 10

// sanitize is what a result goes through before it is stored and sent: secrets removed, size limited.
func sanitize(text string) string {
	return limitResult(redact.Redact(text))
}

func limitResult(s string) string {
	if len(s) <= MaxResultBytes {
		return s
	}
	cut := s[:MaxResultBytes]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1] // a character that the limit cut in two
			continue
		}
		break
	}
	return cut + "\n[cut: the result is longer than 32 KB]"
}

// storable makes the arguments of a call that was refused safe to store: valid JSON and not huge.
func storable(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	if len(raw) <= 8192 && json.Valid(raw) {
		return raw
	}
	text := string(raw)
	if len(text) > 2000 {
		text = text[:2000]
	}
	b, _ := json.Marshal(text)
	return b
}

// toolErrorMessage is what the agent is told about an error of a tool: its own mistakes, or "the tool failed".
func toolErrorMessage(err error) string {
	var arg ArgumentError
	if errors.As(err, &arg) {
		return arg.Error()
	}
	return "the tool failed; this is a problem of the control plane, not of the arguments"
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      struct {
		ToolUseID     string          `json:"claudecode/toolUseId"`
		ProgressToken json.RawMessage `json:"progressToken"`
	} `json:"_meta"`
}

// callTool handles tools/call: it audits the call, validates the arguments, runs a read tool at once and lets a
// mutating tool wait for the maintainer's approval.
func (g *Gatekeeper) callTool(w http.ResponseWriter, r *http.Request, rn run.Run, req request) {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		writeJSON(w, http.StatusOK, failure(req.ID, codeInvalidParams, "tools/call needs a tool name"))
		return
	}
	reply := func(v toolResult) { writeJSON(w, http.StatusOK, result(req.ID, v)) }
	if p.Meta.ToolUseID == "" {
		reply(errorResult("the request has no _meta claudecode/toolUseId, so the call cannot be identified"))
		return
	}

	// Every call is audited, also one that is refused. A refused call is recorded as a read call, whatever the tool:
	// it never asks for an approval.
	tool, known := g.byName[p.Name]
	if known && !tool.offeredTo(rn) {
		tool, known = Tool{}, false // not offered to this run: the same answer as for a tool that does not exist
	}
	args := p.Arguments
	var refused error
	if known {
		args, refused = tool.Decode(p.Arguments)
		if refused == nil && tool.Mutating && tool.Check != nil {
			refused = tool.Check(r.Context(), args)
		}
	} else {
		refused = ArgumentError("unknown tool " + p.Name)
	}
	kind := store.CallKindRead
	if refused == nil && tool.Mutating {
		kind = store.CallKindMutating
	}
	n := store.NewToolCall{RunID: rn.ID, ToolUseID: p.Meta.ToolUseID, Tool: p.Name, Kind: kind, Arguments: storable(args)}
	if refused == nil && known && tool.Incident != nil {
		n.IncidentID = tool.Incident(args)
	}
	call, existed, err := g.store.BeginToolCall(r.Context(), n)
	switch {
	case errors.Is(err, store.ErrCallLimit):
		reply(errorResult(err.Error()))
		return
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "the run is not running"})
		return
	case err != nil:
		g.log.Error("could not record a tool call", "run", rn.ID, "tool", p.Name, "err", err)
		writeJSON(w, http.StatusOK, failure(req.ID, codeInternal, "could not record the call"))
		return
	}

	// One line per call that reaches the audit log: a replay (the CLI sends a call in flight again after SIGTERM) and a
	// refusal are visible here (refused is about this request; the row keeps the answer of the first). Neither the arguments
	// nor the refusal text go in: they are the agent's.
	g.log.Debug("tool call", "run", rn.ID, "tool", p.Name, "tool_use_id", p.Meta.ToolUseID, "call", call.ID,
		"kind", string(kind), "replay", existed, "refused", refused != nil)

	// The context of the store writes that end a call: the client going away must not leave the row running.
	done := context.WithoutCancel(r.Context())
	if existed {
		if call.Kind == store.CallKindMutating && call.Status == store.CallWaiting {
			g.awaitDecision(w, r, req, g.byName[call.Tool], call, p.Meta.ProgressToken)
			return
		}
		g.answerKnown(r.Context(), reply, call)
		return
	}
	if refused != nil {
		msg := toolErrorMessage(refused)
		_ = g.store.FinishToolCall(done, call.ID, store.CallFailed, "", msg)
		reply(errorResult(msg))
		return
	}
	if tool.Mutating {
		g.awaitDecision(w, r, req, tool, call, p.Meta.ProgressToken)
		return
	}

	text, err := tool.Run(r.Context(), Call{RunID: rn.ID, CallID: call.ID, Args: args, RequestedAt: call.CreatedAt})
	if err != nil {
		g.log.Warn("a tool failed", "run", rn.ID, "tool", p.Name, "err", err)
		msg := toolErrorMessage(err)
		_ = g.store.FinishToolCall(done, call.ID, store.CallFailed, "", msg)
		reply(errorResult(msg))
		return
	}
	text = sanitize(text)
	if err := g.store.FinishToolCall(done, call.ID, store.CallSucceeded, text, ""); err != nil {
		g.log.Error("could not record a tool result", "run", rn.ID, "tool", p.Name, "err", err)
	}
	reply(textResult(text))
}

// answerKnown answers a repeat of a call with the state of the first one. A call that is still running (a slow tool
// that was repeated) is waited for.
func (g *Gatekeeper) answerKnown(ctx context.Context, reply func(toolResult), call store.ToolCall) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		switch call.Status {
		case store.CallSucceeded:
			reply(textResult(call.Result))
			return
		case store.CallFailed, store.CallDenied, store.CallAbandoned:
			reply(errorResult(call.Error))
			return
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
		next, err := g.store.GetToolCall(ctx, call.ID)
		if err != nil {
			return
		}
		call = next
	}
}
