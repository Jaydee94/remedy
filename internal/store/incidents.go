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
	KindIncidentReopened  = "incident_reopened"
	KindIncidentRecurred  = "incident_recurred"
	KindIncidentResolved  = "incident_resolved"
	KindIncidentIgnored   = "incident_ignored"
	KindIncidentUnignored = "incident_unignored"
	KindPollFailed        = "poll_failed"
	KindPollRecovered     = "poll_recovered"
	KindConnectionChanged = "connection_changed"
	KindHostChanged       = "host_changed"
	KindRepoAdded         = "repo_added"
	KindRepoRemoved       = "repo_removed"
	KindDiagnosisStarted  = "diagnosis_started"
	KindDiagnosisFinished = "diagnosis_finished"
	KindDiagnosisFailed   = "diagnosis_failed"

	// The runner's login from the Setup page. A login is not a run: these entries link to nothing.
	KindLoginStarted   = "login_started"
	KindLoginSucceeded = "login_succeeded"
	KindLoginFailed    = "login_failed"
	KindLoginCancelled = "login_cancelled"
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
	// DiagnosedAt is when the stored diagnosis was written, nil until there is one. Unlike LastDiagnosisAt, which is the
	// start of the latest attempt, a diagnosis that fails does not move it.
	DiagnosedAt *time.Time
	// Diagnosis is the validated diagnosis as JSON, nil until there is one.
	Diagnosis json.RawMessage
	// DiagnosedSHA is the commit the diagnosis is about.
	DiagnosedSHA string
	// RunID is the latest responder run of the incident.
	RunID string

	// HostID and HostName name the host an alert was mapped to, zero and empty when it was mapped to none (or the host is gone).
	HostID   int64
	HostName string

	// Firings24h counts the activity entries incident_opened and incident_reopened of the incident in the 24 hours before the
	// query, and LastFiredAt is the time of the latest one (nil when there is none). They are derived, not stored.
	Firings24h  int
	LastFiredAt *time.Time
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
	// HostID is the host an alert was mapped to; only an incident of another source can have one.
	HostID int64
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
		if n.HostID != 0 {
			return bad("only an incident of another source can belong to a host")
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

const incidentFrom = ` FROM incidents i LEFT JOIN repos r ON r.id = i.repo_id LEFT JOIN hosts h ON h.id = i.host_id`

// incidentCols is the column list scanIncident reads. The two last columns count the firings of the incident (its
// incident_opened and incident_reopened entries) since a cutoff and name the latest one. The cutoff is the column list's
// placeholder, the first of every incident query: build those with incidentSelect, which puts it first in the arguments.
const incidentCols = `i.id, i.source, i.key, i.title, i.severity, i.auto_diagnose, i.details,
			i.repo_id, r.full_name, i.ref, i.ref_url, i.check_name, i.state, i.conclusion,
			i.head_sha, i.check_url, i.occurrences, i.first_seen, i.last_seen, i.resolved_at, i.resolved_reason,
			i.diagnoses, i.last_diagnosis_at, i.diagnosed_at, i.diagnosis, i.diagnosed_sha, i.run_id, i.host_id, h.name,
			(SELECT COUNT(*) FROM activity f WHERE f.incident_id = i.id AND f.kind IN (` + firingKinds + `)
				AND f.at >= ?),
			(SELECT MAX(f.at) FROM activity f WHERE f.incident_id = i.id AND f.kind IN (` + firingKinds + `))`

// firingKinds lists the activity kinds that count as a firing, as SQL literals. It is a constant of its own so that no
// string literal of incidentCols holds both a quote and the placeholder (the source scan in db_test.go reads literals
// one at a time).
const firingKinds = `'` + KindIncidentOpened + `', '` + KindIncidentReopened + `'`

// incidentSelect returns the statement SELECT incidentCols FROM ... rest, and its arguments: the firings cutoff (24 hours
// before now) first, then args for the placeholders of rest. The text does not depend on the time.
func incidentSelect(rest string, args ...any) (string, []any) {
	all := make([]any, 0, len(args)+1)
	all = append(all, formatTS(time.Now().Add(-24*time.Hour)))
	return `SELECT ` + incidentCols + incidentFrom + rest, append(all, args...)
}

func scanIncident(sc scanner) (Incident, error) {
	var (
		in          Incident
		state       string
		first, last string
		resolved    sql.NullString
		lastDiag    sql.NullString
		diagnosedAt sql.NullString
		diagnosis   sql.NullString
		runID       sql.NullString
		details     string
		repoID      sql.NullInt64
		repoName    sql.NullString
		hostID      sql.NullInt64
		hostName    sql.NullString
		lastFired   sql.NullString
	)
	if err := sc.Scan(&in.ID, &in.Source, &in.Key, &in.Title, &in.Severity, &in.AutoDiagnose, &details,
		&repoID, &repoName, &in.Ref, &in.RefURL, &in.CheckName, &state, &in.Conclusion,
		&in.HeadSHA, &in.CheckURL, &in.Occurrences, &first, &last, &resolved, &in.ResolvedReason,
		&in.Diagnoses, &lastDiag, &diagnosedAt, &diagnosis, &in.DiagnosedSHA, &runID, &hostID, &hostName,
		&in.Firings24h, &lastFired); err != nil {
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
	if diagnosedAt.Valid {
		t, err := parseTS(diagnosedAt.String)
		if err != nil {
			return Incident{}, err
		}
		in.DiagnosedAt = &t
	}
	if diagnosis.Valid {
		in.Diagnosis = json.RawMessage(diagnosis.String)
	}
	in.RunID = runID.String
	in.HostID, in.HostName = hostID.Int64, hostName.String
	if lastFired.Valid {
		t, err := parseTS(lastFired.String)
		if err != nil {
			return Incident{}, err
		}
		in.LastFiredAt = &t
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
	err = s.inTx(ctx, func(tx *txn) error {
		now := formatTS(time.Now())
		err := tx.QueryRowContext(ctx, `
			INSERT INTO incidents (source, key, title, severity, auto_diagnose, details, repo_id, ref, ref_url, check_name,
				state, conclusion, head_sha, check_url, occurrences, first_seen, last_seen, host_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, 1, ?, ?, ?) RETURNING id`,
			n.Source, n.Key, n.Title, n.Severity, flagInt(n.AutoDiagnose), string(n.Details), nullInt(n.RepoID), n.Ref, n.RefURL,
			n.CheckName, n.Conclusion, n.HeadSHA, n.CheckURL, now, now, nullInt(n.HostID)).Scan(&id)
		if err != nil {
			if uniqueConstraint(err) == "incidents_active_key" {
				return ErrExists
			}
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
	return s.inTx(ctx, func(tx *txn) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET occurrences = occurrences + 1, conclusion = ?, head_sha = ?, check_url = ?, last_seen = ?,
				auto_diagnose = CASE WHEN source = 'github' THEN ? ELSE auto_diagnose END
			WHERE id = ? AND state <> 'resolved'`,
			conclusion, headSHA, checkURL, formatTS(time.Now()), flagInt(githubAutoDiagnoses(conclusion)), id)); err != nil {
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

// TouchIncidentSignal records that a signal is still there: it moves last_seen and writes the host the signal maps to now (0 for
// none) and its current details. It changes an incident that is not resolved and returns ErrNotFound for any other.
func (s *Store) TouchIncidentSignal(ctx context.Context, id, hostID int64, details json.RawMessage) error {
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	if !json.Valid(details) || details[0] != '{' {
		return fmt.Errorf("%w: details must be a JSON object", ErrInvalidIncident)
	}
	return oneRow(s.db.ExecContext(ctx, `
		UPDATE incidents SET last_seen = ?, host_id = ?, details = ? WHERE id = ? AND state <> 'resolved'`,
		formatTS(time.Now()), nullInt(hostID), string(details), id))
}

// ListUnresolvedBySource returns the incidents of a source that are not resolved, ignored ones included, oldest first.
func (s *Store) ListUnresolvedBySource(ctx context.Context, source string) ([]Incident, error) {
	q, args := incidentSelect(` WHERE i.source = ? AND i.state <> 'resolved' ORDER BY i.first_seen, i.id`, source)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return collectIncidents(rows)
}

// ResolveIncident resolves an incident that is not resolved yet. It remembers whether the incident was ignored, so that a
// reopen can leave it ignored.
func (s *Store) ResolveIncident(ctx context.Context, id int64, reason string, act NewActivity) error {
	return s.inTx(ctx, func(tx *txn) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'resolved', resolved_at = ?, resolved_reason = ?, resolved_while_ignored = CASE WHEN state = 'ignored' THEN 1 ELSE 0 END
			WHERE id = ? AND state <> 'resolved'`, formatTS(time.Now()), reason, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// FindLatestResolvedByKey returns the most recently resolved incident of a source and key (ErrNotFound if there is none).
func (s *Store) FindLatestResolvedByKey(ctx context.Context, source, key string) (Incident, error) {
	q, args := incidentSelect(
		` WHERE i.source = ? AND i.key = ? AND i.state = 'resolved' ORDER BY i.resolved_at DESC, i.id DESC LIMIT 1`, source, key)
	in, err := scanIncident(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return in, err
}

// Reopen is the new observation of an alert that comes back: the fields a reopen refreshes. AutoDiagnose applies only to an
// incident that was never diagnosed.
type Reopen struct {
	Title, Severity string
	AutoDiagnose    bool
	Details         json.RawMessage
	HostID          int64
}

// ReopenIncident moves a resolved incident back to where it was and logs act in the same transaction. The state is ignored
// when the incident was ignored when it resolved, else what UnignoreIncident derives (diagnosing while a responder run is
// queued or running, diagnosed with a stored diagnosis, else open). It raises occurrences, sets last_seen, clears the
// resolution and refreshes title, severity, details and host; diagnoses, the cooldown and the diagnosis stay. auto_diagnose
// follows r only for an incident that was never diagnosed, else it is off. It returns ErrNotFound unless the incident is
// resolved and ErrExists when another incident of the same source and key is not resolved.
func (s *Store) ReopenIncident(ctx context.Context, id int64, r Reopen, act NewActivity) error {
	if len(r.Details) == 0 {
		r.Details = json.RawMessage(`{}`)
	}
	if !json.Valid(r.Details) || r.Details[0] != '{' {
		return fmt.Errorf("%w: details must be a JSON object", ErrInvalidIncident)
	}
	if r.Title == "" || !slices.Contains(severities, r.Severity) {
		return fmt.Errorf("%w: a reopen needs a title and a known severity", ErrInvalidIncident)
	}
	return s.inTx(ctx, func(tx *txn) error {
		if err := lockIncidentForState(ctx, tx, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE incidents SET
				state = CASE
					WHEN resolved_while_ignored = 1 THEN 'ignored'
					WHEN EXISTS (SELECT 1 FROM runs r WHERE r.incident_id = incidents.id AND r.role = 'responder'
						AND r.status IN ('queued', 'running')) THEN 'diagnosing'
					WHEN diagnosis IS NOT NULL THEN 'diagnosed'
					ELSE 'open' END,
				occurrences = occurrences + 1, last_seen = ?, resolved_at = NULL, resolved_reason = '', resolved_while_ignored = 0,
				title = ?, severity = ?, details = ?, host_id = ?,
				auto_diagnose = CASE WHEN diagnosed_at IS NULL THEN ? ELSE 0 END
			WHERE id = ? AND state = 'resolved'`,
			formatTS(time.Now()), r.Title, r.Severity, string(r.Details), nullInt(r.HostID), flagInt(r.AutoDiagnose), id)
		if err != nil {
			if uniqueConstraint(err) == "incidents_active_key" {
				return ErrExists
			}
			return err
		}
		if err := oneRow(res, nil); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// lockIncidentForState takes the incident's row lock as a statement of its own, before an UPDATE that derives the state
// from the incident's runs. At READ COMMITTED an UPDATE that has to wait for the row is re-checked on the new row, but its
// subqueries keep the snapshot of when the statement started: a run that ended while it waited (and a CompleteDiagnosis
// that held the row) would still look queued or running, and the incident would stay diagnosing for good. After this
// statement the row is held, so the UPDATE that follows starts with a fresh snapshot and nothing can change the row under
// it. A missing incident locks nothing; the UPDATE's guard then reports ErrNotFound.
func lockIncidentForState(ctx context.Context, tx *txn, id int64) error {
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM incidents WHERE id = ? FOR NO KEY UPDATE`, id)
	return err
}

// IgnoreIncident moves an open, diagnosing or diagnosed incident to ignored.
func (s *Store) IgnoreIncident(ctx context.Context, id int64, act NewActivity) error {
	return s.inTx(ctx, func(tx *txn) error {
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = 'ignored'
			WHERE id = ? AND state IN ('open', 'diagnosing', 'diagnosed')`, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

// UnignoreIncident moves an ignored incident back to where it would be. The state before the ignore is not stored, so it is
// derived: diagnosing while a responder run of the incident is queued or running, else diagnosed if a diagnosis is stored,
// else open. It returns ErrNotFound unless the incident is ignored.
func (s *Store) UnignoreIncident(ctx context.Context, id int64, act NewActivity) error {
	return s.inTx(ctx, func(tx *txn) error {
		if err := lockIncidentForState(ctx, tx, id); err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `
			UPDATE incidents SET state = CASE
				WHEN EXISTS (SELECT 1 FROM runs r WHERE r.incident_id = incidents.id AND r.role = 'responder'
					AND r.status IN ('queued', 'running')) THEN 'diagnosing'
				WHEN diagnosis IS NOT NULL THEN 'diagnosed'
				ELSE 'open' END
			WHERE id = ? AND state = 'ignored'`, id)); err != nil {
			return err
		}
		act.IncidentID = id
		return insertActivity(ctx, tx, act)
	})
}

func (s *Store) GetIncident(ctx context.Context, id int64) (Incident, error) {
	q, args := incidentSelect(` WHERE i.id = ?`, id)
	in, err := scanIncident(s.db.QueryRowContext(ctx, q, args...))
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
	q, args := incidentSelect(` WHERE i.source = ? AND i.key = ? AND i.state <> 'resolved'`, source, key)
	in, err := scanIncident(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return in, err
}

// ListActiveIncidents returns the incidents of a repo that are not resolved.
func (s *Store) ListActiveIncidents(ctx context.Context, repoID int64) ([]Incident, error) {
	q, args := incidentSelect(` WHERE i.repo_id = ? AND i.state <> 'resolved' ORDER BY i.id`, repoID)
	rows, err := s.db.QueryContext(ctx, q, args...)
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
	rest := ``
	if len(conds) > 0 {
		rest += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	rest += ` ORDER BY i.last_seen DESC, i.id DESC LIMIT ?`
	args = append(args, limit)

	query, all := incidentSelect(rest, args...)
	rows, err := s.db.QueryContext(ctx, query, all...)
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
// with a smaller id, 0 means no cursor. Kinds, when set, keeps only entries of those kinds. Ascending returns the oldest entries
// first instead of the newest. Limit defaults to 100.
type ActivityQuery struct {
	IncidentID int64
	Before     int64
	Kinds      []string
	Ascending  bool
	Limit      int
}

// insertActivity logs a in the transaction tx. The entry is checked now and queued; runTx writes the queued entries of the
// transaction, in the order they were queued, right before the commit (flushActivity). The ctx parameter is kept so that every
// call site reads as the statement it stands for.
func insertActivity(_ context.Context, tx *txn, a NewActivity) error {
	if len(a.Data) == 0 {
		a.Data = json.RawMessage(`{}`)
	}
	if !json.Valid(a.Data) {
		return errors.New("activity data is not valid JSON")
	}
	tx.acts = append(tx.acts, a)
	return nil
}

// flushActivity writes the queued activity entries of tx. It is the last thing a transaction does before its commit:
//
//  1. it takes FOR KEY SHARE on the repositories, incidents and runs the entries point at, which is the lock each insert's
//     foreign key check needs; a delete of one of those rows (DeleteRepo, DeleteConnection and their cascades) is waited for here;
//  2. it takes activityLockKey;
//  3. it inserts the entries, each with the time of its insert (so the times grow with the ids).
//
// The lock is held until after the commit, so the next transaction draws its ids only once these are visible: ids are handed out
// in commit order, and the stream that follows the log by id (ListActivitySince) never passes over an entry that commits later.
// After step 2 nothing can make the transaction wait (its foreign key locks are held, no statement follows), so the activity lock
// is never part of a deadlock.
func (t *txn) flushActivity(ctx context.Context) error {
	if len(t.acts) == 0 {
		return nil
	}
	var repos, incidents []int64
	var runs []string
	for _, a := range t.acts {
		if a.RepoID != 0 && !slices.Contains(repos, a.RepoID) {
			repos = append(repos, a.RepoID)
		}
		if a.IncidentID != 0 && !slices.Contains(incidents, a.IncidentID) {
			incidents = append(incidents, a.IncidentID)
		}
		if a.RunID != "" && !slices.Contains(runs, a.RunID) {
			runs = append(runs, a.RunID)
		}
	}
	if len(repos) > 0 {
		if _, err := t.ExecContext(ctx, `SELECT 1 FROM repos WHERE id = ANY(?) ORDER BY id FOR KEY SHARE`, repos); err != nil {
			return err
		}
	}
	if len(incidents) > 0 {
		if _, err := t.ExecContext(ctx, `SELECT 1 FROM incidents WHERE id = ANY(?) ORDER BY id FOR KEY SHARE`, incidents); err != nil {
			return err
		}
	}
	if len(runs) > 0 {
		if _, err := t.ExecContext(ctx, `SELECT 1 FROM runs WHERE id = ANY(?) ORDER BY id FOR KEY SHARE`, runs); err != nil {
			return err
		}
	}
	if err := xactLock(ctx, t, activityLockKey); err != nil {
		return err
	}
	for _, a := range t.acts {
		if _, err := t.ExecContext(ctx, `
			INSERT INTO activity (at, kind, repo_id, incident_id, run_id, summary, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			formatTS(time.Now()), a.Kind, nullInt(a.RepoID), nullInt(a.IncidentID), nullString(a.RunID), a.Summary, string(a.Data)); err != nil {
			return err
		}
	}
	t.acts = nil
	return nil
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

// AddActivity appends an entry to the activity log. It runs in a transaction of its own, so that its id is drawn in commit order
// like every other entry's.
func (s *Store) AddActivity(ctx context.Context, a NewActivity) error {
	return s.inTx(ctx, func(tx *txn) error { return insertActivity(ctx, tx, a) })
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
	if len(q.Kinds) > 0 {
		where = append(where, `a.kind IN (`+strings.TrimSuffix(strings.Repeat(`?,`, len(q.Kinds)), `,`)+`)`)
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	query := activitySelect
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	if q.Ascending {
		query += ` ORDER BY a.id ASC LIMIT ?`
	} else {
		query += ` ORDER BY a.id DESC LIMIT ?`
	}
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
