package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// Tool call statuses, decisions and kinds.
const (
	CallRunning   = "running"
	CallWaiting   = "waiting"
	CallSucceeded = "succeeded"
	CallFailed    = "failed"
	CallDenied    = "denied"
	CallAbandoned = "abandoned"

	DecisionPending   = "pending"
	DecisionApproved  = "approved"
	DecisionDenied    = "denied"
	DecisionAbandoned = "abandoned"

	CallKindRead     = "read"
	CallKindMutating = "mutating"
)

// Activity kinds of the gatekeeper.
const (
	KindApprovalRequested = "approval_requested"
	KindApprovalDecided   = "approval_decided"
	KindApprovalAbandoned = "approval_abandoned"
	KindNoteAdded         = "note_added"
	KindRunCancelled      = "run_cancelled"
)

const (
	// MaxCallsPerRun is how many tool calls a run may make.
	MaxCallsPerRun = 100
	// MaxPendingPerRun is how many calls of a run may wait for a decision at the same time.
	MaxPendingPerRun = 5
)

var (
	// ErrCallLimit means a run has made too many calls, or has too many waiting for a decision.
	ErrCallLimit = errors.New("tool call limit reached")
	// ErrNotPending means a call is not waiting for a decision, or its run is no longer running.
	ErrNotPending = errors.New("the approval is not pending")
	// ErrNotCancellable means the run cannot be cancelled in its current state.
	ErrNotCancellable = errors.New("the run cannot be cancelled")
)

// ToolCall is one call of an agent to a gatekeeper tool: a row of the audit log, and for a mutating tool the approval.
type ToolCall struct {
	ID             int64
	RunID          string
	IncidentID     int64 // 0 when the call is not about an incident
	ToolUseID      string
	Tool           string
	Kind           string
	Arguments      json.RawMessage
	Status         string
	Result         string
	Error          string
	Decision       string // empty for a read tool
	DecisionReason string
	DecidedAt      *time.Time
	CreatedAt      time.Time
	FinishedAt     *time.Time
}

type NewToolCall struct {
	RunID      string
	ToolUseID  string
	Tool       string
	Kind       string
	IncidentID int64 // 0 when none; an incident that does not exist is not linked
	Arguments  json.RawMessage
}

const callCols = `id, run_id, incident_id, tool_use_id, tool, kind, arguments, status, result, error, decision,
	decision_reason, decided_at, created_at, finished_at`

func optTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := parseTS(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanCall(sc scanner) (ToolCall, error) {
	var (
		c                 ToolCall
		incident          sql.NullInt64
		args, created     string
		decided, finished sql.NullString
	)
	if err := sc.Scan(&c.ID, &c.RunID, &incident, &c.ToolUseID, &c.Tool, &c.Kind, &args, &c.Status, &c.Result,
		&c.Error, &c.Decision, &c.DecisionReason, &decided, &created, &finished); err != nil {
		return ToolCall{}, err
	}
	c.IncidentID = incident.Int64
	c.Arguments = json.RawMessage(args)
	var err error
	if c.CreatedAt, err = parseTS(created); err != nil {
		return ToolCall{}, err
	}
	if c.DecidedAt, err = optTime(decided); err != nil {
		return ToolCall{}, err
	}
	if c.FinishedAt, err = optTime(finished); err != nil {
		return ToolCall{}, err
	}
	return c, nil
}

func collectCalls(rows *sql.Rows) ([]ToolCall, error) {
	defer rows.Close()
	calls := []ToolCall{}
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

func forIncident(c ToolCall) string {
	if c.IncidentID == 0 {
		return ""
	}
	return fmt.Sprintf(" on incident #%d", c.IncidentID)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// callActivity logs act for a call: linked to its run and incident, and to the incident's repository.
func callActivity(ctx context.Context, tx *sql.Tx, kind, summary string, c ToolCall) error {
	var repoID int64
	if c.IncidentID != 0 {
		var repo sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT repo_id FROM incidents WHERE id = ?`, c.IncidentID).Scan(&repo); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		repoID = repo.Int64
	}
	data, _ := json.Marshal(map[string]any{"callId": c.ID, "tool": c.Tool})
	return insertActivity(ctx, tx, NewActivity{
		Kind: kind, RepoID: repoID, IncidentID: c.IncidentID, RunID: c.RunID, Summary: summary, Data: data,
	})
}

// BeginToolCall records a call of a running run. When the run already made a call with this tool use ID, that
// first call is returned and nothing is written: a repeat (the CLI replays an in-flight call when it is stopped
// with SIGTERM) must never become a second call. A new call needs a running run that was not cancelled, and
// stays within MaxCallsPerRun and MaxPendingPerRun.
func (s *Store) BeginToolCall(ctx context.Context, n NewToolCall) (ToolCall, bool, error) {
	var (
		out     ToolCall
		existed bool
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		first, err := scanCall(tx.QueryRowContext(ctx,
			`SELECT `+callCols+` FROM tool_calls WHERE run_id = ? AND tool_use_id = ?`, n.RunID, n.ToolUseID))
		if err == nil {
			out, existed = first, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		var live int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM runs WHERE id = ? AND status = 'running' AND cancel_requested = 0`, n.RunID).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return ErrNotFound
		}
		var total, waiting int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(SUM(status = 'waiting'), 0) FROM tool_calls WHERE run_id = ?`, n.RunID).Scan(&total, &waiting); err != nil {
			return err
		}
		if total >= MaxCallsPerRun {
			return fmt.Errorf("%w: a run may make %d calls", ErrCallLimit, MaxCallsPerRun)
		}
		mutating := n.Kind == CallKindMutating
		if mutating && waiting >= MaxPendingPerRun {
			return fmt.Errorf("%w: at most %d calls may wait for a decision", ErrCallLimit, MaxPendingPerRun)
		}

		incident := n.IncidentID
		if incident != 0 {
			var one int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM incidents WHERE id = ?`, incident).Scan(&one); errors.Is(err, sql.ErrNoRows) {
				incident = 0
			} else if err != nil {
				return err
			}
		}
		status, decision := CallRunning, ""
		if mutating {
			status, decision = CallWaiting, DecisionPending
		}
		args := n.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO tool_calls (run_id, incident_id, tool_use_id, tool, kind, arguments, status, decision, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.RunID, nullInt(incident), n.ToolUseID, n.Tool, n.Kind, string(args), status, decision, formatTS(time.Now()))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if out, err = scanCall(tx.QueryRowContext(ctx, `SELECT `+callCols+` FROM tool_calls WHERE id = ?`, id)); err != nil {
			return err
		}
		if mutating {
			return callActivity(ctx, tx, KindApprovalRequested,
				fmt.Sprintf("The agent asks for approval to run %s%s", out.Tool, forIncident(out)), out)
		}
		return nil
	})
	if err != nil {
		return ToolCall{}, false, err
	}
	return out, existed, nil
}

// FinishToolCall ends a running call as succeeded or failed.
func (s *Store) FinishToolCall(ctx context.Context, id int64, status, result, errText string) error {
	if status != CallSucceeded && status != CallFailed {
		return fmt.Errorf("a call cannot be finished as %q", status)
	}
	return oneRow(s.db.ExecContext(ctx, `
		UPDATE tool_calls SET status = ?, result = ?, error = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`, status, result, errText, formatTS(time.Now()), id))
}

func (s *Store) GetToolCall(ctx context.Context, id int64) (ToolCall, error) {
	c, err := scanCall(s.db.QueryRowContext(ctx, `SELECT `+callCols+` FROM tool_calls WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ToolCall{}, ErrNotFound
	}
	return c, err
}

// ListToolCalls returns the calls of a run, oldest first.
func (s *Store) ListToolCalls(ctx context.Context, runID string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+callCols+` FROM tool_calls WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	return collectCalls(rows)
}

type ApprovalFilter struct {
	PendingOnly bool
	Limit       int // default 100
}

// ListApprovals returns the mutating calls, newest first.
func (s *Store) ListApprovals(ctx context.Context, f ApprovalFilter) ([]ToolCall, error) {
	query := `SELECT ` + callCols + ` FROM tool_calls WHERE decision != ''`
	if f.PendingOnly {
		query += ` AND decision = 'pending'`
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return collectCalls(rows)
}

// PendingApprovalForRun returns the ID of the approval a run is waiting for, or 0.
func (s *Store) PendingApprovalForRun(ctx context.Context, runID string) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(id) FROM tool_calls WHERE run_id = ? AND decision = 'pending'`, runID).Scan(&id)
	return id.Int64, err
}

// DecideApproval records the maintainer's decision. Approving only records it: the call stays waiting until a
// handler wins BeginExecution. Denying ends the call as denied. The call must be waiting for a decision and its run
// must be running and not cancelled.
func (s *Store) DecideApproval(ctx context.Context, id int64, approve bool, reason string) (ToolCall, error) {
	var out ToolCall
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		c, err := scanCall(tx.QueryRowContext(ctx, `SELECT `+callCols+` FROM tool_calls WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.Decision != DecisionPending || c.Status != CallWaiting {
			return ErrNotPending
		}
		var live int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM runs WHERE id = ? AND status = 'running' AND cancel_requested = 0`, c.RunID).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return ErrNotPending
		}

		now := formatTS(time.Now())
		verb := "Approved"
		if approve {
			_, err = tx.ExecContext(ctx,
				`UPDATE tool_calls SET decision = 'approved', decision_reason = ?, decided_at = ? WHERE id = ?`, reason, now, id)
		} else {
			verb = "Denied"
			text := "denied"
			if reason != "" {
				text += ": " + reason
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE tool_calls SET decision = 'denied', status = 'denied', decision_reason = ?, error = ?,
					decided_at = ?, finished_at = ?
				WHERE id = ?`, reason, text, now, now, id)
		}
		if err != nil {
			return err
		}
		if out, err = scanCall(tx.QueryRowContext(ctx, `SELECT `+callCols+` FROM tool_calls WHERE id = ?`, id)); err != nil {
			return err
		}
		summary := fmt.Sprintf("%s %s%s", verb, out.Tool, forIncident(out))
		if reason != "" {
			summary += ": " + truncateRunes(reason, 200)
		}
		return callActivity(ctx, tx, KindApprovalDecided, summary, out)
	})
	if err != nil {
		return ToolCall{}, err
	}
	return out, nil
}

// BeginExecution lets one caller execute an approved call: it moves the call from waiting and approved to running,
// but only while its run is running and not cancelled. The bool is true for the caller that did it. Every other
// caller gets false and must not execute the tool.
func (s *Store) BeginExecution(ctx context.Context, id int64) (ToolCall, bool, error) {
	c, err := scanCall(s.db.QueryRowContext(ctx, `
		UPDATE tool_calls SET status = 'running'
		WHERE id = ? AND status = 'waiting' AND decision = 'approved'
		  AND run_id IN (SELECT id FROM runs WHERE status = 'running' AND cancel_requested = 0)
		RETURNING `+callCols, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ToolCall{}, false, nil
	}
	if err != nil {
		return ToolCall{}, false, err
	}
	return c, true, nil
}

// abandonTx abandons the waiting calls that match cond (a constant SQL condition with args) and logs each. A call
// whose decision is still pending becomes abandoned; an approved one keeps its decision, so the log says that it was
// approved and did not run. It returns how many calls it abandoned.
func abandonTx(ctx context.Context, tx *sql.Tx, now time.Time, cond string, args ...any) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		UPDATE tool_calls SET status = 'abandoned', error = ?, finished_at = ?,
			decision = CASE WHEN decision = 'pending' THEN 'abandoned' ELSE decision END
		WHERE status = 'waiting' AND `+cond+`
		RETURNING `+callCols,
		append([]any{"abandoned: the agent stopped waiting or the run ended", formatTS(now)}, args...)...)
	if err != nil {
		return 0, err
	}
	calls, err := collectCalls(rows)
	if err != nil {
		return 0, err
	}
	for _, c := range calls {
		summary := fmt.Sprintf("The approval of %s%s was not decided: the agent stopped waiting or the run ended", c.Tool, forIncident(c))
		if c.Decision == DecisionApproved {
			summary = fmt.Sprintf("%s%s was approved but did not run: the agent stopped waiting or the run ended", c.Tool, forIncident(c))
		}
		if err := callActivity(ctx, tx, KindApprovalAbandoned, summary, c); err != nil {
			return 0, err
		}
	}
	return len(calls), nil
}

// AbandonCall abandons a waiting call. The bool says whether it did: a call that is done, or abandoned already, is left alone.
func (s *Store) AbandonCall(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = abandonTx(ctx, tx, time.Now(), `id = ?`, id)
		return err
	})
	return n > 0, err
}

// AbandonAllWaiting abandons every waiting call. The control plane calls it at startup: the requests that waited
// for them died with the process.
func (s *Store) AbandonAllWaiting(ctx context.Context) (int, error) {
	var n int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = abandonTx(ctx, tx, time.Now(), `1 = 1`)
		return err
	})
	return n, err
}

// Note is a note that an agent run added to an incident.
type Note struct {
	ID         int64
	IncidentID int64
	RunID      string
	Note       string
	CreatedAt  time.Time
}

// AddNote stores a note on an incident and logs it. It returns ErrNotFound for an unknown incident.
func (s *Store) AddNote(ctx context.Context, incidentID int64, runID, note string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var repoID int64
		err := tx.QueryRowContext(ctx, `SELECT repo_id FROM incidents WHERE id = ?`, incidentID).Scan(&repoID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO incident_notes (incident_id, run_id, note, created_at) VALUES (?, ?, ?, ?)`,
			incidentID, nullString(runID), note, formatTS(time.Now())); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{
			Kind: KindNoteAdded, RepoID: repoID, IncidentID: incidentID, RunID: runID,
			Summary: fmt.Sprintf("Note added to incident #%d by an agent: %s", incidentID, truncateRunes(note, 200)),
		})
	})
}

// ListNotes returns the notes of an incident, oldest first.
func (s *Store) ListNotes(ctx context.Context, incidentID int64) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, incident_id, run_id, note, created_at FROM incident_notes WHERE incident_id = ? ORDER BY id`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := []Note{}
	for rows.Next() {
		var (
			n       Note
			runID   sql.NullString
			created string
		)
		if err := rows.Scan(&n.ID, &n.IncidentID, &runID, &n.Note, &created); err != nil {
			return nil, err
		}
		n.RunID = runID.String
		if n.CreatedAt, err = parseTS(created); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// CancelRun cancels an ad-hoc run. A queued run ends as failed with ReasonCancelled at once. A running run that has
// gatekeeper access gets cancel_requested, which stops its token and new calls, and its waiting calls are abandoned;
// the runner learns of it from its heartbeat, stops the agent and reports the end. Any other run is ErrNotCancellable:
// a running run without gatekeeper access has no heartbeat to hear it, and a responder run belongs to its incident.
func (s *Store) CancelRun(ctx context.Context, id string) (run.Run, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if r.Role != run.RoleAdhoc {
			return ErrNotCancellable
		}
		now := time.Now()
		summary := "Run cancelled"
		switch {
		case r.Status == run.Queued:
			if _, err := tx.ExecContext(ctx, `
				UPDATE runs SET status = 'failed', exit_code = -1, failure_reason = ?, cancel_requested = 1,
					finished_at = ?, result = 'Cancelled before it started.'
				WHERE id = ? AND status = 'queued'`, run.ReasonCancelled, formatTS(now), id); err != nil {
				return err
			}
			summary = "Run cancelled before it started"
		case r.Status == run.Running && r.MCP:
			if r.CancelRequested {
				return nil // already asked: nothing changes
			}
			if _, err := tx.ExecContext(ctx, `UPDATE runs SET cancel_requested = 1 WHERE id = ?`, id); err != nil {
				return err
			}
			if _, err := abandonTx(ctx, tx, now, `run_id = ?`, id); err != nil {
				return err
			}
		default:
			return ErrNotCancellable
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindRunCancelled, RunID: id, Summary: summary})
	})
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}
