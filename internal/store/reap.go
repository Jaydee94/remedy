package store

import (
	"context"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// FailStaleRuns fails the runs that are still running and started before cutoff: no runner ever
// finished them. It returns their IDs. result becomes the run's result text if it has none.
func (s *Store) FailStaleRuns(ctx context.Context, cutoff time.Time, result string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE runs SET status = 'failed', exit_code = -1, failure_reason = ?, finished_at = ?,
			result = CASE WHEN result = '' THEN ? ELSE result END
		WHERE status = 'running' AND started_at < ?
		RETURNING id`, run.ReasonTimeout, formatTS(time.Now()), result, formatTS(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
