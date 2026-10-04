package gatekeeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// ErrNoWaiter means nobody waits for the answer of a call any more: its agent went away. A decision for it would run
// an action that nobody receives the result of.
var ErrNoWaiter = errors.New("the agent is no longer waiting for this call")

// executeTimeout bounds the execution of an approved tool.
const executeTimeout = 30 * time.Second

// waiterSet is the handlers that wait for the decision of one call, and the timer that abandons the call when the last
// of them has gone.
type waiterSet struct {
	chans map[chan struct{}]struct{}
	timer *time.Timer
}

// attach registers a waiter of a call. The channel is signalled when the call was decided. detach unregisters it;
// settled says the waiter delivered a final answer, so the call needs no watching any more. A call that loses its last
// waiter without being settled is abandoned after the grace period, unless a replay attaches in the meantime.
func (g *Gatekeeper) attach(id int64) (wake <-chan struct{}, detach func(settled bool)) {
	ch := make(chan struct{}, 1)
	g.mu.Lock()
	set := g.waiters[id]
	if set == nil {
		set = &waiterSet{chans: map[chan struct{}]struct{}{}}
		g.waiters[id] = set
	}
	if set.timer != nil {
		set.timer.Stop()
		set.timer = nil
	}
	set.chans[ch] = struct{}{}
	g.mu.Unlock()

	return ch, func(settled bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		set := g.waiters[id]
		if set == nil {
			return
		}
		delete(set.chans, ch)
		if len(set.chans) > 0 {
			return
		}
		if settled {
			delete(g.waiters, id)
			return
		}
		set.timer = time.AfterFunc(g.grace, func() { g.expire(id) })
	}
}

// expire abandons a call that nobody waits for any more.
func (g *Gatekeeper) expire(id int64) {
	g.mu.Lock()
	set := g.waiters[id]
	if set != nil && len(set.chans) > 0 {
		g.mu.Unlock()
		return // a replay attached in time
	}
	delete(g.waiters, id)
	g.mu.Unlock()
	if _, err := g.store.AbandonCall(context.Background(), id); err != nil {
		g.log.Error("could not abandon a call", "call", id, "err", err)
	}
}

// Waiting reports whether a handler waits for the decision of a call: the agent is still there to receive the result.
func (g *Gatekeeper) Waiting(id int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	set := g.waiters[id]
	return set != nil && len(set.chans) > 0
}

func (g *Gatekeeper) notify(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if set := g.waiters[id]; set != nil {
		for ch := range set.chans {
			select {
			case ch <- struct{}{}:
			default: // a wake-up is already pending
			}
		}
	}
}

// Decide records the maintainer's decision for a call that waits for one, and wakes the handlers that wait. It
// returns ErrNoWaiter when nobody waits for the call, store.ErrNotFound for an unknown call and store.ErrNotPending
// when the call is not waiting for a decision.
func (g *Gatekeeper) Decide(ctx context.Context, id int64, approve bool, reason string) (store.ToolCall, error) {
	if !g.Waiting(id) {
		if _, err := g.store.GetToolCall(ctx, id); err != nil {
			return store.ToolCall{}, err
		}
		return store.ToolCall{}, ErrNoWaiter
	}
	c, err := g.store.DecideApproval(ctx, id, approve, reason)
	if err != nil {
		return store.ToolCall{}, err
	}
	g.notify(id)
	return c, nil
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// awaitDecision answers a call of a mutating tool with an event stream and waits for the decision. It sends a progress
// notification every ProgressInterval, so that the CLI does not give up on the call, and ends with the result.
func (g *Gatekeeper) awaitDecision(w http.ResponseWriter, r *http.Request, req request, tool Tool, call store.ToolCall, token json.RawMessage) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, failure(req.ID, codeInternal, "streaming is not supported"))
		return
	}
	// The waiter is registered before the client sees the headers: a client that has them knows the call has a waiter.
	wake, detach := g.attach(call.ID)
	settled := false
	defer func() { detach(settled) }()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		flusher.Flush()
	}
	settle := func(v toolResult) { send(result(req.ID, v)) }

	ctx := context.WithoutCancel(r.Context()) // what a decision starts must not be cut off by the client going away
	tick := time.NewTicker(g.progress)
	defer tick.Stop()
	var progress float64
	for {
		current, err := g.store.GetToolCall(ctx, call.ID)
		if err != nil {
			g.log.Error("could not read a waiting call", "call", call.ID, "err", err)
			settle(errorResult("could not read the state of the call"))
			settled = true
			return
		}
		switch {
		case current.Status == store.CallSucceeded:
			settle(textResult(current.Result))
			settled = true
			return
		case current.Status == store.CallFailed || current.Status == store.CallDenied || current.Status == store.CallAbandoned:
			settle(errorResult(current.Error))
			settled = true
			return
		case current.Status == store.CallWaiting && current.Decision == store.DecisionApproved:
			ran, won, err := g.store.BeginExecution(ctx, call.ID)
			if err != nil {
				g.log.Error("could not start an approved call", "call", call.ID, "err", err)
			}
			if won {
				settle(g.execute(ctx, tool, ran))
				settled = true
				return
			}
			// Another handler executes it, or the run ended meanwhile: look again.
		}

		select {
		case <-wake:
		case <-tick.C:
			progress++
			if len(token) > 0 {
				send(notification{JSONRPC: "2.0", Method: "notifications/progress", Params: map[string]any{
					"progressToken": token, "progress": progress, "message": "waiting for approval",
				}})
			} else {
				fmt.Fprint(w, ": waiting\n\n")
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

// execute runs an approved call with the arguments that were stored when the approval was asked for, and records the
// outcome.
func (g *Gatekeeper) execute(ctx context.Context, tool Tool, call store.ToolCall) toolResult {
	ctx, cancel := context.WithTimeout(ctx, executeTimeout)
	defer cancel()
	text, err := tool.Run(ctx, Call{RunID: call.RunID, CallID: call.ID, Args: call.Arguments})
	if err != nil {
		g.log.Warn("an approved tool failed", "run", call.RunID, "tool", call.Tool, "err", err)
		msg := toolErrorMessage(err)
		_ = g.store.FinishToolCall(ctx, call.ID, store.CallFailed, "", msg)
		g.notify(call.ID)
		return errorResult(msg)
	}
	text = sanitize(text)
	if err := g.store.FinishToolCall(ctx, call.ID, store.CallSucceeded, text, ""); err != nil {
		g.log.Error("could not record the result of an approved tool", "call", call.ID, "err", err)
	}
	g.notify(call.ID) // the other waiters of the call (replays) read the result now, not at the next tick
	return textResult(text)
}
