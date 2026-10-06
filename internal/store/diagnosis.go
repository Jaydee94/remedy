package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// runColsList is runCols for lists: a responder prompt can be 200 KB, so only its start is returned.
var runColsList = strings.Replace(runCols, " prompt,", " substr(prompt, 1, 300),", 1)

var (
	// ErrBusy means a run is queued or running: the runner is sequential, so only one run is allowed at a time.
	ErrBusy = errors.New("a run is already queued or running")
	// ErrNotDiagnosable means the incident is not open or diagnosed.
	ErrNotDiagnosable = errors.New("the incident cannot be diagnosed in its current state")
	// ErrLimit means an automatic diagnosis is not allowed right now. errors.As to *LimitError gives the reason.
	ErrLimit = errors.New("automatic diagnosis is not allowed right now")
)

// LimitError says why an automatic diagnosis was refused.
type LimitError struct{ Reason string }

func (e *LimitError) Error() string { return "automatic diagnosis refused: " + e.Reason }

func (e *LimitError) Is(target error) bool { return target == ErrLimit }

// DiagnosisLimits are the limits of automatic diagnosis (spec section 5).
type DiagnosisLimits struct {
	Cooldown       time.Duration // per incident, between two automatic diagnoses
	MaxPerIncident int           // automatic diagnoses per incident
	MaxPerDay      int           // automatic runs in a rolling 24 hours; 0 turns automatic diagnosis off
}

func DefaultLimits() DiagnosisLimits {
	return DiagnosisLimits{Cooldown: 15 * time.Minute, MaxPerIncident: 3, MaxPerDay: 20}
}

type StartParams struct {
	IncidentID int64
	Provider   string
	Prompt     string
	HeadSHA    string // the commit the prompt and the snapshot are for
	Automatic  bool
	Limits     DiagnosisLimits // only used for an automatic start
	Now        time.Time
}

// StartDiagnosis creates a queued responder run for an incident and marks the incident diagnosing, in
// one transaction. act describes the activity entry; its run and incident are filled in.
func (s *Store) StartDiagnosis(ctx context.Context, p StartParams, act NewActivity) (run.Run, error) {
	id := run.NewID()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		in, err := scanIncident(tx.QueryRowContext(ctx, `SELECT `+incidentCols+incidentFrom+` WHERE i.id = ?`, p.IncidentID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.State != IncOpen && in.State != IncDiagnosed {
			return ErrNotDiagnosable
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status IN ('queued', 'running')`).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return ErrBusy
		}
		if p.Automatic {
			if err := checkAutomatic(ctx, tx, in, p); err != nil {
				return err
			}
		}

		automatic := 0
		if p.Automatic {
			automatic = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, provider, prompt, status, role, incident_id, head_sha, automatic, created_at)
			VALUES (?, ?, ?, 'queued', 'responder', ?, ?, ?, ?)`,
			id, p.Provider, p.Prompt, p.IncidentID, p.HeadSHA, automatic, formatTS(p.Now)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'diagnosing', run_id = ?, last_diagnosis_at = ?, diagnoses = diagnoses + ?
			WHERE id = ?`, id, formatTS(p.Now), automatic, p.IncidentID); err != nil {
			return err
		}
		act.IncidentID, act.RepoID, act.RunID = p.IncidentID, in.RepoID, id
		return insertActivity(ctx, tx, act)
	})
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

func checkAutomatic(ctx context.Context, tx *sql.Tx, in Incident, p StartParams) error {
	if !in.AutoDiagnose {
		return &LimitError{Reason: fmt.Sprintf("a %s result is not diagnosed automatically", in.Conclusion)}
	}
	if in.State == IncDiagnosed && in.HeadSHA == in.DiagnosedSHA {
		return &LimitError{Reason: "this commit was diagnosed already"}
	}
	if in.Diagnoses >= p.Limits.MaxPerIncident {
		return &LimitError{Reason: fmt.Sprintf("%d automatic diagnoses of this incident were started already", in.Diagnoses)}
	}
	if in.LastDiagnosisAt != nil && p.Now.Sub(*in.LastDiagnosisAt) < p.Limits.Cooldown {
		return &LimitError{Reason: fmt.Sprintf("the cooldown of %s has not passed", p.Limits.Cooldown)}
	}
	var today int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM runs WHERE role = 'responder' AND automatic = 1 AND created_at > ?`,
		formatTS(p.Now.Add(-24*time.Hour))).Scan(&today); err != nil {
		return err
	}
	if today >= p.Limits.MaxPerDay {
		return &LimitError{Reason: fmt.Sprintf("%d automatic runs in the last 24 hours", today)}
	}
	return nil
}

// CompleteDiagnosis stores a validated diagnosis on the incident of a responder run, if the run is the
// latest one of that incident. A diagnosing or open incident becomes diagnosed (an open one is an incident that was
// un-ignored between the end of the run and this call); an ignored or resolved incident stays as it is.
func (s *Store) CompleteDiagnosis(ctx context.Context, runID string, diagnosis json.RawMessage, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var incidentID sql.NullInt64
		var sha string
		err := tx.QueryRowContext(ctx, `SELECT incident_id, head_sha FROM runs WHERE id = ?`, runID).Scan(&incidentID, &sha)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !incidentID.Valid) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET diagnosis = ?, diagnosed_sha = ?, diagnosed_at = ?,
				state = CASE WHEN state IN ('diagnosing', 'open') THEN 'diagnosed' ELSE state END
			WHERE id = ? AND run_id = ?`, string(diagnosis), sha, formatTS(time.Now()), incidentID.Int64, runID)); err != nil {
			return err
		}
		return s.logRunActivity(ctx, tx, incidentID.Int64, runID, act)
	})
}

// FailDiagnosis closes the diagnosis of a responder run that failed, if the run is the latest one of its
// incident. A diagnosing incident goes back to diagnosed if it has a diagnosis from before, else to open.
func (s *Store) FailDiagnosis(ctx context.Context, runID string, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var incidentID sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT incident_id FROM runs WHERE id = ?`, runID).Scan(&incidentID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !incidentID.Valid) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = CASE WHEN state = 'diagnosing'
				THEN (CASE WHEN diagnosis IS NULL THEN 'open' ELSE 'diagnosed' END) ELSE state END
			WHERE id = ? AND run_id = ?`, incidentID.Int64, runID)); err != nil {
			return err
		}
		return s.logRunActivity(ctx, tx, incidentID.Int64, runID, act)
	})
}

func (s *Store) logRunActivity(ctx context.Context, tx *sql.Tx, incidentID int64, runID string, act NewActivity) error {
	var repoID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT repo_id FROM incidents WHERE id = ?`, incidentID).Scan(&repoID); err != nil {
		return err
	}
	act.IncidentID, act.RepoID, act.RunID = incidentID, repoID.Int64, runID
	return insertActivity(ctx, tx, act)
}

// ListAutoCandidates returns the incidents an automatic diagnosis may pick now, oldest first: a real
// failure that is open (or diagnosed for an older commit), in an enabled repo, below the per-incident cap
// and past the cooldown. The daily limit and "one run at a time" are checked by StartDiagnosis.
//
// An incident of another source has no repository, so the join with an enabled repository leaves it out: the responder does
// not handle those sources until plan 2d-3 lifts this.
func (s *Store) ListAutoCandidates(ctx context.Context, now time.Time, l DiagnosisLimits) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+incidentFrom+`
		WHERE i.auto_diagnose = 1
		  AND (i.state = 'open' OR (i.state = 'diagnosed' AND i.head_sha <> i.diagnosed_sha))
		  AND i.diagnoses < ?
		  AND (i.last_diagnosis_at IS NULL OR i.last_diagnosis_at <= ?)
		  AND r.enabled = 1
		ORDER BY i.first_seen, i.id LIMIT 20`, l.MaxPerIncident, formatTS(now.Add(-l.Cooldown)))
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}

// AutoRunsSince counts the automatic responder runs created after since.
func (s *Store) AutoRunsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE role = 'responder' AND automatic = 1 AND created_at > ?`, formatTS(since)).Scan(&n)
	return n, err
}

// HasActiveRun reports whether a run is queued or running.
func (s *Store) HasActiveRun(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status IN ('queued', 'running')`).Scan(&n)
	return n > 0, err
}
