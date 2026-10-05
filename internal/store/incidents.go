package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	KindDiagnosisStarted  = "diagnosis_started"
	KindDiagnosisFinished = "diagnosis_finished"
	KindDiagnosisFailed   = "diagnosis_failed"
)

// The sources of an incident. A source decides what its incidents are called (Key) and whether the responder may
// start on its own.
const (
	SourceGitHub       = "github"
	SourceAlertmanager = "alertmanager"
	SourceArgoCD       = "argocd"
)

var sources = []string{SourceGitHub, SourceAlertmanager, SourceArgoCD}

// Severities of an incident. GitHub incidents have none.
var severities = []string{"critical", "warning", "info", "none"}

type IncidentState string

const (
	IncOpen       IncidentState = "open"
	IncDiagnosing IncidentState = "diagnosing"
	IncDiagnosed  IncidentState = "diagnosed"
	IncResolved   IncidentState = "resolved"
	IncIgnored    IncidentState = "ignored"
)

type Incident struct {
	ID int64
	// Source is github, alertmanager or argocd. Key is the identity inside the source, Title a short line for the
	// list, Severity critical, warning, info or none, and Details the signal as JSON (an object).
	Source       string
	Key          string
	Title        string
	Severity     string
	AutoDiagnose bool // the responder may start on its own; decided by the source
	Details      json.RawMessage

	// The GitHub fields. RepoID is 0 and the others are empty for an incident of another source.
	RepoID         int64
	RepoName       string
	Ref            string // "pr:<number>" or "branch:<name>"
	RefURL         string
	CheckName      string
	State          IncidentState
	Conclusion     string // for a GitHub incident the conclusion of the check; for another source its state (firing, degraded, ...)
	HeadSHA        string
	CheckURL       string
	Occurrences    int
	FirstSeen      time.Time
	LastSeen       time.Time
	ResolvedAt     *time.Time
	ResolvedReason string

	// Diagnoses counts the automatic diagnoses that were started.
	Diagnoses       int
	LastDiagnosisAt *time.Time
	// Diagnosis is the validated diagnosis as JSON, nil until there is one.
	Diagnosis json.RawMessage
	// DiagnosedSHA is the commit the diagnosis is about.
	DiagnosedSHA string
	// RunID is the latest responder run of the incident.
	RunID string
}

// NewIncident describes an incident to open. An empty Source means GitHub: then RepoID is required, Key and Title
// default to the key of the check and its name, and AutoDiagnose is decided from the conclusion. Any other source needs
// Key, Title and Conclusion, takes no repository, and decides AutoDiagnose itself.
type NewIncident struct {
	Source                                                string
	Key, Title, Severity                                  string
	AutoDiagnose                                          bool
	Details                                               json.RawMessage
	RepoID                                                int64
	Ref, RefURL, CheckName, Conclusion, HeadSHA, CheckURL string
}

// GitHubKey is the key of a GitHub incident: the repository, the ref and the name of the check, joined with U+001F,
// which no ref can contain. Migration 008 computes the same string in SQL for the incidents that exist.
func GitHubKey(repoID int64, ref, checkName string) string {
	return strconv.FormatInt(repoID, 10) + "\x1f" + ref + "\x1f" + checkName
}

// githubAutoDiagnoses says whether the responder starts on its own for a check conclusion. Cancelled and
// action_required results are shown, and diagnosed by a click.
func githubAutoDiagnoses(conclusion string) bool {
	switch conclusion {
	case "failure", "timed_out", "startup_failure":
		return true
	}
	return false
}

// ErrInvalidIncident means NewIncident cannot be stored.
var ErrInvalidIncident = errors.New("invalid incident")

// normalize fills the defaults of n and checks it.
func (n NewIncident) normalize() (NewIncident, error) {
	bad := func(msg string) (NewIncident, error) {
		return NewIncident{}, fmt.Errorf("%w: %s", ErrInvalidIncident, msg)
	}
	if n.Source == "" {
		n.Source = SourceGitHub
	}
	if !slices.Contains(sources, n.Source) {
		return bad("unknown source " + strconv.Quote(n.Source))
	}
	if n.Severity == "" {
		n.Severity = "none"
	}
	if !slices.Contains(severities, n.Severity) {
		return bad("unknown severity " + strconv.Quote(n.Severity))
	}
	if len(n.Details) == 0 {
		n.Details = json.RawMessage(`{}`)
	}
	if !json.Valid(n.Details) || n.Details[0] != '{' {
		return bad("details must be a JSON object")
	}
	if n.Source == SourceGitHub {
		if n.RepoID == 0 {
			return bad("a GitHub incident needs a repository")
		}
		if n.Key == "" {
			n.Key = GitHubKey(n.RepoID, n.Ref, n.CheckName)
		}
		if n.Title == "" {
			n.Title = n.CheckName
		}
		n.AutoDiagnose = githubAutoDiagnoses(n.Conclusion)
		return n, nil
	}
	if n.RepoID != 0 {
		return bad("only a GitHub incident belongs to a repository")
	}
	if n.Key == "" || n.Title == "" || n.Conclusion == "" {
		return bad("an incident of this source needs a key, a title and a conclusion")
	}
	return n, nil
}

// IncidentFilter selects incidents. State is "" or "all" for every incident, "active" for open,
// diagnosing and diagnosed ones, or one state.
type IncidentFilter struct {
	State  string
	RepoID int64
	Source string // "" for every source
	Limit  int    // default 200
}

const (
	incidentCols = `i.id, i.source, i.key, i.title, i.severity, i.auto_diagnose, i.details,
		i.repo_id, r.full_name, i.ref, i.ref_url, i.check_name, i.state, i.conclusion,
		i.head_sha, i.check_url, i.occurrences, i.first_seen, i.last_seen, i.resolved_at, i.resolved_reason,
		i.diagnoses, i.last_diagnosis_at, i.diagnosis, i.diagnosed_sha, i.run_id`
	incidentFrom = ` FROM incidents i LEFT JOIN repos r ON r.id = i.repo_id`
)

func scanIncident(sc scanner) (Incident, error) {
	var (
		in          Incident
		state       string
		first, last string
		resolved    sql.NullString
		lastDiag    sql.NullString
		diagnosis   sql.NullString
		runID       sql.NullString
		details     string
		repoID      sql.NullInt64
		repoName    sql.NullString
	)
	if err := sc.Scan(&in.ID, &in.Source, &in.Key, &in.Title, &in.Severity, &in.AutoDiagnose, &details,
		&repoID, &repoName, &in.Ref, &in.RefURL, &in.CheckName, &state, &in.Conclusion,
		&in.HeadSHA, &in.CheckURL, &in.Occurrences, &first, &last, &resolved, &in.ResolvedReason,
		&in.Diagnoses, &lastDiag, &diagnosis, &in.DiagnosedSHA, &runID); err != nil {
		return Incident{}, err
	}
	in.State = IncidentState(state)
	in.Details = json.RawMessage(details)
	in.RepoID, in.RepoName = repoID.Int64, repoName.String
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
	if lastDiag.Valid {
		t, err := parseTS(lastDiag.String)
		if err != nil {
			return Incident{}, err
		}
		in.LastDiagnosisAt = &t
	}
	if diagnosis.Valid {
		in.Diagnosis = json.RawMessage(diagnosis.String)
	}
	in.RunID = runID.String
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
	n, err := n.normalize()
	if err != nil {
		return Incident{}, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		now := formatTS(time.Now())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO incidents (source, key, title, severity, auto_diagnose, details, repo_id, ref, ref_url, check_name,
				state, conclusion, head_sha, check_url, occurrences, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, 1, ?, ?)`,
			n.Source, n.Key, n.Title, n.Severity, n.AutoDiagnose, string(n.Details), nullInt(n.RepoID), n.Ref, n.RefURL,
			n.CheckName, n.Conclusion, n.HeadSHA, n.CheckURL, now, now)
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
			UPDATE incidents SET occurrences = occurrences + 1, conclusion = ?, head_sha = ?, check_url = ?, last_seen = ?,
				auto_diagnose = CASE WHEN source = 'github' THEN ? ELSE auto_diagnose END
			WHERE id = ? AND state <> 'resolved'`,
			conclusion, headSHA, checkURL, formatTS(time.Now()), githubAutoDiagnoses(conclusion), id)); err != nil {
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

// FindActiveIncident returns the GitHub incident of a check on a ref that is not resolved (ErrNotFound if there is none).
func (s *Store) FindActiveIncident(ctx context.Context, repoID int64, ref, checkName string) (Incident, error) {
	return s.FindActiveIncidentByKey(ctx, SourceGitHub, GitHubKey(repoID, ref, checkName))
}

// FindActiveIncidentByKey returns the incident of a source and key that is not resolved (ErrNotFound if there is none).
func (s *Store) FindActiveIncidentByKey(ctx context.Context, source, key string) (Incident, error) {
	in, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incidentCols+incidentFrom+
		` WHERE i.source = ? AND i.key = ? AND i.state <> 'resolved'`, source, key))
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
	if f.Source != "" {
		conds = append(conds, `i.source = ?`)
		args = append(args, f.Source)
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
	RepoName   string // empty when the entry has no repository, or the repository was removed
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}

// ActivityQuery selects activity entries. IncidentID 0 means all of them. Before is a cursor: only entries
// with a smaller id, 0 means no cursor. Limit defaults to 100.
type ActivityQuery struct {
	IncidentID int64
	Before     int64
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

const activitySelect = `SELECT a.id, a.at, a.kind, a.repo_id, a.incident_id, a.run_id, a.summary, a.data, r.full_name
	FROM activity a LEFT JOIN repos r ON r.id = a.repo_id`

func activityLimit(n int) int {
	if n <= 0 {
		return 100
	}
	return n
}

// ListActivity returns activity entries, newest first.
func (s *Store) ListActivity(ctx context.Context, q ActivityQuery) ([]Activity, error) {
	var where []string
	var args []any
	if q.IncidentID != 0 {
		where = append(where, `a.incident_id = ?`)
		args = append(args, q.IncidentID)
	}
	if q.Before != 0 {
		where = append(where, `a.id < ?`)
		args = append(args, q.Before)
	}
	query := activitySelect
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, activityLimit(q.Limit))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectActivity(rows)
}

// ListActivitySince returns the entries with an id above afterID, oldest first. It is how a stream follows
// the log.
func (s *Store) ListActivitySince(ctx context.Context, afterID int64, limit int) ([]Activity, error) {
	rows, err := s.db.QueryContext(ctx, activitySelect+` WHERE a.id > ? ORDER BY a.id LIMIT ?`, afterID, activityLimit(limit))
	if err != nil {
		return nil, err
	}
	return collectActivity(rows)
}

// LastActivityID returns the highest activity id, or 0 when the log is empty.
func (s *Store) LastActivityID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM activity`).Scan(&id); err != nil {
		return 0, err
	}
	return id.Int64, nil
}

func collectActivity(rows *sql.Rows) ([]Activity, error) {
	defer rows.Close()
	list := []Activity{}
	for rows.Next() {
		var (
			a               Activity
			at, data        string
			repo, inc       sql.NullInt64
			runID, repoName sql.NullString
		)
		if err := rows.Scan(&a.ID, &at, &a.Kind, &repo, &inc, &runID, &a.Summary, &data, &repoName); err != nil {
			return nil, err
		}
		var err error
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		a.RepoID, a.RepoName, a.IncidentID, a.RunID = repo.Int64, repoName.String, inc.Int64, runID.String
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
