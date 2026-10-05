package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// runColsIncident is runCols for the runs of an incident: only a responder prompt can be 200 KB, so only that kind is cut. The
// prompt of an ad-hoc run is the maintainer's question and is returned whole.
var runColsIncident = strings.Replace(runCols, " prompt,", " CASE WHEN role = 'responder' THEN substr(prompt, 1, 300) ELSE prompt END,", 1)

// CreateIncidentRun queues a responder run for an incident. It fails if the incident does not exist.
func (s *Store) CreateIncidentRun(ctx context.Context, incidentID int64, provider, prompt string) (run.Run, error) {
	id := run.NewID()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (id, provider, prompt, status, role, incident_id, created_at)
		VALUES (?, ?, ?, 'queued', 'responder', ?, ?)`,
		id, provider, prompt, incidentID, formatTS(time.Now()))
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

// CreateQuestionRun queues an ad-hoc run with gatekeeper access that is about an incident: the prompt is the maintainer's
// question, and the incident is linked so that its thread can show the run. It is not a responder run. It returns
// ErrNotFound for an unknown incident and then creates nothing.
func (s *Store) CreateQuestionRun(ctx context.Context, provider, prompt string, incidentID int64, cluster bool) (run.Run, error) {
	id := run.NewID()
	clusterFlag := 0
	if cluster {
		clusterFlag = 1
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE id = ?`, incidentID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, provider, prompt, status, mcp, cluster, incident_id, created_at)
			VALUES (?, ?, ?, 'queued', 1, ?, ?, ?)`,
			id, provider, prompt, clusterFlag, incidentID, formatTS(time.Now()))
		return err
	})
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

// ListIncidentRuns returns the runs of an incident of every role, newest first. The list is never nil.
func (s *Store) ListIncidentRuns(ctx context.Context, incidentID int64, limit int) ([]run.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColsIncident+` FROM runs WHERE incident_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, incidentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []run.Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
