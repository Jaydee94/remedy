package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Activity kinds.
const (
	KindIncidentOpened    = "incident_opened"
	KindIncidentRecurred  = "incident_recurred"
	KindIncidentResolved  = "incident_resolved"
	KindIncidentIgnored   = "incident_ignored"
	KindPollFailed        = "poll_failed"
	KindPollRecovered     = "poll_recovered"
	KindConnectionChanged = "connection_changed"
	KindRepoAdded         = "repo_added"
	KindRepoRemoved       = "repo_removed"
)

type IncidentState string

const (
	IncOpen       IncidentState = "open"
	IncDiagnosing IncidentState = "diagnosing"
	IncDiagnosed  IncidentState = "diagnosed"
	IncResolved   IncidentState = "resolved"
	IncIgnored    IncidentState = "ignored"
)

type Incident struct {
	ID             int64
	RepoID         int64
	RepoName       string
	Ref            string // "pr:<number>" or "branch:<name>"
	RefURL         string
	CheckName      string
	State          IncidentState
	Conclusion     string
	HeadSHA        string
	CheckURL       string
	Occurrences    int
	FirstSeen      time.Time
	LastSeen       time.Time
	ResolvedAt     *time.Time
	ResolvedReason string
}

type NewIncident struct {
	RepoID                                                int64
	Ref, RefURL, CheckName, Conclusion, HeadSHA, CheckURL string
}

// IncidentFilter selects incidents. State is "" or "all" for every incident, "active" for open,
// diagnosing and diagnosed ones, or one state.
type IncidentFilter struct {
	State  string
	RepoID int64
	Limit  int // default 200
}

const (
	incidentCols = `i.id, i.repo_id, r.full_name, i.ref, i.ref_url, i.check_name, i.state, i.conclusion,
		i.head_sha, i.check_url, i.occurrences, i.first_seen, i.last_seen, i.resolved_at, i.resolved_reason`
	incidentFrom = ` FROM incidents i JOIN repos r ON r.id = i.repo_id`
)

func scanIncident(sc scanner) (Incident, error) {
	var (
		in          Incident
		state       string
		first, last string
		resolved    sql.NullString
	)
	if err := sc.Scan(&in.ID, &in.RepoID, &in.RepoName, &in.Ref, &in.RefURL, &in.CheckName, &state, &in.Conclusion,
		&in.HeadSHA, &in.CheckURL, &in.Occurrences, &first, &last, &resolved, &in.ResolvedReason); err != nil {
		return Incident{}, err
	}
	in.State = IncidentState(state)
	var err error
	if in.FirstSeen, err = parseTS(first); err != nil {
		return Incident{}, err
	}
	if in.LastSeen, err = parseTS(last); err != nil {
		return Incident{}, err
	}
	if resolved.Valid {
		t, err := parseTS(resolved.String)
		if err != nil {
			return Incident{}, err
		}
		in.ResolvedAt = &t
	}
	return in, nil
}

func collectIncidents(rows *sql.Rows) ([]Incident, error) {
	defer rows.Close()
	list := []Incident{}
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, in)
	}
	return list, rows.Err()
}

// inTx runs fn in a transaction. The store has a single connection, so fn must only use tx.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// oneRow turns "no row changed" into ErrNotFound.
func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// OpenIncident creates an open incident and logs act in the same transaction. It returns ErrExists
// if the key already has an active incident.
func (s *Store) OpenIncident(ctx context.Context, n NewIncident, act NewActivity) (Incident, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := formatTS(time.Now())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO incidents (repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url,
				occurrences, first_seen, last_seen)
			VALUES (?, ?, ?, ?, 'open', ?, ?, ?, 1, ?, ?)`,
			n.RepoID, n.Ref, n.RefURL, n.CheckName, n.Conclusion, n.HeadSHA, n.CheckURL, now, now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return ErrExists
			}
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		act.RepoID, act.IncidentID = n.RepoID, id
		return insertActivity(ctx, tx, act)
	})
	if err != nil {
		return Incident{}, err
	}
	return s.GetIncident(ctx, id)
}

// RecordRecurrence records a new failing commit of an incident that is not resolved.
func (s *Store) RecordRecurrence(ctx context.Context, id int64, conclusion, headSHA, checkURL string, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET occurrences = occurrences + 1, conclusion = ?, head_sha = ?, check_url = ?, last_seen = ?
			WHERE id = ? AND state <> 'resolved'`, conclusion, headSHA, checkURL, formatTS(time.Now()), id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// TouchIncident records that the incident was seen failing again on the same commit. It writes no
// activity entry.
func (s *Store) TouchIncident(ctx context.Context, id int64) error {
	return oneRow(s.db.ExecContext(ctx,
		`UPDATE incidents SET last_seen = ? WHERE id = ? AND state <> 'resolved'`, formatTS(time.Now()), id))
}

// ResolveIncident resolves an incident that is not resolved yet.
func (s *Store) ResolveIncident(ctx context.Context, id int64, reason string, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'resolved', resolved_at = ?, resolved_reason = ?
			WHERE id = ? AND state <> 'resolved'`, formatTS(time.Now()), reason, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// IgnoreIncident moves an open, diagnosing or diagnosed incident to ignored.
func (s *Store) IgnoreIncident(ctx context.Context, id int64, act NewActivity) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'ignored'
			WHERE id = ? AND state IN ('open', 'diagnosing', 'diagnosed')`, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

func (s *Store) GetIncident(ctx context.Context, id int64) (Incident, error) {
	in, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incidentCols+incidentFrom+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return in, err
}

// FindActiveIncident returns the incident of a key that is not resolved (ErrNotFound if there is none).
func (s *Store) FindActiveIncident(ctx context.Context, repoID int64, ref, checkName string) (Incident, error) {
	in, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incidentCols+incidentFrom+
		` WHERE i.repo_id = ? AND i.ref = ? AND i.check_name = ? AND i.state <> 'resolved'`, repoID, ref, checkName))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return in, err
}

// ListActiveIncidents returns the incidents of a repo that are not resolved.
func (s *Store) ListActiveIncidents(ctx context.Context, repoID int64) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+incidentFrom+
		` WHERE i.repo_id = ? AND i.state <> 'resolved' ORDER BY i.id`, repoID)
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}

// ListIncidents returns incidents, the most recently seen first.
func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	var (
		conds []string
		args  []any
	)
	switch f.State {
	case "", "all":
	case "active":
		conds = append(conds, `i.state IN ('open', 'diagnosing', 'diagnosed')`)
	default:
		conds = append(conds, `i.state = ?`)
		args = append(args, f.State)
	}
	if f.RepoID != 0 {
		conds = append(conds, `i.repo_id = ?`)
		args = append(args, f.RepoID)
	}
	query := `SELECT ` + incidentCols + incidentFrom
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	query += ` ORDER BY i.last_seen DESC, i.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}

// NewActivity describes an activity entry. A zero RepoID, IncidentID or RunID means no link, and nil
// Data means {}.
type NewActivity struct {
	Kind       string
	RepoID     int64
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}

type Activity struct {
	ID         int64
	At         time.Time
	Kind       string
	RepoID     int64
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}

// ActivityQuery selects activity entries. IncidentID 0 means all of them; Limit defaults to 100.
type ActivityQuery struct {
	IncidentID int64
	Limit      int
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertActivity(ctx context.Context, q execer, a NewActivity) error {
	data := a.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	if !json.Valid(data) {
		return errors.New("activity data is not valid JSON")
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO activity (at, kind, repo_id, incident_id, run_id, summary, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		formatTS(time.Now()), a.Kind, nullInt(a.RepoID), nullInt(a.IncidentID), nullString(a.RunID), a.Summary, string(data))
	return err
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// AddActivity appends an entry to the activity log.
func (s *Store) AddActivity(ctx context.Context, a NewActivity) error {
	return insertActivity(ctx, s.db, a)
}

// ListActivity returns activity entries, newest first.
func (s *Store) ListActivity(ctx context.Context, q ActivityQuery) ([]Activity, error) {
	query := `SELECT id, at, kind, repo_id, incident_id, run_id, summary, data FROM activity`
	var args []any
	if q.IncidentID != 0 {
		query += ` WHERE incident_id = ?`
		args = append(args, q.IncidentID)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Activity{}
	for rows.Next() {
		var (
			a         Activity
			at, data  string
			repo, inc sql.NullInt64
			runID     sql.NullString
		)
		if err := rows.Scan(&a.ID, &at, &a.Kind, &repo, &inc, &runID, &a.Summary, &data); err != nil {
			return nil, err
		}
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		a.RepoID, a.IncidentID, a.RunID = repo.Int64, inc.Int64, runID.String
		a.Data = json.RawMessage(data)
		list = append(list, a)
	}
	return list, rows.Err()
}

// MarkRepoPolled records the time and the outcome of a polling attempt. An empty lastError means it worked.
func (s *Store) MarkRepoPolled(ctx context.Context, id int64, at time.Time, lastError string) error {
	return oneRow(s.db.ExecContext(ctx,
		`UPDATE repos SET last_polled_at = ?, last_error = ? WHERE id = ?`, formatTS(at), lastError, id))
}
