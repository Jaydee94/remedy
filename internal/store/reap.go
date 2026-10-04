package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// FailStaleRuns fails the runs that are still running and started before cutoff: no runner ever
// finished them. It returns their IDs. result becomes the run's result text if it has none.
func (s *Store) FailStaleRuns(ctx context.Context, cutoff time.Time, result string) ([]string, error) {
	var ids []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		rows, err := tx.QueryContext(ctx, `
			UPDATE runs SET status = 'failed', exit_code = -1, failure_reason = ?, finished_at = ?,
				result = CASE WHEN result = '' THEN ? ELSE result END
			WHERE status = 'running' AND started_at < ?
			RETURNING id`, run.ReasonTimeout, formatTS(now), result, formatTS(cutoff))
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
