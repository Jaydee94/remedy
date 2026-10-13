package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// ErrNoSuchHost means a run was to be created with a host that does not exist.
var ErrNoSuchHost = errors.New("no such host")

// RunOptions say how an ad-hoc run is created. Tools gives it the gatekeeper's tools; Cluster adds the cluster tools,
// IncidentID links it to an incident (a question about it) and HostIDs bind hosts to it. Each of the last three implies Tools.
type RunOptions struct {
	Provider, Prompt string
	Tools            bool
	Cluster          bool
	IncidentID       int64
	HostIDs          []int64
}

func flagInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreateRunWith queues a run in one transaction together with its host bindings. It returns ErrNotFound for an unknown
// incident and ErrNoSuchHost for an unknown host, and then creates nothing. Duplicate host ids count once.
func (s *Store) CreateRunWith(ctx context.Context, o RunOptions) (run.Run, error) {
	id := run.NewID()
	var hosts []int64
	seen := map[int64]bool{}
	for _, h := range o.HostIDs {
		if !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	mcp := o.Tools || o.Cluster || o.IncidentID != 0 || len(hosts) > 0
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if o.IncidentID != 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE id = ?`, o.IncidentID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
		}
		for _, h := range hosts {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hosts WHERE id = ?`, h).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return ErrNoSuchHost
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, provider, prompt, status, mcp, cluster, incident_id, host_count, created_at)
			VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, ?)`,
			id, o.Provider, o.Prompt, flagInt(mcp), flagInt(o.Cluster), nullInt(o.IncidentID), len(hosts), formatTS(time.Now())); err != nil {
			return err
		}
		for _, h := range hosts {
			if _, err := tx.ExecContext(ctx, `INSERT INTO run_hosts (run_id, host_id) VALUES (?, ?)`, id, h); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

// RunHosts returns the hosts bound to a run with their logins, ordered by name. A host that was deleted is not in it.
func (s *Store) RunHosts(ctx context.Context, runID string) ([]HostEntry, error) {
	rows, err := s.db.QueryContext(ctx, hostSelect+` JOIN run_hosts rh ON rh.host_id = h.id WHERE rh.run_id = ? ORDER BY h.name`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostEntry{}
	for rows.Next() {
		e, err := scanHostEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
