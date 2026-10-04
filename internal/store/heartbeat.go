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
