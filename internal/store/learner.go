package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
)

// Activity kinds of the learning run. The text of an entry names the host, never what the agent found.
const (
	KindLearnStarted = "learn_started"
	KindLearnFailed  = "learn_failed"
)

var (
	// ErrLearning means a learning run for the host is queued or running.
	ErrLearning = errors.New("a learning run for this host is queued or running")
	// ErrProposalsWaiting means so many proposals of the host wait for a review that another learning run could not store its own.
	ErrProposalsWaiting = errors.New("too many proposals wait for a review")
	// ErrTooManyProposals means a set of proposals would take the host past skill.MaxPendingPerHost. It is the backstop of
	// ErrProposalsWaiting: the start of a run is refused first, so a finished run can store its set.
	ErrTooManyProposals = errors.New("too many proposals would wait for a review")
)

// learnRoom is how many proposals may wait for a host when a learning run starts: the run can add its profile and its skills and stay at
// skill.MaxPendingPerHost.
const learnRoom = skill.MaxPendingPerHost - skill.MaxProposalsPerRun - 1

// LearnStart says how a learning run is created.
type LearnStart struct {
	HostID   int64
	Provider string
	Prompt   string
	Now      time.Time
}

// StartLearning creates the queued learning run for a host, with its binding to that host, in one transaction, and logs it. A learning
// run has gatekeeper access (mcp), no cluster tools, one host and is never automatic. It returns ErrNotFound for an unknown host,
// ErrLearning when a learning run for the host is queued or running, ErrBusy when any run is, and ErrProposalsWaiting when more than
// learnRoom proposals of the host wait for a review. It creates nothing then.
func (s *Store) StartLearning(ctx context.Context, p LearnStart) (run.Run, error) {
	id := run.NewID()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, p.HostID)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM runs r JOIN run_hosts h ON h.run_id = r.id
			WHERE r.role = 'learner' AND r.status IN ('queued', 'running') AND h.host_id = ?`, p.HostID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrLearning
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status IN ('queued', 'running')`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrBusy
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE host_id = ? AND state = 'proposed'`, p.HostID).Scan(&n); err != nil {
			return err
		}
		if n > learnRoom {
			return ErrProposalsWaiting
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, provider, prompt, status, role, mcp, cluster, automatic, host_count, created_at)
			VALUES (?, ?, ?, 'queued', 'learner', 1, 0, 0, 1, ?)`, id, p.Provider, p.Prompt, formatTS(p.Now)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_hosts (run_id, host_id) VALUES (?, ?)`, id, p.HostID); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindLearnStarted, RunID: id, Summary: "Learning about " + name + " started"})
	})
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

// LastLearnerRun returns the newest learning run that was started for the host. ok is false when there is none (or the host was deleted:
// its bindings go with it).
func (s *Store) LastLearnerRun(ctx context.Context, hostID int64) (r run.Run, ok bool, err error) {
	r, err = scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = (
		SELECT r2.id FROM runs r2 JOIN run_hosts h ON h.run_id = r2.id
		WHERE r2.role = 'learner' AND h.host_id = ? ORDER BY r2.created_at DESC, r2.id DESC LIMIT 1)`, hostID))
	if errors.Is(err, sql.ErrNoRows) {
		return run.Run{}, false, nil
	}
	if err != nil {
		return run.Run{}, false, err
	}
	return r, true, nil
}

// SucceededReads returns the commands of the host_read calls of a run that succeeded, as the words the agent sent, oldest first. A
// refused call (a command outside the table of reads) and a call that failed are not in it.
func (s *Store) SucceededReads(ctx context.Context, runID string) ([][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT arguments FROM tool_calls WHERE run_id = ? AND tool = 'host_read' AND status = 'succeeded' ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]string{}
	for rows.Next() {
		var args string
		if err := rows.Scan(&args); err != nil {
			return nil, err
		}
		var v struct {
			Argv []string `json:"argv"`
		}
		if json.Unmarshal([]byte(args), &v) != nil || len(v.Argv) == 0 {
			continue
		}
		out = append(out, v.Argv)
	}
	return out, rows.Err()
}

// CountProposals returns how many proposals of a host wait for a review.
func (s *Store) CountProposals(ctx context.Context, hostID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE host_id = ? AND state = 'proposed'`, hostID).Scan(&n)
	return n, err
}

// checkPending refuses a set of proposals that would leave the host with more than skill.MaxPendingPerHost proposals waiting. A
// pending proposal with a name in the set is replaced by it and does not count.
func checkPending(ctx context.Context, tx *sql.Tx, hostID int64, items []Proposal) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE host_id = ? AND state = 'proposed'`, hostID).Scan(&pending); err != nil {
		return err
	}
	for _, it := range items {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE host_id = ? AND name = ? AND state = 'proposed'`, hostID, it.Name).Scan(&n); err != nil {
			return err
		}
		pending -= n
	}
	if pending+len(items) > skill.MaxPendingPerHost {
		return fmt.Errorf("%w: %d would wait for the host, at most %d", ErrTooManyProposals, pending+len(items), skill.MaxPendingPerHost)
	}
	return nil
}
