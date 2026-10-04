package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// CreateToolRun creates a queued ad-hoc run that has access to the gatekeeper.
func (s *Store) CreateToolRun(ctx context.Context, provider, prompt string) (run.Run, error) {
	id := run.NewID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, provider, prompt, status, mcp, created_at) VALUES (?, ?, ?, 'queued', 1, ?)`,
		id, provider, prompt, formatTS(time.Now()))
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

// hashToken is what is stored of a run token. The token is 256 random bits, so a plain SHA-256 is enough.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// MintRunToken makes the token of a running run that has gatekeeper access and returns it. It is the only
// time the token exists in the clear: the claim hands it to the runner. It returns ErrNotFound for a run that
// is not running or has no gatekeeper access, and ErrExists when the run has a token already.
func (s *Store) MintRunToken(ctx context.Context, runID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err // crypto/rand failing is unrecoverable
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM runs WHERE id = ? AND status = 'running' AND mcp = 1`, runID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO run_tokens (run_id, token_hash, created_at) VALUES (?, ?, ?)`,
			runID, hashToken(token), formatTS(time.Now()))
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrExists
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// RunForToken returns the run a token belongs to. The token must not be revoked and the run must be running.
func (s *Store) RunForToken(ctx context.Context, token string) (run.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `
		SELECT `+runCols+` FROM runs
		WHERE status = 'running' AND cancel_requested = 0
		  AND id = (SELECT run_id FROM run_tokens WHERE token_hash = ? AND revoked_at IS NULL)`, hashToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return run.Run{}, ErrNotFound
	}
	return r, err
}

// closeRunTx is what ending a run does besides setting its status, in the transaction that ends it: its token
// stops working and its waiting calls are abandoned.
func closeRunTx(ctx context.Context, tx *sql.Tx, runID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE run_tokens SET revoked_at = ? WHERE run_id = ? AND revoked_at IS NULL`, formatTS(now), runID); err != nil {
		return err
	}
	_, err := abandonTx(ctx, tx, now, `run_id = ?`, runID)
	return err
}
