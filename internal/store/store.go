// Package store persists runs and run events in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested, cluster`

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

// foreignKeysOff is the first line of a migration that must run with foreign keys off: one that rebuilds a table other
// tables point at (SQLite has no ALTER for some changes). Dropping such a table with foreign keys on runs the ON DELETE
// actions of its children. See 008_incident_sources.sql.
const foreignKeysOff = "-- remedy:foreign-keys-off\n"

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
		if err := s.apply(e.Name(), string(body)); err != nil {
			return err
		}
	}
	return nil
}

// apply runs one migration in a transaction and records it. A migration that starts with foreignKeysOff runs with foreign
// keys off, on one pinned connection (the setting belongs to the connection, and cannot change inside a transaction), and
// is checked for violations before it commits. Foreign keys are switched on again afterwards, whatever happened.
func (s *Store) apply(name, body string) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	off := strings.HasPrefix(body, foreignKeysOff)
	if off {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(body); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%s: %w", name, err)
	}
	if off {
		rows, err := tx.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		violated := rows.Next()
		_ = rows.Close()
		if violated {
			_ = tx.Rollback()
			return fmt.Errorf("%s: the migration left rows that break a foreign key", name)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
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
		automatic         int
	)
	var mcp, cancelRequested, cluster int
	if err := sc.Scan(&r.ID, &r.Provider, &r.Prompt, &status, &exit, &r.Result, &r.SessionID,
		&r.CostUSD, &created, &started, &finished, &role, &incident, &output, &r.FailureReason,
		&r.HeadSHA, &automatic, &mcp, &cancelRequested, &cluster); err != nil {
		return run.Run{}, err
	}
	r.Status = run.Status(status)
	r.Automatic = automatic != 0
	r.MCP, r.CancelRequested = mcp != 0, cancelRequested != 0
	r.Cluster = cluster != 0
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
		`SELECT `+runColsList+` FROM runs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
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
	if o.ExitCode != 0 || o.FailureReason != "" {
		status = run.Failed
	}
	var output any // NULL unless the run produced structured output
	if len(o.Output) > 0 && string(o.Output) != "null" {
		output = string(o.Output)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		res, err := tx.ExecContext(ctx, `
			UPDATE runs SET status = ?, exit_code = ?, result = ?, session_id = ?, cost_usd = ?, output = ?,
				failure_reason = ?, finished_at = ?
			WHERE id = ? AND status = 'running'`,
			string(status), o.ExitCode, o.Result, o.SessionID, o.CostUSD, output, o.FailureReason, formatTS(now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("run %s is not running: %w", id, ErrNotFound)
		}
		return closeRunTx(ctx, tx, id, now)
	})
}
