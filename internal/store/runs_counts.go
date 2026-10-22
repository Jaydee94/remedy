package store

import "context"

// RunCounts is how many runs are running and how many are queued. The login relay refuses to start while a run is
// running, and the Setup page says how many runs wait for a login.
func (s *Store) RunCounts(ctx context.Context) (running, queued int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN status = 'queued' THEN 1 ELSE 0 END), 0) FROM runs`).Scan(&running, &queued)
	return running, queued, err
}
