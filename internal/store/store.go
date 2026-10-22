// Package store persists runs and run events in PostgreSQL.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// ErrNotFound is returned when a run does not exist or is not in the expected state.
var ErrNotFound = errors.New("not found")

// tsLayout is fixed-width so that string order equals time order.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

const runCols = `id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at,
	role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested, cluster, host_count`

type Store struct{ db *pool }

func (s *Store) Close() error { return s.db.Close() }

// migrationsFS holds the PostgreSQL migrations: 001_baseline.sql (the schema as SQLite migration 018 left it) and the ones
// after it, applied in name order. The SQLite migrations are not here; they are the importer's test data.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrateLockClass is the first key of the advisory lock the migration runner holds while it migrates a schema; the second
// key is hashtext(current_schema()): pg_advisory_lock(migrateLockClass, hashtext(schema)). Advisory locks belong to the
// database, so the schema in the key keeps two schemas of one database (two tests of the shared test server, or two
// installations) from waiting for each other, while two processes on the same schema migrate one after the other. The value
// is 0x726d_6479, the ASCII bytes of "rmdy".
//
// Reserved advisory keys of Remedy: the two-key (int4, int4) form with migrateLockClass as its first key is the migration
// runner's alone. PostgreSQL keeps the one-key (bigint) form and the two-key form apart (they never conflict), so the
// one-key form is left to the store's transaction locks (pg_advisory_xact_lock(bigint), plan task 6). No other code may
// use the two-key form with this class.
const migrateLockClass int32 = 0x726d_6479

// errNoSchema refuses a connection whose search_path names no schema that exists: current_schema() is NULL there, and
// pg_advisory_lock, which is STRICT, would return at once without locking anything.
var errNoSchema = errors.New("store: no schema to migrate: no schema on the connection's search_path exists")

// migrate brings the schema up to date.
func (s *Store) migrate(ctx context.Context) error { return s.migrateFS(ctx, migrationsFS) }

// migrateFS applies the files migrations/*.sql of fsys that schema_migrations does not record, in name order, each in a
// transaction of its own that also records it. It holds the migration lock of the connection's schema (migrateLockClass)
// on one connection of the pool for the whole time, so that a second process that starts at once on the same schema waits and then finds every migration recorded; the transactions run on the
// same connection, so a pool of one connection works too. The lock is released on every way out: an error, a cancelled
// context (the wait for the lock ends with it) or a panic. When the release cannot be sent, the connection is dropped from
// the pool, which ends its session and with it the lock.
//
// The statements here go to the connection directly, not through the placeholder rewrite of db.go, and use $1.
func (s *Store) migrateFS(ctx context.Context, fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return err
	}
	conn, err := s.db.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var schema sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	if !schema.Valid {
		return errNoSchema
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, hashtext($2))`, migrateLockClass, schema.String); err != nil {
		// The server may have granted the lock just as the wait was given up: drop the session to be sure.
		discardConn(conn)
		return fmt.Errorf("take the migration lock: %w", err)
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		var released bool
		err := conn.QueryRowContext(uctx, `SELECT pg_advisory_unlock($1, hashtext($2))`, migrateLockClass, schema.String).Scan(&released)
		if err != nil || !released {
			discardConn(conn)
		}
	}()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		var applied int
		err := conn.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = $1`, name).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return err
		}
		if err := apply(ctx, conn, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// apply runs one migration and records it, in one transaction: PostgreSQL's DDL is transactional, so a migration that fails
// leaves nothing behind. A file may hold several statements (it is sent without arguments, in one simple query).
func apply(ctx context.Context, conn *sql.Conn, name, body string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op after the commit
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("%s: record it: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", name, err)
	}
	return nil
}

// discardConn takes conn out of the pool and closes it: its session, and every advisory lock of it, ends.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
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
	var mcp, cancelRequested, cluster, hostCount int
	if err := sc.Scan(&r.ID, &r.Provider, &r.Prompt, &status, &exit, &r.Result, &r.SessionID,
		&r.CostUSD, &created, &started, &finished, &role, &incident, &output, &r.FailureReason,
		&r.HeadSHA, &automatic, &mcp, &cancelRequested, &cluster, &hostCount); err != nil {
		return run.Run{}, err
	}
	r.HostCount = hostCount
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
	return s.CreateRunWith(ctx, RunOptions{Provider: provider, Prompt: prompt})
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
//
// Two claims at once must not get the same run: the subquery locks the row it picks and skips a row another claim (or a
// cancel) holds, and the outer guard refuses a row that is no longer queued. A run that is locked only for a moment is
// skipped by this call and found by the next one.
func (s *Store) ClaimNext(ctx context.Context) (*run.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE runs SET status = 'running', started_at = ?
		WHERE id = (SELECT id FROM runs WHERE status = 'queued' ORDER BY created_at, id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED)
		  AND status = 'queued'
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
//
// The run's row is locked first, so the appends of one run take their turns: seq has no gap and no duplicate, and a stream
// that follows the events by seq (Events) never passes over one that commits later.
func (s *Store) AppendEvent(ctx context.Context, runID, kind string, payload json.RawMessage) (run.Event, error) {
	if !json.Valid(payload) {
		return run.Event{}, errors.New("payload is not valid JSON")
	}
	now := time.Now()
	var seq int
	err := s.inTx(ctx, func(tx *txn) error {
		// No row (an unknown run) locks nothing; the insert then fails on the foreign key, as before.
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM runs WHERE id = ? FOR NO KEY UPDATE`, runID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO run_events (run_id, seq, kind, payload, created_at)
			SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ? FROM run_events WHERE run_id = ?
			RETURNING seq`, runID, kind, string(payload), formatTS(now), runID).Scan(&seq)
	})
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
	return s.inTx(ctx, func(tx *txn) error {
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
