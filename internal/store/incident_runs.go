package store

import (
	"context"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

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
