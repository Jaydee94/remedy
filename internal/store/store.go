// Package store persists runs and run events in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/Jaydee94/remedy/internal/run"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a run does not exist or is not in the expected state.
var ErrNotFound = errors.New("not found")

// tsLayout is fixed-width so that string order equals time order.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

const runCols = `id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at,
	role, incident_id, output, failure_reason`

type Store struct{ db *sql.DB }

// Open opens (and migrates) the SQLite database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps SQLite's single-writer model simple and race-free.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries { // ReadDir returns entries sorted by filename
		var applied int
		err := s.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ?`, e.Name()).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, e.Name()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) (time.Time, error) { return time.Parse(tsLayout, s) }

type scanner interface{ Scan(dest ...any) error }

func scanRun(sc scanner) (run.Run, error) {
	var (
		r                 run.Run
		status, created   string
		role              string
		exit, incident    sql.NullInt64
		started, finished sql.NullString
		output            sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.Provider, &r.Prompt, &status, &exit, &r.Result, &r.SessionID,
		&r.CostUSD, &created, &started, &finished, &role, &incident, &output, &r.FailureReason); err != nil {
		return run.Run{}, err
	}
	r.Status = run.Status(status)
	r.Role = run.Role(role)
	if incident.Valid {
		r.IncidentID = &incident.Int64
	}
	if output.Valid {
		r.Output = json.RawMessage(output.String)
	}
	if exit.Valid {
		code := int(exit.Int64)
		r.ExitCode = &code
	}
	var err error
	if r.CreatedAt, err = parseTS(created); err != nil {
		return run.Run{}, err
	}
	if started.Valid {
		t, err := parseTS(started.String)
		if err != nil {
			return run.Run{}, err
		}
		r.StartedAt = &t
	}
	if finished.Valid {
		t, err := parseTS(finished.String)
		if err != nil {
			return run.Run{}, err
		}
		r.FinishedAt = &t
	}
	return r, nil
}

func (s *Store) CreateRun(ctx context.Context, provider, prompt string) (run.Run, error) {
	id := run.NewID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES (?, ?, ?, 'queued', ?)`,
		id, provider, prompt, formatTS(time.Now()))
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *Store) GetRun(ctx context.Context, id string) (run.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return run.Run{}, ErrNotFound
	}
	return r, err
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]run.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runCols+` FROM runs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
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

// ClaimNext atomically moves the oldest queued run to running. It returns nil when none is queued.
func (s *Store) ClaimNext(ctx context.Context) (*run.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE runs SET status = 'running', started_at = ?
		WHERE id = (SELECT id FROM runs WHERE status = 'queued' ORDER BY created_at, id LIMIT 1)
		RETURNING `+runCols, formatTS(time.Now()))
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// AppendEvent stores the next event of a run. payload must be valid JSON.
func (s *Store) AppendEvent(ctx context.Context, runID, kind string, payload json.RawMessage) (run.Event, error) {
	if !json.Valid(payload) {
		return run.Event{}, errors.New("payload is not valid JSON")
	}
	now := time.Now()
	var seq int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO run_events (run_id, seq, kind, payload, created_at)
		SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ? FROM run_events WHERE run_id = ?
		RETURNING seq`, runID, kind, string(payload), formatTS(now), runID).Scan(&seq)
	if err != nil {
		return run.Event{}, err
	}
	return run.Event{Seq: seq, Kind: kind, Payload: payload, CreatedAt: now.UTC()}, nil
}

// Events returns the events of a run with seq greater than afterSeq, in order.
func (s *Store) Events(ctx context.Context, runID string, afterSeq int) ([]run.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, kind, payload, created_at FROM run_events WHERE run_id = ? AND seq > ? ORDER BY seq`,
		runID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []run.Event{}
	for rows.Next() {
		var (
			e              run.Event
			payload, ctime string
		)
		if err := rows.Scan(&e.Seq, &e.Kind, &payload, &ctime); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		if e.CreatedAt, err = parseTS(ctime); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// FinishRun records the outcome of a running run. It returns ErrNotFound if the run is not running.
func (s *Store) FinishRun(ctx context.Context, id string, o run.Outcome) error {
	status := run.Succeeded
	if o.ExitCode != 0 {
		status = run.Failed
	}
	var output any // NULL unless the run produced structured output
	if len(o.Output) > 0 && string(o.Output) != "null" {
		output = string(o.Output)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, exit_code = ?, result = ?, session_id = ?, cost_usd = ?, output = ?,
			failure_reason = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		string(status), o.ExitCode, o.Result, o.SessionID, o.CostUSD, output, o.FailureReason, formatTS(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("run %s is not running: %w", id, ErrNotFound)
	}
	return nil
}
