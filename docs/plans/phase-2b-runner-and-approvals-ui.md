# Phase 2b: Runner, Heartbeat, Approvals UI and the Real Run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An ad-hoc run with gatekeeper tools works from end to end: the runner hands the agent its tools, a run that waits for an approval is neither timed out nor reaped, the maintainer decides in a new Approvals page and can cancel a waiting run, and the whole thing is run once with the real CLI.

**Architecture:** The runner writes the MCP config (URL and run token) to a file the agent cannot read and starts the CLI without `--safe-mode`, which turns MCP off. While such a run executes, the runner sends a heartbeat; the answer says whether the run waits for an approval (the runner's time budget stands still) or was cancelled (the runner stops the CLI). The control plane's reaper fails a gatekeeper run that stops sending heartbeats. The UI gets an Approvals page with a count in the sidebar, and the run view shows the waiting state, the audit of tool calls and a cancel button.

**Tech Stack:** Go 1.27 stdlib only, SQLite, React 19 and the shadcn components that are already installed (no new dependencies).

**Spec:** [`docs/specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`](../specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md), sections 7 to 9 and steps 4 to 6 of section 11. The server side is [plan 2a](phase-2a-gatekeeper-server.md), implemented. The CLI's behaviour is in [`docs/research/spike-mcp-blocking.md`](../research/spike-mcp-blocking.md).

**Scope note:** This plan adds no tool and changes nothing about how a call is decided; it connects the pieces that plan 2a left open. The automatic responder keeps `--safe-mode` and no tools.

## Decisions made while planning

These refine the spec after reading the runner and the web code, and after trying the real CLI.

| Topic | Spec said | This plan |
|---|---|---|
| Which MCP tools the CLI may call | not mentioned | `--allowedTools mcp__remedy`. The real CLI accepted this server-level form and also `mcp__remedy__*`; with `--permission-mode dontAsk` a tool that is not allowed is denied. The gatekeeper stays the enforcement point: what the agent may do is decided by its registry and approvals, not by this flag. |
| Failure reason of a lost run | "runner lost" | a new reason `runner_lost`, next to `timeout` and `cancelled`, so the UI can say what happened. |
| The 15 minute rule | does not apply to gatekeeper runs | `FailStaleRuns` skips them (`mcp = 0`); `FailLostRuns` applies the heartbeat rule to them. |
| The run clock | "does not advance while waiting" | One clock for every run (`runClock`): a budget that can be paused. A run without gatekeeper access never pauses it, so its behaviour is the old one. The heartbeat interval (10 s by default) is the granularity of the pause. |
| A heartbeat that fails | not mentioned | The run goes on. A failed heartbeat (the control plane is down or slow) must never stop an agent; only the answer `cancel` or a 404 does, and a run that stays silent is the reaper's business. |
| A 404 to a heartbeat | not mentioned | The control plane no longer has the run as running (the reaper failed it, or it was finished): the runner stops the agent, as for a cancel. |
| Count in the sidebar | "the pending count" | The number of calls with a pending decision, refreshed every 5 seconds. A pending call whose agent no longer waits still counts until it is abandoned, which happens within the grace period. |
| Showing arguments | "escaped, in full" | As a list of names and values (a string value in its own block that keeps line breaks), so that a long note stays readable; React escapes everything. |
| The real run | the maintainer approves after more than 10 minutes | Done by the assistant against a database with a seeded incident (no GitHub needed). The leak check of plan 1d is reused for the run token. |

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, UI copy, commit messages.
- No new Go dependencies and no new web dependencies.
- The run token never appears in a log line, an event of a run, an API response (except the claim), an error message or a file the agent can read. The runner writes it to a file with mode 0600 in a directory of its own, outside the agent's workspace, and removes it when the run ends.
- A heartbeat that fails never stops a run. A run that has no gatekeeper access behaves exactly as before: `--safe-mode`, the old time limit, no heartbeat.
- Agent-written text (arguments, results, notes) is shown as escaped text in the UI.
- `web/tsconfig.app.json` keeps `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types.
- Every UI change is checked in a real browser (Playwright) before it is called done.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- `go test ./... -race -count=1` and `make check` must pass at the end of every task.

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/heartbeat.go`, `internal/store/reap.go` | `Heartbeat`, `FailLostRuns`; the stale rule leaves gatekeeper runs alone |
| `internal/reaper/reaper.go` | The heartbeat rule next to the 15 minute rule |
| `internal/server/heartbeat.go`, `internal/server/server.go` | `POST /runner/v1/runs/{id}/heartbeat` |
| `internal/provider/`, `internal/runner/mcpconfig.go` | The MCP config file and the CLI arguments of a run with tools |
| `internal/runner/heartbeat.go`, `internal/runner/loop.go` | The heartbeat client, the pausable run clock, cancelling |
| `internal/testutil/` | Fake `claude` scripts that record their arguments, and that finish after a delay |
| `web/src/` | Approvals page, sidebar count, run view with tool calls, the runs form |
| `docs/runbook/gatekeeper-real-run.md`, `docs/research/phase-2ab-real-run.md`, `README.md`, `CLAUDE.md`, `docs/specs/...`, `docs/design.md` | The real run and the status of the documents |

---

### Task 1: Heartbeats and the reaper rule for gatekeeper runs

**Files:**
- Create: `internal/store/heartbeat.go`, `internal/store/heartbeat_test.go`, `internal/server/heartbeat.go`, `internal/server/heartbeat_test.go`, `internal/reaper/lost_test.go`
- Overwrite: `internal/store/reap.go`, `internal/reaper/reaper.go`
- Modify: `internal/run/run.go`, `internal/server/server.go`, `internal/store/runtokens_test.go`

**Interfaces:**
- Consumes: `Store.inTx`, `formatTS`, `closeRunTx`, `claimedToolRun` and `waitingCall` (the store tests of plan 2a), `gateEnv` (the server tests of plan 2a), `store.ErrNotFound`.
- Produces:
  - `run.ReasonRunnerLost`.
  - `store.Beat{Waiting, Cancel bool}` and `(*Store).Heartbeat(ctx, runID string, now time.Time) (Beat, error)`: for a running run, records the time and answers whether the run has a call that waits for a decision and whether it was cancelled; `ErrNotFound` when the run is not running.
  - `(*Store).FailLostRuns(ctx, cutoff time.Time, result string) ([]string, error)`: fails the running runs **with gatekeeper access** whose last heartbeat (or, before the first, their start) is before `cutoff`, with the reason `runner_lost`; it also revokes their tokens and abandons their waiting calls.
  - `(*Store).FailStaleRuns` no longer touches runs with gatekeeper access.
  - `reaper.Reaper.HeartbeatMaxAge` (default `reaper.DefaultHeartbeatMaxAge`, 2 minutes); `Sweep` applies both rules and calls `OnFailed` with all failed IDs.
  - `POST /runner/v1/runs/{id}/heartbeat` (runner token): 200 `{"waiting": bool, "cancel": bool}`, 404 when the run is not running.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/heartbeat_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func TestHeartbeatSaysWhetherTheRunWaitsOrWasCancelled(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)

	beat, err := s.Heartbeat(ctx, r.ID, time.Now())
	if err != nil || beat.Waiting || beat.Cancel {
		t.Fatalf("a quiet run: %+v, %v", beat, err)
	}

	first := waitingCall(t, s, r, "w1")
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); !beat.Waiting {
		t.Fatalf("a run with a pending approval: %+v, want Waiting", beat)
	}
	if _, err := s.DecideApproval(ctx, first.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); beat.Waiting {
		t.Fatalf("after the decision: %+v, want it no longer waiting", beat)
	}

	// An approved call that has not run yet is not waiting for the maintainer any more.
	second := waitingCall(t, s, r, "w2")
	if _, err := s.DecideApproval(ctx, second.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); beat.Waiting {
		t.Fatalf("an approved call: %+v, want it no longer waiting", beat)
	}

	waitingCall(t, s, r, "w3")
	if _, err := s.CancelRun(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if beat, _ = s.Heartbeat(ctx, r.ID, time.Now()); !beat.Cancel || beat.Waiting {
		t.Fatalf("a cancelled run: %+v, want Cancel and no waiting (its call was abandoned)", beat)
	}
}

func TestHeartbeatOfARunThatIsNotRunning(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	queued, _ := s.CreateToolRun(ctx, "claude", "x")
	if _, err := s.Heartbeat(ctx, queued.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a queued run: %v, want ErrNotFound", err)
	}
	if _, err := s.Heartbeat(ctx, "no-such-run", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown run: %v, want ErrNotFound", err)
	}
	claimed, _ := s.ClaimNext(ctx)
	if err := s.FinishRun(ctx, claimed.ID, run.Outcome{Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(ctx, claimed.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a finished run: %v, want ErrNotFound", err)
	}
}

func TestFailLostRunsFailsAGatekeeperRunThatStoppedBeating(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	token, err := s.MintRunToken(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := waitingCall(t, s, r, "w")
	base := time.Now()

	// It started just now: not lost.
	if ids, err := s.FailLostRuns(ctx, base.Add(-time.Minute), "lost"); err != nil || len(ids) != 0 {
		t.Fatalf("a fresh run: %v, %v", ids, err)
	}
	// A heartbeat later than the cutoff keeps it alive, whatever its start was.
	if _, err := s.Heartbeat(ctx, r.ID, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.FailLostRuns(ctx, base.Add(time.Hour), "lost"); len(ids) != 0 {
		t.Fatalf("a run that heartbeats: %v, want none", ids)
	}

	ids, err := s.FailLostRuns(ctx, base.Add(3*time.Hour), "the runner is gone")
	if err != nil || len(ids) != 1 || ids[0] != r.ID {
		t.Fatalf("a silent run: %v, %v", ids, err)
	}
	got, _ := s.GetRun(ctx, r.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonRunnerLost || got.Result != "the runner is gone" || got.FinishedAt == nil {
		t.Fatalf("lost run = %+v", got)
	}
	if _, err := s.RunForToken(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the token of a lost run: %v", err)
	}
	if c, _ := s.GetToolCall(ctx, call.ID); c.Status != store.CallAbandoned {
		t.Fatalf("the waiting call of a lost run = %+v", c)
	}
}

func TestOnlyTheHeartbeatRuleAppliesToAGatekeeperRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	tool := claimedToolRun(t, s)
	if _, err := s.CreateRun(ctx, "claude", "plain"); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.ClaimNext(ctx)
	future := time.Now().Add(10 * time.Hour)

	lost, err := s.FailLostRuns(ctx, future, "lost")
	if err != nil || len(lost) != 1 || lost[0] != tool.ID {
		t.Fatalf("FailLostRuns = %v, %v, want only the gatekeeper run", lost, err)
	}

	// The stale rule is the other way round.
	again, _ := s.CreateToolRun(ctx, "claude", "again")
	_, _ = s.ClaimNext(ctx)
	stale, err := s.FailStaleRuns(ctx, future, "stale")
	if err != nil || len(stale) != 1 || stale[0] != plain.ID {
		t.Fatalf("FailStaleRuns = %v, %v, want only the plain run (not %s)", stale, err, again.ID)
	}
}
```

Create `internal/reaper/lost_test.go`:

```go
package reaper_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func toolRun(t *testing.T) (*store.Store, run.Run) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if _, err := st.CreateToolRun(ctx, "claude", "use the tools"); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	return st, *claimed
}

func TestAGatekeeperRunLivesAsLongAsItSendsHeartbeats(t *testing.T) {
	st, r := toolRun(t)
	ctx := context.Background()
	base := time.Now()
	now := base
	rp := &reaper.Reaper{
		Store: st, Log: quiet, MaxAge: 15 * time.Minute, HeartbeatMaxAge: 2 * time.Minute,
		Now: func() time.Time { return now },
	}

	// Half an hour with a heartbeat every minute: far beyond the 15 minute rule, which does not apply to it.
	for i := 1; i <= 30; i++ {
		now = base.Add(time.Duration(i) * time.Minute)
		if _, err := st.Heartbeat(ctx, r.ID, now); err != nil {
			t.Fatal(err)
		}
		if n, err := rp.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("sweep at minute %d = %d, %v, want it left alone", i, n, err)
		}
	}

	// The heartbeats stop.
	now = base.Add(33 * time.Minute)
	if n, err := rp.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep after three silent minutes = %d, %v, want 1", n, err)
	}
	got, _ := st.GetRun(ctx, r.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonRunnerLost {
		t.Fatalf("run = %+v", got)
	}
}

func TestAGatekeeperRunThatNeverBeatsIsLostToo(t *testing.T) {
	st, r := toolRun(t)
	base := time.Now()
	var mu sync.Mutex
	var failed []string
	rp := &reaper.Reaper{
		Store: st, Log: quiet, HeartbeatMaxAge: 2 * time.Minute,
		Now: func() time.Time { return base.Add(3 * time.Minute) }, // counted from its start
		OnFailed: func(_ context.Context, ids []string) {
			mu.Lock()
			defer mu.Unlock()
			failed = append(failed, ids...)
		},
	}
	if n, err := rp.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failed) != 1 || failed[0] != r.ID {
		t.Fatalf("OnFailed got %v, want the lost run", failed)
	}
}
```

Create `internal/server/heartbeat_test.go`:

```go
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestHeartbeatAnswersWhetherTheRunWaitsOrWasCancelled(t *testing.T) {
	e := newGateEnv(t)
	runID, _ := e.toolRun(t)
	path := "/runner/v1/runs/" + runID + "/heartbeat"

	// Only the runner token opens it.
	resp, err := http.Post(e.ts.URL+path, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a heartbeat without the runner token = %d, want 401", resp.StatusCode)
	}

	beat := func() (waiting, cancel bool) {
		code, body := e.runner(t, http.MethodPost, path, "")
		var got struct {
			Waiting bool `json:"waiting"`
			Cancel  bool `json:"cancel"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
			t.Fatalf("heartbeat = %d %s", code, body)
		}
		return got.Waiting, got.Cancel
	}
	if waiting, cancel := beat(); waiting || cancel {
		t.Fatalf("a quiet run: waiting %v, cancel %v", waiting, cancel)
	}

	_, _, err = e.st.BeginToolCall(context.Background(), store.NewToolCall{
		RunID: runID, ToolUseID: "toolu_1", Tool: "incident_add_note", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if waiting, _ := beat(); !waiting {
		t.Fatal("a run with a pending approval is not reported as waiting")
	}

	if code, _ := e.admin(t, http.MethodPost, "/api/runs/"+runID+"/cancel", ""); code != http.StatusNoContent {
		t.Fatalf("cancel = %d", code)
	}
	if _, cancel := beat(); !cancel {
		t.Fatal("a cancelled run is not reported as cancelled")
	}
}

func TestHeartbeatOfARunThatIsGoneIs404(t *testing.T) {
	e := newGateEnv(t)
	runID, _ := e.toolRun(t)
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/no-such-run/heartbeat", ""); code != http.StatusNotFound {
		t.Fatalf("an unknown run = %d %s, want 404", code, body)
	}
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/finish", `{"exitCode":0,"result":"done"}`); code != http.StatusNoContent {
		t.Fatalf("finish = %d %s", code, body)
	}
	if code, body := e.runner(t, http.MethodPost, "/runner/v1/runs/"+runID+"/heartbeat", ""); code != http.StatusNotFound {
		t.Fatalf("a finished run = %d %s, want 404", code, body)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store ./internal/reaper ./internal/server 2>&1 | head -12`
Expected: none of the three packages compiles (`s.Heartbeat undefined`, `s.FailLostRuns undefined`, `unknown field HeartbeatMaxAge`).

- [ ] **Step 3: The store**

The test of plan 2a that checks the revocation of a token when a run is reaped used a gatekeeper run and the stale rule. That rule no longer covers such runs.

In `internal/store/runtokens_test.go`, replace:

```go
			ids, err := s.FailStaleRuns(ctx, time.Now().Add(time.Hour), "stuck")
```

with:

```go
			ids, err := s.FailLostRuns(ctx, time.Now().Add(time.Hour), "stuck")
```

In `internal/run/run.go`, replace:

```go
	// ReasonCancelled means the maintainer cancelled the run. A runner reports it after stopping the agent.
	ReasonCancelled = "cancelled"
```

with:

```go
	// ReasonCancelled means the maintainer cancelled the run. A runner reports it after stopping the agent.
	ReasonCancelled = "cancelled"
	// ReasonRunnerLost means the control plane failed a run with gatekeeper access because its runner stopped
	// sending heartbeats.
	ReasonRunnerLost = "runner_lost"
```

Create `internal/store/heartbeat.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Beat is the answer to a heartbeat: what the runner of a run needs to know.
type Beat struct {
	// Waiting means a call of the run waits for the maintainer's decision. The runner does not count that time
	// against the run's time limit.
	Waiting bool
	// Cancel means the maintainer cancelled the run. The runner stops the agent.
	Cancel bool
}

// Heartbeat records that the runner of a running run is alive at now, and says whether the run waits for a decision
// and whether it was cancelled. It returns ErrNotFound when the run is not running (it was finished, or the reaper
// failed it): the runner then stops the agent.
func (s *Store) Heartbeat(ctx context.Context, runID string, now time.Time) (Beat, error) {
	var beat Beat
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var cancel int
		err := tx.QueryRowContext(ctx, `SELECT cancel_requested FROM runs WHERE id = ? AND status = 'running'`, runID).Scan(&cancel)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET last_heartbeat_at = ? WHERE id = ?`, formatTS(now), runID); err != nil {
			return err
		}
		var pending int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tool_calls WHERE run_id = ? AND decision = 'pending'`, runID).Scan(&pending); err != nil {
			return err
		}
		beat = Beat{Waiting: pending > 0, Cancel: cancel != 0}
		return nil
	})
	if err != nil {
		return Beat{}, err
	}
	return beat, nil
}
```

Overwrite `internal/store/reap.go`:

```go
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// failRuns fails the running runs that match cond (a constant SQL condition with args), closes each in the same
// transaction (its token, its waiting calls) and returns their IDs. result becomes a run's result text if it has none.
func (s *Store) failRuns(ctx context.Context, reason, result, cond string, condArgs ...any) ([]string, error) {
	var ids []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		rows, err := tx.QueryContext(ctx, `
			UPDATE runs SET status = 'failed', exit_code = -1, failure_reason = ?, finished_at = ?,
				result = CASE WHEN result = '' THEN ? ELSE result END
			WHERE status = 'running' AND `+cond+`
			RETURNING id`, append([]any{reason, formatTS(now), result}, condArgs...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := closeRunTx(ctx, tx, id, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// FailStaleRuns fails the runs that are still running and started before cutoff: no runner ever finished them. It
// returns their IDs. A run with gatekeeper access is not subject to this rule: it may wait for an approval for as long
// as it takes, and FailLostRuns watches its runner instead.
func (s *Store) FailStaleRuns(ctx context.Context, cutoff time.Time, result string) ([]string, error) {
	return s.failRuns(ctx, run.ReasonTimeout, result, `mcp = 0 AND started_at < ?`, formatTS(cutoff))
}

// FailLostRuns fails the running runs with gatekeeper access whose runner has been silent since before cutoff: the
// last heartbeat, or, before the first one, the start of the run. It returns their IDs.
func (s *Store) FailLostRuns(ctx context.Context, cutoff time.Time, result string) ([]string, error) {
	return s.failRuns(ctx, run.ReasonRunnerLost, result,
		`mcp = 1 AND COALESCE(last_heartbeat_at, started_at) < ?`, formatTS(cutoff))
}
```

- [ ] **Step 4: The reaper and the route**

Overwrite `internal/reaper/reaper.go`:

```go
// Package reaper fails runs that stay "running" because their runner died or lost the connection.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const (
	// DefaultMaxAge is longer than the runner's own timeout (10 minutes), so the reaper only acts
	// when the runner could not report.
	DefaultMaxAge   = 15 * time.Minute
	DefaultInterval = time.Minute
	// DefaultHeartbeatMaxAge is how long the runner of a run with gatekeeper access may stay silent. Such a run is
	// not subject to MaxAge, because waiting for an approval can take hours; its runner sends a heartbeat every
	// ten seconds instead.
	DefaultHeartbeatMaxAge = 2 * time.Minute
)

type Reaper struct {
	Store           *store.Store
	MaxAge          time.Duration // default DefaultMaxAge
	HeartbeatMaxAge time.Duration // default DefaultHeartbeatMaxAge
	Interval        time.Duration // default DefaultInterval
	Log             *slog.Logger
	Now             func() time.Time // default time.Now

	// OnFailed is called with the IDs of the runs a sweep failed, for example to close the diagnosis of a
	// responder run.
	OnFailed func(ctx context.Context, ids []string)
}

// Sweep fails the runs that have been running for longer than MaxAge, and the runs with gatekeeper access whose
// runner has been silent for longer than HeartbeatMaxAge. It returns how many it failed.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	maxAge := r.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	silence := r.HeartbeatMaxAge
	if silence <= 0 {
		silence = DefaultHeartbeatMaxAge
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}

	stale, staleErr := r.Store.FailStaleRuns(ctx, now.Add(-maxAge),
		fmt.Sprintf("The run did not finish within %s and was failed by the control plane.", maxAge))
	for _, id := range stale {
		r.Log.Warn("failed a stale run", "run", id, "after", maxAge)
	}
	lost, lostErr := r.Store.FailLostRuns(ctx, now.Add(-silence),
		fmt.Sprintf("The runner stopped reporting for more than %s and the run was failed by the control plane.", silence))
	for _, id := range lost {
		r.Log.Warn("failed a run whose runner was lost", "run", id, "silent for", silence)
	}

	ids := append(stale, lost...)
	if len(ids) > 0 && r.OnFailed != nil {
		r.OnFailed(ctx, ids)
	}
	return len(ids), errors.Join(staleErr, lostErr)
}

// Run sweeps every Interval until ctx ends.
func (r *Reaper) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.Log.Error("reaper sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
```

Create `internal/server/heartbeat.go`:

```go
package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

// heartbeat is what the runner of a run with gatekeeper access calls every few seconds. The answer tells it whether
// the run waits for an approval (its time limit stands still) and whether it was cancelled (it stops the agent). A 404
// means the control plane no longer has the run as running, which the runner treats like a cancel.
func (s *srv) heartbeat(w http.ResponseWriter, r *http.Request) {
	beat, err := s.d.Store.Heartbeat(r.Context(), r.PathValue("id"), time.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "run not found or not running")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not record the heartbeat")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"waiting": beat.Waiting, "cancel": beat.Cancel})
	}
}
```

In `internal/server/server.go`, replace:

```go
	mux.HandleFunc("POST /runner/v1/runs/{id}/finish", s.runner(s.finish))
```

with:

```go
	mux.HandleFunc("POST /runner/v1/runs/{id}/finish", s.runner(s.finish))
	mux.HandleFunc("POST /runner/v1/runs/{id}/heartbeat", s.runner(s.heartbeat))
```

- [ ] **Step 5: Run the tests and watch them pass**

Run: `gofmt -l internal; go vet ./... && go test ./internal/store ./internal/reaper ./internal/server -race -count=1`
Expected: no output from `gofmt -l`, then `ok` for all three. If `gofmt -l` lists `internal/reaper/reaper.go`, run `gofmt -d` on it and apply the alignment it shows (the struct has a longer field name than before).

- [ ] **Step 6: Mutation checks**

Make each change, run `go test ./internal/store ./internal/reaper ./internal/server -count=1`, expect the named test to fail, and revert it.

1. In `FailStaleRuns`, change `mcp = 0 AND started_at < ?` to `started_at < ?`: `TestOnlyTheHeartbeatRuleAppliesToAGatekeeperRun` fails.
2. In `FailLostRuns`, change `COALESCE(last_heartbeat_at, started_at) < ?` to `started_at < ?`: `TestFailLostRunsFailsAGatekeeperRunThatStoppedBeating` fails (a run that beats is failed).
3. In `Heartbeat`, change `AND decision = 'pending'` to `AND decision != ''`: `TestHeartbeatSaysWhetherTheRunWaitsOrWasCancelled` fails (a decided call still counts as waiting).
4. In `Heartbeat`, change `UPDATE runs SET last_heartbeat_at = ? WHERE id = ?` to `UPDATE runs SET last_heartbeat_at = ? WHERE id = ? AND 0`: `TestAGatekeeperRunLivesAsLongAsItSendsHeartbeats` fails (the heartbeats are not recorded).
5. In `heartbeat` (server), change `case errors.Is(err, store.ErrNotFound):` to `case errors.Is(err, store.ErrNotFound) && false:`: `TestHeartbeatOfARunThatIsGoneIs404` fails.

- [ ] **Step 7: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal
git commit -m "feat: heartbeats for runs with gatekeeper access and a reaper rule that follows them" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 2: The runner gives a run its tools

**Files:**
- Create: `internal/provider/mcp_test.go`, `internal/runner/mcpconfig.go`, `internal/runner/mcpconfig_test.go`, `internal/testutil/recording.go`
- Modify: `internal/provider/provider.go`, `internal/provider/claude.go`, `internal/runner/loop.go`

**Interfaces:**
- Consumes: `run.Claim.MCPToken` (plan 2a), `provider.Spec`, `provider.Claude`, `runner.Loop`, `runner.Client.BaseURL`, `startLoop` and `waitTerminal` (the runner tests), `store.CreateToolRun`.
- Produces:
  - `provider.MCPServerName` (`"remedy"`) and `provider.Spec.MCPConfig`: the path of an MCP config file. When it is set the command line **has no `--safe-mode`** (it turns MCP off), and has `--mcp-config <path>` and `--allowedTools mcp__remedy`; `--restricted`, `--strict-mcp-config`, `--tools` and `--model` stay. When it is empty the command line is exactly what it was.
  - `runner.writeMCPConfig(root, runID, baseURL, token) (path string, cleanup func(), err error)`: a directory `root/<runID>-mcp-*` (mode 0700, a sibling of the workspace, not inside it) with `mcp.json` (mode 0600): `{"mcpServers":{"remedy":{"type":"http","url":"<baseURL>/mcp","headers":{"Authorization":"Bearer <token>"}}}}`. `cleanup` removes the directory.
  - The loop writes that file for a claim that carries `MCPToken`, passes it in the spec, and removes it when the run ends. A run whose file cannot be written fails with exit code 127 without running the agent.
  - `testutil.RecordingClaude(t, outFile)`: a fake `claude` that writes its arguments, working directory and the mode and content of its `--mcp-config` file to `outFile`.

- [ ] **Step 1: Write the failing tests**

Create `internal/provider/mcp_test.go`:

```go
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
```

Create `internal/runner/mcpconfig_test.go`:

```go
package runner_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// record reads what RecordingClaude wrote: one key=value per line.
func record(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the agent recorded nothing: %v", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestARunWithToolsGetsItsConfigInAFileTheAgentCannotReach(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "record.txt")
	st, ts := startLoop(t, map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.RecordingClaude(t, recorded)}})
	queued, err := st.CreateToolRun(context.Background(), "claude", "use the tools")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}

	rec := record(t, recorded)
	args := " " + rec["args"] + " "
	if strings.Contains(args, " --safe-mode ") || !strings.Contains(args, " --mcp-config ") || !strings.Contains(args, " --allowedTools mcp__remedy ") {
		t.Fatalf("args = %q", rec["args"])
	}
	if rec["mode"] != "600" || rec["dirmode"] != "700" {
		t.Fatalf("the config has mode %q in a directory of mode %q, want 600 and 700", rec["mode"], rec["dirmode"])
	}

	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(rec["body"]), &cfg); err != nil {
		t.Fatalf("the config is not JSON: %v\n%s", err, rec["body"])
	}
	server, ok := cfg.MCPServers["remedy"]
	if !ok || len(cfg.MCPServers) != 1 || server.Type != "http" || server.URL != ts.URL+"/mcp" {
		t.Fatalf("config = %+v, want one http server at %s/mcp", cfg, ts.URL)
	}
	bearer, found := strings.CutPrefix(server.Headers["Authorization"], "Bearer ")
	if !found || len(bearer) != 43 {
		t.Fatalf("Authorization = %q, want a bearer token of 43 characters", server.Headers["Authorization"])
	}

	// The file is outside the workspace of the agent, and gone when the run has ended.
	cfgPath := rec["cfg"]
	if filepath.Dir(cfgPath) == rec["cwd"] || strings.HasPrefix(cfgPath, rec["cwd"]+string(filepath.Separator)) ||
		filepath.Base(filepath.Dir(cfgPath)) == filepath.Base(rec["cwd"]) {
		t.Fatalf("the config %s is inside the workspace %s", cfgPath, rec["cwd"])
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("the config still exists after the run: %v", err)
	}

	// The token is nowhere in what the run reported.
	events, _ := st.Events(context.Background(), queued.ID, 0)
	for _, e := range events {
		if strings.Contains(string(e.Payload), bearer) {
			t.Fatalf("an event of the run contains the token: %s", e.Payload)
		}
	}
}

func TestARunWithoutToolsHasNoConfigAndKeepsSafeMode(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "record.txt")
	st, _ := startLoop(t, map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.RecordingClaude(t, recorded)}})
	queued, _ := st.CreateRun(context.Background(), "claude", "plain")
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}

	rec := record(t, recorded)
	args := " " + rec["args"] + " "
	if !strings.Contains(args, " --safe-mode ") || strings.Contains(args, " --mcp-config ") || rec["cfg"] != "" {
		t.Fatalf("args = %q, cfg = %q", rec["args"], rec["cfg"])
	}
}
```

Create `internal/testutil/recording.go`:

```go
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// RecordingClaude writes an executable script that mimics a `claude` that finishes at once and writes what it was
// started with to outFile, one key=value per line: its arguments (args), its working directory (cwd), the path
// of its --mcp-config file (cfg) and, when there is one, that file's mode (mode), its directory's mode (dirmode)
// and its content (body).
func RecordingClaude(t testing.TB, outFile string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
cfg=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--mcp-config" ]; then cfg="$a"; fi
  prev="$a"
done
{
  echo "args=$*"
  echo "cwd=$(pwd -P)"
  echo "cfg=$cfg"
  if [ -n "$cfg" ]; then
    echo "mode=$(stat -c %%a "$cfg" 2>/dev/null || stat -f %%Lp "$cfg")"
    echo "dirmode=$(stat -c %%a "$(dirname "$cfg")" 2>/dev/null || stat -f %%Lp "$(dirname "$cfg")")"
    echo "body=$(cat "$cfg")"
  fi
} > %q
echo '{"type":"result","subtype":"success","result":"done","session_id":"s-rec","total_cost_usd":0}'
`, outFile)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording claude: %v", err)
	}
	return path
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/provider ./internal/runner -run 'ARunWith|TheServerIs' 2>&1 | head`
Expected: neither package compiles (`unknown field MCPConfig`, `undefined: provider.MCPServerName`).

- [ ] **Step 3: The command line**

In `internal/provider/provider.go`, replace:

```go
type Spec struct {
	Prompt  string
	Workdir string
	Schema  string // JSON schema of the answer; empty means free text
}
```

with:

```go
type Spec struct {
	Prompt    string
	Workdir   string
	Schema    string // JSON schema of the answer; empty means free text
	MCPConfig string // MCP config file of a run with gatekeeper access; empty means no tools
}
```

In `internal/provider/claude.go`, replace:

```go
// readOnlyTools is everything an agent may use for now.
```

with:

```go
// MCPServerName is the name the gatekeeper has in the MCP config of a run. The CLI calls its tools
// mcp__remedy__<tool>.
const MCPServerName = "remedy"

// readOnlyTools is everything an agent may use for now.
```

In `internal/provider/claude.go`, replace:

```go
	// Isolation (measured against the real CLI, see docs/research/spike-claude-billing.md):
	//   --safe-mode        disables the user's CLAUDE.md, plugins, hooks, MCP servers and skills
```

with:

```go
	// Isolation (measured against the real CLI, see docs/research/spike-claude-billing.md):
	//   --safe-mode        disables the user's CLAUDE.md, plugins, hooks, MCP servers and skills. It also turns MCP
	//                      off for a server passed with --mcp-config (docs/research/spike-mcp-blocking.md), so a run
	//                      with gatekeeper access has to go without it; --restricted alone keeps the user's plugins out.
```

In `internal/provider/claude.go`, replace:

```go
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk",
		"--safe-mode", "--restricted", "--strict-mcp-config", "--tools", readOnlyTools, "--model", model,
	}
```

with:

```go
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
```

- [ ] **Step 4: The config file and the loop**

Create `internal/runner/mcpconfig.go`:

```go
package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jaydee94/remedy/internal/provider"
)

// writeMCPConfig writes the MCP config of a run with gatekeeper access: where the gatekeeper is and the run token
// that opens it. The file holds a secret, so it is made with mode 0600 in a directory of its own (mode 0700) next to
// the workspace of the run, not inside it: the file tools of the agent reach the workspace only, and the run token
// must not become part of what the agent reads and sends on. cleanup removes the directory.
func writeMCPConfig(root, runID, baseURL, token string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(root, runID+"-mcp-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	cfg := map[string]any{"mcpServers": map[string]any{
		provider.MCPServerName: map[string]any{
			"type":    "http",
			"url":     strings.TrimRight(baseURL, "/") + "/mcp",
			"headers": map[string]string{"Authorization": "Bearer " + token},
		},
	}}
	body, err := json.Marshal(cfg)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
```

In `internal/runner/loop.go`, replace:

```go
	timeout := l.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := Execute(execCtx, p, provider.Spec{Prompt: r.Prompt, Workdir: dir, Schema: string(c.Schema)}, l.Env,
		clientSink{client: l.Client, runID: r.ID})
```

with:

```go
	spec := provider.Spec{Prompt: r.Prompt, Workdir: dir, Schema: string(c.Schema)}
	if c.MCPToken != "" {
		path, cleanup, err := writeMCPConfig(l.WorkspaceRoot, r.ID, l.Client.BaseURL, c.MCPToken)
		if err != nil {
			log.Error("cannot write the MCP config", "err", err)
			l.finish(log, r.ID, run.Outcome{ExitCode: 127, Result: "The tools of the run could not be prepared."})
			return
		}
		defer cleanup()
		spec.MCPConfig = path
	}

	timeout := l.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := Execute(execCtx, p, spec, l.Env, clientSink{client: l.Client, runID: r.ID})
```

- [ ] **Step 5: Run the tests and watch them pass**

Run: `gofmt -l internal; go vet ./... && go test ./internal/provider ./internal/runner -race -count=1`
Expected: no output from `gofmt -l`, then `ok` for both. The existing tests of the provider and the runner pass unchanged: a run without tools gets exactly the old command line.

- [ ] **Step 6: Mutation checks**

Make each change, run `go test ./internal/provider ./internal/runner -count=1`, expect the named test to fail, and revert it.

1. In `Command`, change `if spec.MCPConfig == "" {` (the one before `args = append(args, "--safe-mode")`) to `if true {`: `TestARunWithToolsDropsSafeModeAndNamesItsConfig` fails (and `TestARunWithToolsGetsItsConfigInAFileTheAgentCannotReach`).
2. In `writeMCPConfig`, change `0o600` to `0o644`: `TestARunWithToolsGetsItsConfigInAFileTheAgentCannotReach` fails (the mode).
3. In `loop.go`, change `if c.MCPToken != "" {` (the first one, before `writeMCPConfig`) to `if false {`: `TestARunWithToolsGetsItsConfigInAFileTheAgentCannotReach` fails (no config, `--safe-mode` stays).
4. In `writeMCPConfig`, replace `os.MkdirTemp(root, runID+"-mcp-")` with `os.MkdirTemp(root, runID+"-")`: not expected to fail (the workspace has the same name pattern but is another directory); skip this one.

- [ ] **Step 7: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal
git commit -m "feat(runner): hand a run its tools: an MCP config the agent cannot read, and a command line without --safe-mode" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The heartbeat, a run clock that can stand still, and cancelling

**Files:**
- Create: `internal/runner/heartbeat.go`, `internal/runner/clock_test.go`, `internal/runner/heartbeat_test.go`, `internal/testutil/delayed.go`
- Modify: `internal/runner/loop.go`

**Interfaces:**
- Consumes: `POST /runner/v1/runs/{id}/heartbeat` (Task 1), `run.ReasonCancelled`, `run.Claim.MCPToken`, `store.CancelRun`, `store.FailLostRuns`, `store.BeginToolCall`, `store.DecideApproval`, the runner test helpers (`waitTerminal`).
- Produces:
  - `runner.Beat{Waiting, Cancel bool}`, `runner.ErrRunGone`, `(*Client).Heartbeat(ctx, runID) (Beat, error)` (a 404 is `ErrRunGone`).
  - `runner.DefaultHeartbeatInterval` (10 seconds) and `Loop.HeartbeatInterval`.
  - `runClock`: a budget of running time with `Pause`, `Resume`, `Stop` and `Expired`; it calls its `onExpire` once when the budget is used up.
  - The loop's run time limit is a `runClock` for every run. For a run with `MCPToken` a goroutine sends the heartbeat at once and then every interval: `waiting` pauses the clock and anything else resumes it; `cancel` or `ErrRunGone` stops the agent (the context is cancelled, which kills the CLI with `SIGKILL`) and the run is reported as failed with `run.ReasonCancelled` and the result "The run was cancelled."; any other error of a heartbeat is logged and changes nothing.
  - `testutil.FakeClaudeAfter(t, seconds)`: a fake `claude` that finishes successfully after a delay.

- [ ] **Step 1: Write the failing tests**

Create `internal/runner/clock_test.go`:

```go
package runner

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestTheClockExpiresOnceItsBudgetIsUsedUp(t *testing.T) {
	var fired atomic.Int32
	c := newRunClock(60*time.Millisecond, func() { fired.Add(1) })
	defer c.Stop()

	time.Sleep(20 * time.Millisecond)
	if c.Expired() || fired.Load() != 0 {
		t.Fatal("the clock expired early")
	}
	time.Sleep(200 * time.Millisecond)
	if !c.Expired() || fired.Load() != 1 {
		t.Fatalf("expired = %v, fired %d times, want true and once", c.Expired(), fired.Load())
	}
}

func TestTheClockStandsStillWhilePausedAndKeepsWhatIsLeft(t *testing.T) {
	var fired atomic.Int32
	c := newRunClock(200*time.Millisecond, func() { fired.Add(1) })
	defer c.Stop()

	time.Sleep(80 * time.Millisecond)
	c.Pause()
	c.Pause() // idempotent

	time.Sleep(400 * time.Millisecond) // twice the budget
	if c.Expired() || fired.Load() != 0 {
		t.Fatal("the clock ran while it was paused")
	}

	c.Resume()
	c.Resume() // idempotent: no second timer
	time.Sleep(60 * time.Millisecond)
	if c.Expired() {
		t.Fatal("the clock forgot the time before the pause: it expired after 140 ms of a 200 ms budget")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !c.Expired() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Expired() || fired.Load() != 1 {
		t.Fatalf("expired = %v, fired %d times, want true and once", c.Expired(), fired.Load())
	}
}

func TestAStoppedClockNeverExpires(t *testing.T) {
	var fired atomic.Int32
	c := newRunClock(40*time.Millisecond, func() { fired.Add(1) })
	c.Stop()
	c.Resume()
	time.Sleep(150 * time.Millisecond)
	if c.Expired() || fired.Load() != 0 {
		t.Fatal("a stopped clock expired")
	}
}
```

Create `internal/runner/heartbeat_test.go`:

```go
package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

type tripFunc func(*http.Request) (*http.Response, error)

func (f tripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// startToolLoop runs a loop with a heartbeat every 30 ms. wrap, when given, wraps the transport the runner uses.
func startToolLoop(t *testing.T, p provider.Provider, timeout time.Duration, wrap func(http.RoundTripper) http.RoundTripper) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New("correct horse battery"), RunnerToken: token}))
	t.Cleanup(ts.Close)

	httpClient := ts.Client()
	if wrap != nil {
		httpClient = &http.Client{Transport: wrap(http.DefaultTransport)}
	}
	loop := &runner.Loop{
		Client:            &runner.Client{BaseURL: ts.URL, Token: token, HTTP: httpClient},
		Providers:         map[string]provider.Provider{"claude": p},
		WorkspaceRoot:     t.TempDir(),
		Env:               os.Environ(),
		Log:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:           50 * time.Millisecond,
		RunTimeout:        timeout,
		HeartbeatInterval: 30 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return st, ts
}

func waitRunning(t *testing.T, st *store.Store, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := st.GetRun(context.Background(), id); err == nil && r.Status == run.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the run did not start")
}

func TestAWaitingRunIsNotTimedOutUntilTheWaitIsOver(t *testing.T) {
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.SlowClaude(t)}, 400*time.Millisecond, nil)
	ctx := context.Background()
	queued, _ := st.CreateToolRun(ctx, "claude", "use the tools")
	waitRunning(t, st, queued.ID)

	call, _, err := st.BeginToolCall(ctx, store.NewToolCall{
		RunID: queued.ID, ToolUseID: "toolu_1", Tool: "incident_add_note", Kind: store.CallKindMutating, Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Three times the budget, waiting for an approval: the run is still there.
	time.Sleep(1200 * time.Millisecond)
	if got, _ := st.GetRun(ctx, queued.ID); got.Status != run.Running {
		t.Fatalf("a run that waits for an approval was stopped: %+v", got)
	}

	// The decision ends the wait; the clock runs again with what was left, and the run is stopped.
	if _, err := st.DecideApproval(ctx, call.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonTimeout {
		t.Fatalf("run = %+v, want a timeout once nobody waits any more", got)
	}
}

func TestCancellingARunStopsTheAgent(t *testing.T) {
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.SlowClaude(t)}, 0, nil) // the agent would sleep for a minute
	ctx := context.Background()
	queued, _ := st.CreateToolRun(ctx, "claude", "use the tools")
	waitRunning(t, st, queued.ID)

	started := time.Now()
	if _, err := st.CancelRun(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.FailureReason != run.ReasonCancelled || got.Result != "The run was cancelled." {
		t.Fatalf("run = %+v", got)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("it took %s to stop the agent", time.Since(started))
	}
}

func TestARunTheControlPlaneNoLongerHasIsStopped(t *testing.T) {
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.SlowClaude(t)}, 0, nil)
	ctx := context.Background()
	lost, _ := st.CreateToolRun(ctx, "claude", "use the tools")
	waitRunning(t, st, lost.ID)
	next, _ := st.CreateRun(ctx, "claude", "the next run")

	// The reaper gave up on the run: the runner's next heartbeat is a 404 and it stops the agent, which frees the
	// loop for the run that waits behind it.
	if ids, err := st.FailLostRuns(ctx, time.Now().Add(time.Hour), "lost"); err != nil || len(ids) != 1 {
		t.Fatalf("FailLostRuns = %v, %v", ids, err)
	}
	waitRunning(t, st, next.ID)
}

func TestAFailingHeartbeatDoesNotStopTheRun(t *testing.T) {
	var beats atomic.Int32
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.FakeClaudeAfter(t, "0.5")}, 0, func(rt http.RoundTripper) http.RoundTripper {
		return tripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/heartbeat") {
				beats.Add(1)
				return nil, errors.New("the control plane is unreachable")
			}
			return rt.RoundTrip(r)
		})
	})
	queued, _ := st.CreateToolRun(context.Background(), "claude", "use the tools")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Succeeded || got.Result != "done" {
		t.Fatalf("run = %+v, want it to finish although every heartbeat failed", got)
	}
	if beats.Load() < 2 {
		t.Fatalf("%d heartbeats were attempted, want several", beats.Load())
	}
}

func TestARunWithoutToolsSendsNoHeartbeat(t *testing.T) {
	var beats atomic.Int32
	st, _ := startToolLoop(t, provider.Claude{Binary: testutil.FakeClaudeAfter(t, "0.4")}, 0, func(rt http.RoundTripper) http.RoundTripper {
		return tripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/heartbeat") {
				beats.Add(1)
			}
			return rt.RoundTrip(r)
		})
	})
	queued, _ := st.CreateRun(context.Background(), "claude", "plain")
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}
	if beats.Load() != 0 {
		t.Fatalf("%d heartbeats of a run without tools", beats.Load())
	}
}
```

Create `internal/testutil/delayed.go`:

```go
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaudeAfter writes an executable script that mimics a `claude` that works for a while and then finishes
// successfully with the result "done". seconds is what `sleep` takes, for example "0.5".
func FakeClaudeAfter(t testing.TB, seconds string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
cat > /dev/null
echo '{"type":"system","subtype":"init","session_id":"s-late"}'
sleep %s
echo '{"type":"result","subtype":"success","result":"done","session_id":"s-late","total_cost_usd":0}'
`, seconds)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write delayed fake claude: %v", err)
	}
	return path
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/runner 2>&1 | head`
Expected: the package does not compile (`undefined: newRunClock`, `unknown field HeartbeatInterval`, `undefined: testutil.FakeClaudeAfter`).

- [ ] **Step 3: The clock, the client and the watcher**

Create `internal/runner/heartbeat.go`:

```go
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultHeartbeatInterval is how often the runner of a run with gatekeeper access reports to the control plane.
const DefaultHeartbeatInterval = 10 * time.Second

// Beat is the control plane's answer to a heartbeat.
type Beat struct {
	// Waiting means the run waits for the maintainer's decision: its time budget stands still.
	Waiting bool `json:"waiting"`
	// Cancel means the maintainer cancelled the run: the agent is stopped.
	Cancel bool `json:"cancel"`
}

// ErrRunGone means the control plane no longer has the run as running: it was finished, or the reaper failed it.
var ErrRunGone = errors.New("the control plane no longer has this run as running")

// Heartbeat tells the control plane that the runner of a run is alive, and learns whether the run waits for an
// approval and whether it was cancelled.
func (c *Client) Heartbeat(ctx context.Context, runID string) (Beat, error) {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/heartbeat", nil)
	if err != nil {
		return Beat{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Beat{}, ErrRunGone
	}
	if err := expect(resp, http.StatusOK); err != nil {
		return Beat{}, err
	}
	var b Beat
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return Beat{}, err
	}
	return b, nil
}

// runClock is the time limit of a run: a budget of running time. It can stand still, which is what a run does while
// it waits for an approval, and it calls onExpire once when the budget is used up.
type runClock struct {
	mu        sync.Mutex
	remaining time.Duration
	since     time.Time // when the clock last started running; zero while it stands still
	timer     *time.Timer
	expired   bool
	stopped   bool
	onExpire  func()
}

func newRunClock(budget time.Duration, onExpire func()) *runClock {
	c := &runClock{remaining: budget, onExpire: onExpire}
	c.mu.Lock()
	c.startLocked()
	c.mu.Unlock()
	return c
}

func (c *runClock) startLocked() {
	c.since = time.Now()
	c.timer = time.AfterFunc(c.remaining, c.expire)
}

func (c *runClock) expire() {
	c.mu.Lock()
	if c.expired || c.stopped {
		c.mu.Unlock()
		return
	}
	c.expired = true
	c.mu.Unlock()
	c.onExpire()
}

// Pause makes the clock stand still. What is left of the budget is kept.
func (c *runClock) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() || c.expired || c.stopped {
		return
	}
	c.timer.Stop()
	c.remaining = max(0, c.remaining-time.Since(c.since))
	c.since = time.Time{}
}

// Resume lets a paused clock run again.
func (c *runClock) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.since.IsZero() || c.expired || c.stopped {
		return
	}
	c.startLocked()
}

// Stop ends the clock for good: it will not expire any more.
func (c *runClock) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
	}
}

// Expired reports whether the budget was used up.
func (c *runClock) Expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}

// watch is the heartbeat of a run with gatekeeper access. It reports at once and then every interval until ctx ends.
// While the run waits for an approval the clock stands still. When the run was cancelled, or the control plane no
// longer has it, stop is called and the agent goes. A heartbeat that fails for any other reason is only logged: the
// control plane being unreachable must never stop an agent that works.
func (l *Loop) watch(ctx context.Context, log *slog.Logger, runID string, clock *runClock, stop func()) {
	interval := l.HeartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	failures := 0
	for {
		beat, err := l.Client.Heartbeat(ctx, runID)
		switch {
		case errors.Is(err, ErrRunGone):
			log.Warn("the control plane no longer has this run as running: stopping the agent")
			stop()
			return
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			failures++
			log.Warn("heartbeat failed, the run goes on", "err", err, "failures", failures)
		default:
			failures = 0
			if beat.Waiting {
				clock.Pause()
			} else {
				clock.Resume()
			}
			if beat.Cancel {
				log.Info("the run was cancelled: stopping the agent")
				stop()
				return
			}
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}
```

- [ ] **Step 4: The loop**

In `internal/runner/loop.go`, replace:

```go
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
```

with:

```go
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
```

In `internal/runner/loop.go`, replace:

```go
type Loop struct {
	Client        *Client
	Providers     map[string]provider.Provider
	WorkspaceRoot string
	Env           []string
	Log           *slog.Logger
	Backoff       time.Duration // wait after a failed claim, default 3s
	RunTimeout    time.Duration // longest a run may take, default DefaultRunTimeout
}
```

with:

```go
type Loop struct {
	Client            *Client
	Providers         map[string]provider.Provider
	WorkspaceRoot     string
	Env               []string
	Log               *slog.Logger
	Backoff           time.Duration // wait after a failed claim, default 3s
	RunTimeout        time.Duration // longest a run may take while it does not wait for an approval, default DefaultRunTimeout
	HeartbeatInterval time.Duration // how often a run with tools reports to the control plane, default DefaultHeartbeatInterval
}
```

In `internal/runner/loop.go`, replace:

```go
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := Execute(execCtx, p, spec, l.Env, clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	// The deadline kills the subprocess. A run that ended by itself is not a timeout, even if the
	// deadline passed a moment later; neither is a run that was cut short by the runner shutting down.
	if ctx.Err() == nil && errors.Is(execCtx.Err(), context.DeadlineExceeded) && out.ExitCode != 0 {
		out.FailureReason = run.ReasonTimeout
		if out.Result == "" {
			out.Result = fmt.Sprintf("The run was stopped after %s.", timeout)
		}
		log.Warn("run timed out", "after", timeout)
	}
	l.finish(log, r.ID, out)
```

with:

```go
	// The time limit is a budget of running time. A run that waits for an approval does not use it up, and the
	// control plane can cancel the run: both reach the runner through the heartbeat of a run with tools.
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	clock := newRunClock(timeout, cancel)
	defer clock.Stop()
	var stopped atomic.Bool // the control plane cancelled the run, or no longer has it
	if c.MCPToken != "" {
		go l.watch(execCtx, log, r.ID, clock, func() { stopped.Store(true); cancel() })
	}
	out, err := Execute(execCtx, p, spec, l.Env, clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	switch {
	case stopped.Load():
		out.FailureReason = run.ReasonCancelled
		out.Result = "The run was cancelled."
		if out.ExitCode == 0 {
			out.ExitCode = -1
		}
	// The budget kills the subprocess. A run that ended by itself is not a timeout, even if the budget ran out a
	// moment later; neither is a run that was cut short by the runner shutting down.
	case ctx.Err() == nil && clock.Expired() && out.ExitCode != 0:
		out.FailureReason = run.ReasonTimeout
		if out.Result == "" {
			out.Result = fmt.Sprintf("The run was stopped after %s.", timeout)
		}
		log.Warn("run timed out", "after", timeout)
	}
	l.finish(log, r.ID, out)
```

- [ ] **Step 5: Run the tests and watch them pass**

Run: `gofmt -l internal; go vet ./... && go test ./internal/runner -race -count=1`
Expected: no output from `gofmt -l`, then `ok`. Run it again with `-cpu 1` and `-count=5`: the tests are about time, so a flaky one means a race in the code or a margin that is too small.

- [ ] **Step 6: Mutation checks**

Make each change, run `go test ./internal/runner -count=1`, expect the named test to fail, and revert it.

1. In `Pause`, delete the line `c.timer.Stop()`: `TestTheClockStandsStillWhilePausedAndKeepsWhatIsLeft` and `TestAWaitingRunIsNotTimedOutUntilTheWaitIsOver` fail.
2. In `watch`, change `if beat.Cancel {` to `if beat.Cancel && false {`: `TestCancellingARunStopsTheAgent` fails.
3. In `watch`, change `case errors.Is(err, ErrRunGone):` to `case errors.Is(err, ErrRunGone) && false:`: `TestARunTheControlPlaneNoLongerHasIsStopped` fails (the next run never starts).
4. In `watch`, change `failures++` to `failures++; stop()`: `TestAFailingHeartbeatDoesNotStopTheRun` fails.
5. In `loop.go`, change the condition `if c.MCPToken != "" {` in front of `go l.watch(` to `if true {`: `TestARunWithoutToolsSendsNoHeartbeat` fails (every run gets a heartbeat).
6. In `Resume`, delete the whole `c.startLocked()` line: `TestTheClockStandsStillWhilePausedAndKeepsWhatIsLeft` fails (the clock never runs again).

- [ ] **Step 7: Run the whole suite and commit**

Run: `go test ./... -race -count=1 && go test ./internal/runner -race -cpu 1 -count=3`
Expected: all packages `ok`.

```bash
git add internal
git commit -m "feat(runner): heartbeat a run with tools, pause its time limit while it waits for an approval, and stop it when it is cancelled" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 4: The Approvals page and the count in the sidebar

**Files:**
- Create: `web/src/approvals.ts`, `web/src/usePendingApprovals.ts`, `web/src/ApprovalCard.tsx`, `web/src/ApprovalsPage.tsx`
- Overwrite: `web/src/components/AppLayout.tsx`
- Modify: `web/src/api.ts`, `web/src/App.tsx`

**Interfaces:**
- Consumes: `GET /api/approvals?status=`, `POST /api/approvals/{id}/approve|deny`, `GET /api/runs/{id}/tool-calls`, `POST /api/runs/{id}/cancel`, `POST /api/runs` with `tools` (plan 2a); `request<T>`, `ApiError`, `timeAgo`, `Card`, `Button`, `Input`, `Alert`, `Skeleton`.
- Produces:
  - `api.ts`: the type `ToolCall`, the fields `mcp`, `cancelRequested`, `waitingApproval` and the failure reasons `cancelled` and `runner_lost` on `Run`, and `api.createRun(prompt, tools?)`, `api.listToolCalls(runId)`, `api.cancelRun(id)`, `api.listApprovals('pending' | 'all')`, `api.decideApproval(id, approve, reason?)`.
  - `approvals.ts`: `argumentList(args)`, `outcomeText(call)`, `decisionLabel`, `callStatusColor`.
  - `usePendingApprovals(): number`, refreshed every 5 seconds.
  - The route `/approvals` and a sidebar item **Approvals** that shows the number of calls that wait for a decision.
  - `ApprovalCard`: the tool, the arguments as names and values (escaped text), links to the run and the incident, a reason field and **Approve** / **Deny** while the agent still waits; otherwise a sentence that says the call can no longer be decided.
  - `ApprovalsPage`: the pending approvals, oldest first, and the history below, refreshed every 3 seconds.

There is no web test runner in this repository, so this task is checked by the type checker, the linter and a real browser (Step 7).

- [ ] **Step 1: The API client**

In `web/src/api.ts`, replace:

```ts
  /** Set when the run was stopped for taking too long, or its answer was not a valid diagnosis. */
  failureReason?: 'timeout' | 'invalid_output'
}
```

with:

```ts
  /** Why a failed run failed, when it was not the agent's own exit code. */
  failureReason?: 'timeout' | 'invalid_output' | 'cancelled' | 'runner_lost'
  /** The run has access to the gatekeeper's tools. */
  mcp?: boolean
  /** The maintainer cancelled the run; the runner is stopping the agent. */
  cancelRequested?: boolean
  /** The id of the approval the run waits for. Only `GET /api/runs/{id}` has it, and only while there is one. */
  waitingApproval?: number
}

/** A call of an agent to a gatekeeper tool: a row of the audit log, and for a mutating tool an approval. */
export interface ToolCall {
  id: number
  runId: string
  incidentId?: number
  tool: string
  kind: 'read' | 'mutating'
  /** What the agent asked for. Usually an object. Show it as text, never as HTML. */
  arguments: unknown
  status: 'running' | 'waiting' | 'succeeded' | 'failed' | 'denied' | 'abandoned'
  /** Empty for a read tool. */
  decision: '' | 'pending' | 'approved' | 'denied' | 'abandoned'
  reason?: string
  result?: string
  error?: string
  requestedAt: string
  decidedAt?: string
  finishedAt?: string
  /** An agent still waits for the answer of the call: only then can it be decided. */
  waiting: boolean
}
```

In `web/src/api.ts`, replace:

```ts
  createRun: (prompt: string) => request<Run>('POST', '/api/runs', { prompt }),
  getRun: (id: string) => request<Run>('GET', `/api/runs/${id}`),
```

with:

```ts
  createRun: (prompt: string, tools = false) => request<Run>('POST', '/api/runs', { prompt, tools }),
  getRun: (id: string) => request<Run>('GET', `/api/runs/${id}`),
  listToolCalls: (runId: string) => request<ToolCall[]>('GET', `/api/runs/${runId}/tool-calls`),
  cancelRun: (id: string) => request<void>('POST', `/api/runs/${id}/cancel`),

  /** The mutating calls of agents: "pending" are the ones that wait for a decision, "all" is the history as well. */
  listApprovals: (status: 'pending' | 'all') => request<ToolCall[]>('GET', `/api/approvals?status=${status}`),
  decideApproval: (id: number, approve: boolean, reason?: string) =>
    request<ToolCall>('POST', `/api/approvals/${id}/${approve ? 'approve' : 'deny'}`, reason ? { reason } : undefined),
```

- [ ] **Step 2: The helpers and the count**

Create `web/src/approvals.ts`:

```ts
import type { ToolCall } from './api.ts'

/** The arguments of a call as name and value pairs. A value that is not text is shown as JSON. */
export function argumentList(args: unknown): { name: string; value: string }[] {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) {
    return [{ name: 'arguments', value: JSON.stringify(args) ?? '' }]
  }
  return Object.entries(args).map(([name, value]) => ({
    name,
    value: typeof value === 'string' ? value : (JSON.stringify(value) ?? ''),
  }))
}

export const decisionLabel: Record<string, string> = {
  '': 'read',
  pending: 'waiting for you',
  approved: 'approved',
  denied: 'denied',
  abandoned: 'abandoned',
}

export const callStatusColor: Record<ToolCall['status'], string> = {
  running: 'bg-amber-500',
  waiting: 'bg-amber-500',
  succeeded: 'bg-emerald-500',
  failed: 'bg-rose-500',
  denied: 'bg-slate-500',
  abandoned: 'bg-slate-600',
}

/** What came of a call, in a sentence: its result, or why it did not run. */
export function outcomeText(call: ToolCall): string {
  switch (call.status) {
    case 'succeeded':
      return call.result ? `Result: ${call.result}` : 'Done.'
    case 'failed':
    case 'denied':
    case 'abandoned':
      return call.error ?? ''
    case 'running':
      return 'Running.'
    case 'waiting':
      return call.decision === 'approved' ? 'Approved, not run yet.' : 'Waiting for a decision.'
  }
}
```

Create `web/src/usePendingApprovals.ts`:

```ts
import { useEffect, useState } from 'react'
import { api } from './api.ts'

/** The number of calls that wait for a decision, refreshed every five seconds. */
export function usePendingApprovals(): number {
  const [count, setCount] = useState(0)

  useEffect(() => {
    let active = true
    const load = () =>
      api
        .listApprovals('pending')
        .then((list) => {
          if (active) setCount(list.length)
        })
        .catch(() => undefined) // a failed refresh keeps the last number
    void load()
    const timer = setInterval(load, 5000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  return count
}
```

- [ ] **Step 3: The card and the page**

Create `web/src/ApprovalCard.tsx`:

```tsx
import { useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'
import { argumentList } from './approvals.ts'
import { timeAgo } from './incidents.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

const linkClass = 'underline decoration-muted-foreground/50 underline-offset-4 hover:text-foreground hover:decoration-foreground'

/** A mutating call that waits for a decision. What it shows is exactly what runs when it is approved. */
export default function ApprovalCard({ call, onChanged }: { call: ToolCall; onChanged: () => void }) {
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function decide(approve: boolean) {
    setBusy(true)
    setError('')
    try {
      await api.decideApproval(call.id, approve, reason.trim() || undefined)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not record the decision')
    } finally {
      setBusy(false)
      onChanged() // the state may have changed under us, so look again whatever happened
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="font-mono text-base break-all">{call.tool}</CardTitle>
        <p className="flex flex-wrap gap-x-3 text-sm text-muted-foreground">
          <span title={new Date(call.requestedAt).toLocaleString()}>asked {timeAgo(call.requestedAt)}</span>
          <Link to={`/runs/${encodeURIComponent(call.runId)}`} className={linkClass}>
            Run
          </Link>
          {call.incidentId !== undefined && (
            <Link to={`/incidents/${call.incidentId}`} className={linkClass}>
              Incident #{call.incidentId}
            </Link>
          )}
        </p>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
          {argumentList(call.arguments).map(({ name, value }) => (
            <div key={name} className="contents">
              <dt className="text-muted-foreground">{name}</dt>
              <dd className="break-words whitespace-pre-wrap">{value}</dd>
            </div>
          ))}
        </dl>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {call.waiting ? (
          <div className="flex flex-wrap items-center gap-2">
            <Input
              aria-label="Reason (optional)"
              placeholder="Reason (optional)"
              maxLength={500}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              className="max-w-xs"
            />
            <Button disabled={busy} onClick={() => void decide(true)}>
              Approve
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => void decide(false)}>
              Deny
            </Button>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            The agent is no longer waiting for this call, so it can no longer be decided. It is marked as abandoned shortly.
          </p>
        )}
      </CardContent>
    </Card>
  )
}
```

Create `web/src/ApprovalsPage.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'
import ApprovalCard from './ApprovalCard.tsx'
import { argumentList, callStatusColor, decisionLabel, outcomeText } from './approvals.ts'
import { timeAgo } from './incidents.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

function HistoryRow({ call }: { call: ToolCall }) {
  const when = call.decidedAt ?? call.finishedAt ?? call.requestedAt
  return (
    <li className="flex gap-3 py-3 first:pt-0 last:pb-0">
      <span aria-hidden className={`mt-2 h-2 w-2 shrink-0 rounded-full ${callStatusColor[call.status]}`} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-mono break-all">{call.tool}</span>
          <span className="text-sm text-muted-foreground">{decisionLabel[call.decision] ?? call.decision}</span>
          <span className="text-xs text-muted-foreground" title={new Date(when).toLocaleString()}>
            {timeAgo(when)}
          </span>
        </span>
        <span className="text-sm break-words whitespace-pre-wrap text-muted-foreground">
          {argumentList(call.arguments)
            .map(({ name, value }) => `${name}: ${value}`)
            .join(' · ')}
        </span>
        {call.reason && <span className="text-sm break-words">Reason: {call.reason}</span>}
        <span className="text-sm break-words whitespace-pre-wrap text-muted-foreground">{outcomeText(call)}</span>
        <span className="flex gap-3 text-xs text-muted-foreground">
          <Link to={`/runs/${encodeURIComponent(call.runId)}`} className="hover:text-foreground">
            Run
          </Link>
          {call.incidentId !== undefined && (
            <Link to={`/incidents/${call.incidentId}`} className="hover:text-foreground">
              Incident #{call.incidentId}
            </Link>
          )}
        </span>
      </div>
    </li>
  )
}

export default function ApprovalsPage() {
  const [calls, setCalls] = useState<ToolCall[] | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api
      .listApprovals('all')
      .then((list) => {
        setCalls(list)
        setError('')
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the approvals'))
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, 3000)
    return () => clearInterval(timer)
  }, [load])

  // The API answers newest first. What waits is shown oldest first: the call that was asked first is decided first.
  const pending = (calls ?? []).filter((c) => c.decision === 'pending').reverse()
  const history = (calls ?? []).filter((c) => c.decision !== 'pending')

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-2xl font-semibold tracking-tight">Approvals</h1>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {calls === null ? (
        !error && <Skeleton className="h-40 w-full" />
      ) : (
        <>
          <section className="flex flex-col gap-3">
            <h2 className="text-sm tracking-wide text-muted-foreground uppercase">Waiting for you</h2>
            {pending.length === 0 && (
              <p className="text-muted-foreground">
                Nothing waits for a decision. A run with tools asks here before it changes anything.
              </p>
            )}
            {pending.map((call) => (
              <ApprovalCard key={call.id} call={call} onChanged={load} />
            ))}
          </section>

          <section className="flex flex-col gap-3">
            <h2 className="text-sm tracking-wide text-muted-foreground uppercase">History</h2>
            {history.length === 0 ? (
              <p className="text-muted-foreground">No decisions yet.</p>
            ) : (
              <Card>
                <CardContent>
                  <ol className="flex flex-col divide-y divide-border">
                    {history.map((call) => (
                      <HistoryRow key={call.id} call={call} />
                    ))}
                  </ol>
                </CardContent>
              </Card>
            )}
          </section>
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: The route and the sidebar**

In `web/src/App.tsx`, replace:

```tsx
import AppLayout from './components/AppLayout.tsx'
```

with:

```tsx
import AppLayout from './components/AppLayout.tsx'
import ApprovalsPage from './ApprovalsPage.tsx'
```

In `web/src/App.tsx`, replace:

```tsx
        <Route path="runs" element={<RunsPage />} />
```

with:

```tsx
        <Route path="approvals" element={<ApprovalsPage />} />
        <Route path="runs" element={<RunsPage />} />
```

Overwrite `web/src/components/AppLayout.tsx`:

```tsx
import { History, LogOut, Play, Settings, ShieldCheck, TriangleAlert } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'
import { usePendingApprovals } from '@/usePendingApprovals'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const nav = [
  { to: '/', label: 'Timeline', icon: History, end: true },
  { to: '/incidents', label: 'Incidents', icon: TriangleAlert, end: false },
  { to: '/approvals', label: 'Approvals', icon: ShieldCheck, end: false },
  { to: '/runs', label: 'Runs', icon: Play, end: false },
  { to: '/settings', label: 'Settings', icon: Settings, end: false },
]

export default function AppLayout({ onSignOut }: { onSignOut: () => void }) {
  const pending = usePendingApprovals()

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col gap-6 border-r border-border bg-card/40 p-4">
        <NavLink to="/" className="px-2 text-xl font-semibold tracking-tight">
          Remedy
        </NavLink>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
                  isActive && 'bg-accent text-accent-foreground',
                )
              }
            >
              <Icon className="size-4" />
              {label}
              {to === '/approvals' && pending > 0 && (
                <span
                  aria-label={`${pending} waiting for a decision`}
                  className="ml-auto rounded-full bg-amber-500 px-1.5 text-xs font-medium text-black"
                >
                  {pending}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
        <Button variant="ghost" size="sm" className="justify-start" onClick={onSignOut}>
          <LogOut /> Sign out
        </Button>
      </aside>
      <main className="min-w-0 flex-1 p-8">
        <div className="mx-auto max-w-5xl">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
```

- [ ] **Step 5: Lint and build**

Run: `make web-install` (a fresh worktree has no `web/node_modules`), then `cd web && npm run lint && npm run build`
Expected: oxlint reports no warnings or errors; `tsc -b` and `vite build` succeed. If `tsc` complains that the `switch` in `outcomeText` does not return on all paths, the union of statuses in `ToolCall` was changed: keep them in step. If the `@/usePendingApprovals` import does not resolve, write `'../usePendingApprovals.ts'` (the alias `@/` points at `src/`, and the other components import their siblings that way).

- [ ] **Step 6: A seeded database and live calls for the browser check**

This script builds the server with the UI, starts it on a fresh database, seeds an incident and one **stale** pending call (a row without an agent that waits for it), creates a run with tools, and holds two real MCP calls open with `curl` so that two approvals have a waiting agent. Run it from the worktree.

```bash
make build
export REMEDY_ADMIN_PASSWORD='ui-test-password' REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)" \
  REMEDY_MASTER_KEY="$(openssl rand -base64 32)" REMEDY_DB="$(mktemp -d)/remedy.db" REMEDY_ADDR=127.0.0.1:8080
W=$(mktemp -d)
./bin/remedy-server > server.log 2>&1 &
until curl -sf http://127.0.0.1:8080/healthz > /dev/null; do sleep 0.2; done
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" <<'SQL'
INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
VALUES (1, x'00', 'seed', 'octo', 'error', 'Seeded for the UI check; polling is off.', '2026-10-02T12:00:00.000000000Z');
INSERT INTO repos (id, connection_id, full_name, default_branch, enabled, created_at)
VALUES (1, 1, 'octo/hello', 'main', 1, '2026-10-02T12:00:00.000000000Z');
INSERT INTO incidents (id, repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, resolved_at, resolved_reason, diagnosis, diagnosed_sha, run_id)
VALUES (1, 1, 'pr:7', 'https://github.com/octo/hello/pull/7', 'web', 'open', 'failure', 'abc1234def5678', '', 1, 0, '2026-10-02T10:00:00.000000000Z', '2026-10-02T10:00:00.000000000Z', NULL, NULL, '', NULL, '', NULL);
SQL
B=http://127.0.0.1:8080
printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" | curl -sf -c "$W/cookies" -H 'X-Remedy-CSRF: 1' --data-binary @- $B/api/login > /dev/null
RUN=$(curl -s -b "$W/cookies" -H 'X-Remedy-CSRF: 1' -d '{"prompt":"investigate the failing web check","tools":true}' $B/api/runs | jq -r .id)
TOKEN=$(curl -s -X POST -H "Authorization: Bearer $REMEDY_RUNNER_TOKEN" $B/runner/v1/claim | jq -r .mcp_token)
echo "$RUN" > "$W/run-id"; echo "$TOKEN" > "$W/token"
call() { # call <toolUseId> <note>
  nohup curl -sN -X POST -H "Authorization: Bearer $TOKEN" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"incident_add_note\",\"arguments\":{\"id\":1,\"note\":\"$2\"},\"_meta\":{\"claudecode/toolUseId\":\"$1\",\"progressToken\":1}}}" $B/mcp > "$W/$1.out" 2>&1 &
}
call toolu_a 'The lock file is stale.\nRun npm install in web/ and commit package-lock.json.'
call toolu_b '<img src=x onerror=alert(1)> note with markup'
sleep 1
# A call whose agent is gone: a row that nobody waits for.
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" "INSERT INTO tool_calls (run_id, incident_id, tool_use_id, tool, kind, arguments, status, decision, created_at) VALUES ('$RUN', 1, 'toolu_stale', 'incident_add_note', 'mutating', '{\"id\":1,\"note\":\"nobody waits for this one\"}', 'waiting', 'pending', strftime('%Y-%m-%dT%H:%M:%S','now') || '.000000000Z')"
echo "server up, run $RUN, outputs in $W"
```

- [ ] **Step 7: Check it in a real browser**

With the Playwright tools, sign in at `http://127.0.0.1:8080` (password `ui-test-password`) and check, deleting any screenshot files afterwards:

1. The sidebar lists Timeline, Incidents, **Approvals**, Runs, Settings, and **Approvals** shows a count of **3** (two live calls and the stale one) within five seconds.
2. `/approvals` has the heading **Approvals**, the section **Waiting for you** with three cards (oldest first: the note with the line break, the markup note, the stale one) and the section **History** saying "No decisions yet."
3. The first card shows `incident_add_note`, "asked just now", links **Run** and **Incident #1**, and the arguments as `id` / `note`: the note keeps its two lines. The second card shows `<img src=x onerror=alert(1)> note with markup` as literal text; no dialog opens and there is no `<img>` in the page.
4. The first two cards have a reason field, **Approve** and **Deny**. The third has neither and says that the agent is no longer waiting.
5. Type a reason in the first card and click **Approve**. The card disappears from **Waiting for you** and a row appears under **History**: `incident_add_note`, "approved", the arguments, "Reason: ...", "Result: note added". `cat "$W/toolu_a.out"` ends with an event whose text is `note added`. The sidebar count drops to 2 within five seconds.
6. Click **Deny** on the second card with the reason "not now". The history shows it as "denied" with "denied: not now", and `cat "$W/toolu_b.out"` has the text `denied: not now`.
7. The stale card stays, and the sidebar count stays at 1: nothing abandons a row that never had an agent waiting for it (in real use the gatekeeper abandons a call 30 seconds after its agent went away). It is abandoned when the run is cancelled, which Task 5 checks.
8. Sign in state: reload the page on `/approvals`; it shows the same lists. The browser console has no errors other than the 401 of the sign-in check.
9. Narrow the window to 400 px: the cards and the history do not overflow horizontally.

Stop the server afterwards (`lsof -ti tcp:8080 | xargs kill`), and delete `server.log`, `$W` and any screenshots.

- [ ] **Step 8: Commit**

Run: `make check`
Expected: everything passes.

```bash
git add web
git commit -m "feat(web): decide the calls of agents in an Approvals page with a count in the sidebar" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The run view shows the wait, the tool calls and a cancel button

**Files:**
- Create: `web/src/ToolCallsCard.tsx`
- Overwrite: `web/src/RunView.tsx`, `web/src/RunsPage.tsx`

**Interfaces:**
- Consumes: `api.getRun`, `api.listToolCalls`, `api.cancelRun`, `api.createRun(prompt, tools)`, the type `ToolCall`, `argumentList`, `outcomeText`, `callStatusColor`, `decisionLabel` (Task 4), `ConfirmButton`, `Switch`, `Label`, `timeAgo`.
- Produces:
  - `ToolCallsCard`: the audit of a run, one line per call (tool, read or mutating, status, time), with the arguments and the outcome on expansion.
  - The run view refreshes the run and its calls every 3 seconds until the run has ended, shows a banner **Waiting for your approval** with a link to Approvals while the run waits, a **Cancel run** button (with a confirmation) for a queued run or a running run with tools, "Cancelling" while the cancel has not taken effect, and badges for the failure reasons `cancelled` and `runner_lost`.
  - The runs page has a switch **Allow gatekeeper tools** that creates the run with `tools: true`.

- [ ] **Step 1: The tool calls card**

Create `web/src/ToolCallsCard.tsx`:

```tsx
import type { ToolCall } from './api.ts'
import { argumentList, callStatusColor, decisionLabel, outcomeText } from './approvals.ts'
import { timeAgo } from './incidents.ts'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

/** The audit of a run: every call the agent made to a gatekeeper tool. Nothing here is interpreted: it is all text. */
export default function ToolCallsCard({ calls }: { calls: ToolCall[] }) {
  if (calls.length === 0) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>Tool calls</CardTitle>
      </CardHeader>
      <CardContent>
        <ol className="flex flex-col divide-y divide-border">
          {calls.map((c) => (
            <li key={c.id} className="py-2 first:pt-0 last:pb-0">
              <details>
                <summary className="flex cursor-pointer flex-wrap items-center gap-2">
                  <span aria-hidden className={`h-2 w-2 shrink-0 rounded-full ${callStatusColor[c.status]}`} />
                  <span className="font-mono break-all">{c.tool}</span>
                  <Badge variant="secondary">{c.kind}</Badge>
                  <span className="text-sm text-muted-foreground">
                    {c.kind === 'mutating' ? (decisionLabel[c.decision] ?? c.decision) : c.status}
                  </span>
                  <span className="text-xs text-muted-foreground" title={new Date(c.requestedAt).toLocaleString()}>
                    {timeAgo(c.requestedAt)}
                  </span>
                </summary>
                <div className="mt-2 flex flex-col gap-2 pl-4 text-sm">
                  <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
                    {argumentList(c.arguments).map(({ name, value }) => (
                      <div key={name} className="contents">
                        <dt className="text-muted-foreground">{name}</dt>
                        <dd className="break-words whitespace-pre-wrap">{value}</dd>
                      </div>
                    ))}
                  </dl>
                  {c.reason && <p className="break-words">Reason: {c.reason}</p>}
                  <p className="break-words whitespace-pre-wrap text-muted-foreground">{outcomeText(c)}</p>
                </div>
              </details>
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 2: The run view**

Overwrite `web/src/RunView.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, streamRun } from './api.ts'
import type { Run, RunEvent, ToolCall } from './api.ts'
import { statusColor } from './status.ts'
import ToolCallsCard from './ToolCallsCard.tsx'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'

function summarize(e: RunEvent): string {
  if (e.kind === 'result' && typeof e.payload === 'object' && e.payload !== null) {
    const result = (e.payload as { result?: unknown }).result
    if (typeof result === 'string') return result
  }
  if (typeof e.payload === 'string') return e.payload
  const text = JSON.stringify(e.payload)
  return text.length > 600 ? `${text.slice(0, 600)}...` : text
}

/** Mount with `key={id}` so that switching runs resets the state instead of resetting it in the effect. */
export default function RunView({ id }: { id: string }) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  const [calls, setCalls] = useState<ToolCall[]>([])
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    api.getRun(id).then(setRun).catch(() => undefined)
    api.listToolCalls(id).then(setCalls).catch(() => undefined)
  }, [id])

  const ended = run?.status === 'succeeded' || run?.status === 'failed'

  // The run is fetched again every few seconds while it can still change: it may start waiting for an approval,
  // or be cancelled. One more fetch happens when it has ended, so that its calls are complete.
  useEffect(() => {
    refresh()
    if (ended) return
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  }, [refresh, ended])

  useEffect(
    () =>
      streamRun(
        id,
        (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])),
        setRun,
      ),
    [id],
  )

  async function cancel() {
    setError('')
    try {
      await api.cancelRun(id)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not cancel the run')
    }
    refresh()
  }

  // Events exist only after the runner has started the run, so a queued run that already has events is in fact running.
  const status = run && run.status === 'queued' && events.length > 0 ? 'running' : run?.status
  const canCancel = run !== null && !ended && !run.cancelRequested && (status === 'queued' || (status === 'running' && run.mcp === true))

  return (
    <div className="flex flex-col gap-6">
      <Link to="/runs" className="text-sm text-muted-foreground hover:text-foreground">
        ← All runs
      </Link>

      {run && status && (
        <header className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-3">
            <Badge variant="outline" className="gap-2">
              <span className={`h-2 w-2 rounded-full ${statusColor[status]}`} />
              {status}
            </Badge>
            {run.exitCode !== undefined && <span className="text-sm text-muted-foreground">exit {run.exitCode}</span>}
            {run.role === 'responder' && <Badge variant="secondary">responder</Badge>}
            {run.mcp && <Badge variant="secondary">tools</Badge>}
            {run.failureReason === 'timeout' && <Badge variant="destructive">timed out</Badge>}
            {run.failureReason === 'invalid_output' && <Badge variant="destructive">invalid answer</Badge>}
            {run.failureReason === 'cancelled' && <Badge variant="destructive">cancelled</Badge>}
            {run.failureReason === 'runner_lost' && <Badge variant="destructive">runner lost</Badge>}
            {run.incidentId !== undefined && (
              <Link to={`/incidents/${run.incidentId}`} className="text-sm text-muted-foreground hover:text-foreground">
                Incident #{run.incidentId}
              </Link>
            )}
            {canCancel && <ConfirmButton label="Cancel run" confirmLabel="Confirm cancel" onConfirm={() => void cancel()} />}
          </div>
          {run.role === 'responder' ? (
            <details className="rounded-lg bg-card p-3">
              <summary className="cursor-pointer text-sm text-muted-foreground">
                Prompt ({run.prompt.length.toLocaleString()} characters, it contains data from GitHub)
              </summary>
              <p className="mt-2 font-mono text-xs break-words whitespace-pre-wrap">{run.prompt}</p>
            </details>
          ) : (
            <p className="whitespace-pre-wrap rounded-lg bg-card p-3">{run.prompt}</p>
          )}
          {run.failureReason && run.result && <p className="text-sm text-destructive">{run.result}</p>}
          {run.cancelRequested && !ended && (
            <p className="text-sm text-amber-400">Cancelling: the runner is stopping the agent.</p>
          )}
        </header>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {run?.waitingApproval !== undefined && !ended && (
        <Alert>
          <AlertDescription>
            This run is waiting for your approval.{' '}
            <Link to="/approvals" className="underline underline-offset-4">
              Open the approvals
            </Link>
          </AlertDescription>
        </Alert>
      )}

      <ToolCallsCard calls={calls} />

      <ol className="flex flex-col gap-2 font-mono text-sm">
        {events.map((e) => (
          <li key={e.seq} className="rounded-lg border border-border bg-card/50 p-2">
            <Badge variant="secondary" className="mr-2">
              {e.kind}
            </Badge>
            <span className="whitespace-pre-wrap break-words text-muted-foreground">{summarize(e)}</span>
          </li>
        ))}
      </ol>

      {status === 'running' && run?.waitingApproval === undefined && (
        <p className="text-sm text-amber-400">Waiting for more output...</p>
      )}
    </div>
  )
}
```

- [ ] **Step 3: The runs page**

Overwrite `web/src/RunsPage.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Run } from './api.ts'
import { statusColor } from './status.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

export default function RunsPage() {
  const navigate = useNavigate()
  const [runs, setRuns] = useState<Run[]>([])
  const [prompt, setPrompt] = useState('')
  const [tools, setTools] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    api.listRuns().then(setRuns).catch((e: unknown) => setError(String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [refresh])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const created = await api.createRun(prompt, tools)
      setPrompt('')
      navigate(`/runs/${created.id}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start the run')
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-2xl font-semibold tracking-tight">Runs</h1>

      <Card>
        <CardHeader>
          <CardTitle>Start a run</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="flex flex-col gap-3">
            <Textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={4}
              placeholder="What should the agent do?"
              aria-label="Prompt"
            />
            <div className="flex items-center gap-3">
              <Switch id="tools" checked={tools} onCheckedChange={setTools} />
              <Label htmlFor="tools">Allow gatekeeper tools</Label>
            </div>
            <p className="text-sm text-muted-foreground">
              {tools
                ? 'The agent can read incidents and ask to add a note to one. Every note waits for your decision under Approvals, and the run waits with it.'
                : 'The agent can only read the files of its workspace.'}
            </p>
            <Button type="submit" className="self-start" disabled={prompt.trim() === ''}>
              Start run
            </Button>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
          </form>
        </CardContent>
      </Card>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm uppercase tracking-wide text-muted-foreground">Recent runs</h2>
        {runs.length === 0 && <p className="text-muted-foreground">No runs yet.</p>}
        {runs.map((r) => (
          <Link
            key={r.id}
            to={`/runs/${r.id}`}
            className="flex items-center gap-3 rounded-lg border border-border bg-card/50 px-3 py-2 transition-colors hover:border-ring"
          >
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[r.status]}`} />
            <span className="flex-1 truncate">{r.prompt}</span>
            {r.mcp && <span className="text-xs text-muted-foreground">tools</span>}
            <span className="text-xs text-muted-foreground">{new Date(r.createdAt).toLocaleString()}</span>
          </Link>
        ))}
      </section>
    </div>
  )
}
```

- [ ] **Step 4: Lint and build**

Run: `cd web && npm run lint && npm run build`
Expected: oxlint reports no warnings or errors; `tsc -b` and `vite build` succeed.

- [ ] **Step 5: Check it in a real browser**

Use the script of Task 4, Step 6 again (a fresh database and server). Add a second run first, so that the runs page has a plain one: `curl -s -b "$W/cookies" -H 'X-Remedy-CSRF: 1' -d '{"prompt":"a plain run"}' $B/api/runs` (it stays queued, there is no runner). Add one run that the control plane lost: `sqlite3 "$REMEDY_DB" "INSERT INTO runs (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at, role, output, failure_reason, head_sha, automatic, mcp) VALUES ('lost-run', 'claude', 'a run whose runner was lost', 'failed', -1, 'The runner stopped reporting for more than 2m0s and the run was failed by the control plane.', '', 0, '2026-10-02T09:00:00.000000000Z', '2026-10-02T09:00:01.000000000Z', '2026-10-02T09:05:00.000000000Z', 'adhoc', NULL, 'runner_lost', '', 0, 1)"`.

With the Playwright tools, check:

1. `/runs`: the form has a switch **Allow gatekeeper tools** (off) and the text "The agent can only read the files of its workspace." Turning it on changes the text to the one about notes and Approvals. Submitting a prompt with the switch on creates a run (check the request body in the network tab: `"tools":true`) and opens its page; the run list marks it **tools**.
2. The page of the run from the script (open it from the list): the badge **running**, the badge **tools**, the banner **This run is waiting for your approval** with a working link **Open the approvals**, and the card **Tool calls** with three lines (`incident_add_note`, mutating, "waiting for you"). Opening one shows its arguments as text (the markup note as literal text) and "Waiting for a decision."
3. Approve one call on `/approvals` and come back: within three seconds the line says "approved", and after the result "Result: note added" shows when opened.
4. **Cancel run** is there. A first click arms it ("Confirm cancel"); clicking elsewhere disarms it; a second click cancels: the page shows "Cancelling: the runner is stopping the agent." (the seeded run has no runner, so it stays running) and the button is gone. The two other calls are abandoned: the banner disappears and the lines say "abandoned".
5. The queued plain run: its page has **Cancel run** (the run has no tools but it is queued). Cancelling it makes it **failed** with the badge **cancelled** within three seconds.
6. The run `lost-run` shows the badge **runner lost**, the status **failed** and the result text in red, and no cancel button.
7. The console has no errors other than the 401 of the sign-in check.

Stop the server afterwards, and delete `server.log`, the temporary directory and any screenshots.

- [ ] **Step 6: Commit**

Run: `make check`
Expected: everything passes.

```bash
git add web
git commit -m "feat(web): show a run's wait, its tool calls and a cancel button, and start runs with tools" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 6: The runbook, the real run and the documents

**Files:**
- Create: `docs/runbook/gatekeeper-real-run.md`, `docs/research/phase-2ab-real-run.md` (written during the run)
- Modify: `README.md`, `CLAUDE.md`, `docs/specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`, `docs/design.md`

**Interfaces:**
- Consumes: everything of plans 2a and 2b, the real `claude` CLI logged in with the maintainer's subscription, and `scripts/check-no-token-leak.sh` (plan 1d).
- Produces: a runbook for the run, the record of it, and the status of the documents.

The run needs no GitHub token: the incident it works on is seeded into a fresh database. It uses subscription quota (a few cents per run, as the CLI reports it). Steps 2 to 7 are done by the assistant with the Playwright tools; the maintainer needs to do nothing unless the CLI is not logged in.

- [ ] **Step 1: Write the runbook**

Create `docs/runbook/gatekeeper-real-run.md`:

````markdown
# Runbook: a run with gatekeeper tools against the real CLI

This runs the gatekeeper and the approvals once with the real `claude` CLI, and checks the success criteria of the
[spec](../specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md#12-success-criteria). It needs no GitHub token:
the incident the agent works on is put into a fresh database. Plan on about 40 minutes, most of it waiting.

## 1. Build and start

```sh
make web-install && make build
mkdir -p ~/remedy-gate-run && cd ~/remedy-gate-run
cat > env.sh <<EOF
export REMEDY_ADMIN_PASSWORD='$(openssl rand -hex 12)'
export REMEDY_RUNNER_TOKEN='$(openssl rand -hex 24)'
export REMEDY_MASTER_KEY='$(openssl rand -base64 32)'
export REMEDY_DB='$PWD/remedy.db' REMEDY_LOG_LEVEL=debug
EOF
chmod 600 env.sh && . ./env.sh

<path-to-remedy>/bin/remedy-server > server.log 2>&1 &
<path-to-remedy>/bin/remedy-runner > runner.log 2>&1 &
```

The `claude` CLI must be installed and logged in. The runner's time limit stays at its default of 10 minutes: the point of
scenario A is that a run which waits longer than that is not stopped.

Put an incident into the database (the server has created it on start):

```sh
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" <<'SQL'
INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
VALUES (1, x'00', 'seed', 'octo', 'error', 'Seeded for the real run; polling is off.', '2026-10-02T12:00:00.000000000Z');
INSERT INTO repos (id, connection_id, full_name, default_branch, enabled, created_at)
VALUES (1, 1, 'octo/hello', 'main', 1, '2026-10-02T12:00:00.000000000Z');
INSERT INTO incidents (id, repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, resolved_at, resolved_reason, diagnosis, diagnosed_sha, run_id)
VALUES (1, 1, 'pr:7', 'https://github.com/octo/hello/pull/7', 'web', 'open', 'failure', 'abc1234def5678', '', 1, 0, '2026-10-02T10:00:00.000000000Z', '2026-10-02T10:00:00.000000000Z', NULL, NULL, '', NULL, '', NULL);
SQL
```

Sign in at <http://localhost:8080> with `REMEDY_ADMIN_PASSWORD` from `env.sh`. For the `curl` checks below, keep a session in a file:

```sh
printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" | curl -sf -c cookies -H 'X-Remedy-CSRF: 1' --data-binary @- http://localhost:8080/api/login
```

## 2. Scenario A: a wait that outlasts the runner's time limit

Under **Runs**, switch on **Allow gatekeeper tools** and start a run with the prompt

> Use your tools to look at incident 1, then add a short note to it that says what you found. Reply with "finished" when the note is added.

Expected: the run page shows the badge **tools**, the **Tool calls** card gets a read call (`incident_get` or
`incident_list`) and then an `incident_add_note` call "waiting for you", the banner "This run is waiting for your approval"
appears, and **Approvals** in the sidebar shows 1. The card shows the note the agent wants to add.

Now wait **more than 11 minutes** without deciding. Every minute check that the run is still `running`:
`curl -s -b cookies http://localhost:8080/api/runs/<id> | jq .status`, and that `runner.log` has no "run timed out" and no "heartbeat failed".

While it waits, take the run token from the file the runner wrote (the leak check needs it later) and keep it only in a file with mode 0600:

```sh
WS=${REMEDY_WORKSPACES:-${TMPDIR:-/tmp}/remedy-workspaces}   # the runner logs it as "workspaces" at start
(umask 077; jq -r '.mcpServers.remedy.headers.Authorization' "$WS"/*-mcp-*/mcp.json | sed 's/^Bearer //' > token.txt)
ls -l "$WS"/*-mcp-*/mcp.json     # -rw------- and a directory of its own, next to the workspace of the run
```

Then **Approve** with a reason. Expected: the agent receives "note added" and finishes, the run ends `succeeded`, the incident's
history (`/incidents/1`) shows "Note added to incident #1 by an agent: ...", the Timeline shows `approval_requested`,
`approval_decided` and `note_added`, and the config file and its directory are gone from the workspace root.

## 3. Scenario B: a denial

Start the same run again and **Deny** with the reason "not now". Expected: the agent is told `denied: not now`, says so and finishes
(its final answer is not "finished" with a note added), the incident has no second note, and the call shows as denied in the history.

## 4. Scenario C: cancelling a waiting run

Start the run again and, while it waits, click **Cancel run** and confirm. Expected: "Cancelling: the runner is stopping the agent." for
a few seconds, then the run is `failed` with the badge **cancelled**, the approval is abandoned (it leaves the waiting list), no `claude`
process of that run is left (`pgrep -fl 'mcp-config'` shows nothing), and the runner takes the next run at once: start a short plain
run and see it finish.

## 5. Scenario D: the CLI is stopped with SIGTERM

Start the run again and wait for the approval. Stop the CLI the way a pod stop would:

```sh
pkill -TERM -f 'mcp-config'
```

The CLI replays its in-flight call while it shuts down (see the [spike](../research/spike-mcp-blocking.md)). Expected: **never a second approval**:
`curl -s -b cookies http://localhost:8080/api/runs/<id>/tool-calls | jq '[.[] | select(.kind=="mutating")] | length'` is 1 for that run, whatever else happens. The run ends
(failed, the agent was killed) and its approval is abandoned.

## 6. Scenario E: the runner dies

Start the run again, wait for the approval, and kill the runner hard (`kill -9` its process); then `pkill -f 'mcp-config'` for the orphaned CLI.
Wait about three minutes. Expected: the reaper fails the run with the badge **runner lost** and the result text about the runner, and the
approval is abandoned. Start the runner again; it works as before.

## 7. The audit

- Run token: `scripts/check-no-token-leak.sh http://localhost:8080 server.log runner.log "$REMEDY_DB" < token.txt` (from the Remedy
  checkout, with `REMEDY_ADMIN_PASSWORD` exported). It must end with "the token appears nowhere that was searched": the token is in no log, not in
  the database (only its hash is), and in no admin API answer, the run events included.
- `grep -c 'could not' server.log runner.log` and a look at both logs: no warnings that are not explained by a scenario.
- The audit of a run: `curl -s -b cookies http://localhost:8080/api/runs/<id>/tool-calls | jq` lists every call, with its arguments, outcome and decision.

## 8. Clean up

Stop the server and the runner, `pkill -f 'mcp-config'`, and delete `~/remedy-gate-run` (it holds the master key and the database).
````

- [ ] **Step 2: (assistant) Build, start and seed**

Follow section 1 of the runbook. Check that the server and the runner are up (`curl -s localhost:8080/healthz`, `tail runner.log`), that the incident exists (`sqlite3 "$REMEDY_DB" 'select id, state from incidents'`), and sign in with the Playwright tools. If the CLI says it is not logged in, stop and ask the maintainer to run `claude` and `/login`.

- [ ] **Step 3: (assistant) Run the scenarios A to E**

Do sections 2 to 6 of the runbook with the Playwright tools for the UI and the shell for the rest, taking notes with times: when each run started, when the approval appeared (seconds after the start), how long the wait was, when the decision was made, how the run ended, what the agent said, and what each check showed. While scenario A waits, use the time for scenario B only after A has ended: the runner runs one run at a time, so the scenarios are sequential.

If something does not behave as the runbook says, stop and look at it before going on: it is a finding. Do not weaken a check to make it pass.

- [ ] **Step 4: (assistant) Run the audit**

Do section 7 of the runbook and keep the output.

- [ ] **Step 5: (assistant) Record the run**

Create `docs/research/phase-2ab-real-run.md` with these headings, filled with what was observed (times, texts and numbers as they were, nothing rounded up):

```markdown
# Phase 2 (parts A and B) run against the real CLI

Date, Remedy commit, `claude --version`, the model, the runner's time limit and heartbeat interval.

## What was run
The database (a fresh one with a seeded incident), the five scenarios, who did what.

## Scenario A: a wait longer than the runner's time limit
Start, first read call, approval requested (seconds after the start), the wait in minutes, checks during the wait,
the decision, what the agent said, how the run ended, the cost, the note in the incident history, the config file.

## Scenario B: a denial
## Scenario C: cancelling a waiting run
How long the cancel took, what was left of the process, whether the runner took the next run.
## Scenario D: SIGTERM
How many approvals there were for the run (must be 1), how the run ended.
## Scenario E: the runner dies
How long it took until the run was failed as runner lost, what the approval did.

## Audit
The output of the leak check, the audit of a run, anything in the logs.

## Success criteria
One line per criterion of section 12 of the spec: met or not met, with the evidence above.

## Problems found
Each thing that went wrong or surprised, what caused it, and where it was fixed or filed.
```

If a criterion is **not met**, say so in the record and do not write "done" anywhere else in this task. Fix the cause in a separate pull request with a test first, repeat the scenario that failed, and update the record.

- [ ] **Step 6: (assistant) Clean up**

Section 8 of the runbook. Check that no `claude` process of the run is left and that nothing listens on port 8080. Delete screenshots from the checkout that started the browser.

- [ ] **Step 7: (assistant) Update the documents**

Do this only for what the record shows; where a criterion is not met, say what is open instead.

In `README.md`, replace:

```markdown
> client is read-only down to its HTTP transport. The fixer, the approval gatekeeper and the learning graph come next (see
```

with:

```markdown
> client is read-only down to its HTTP transport. Agents get tools through an MCP gatekeeper whose mutating tools wait for your
> approval. The fixer, cluster access and the learning graph come next (see
```

In `README.md`, replace:

```markdown
Under **Runs** you can start a read-only
agent run by hand.
```

with:

```markdown
Under **Runs** you can start an agent run by
hand, with or without the gatekeeper tools: a run with tools can read incidents and ask to add a note to one, and every note waits
for your decision under **Approvals** (the run waits with it, as long as it takes; **Cancel run** ends it). A first run against the
real CLI is described in [`docs/runbook/gatekeeper-real-run.md`](docs/runbook/gatekeeper-real-run.md) and
[`docs/research/phase-2ab-real-run.md`](docs/research/phase-2ab-real-run.md).
```

In `CLAUDE.md`, replace:

```markdown
a reaper for runs that stay `running` (`internal/reaper`),
```

with:

```markdown
a reaper for runs that stay `running` (`internal/reaper`: 15 minutes for a run without gatekeeper access, 2 minutes without a heartbeat for one with it),
```

In `CLAUDE.md`, replace:

```markdown
It has no GitHub, cluster or DB credentials.
```

with:

```markdown
It has no GitHub, cluster or DB credentials. A run with gatekeeper access also gets a heartbeat (`POST /runner/v1/runs/{id}/heartbeat`, every 10 s): its answer stops the run's time budget while the run waits for an approval (`runClock`) and carries a cancel; a cancel or a 404 stops the agent (the context is cancelled, which `SIGKILL`s the CLI), and a heartbeat that fails never does.
```

In `CLAUDE.md`, replace:

```markdown
(only the builtin plugins and the explicit MCP server load).
```

with:

```markdown
(only the builtin plugins and the explicit MCP server load). The runner does exactly that for a claim that carries `mcp_token` (`provider.Spec.MCPConfig`): `--mcp-config <file> --allowedTools mcp__remedy`, with the config in a 0600 file in a directory of its own next to the workspace, removed when the run ends.
```

In `CLAUDE.md`, replace:

```markdown
plan 2a, the gatekeeper on the server side, is implemented; plan 2b (runner, heartbeat, reaper rule, UI, the real run) is next.
```

with:

```markdown
plans 2a (the gatekeeper on the server side) and 2b (runner, heartbeat, reaper rule, UI) are implemented, and `docs/research/phase-2ab-real-run.md` records a run against the real CLI. Phase 2 parts C (cluster) and D (signals) are later cycles.
```

In `docs/specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`, replace:

```markdown
; plan 2b (steps 4 to 6: runner, heartbeat, reaper, UI, the real run) follows.
```

with:

```markdown
; [`phase-2b`](../plans/phase-2b-runner-and-approvals-ui.md) (steps 4 to 6: runner, heartbeat, reaper, UI, the real run), implemented. The run against the real CLI is recorded in [`phase-2ab-real-run.md`](../research/phase-2ab-real-run.md).
```

In `docs/design.md`, replace:

```markdown
the Home Assistant notification, are separate later cycles.
```

with:

```markdown
the Home Assistant notification, are separate later cycles. Phase 2 is delivered in parts as well: the gatekeeper and the approvals
(parts A and B) are implemented, cluster access and the signal adapters are later cycles.
```

- [ ] **Step 8: Check and commit**

Run: `make check`
Expected: everything passes (the documents and the runbook are not part of it).

Check that no token, password or master key is in the files: `git diff --cached | grep -E 'ghp_|github_pat_|REMEDY_MASTER_KEY=.{20}'` must print nothing.

```bash
git add docs README.md CLAUDE.md
git commit -m "docs: add the runbook and the record of the run against the real CLI, and update the status of phase 2 parts A and B" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
