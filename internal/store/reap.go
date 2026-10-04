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
