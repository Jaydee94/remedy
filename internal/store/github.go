package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type ConnectionStatus string

const (
	ConnOK            ConnectionStatus = "ok"
	ConnError         ConnectionStatus = "error"
	ConnUndecryptable ConnectionStatus = "undecryptable"
)

// ConnectionID is the only connection for now. The schema allows more.
const ConnectionID int64 = 1

// ErrExists is returned when a unique constraint is violated.
var ErrExists = errors.New("already exists")

// Connection is the stored GitHub connection. The token is only ever stored sealed.
type Connection struct {
	ID              int64
	TokenCiphertext []byte
	TokenHint       string
	Login           string
	Status          ConnectionStatus
	StatusDetail    string
	CheckedAt       time.Time
}

// SaveConnection creates the connection or replaces the existing one.
func (s *Store) SaveConnection(ctx context.Context, c Connection) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			token_ciphertext = excluded.token_ciphertext, token_hint = excluded.token_hint,
			login = excluded.login, status = excluded.status,
			status_detail = excluded.status_detail, checked_at = excluded.checked_at`,
		ConnectionID, c.TokenCiphertext, c.TokenHint, c.Login, string(c.Status), c.StatusDetail, formatTS(c.CheckedAt))
	return err
}

func (s *Store) GetConnection(ctx context.Context) (Connection, error) {
	var (
		c       Connection
		status  string
		checked string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, token_ciphertext, token_hint, login, status, status_detail, checked_at
		FROM github_connections WHERE id = ?`, ConnectionID).
		Scan(&c.ID, &c.TokenCiphertext, &c.TokenHint, &c.Login, &status, &c.StatusDetail, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, err
	}
	c.Status = ConnectionStatus(status)
	if c.CheckedAt, err = parseTS(checked); err != nil {
		return Connection{}, err
	}
	return c, nil
}

func (s *Store) UpdateConnectionStatus(ctx context.Context, status ConnectionStatus, detail string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE github_connections SET status = ?, status_detail = ?, checked_at = ? WHERE id = ?`,
		string(status), detail, formatTS(at), ConnectionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// DeleteConnection removes the connection and, through the foreign key, its repos.
func (s *Store) DeleteConnection(ctx context.Context) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM github_connections WHERE id = ?`, ConnectionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

type Repo struct {
	ID            int64
	ConnectionID  int64
	FullName      string
	DefaultBranch string
	Enabled       bool
	LastPolledAt  *time.Time
	LastError     string
	CreatedAt     time.Time
}

const repoCols = `id, connection_id, full_name, default_branch, enabled, last_polled_at, last_error, created_at`

func scanRepo(sc scanner) (Repo, error) {
	var (
		r       Repo
		enabled int
		polled  sql.NullString
		created string
	)
	if err := sc.Scan(&r.ID, &r.ConnectionID, &r.FullName, &r.DefaultBranch, &enabled, &polled, &r.LastError, &created); err != nil {
		return Repo{}, err
	}
	r.Enabled = enabled != 0
	var err error
	if r.CreatedAt, err = parseTS(created); err != nil {
		return Repo{}, err
	}
	if polled.Valid {
		t, err := parseTS(polled.String)
		if err != nil {
			return Repo{}, err
		}
		r.LastPolledAt = &t
	}
	return r, nil
}

// AddRepo registers a repository. It returns ErrExists if the name is already registered,
// ignoring case.
func (s *Store) AddRepo(ctx context.Context, connectionID int64, fullName, defaultBranch string) (Repo, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO repos (connection_id, full_name, default_branch, enabled, created_at) VALUES (?, ?, ?, 1, ?)`,
		connectionID, fullName, defaultBranch, formatTS(time.Now()))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Repo{}, ErrExists
		}
		return Repo{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Repo{}, err
	}
	return s.GetRepo(ctx, id)
}

func (s *Store) GetRepo(ctx context.Context, id int64) (Repo, error) {
	r, err := scanRepo(s.db.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return r, err
}

func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+repoCols+` FROM repos ORDER BY full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	repos := []Repo{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func (s *Store) SetRepoEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE repos SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRepo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
