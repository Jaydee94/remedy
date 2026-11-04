package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
)

// ErrBadWebhook is returned for a name or a value of a webhook endpoint outside its bounds.
var ErrBadWebhook = errors.New("invalid webhook endpoint")

// RotationOverlap is how long the previous token of an endpoint stays valid after a rotation, so that a sender can be changed
// without a gap.
const RotationOverlap = 24 * time.Hour

// MinResolveAfter is the shortest silence after which a webhook incident is resolved (0 turns the resolution by silence off).
const MinResolveAfter = 5 * time.Minute

var webhookNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// WebhookFields are the editable fields of an endpoint.
type WebhookFields struct {
	Enabled       bool
	AutoDiagnose  bool
	ResolveAfter  time.Duration // 0: only an explicit "resolved" resolves
	RatePerMinute int           // requests per minute; the burst is twice this
	NewPerHour    int           // new incidents per hour
}

func (f WebhookFields) check() error {
	switch {
	case f.ResolveAfter < 0, f.ResolveAfter%time.Second != 0, f.ResolveAfter != 0 && f.ResolveAfter < MinResolveAfter:
		return ErrBadWebhook
	case f.RatePerMinute < 1 || f.RatePerMinute > 6000:
		return ErrBadWebhook
	case f.NewPerHour < 1 || f.NewPerHour > 1000:
		return ErrBadWebhook
	}
	return nil
}

// WebhookEndpoint is an endpoint as the admin API shows it. It holds no token and no hash.
type WebhookEndpoint struct {
	ID                 int64
	Name               string
	Enabled            bool
	AutoDiagnose       bool
	ResolveAfter       time.Duration
	RatePerMinute      int
	NewPerHour         int
	CreatedAt          time.Time
	LastUsedAt         *time.Time
	PreviousValidUntil *time.Time
}

// WebhookAuth is what the handler needs to check a request: the endpoint and both hashes.
type WebhookAuth struct {
	WebhookEndpoint
	TokenHash         []byte
	PreviousTokenHash []byte
}

// WebhookKeyParts splits the key of a webhook incident, "<endpoint>/<sender's key>", at its first slash. A key without a slash has
// no endpoint.
func WebhookKeyParts(key string) (endpoint, rest string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

// IsOutageSource says whether the incidents of a source are alerts about a running system, diagnosed by the outage responder.
func IsOutageSource(source string) bool {
	return source == SourceAlertmanager || source == SourceWebhook
}

// TokenMatches says whether token hashes to hash, in constant time.
func TokenMatches(hash []byte, token string) bool {
	return subtle.ConstantTimeCompare(hash, hashToken(token)) == 1
}

func newWebhookToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err // crypto/rand failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

const webhookCols = `id, name, enabled, auto_diagnose, resolve_after_seconds, rate_per_minute, new_per_hour, created_at, last_used_at, previous_valid_until`

type rowScanner interface{ Scan(dest ...any) error }

func scanWebhook(r rowScanner) (WebhookEndpoint, error) {
	var (
		e                  WebhookEndpoint
		enabled, auto, sec int
		created            string
		used, prev         sql.NullString
	)
	if err := r.Scan(&e.ID, &e.Name, &enabled, &auto, &sec, &e.RatePerMinute, &e.NewPerHour, &created, &used, &prev); err != nil {
		return WebhookEndpoint{}, err
	}
	e.Enabled, e.AutoDiagnose, e.ResolveAfter = enabled != 0, auto != 0, time.Duration(sec)*time.Second
	var err error
	if e.CreatedAt, err = parseTS(created); err != nil {
		return WebhookEndpoint{}, err
	}
	if used.Valid {
		t, err := parseTS(used.String)
		if err != nil {
			return WebhookEndpoint{}, err
		}
		e.LastUsedAt = &t
	}
	if prev.Valid {
		t, err := parseTS(prev.String)
		if err != nil {
			return WebhookEndpoint{}, err
		}
		e.PreviousValidUntil = &t
	}
	return e, nil
}

// CreateWebhook adds an endpoint and returns its token, which exists in the clear only in this answer. It returns ErrBadWebhook for
// a name or a value out of bounds and ErrExists when the name is taken.
func (s *Store) CreateWebhook(ctx context.Context, name string, f WebhookFields) (WebhookEndpoint, string, error) {
	if !webhookNameRe.MatchString(name) || strings.ToLower(name) != name {
		return WebhookEndpoint{}, "", ErrBadWebhook
	}
	if err := f.check(); err != nil {
		return WebhookEndpoint{}, "", err
	}
	token, err := newWebhookToken()
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	var e WebhookEndpoint
	err = s.inTx(ctx, func(tx *txn) error {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO webhook_endpoints (name, token_hash, enabled, auto_diagnose, resolve_after_seconds, rate_per_minute, new_per_hour, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+webhookCols,
			name, hashToken(token), flagInt(f.Enabled), flagInt(f.AutoDiagnose), int(f.ResolveAfter/time.Second), f.RatePerMinute, f.NewPerHour, formatTS(time.Now()))
		var err error
		e, err = scanWebhook(row)
		if err != nil && isUnique(err) {
			return ErrExists
		}
		return err
	})
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	return e, token, nil
}

// RotateWebhookToken replaces the token. The previous one stays valid for RotationOverlap after now.
func (s *Store) RotateWebhookToken(ctx context.Context, id int64, now time.Time) (WebhookEndpoint, string, error) {
	token, err := newWebhookToken()
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	var e WebhookEndpoint
	err = s.inTx(ctx, func(tx *txn) error {
		row := tx.QueryRowContext(ctx, `
			UPDATE webhook_endpoints SET previous_token_hash = token_hash, previous_valid_until = ?, token_hash = ?
			WHERE id = ? RETURNING `+webhookCols, formatTS(now.Add(RotationOverlap)), hashToken(token), id)
		var err error
		e, err = scanWebhook(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	return e, token, nil
}

// UpdateWebhook replaces the editable fields. The token and the name stay.
func (s *Store) UpdateWebhook(ctx context.Context, id int64, f WebhookFields) (WebhookEndpoint, error) {
	if err := f.check(); err != nil {
		return WebhookEndpoint{}, err
	}
	var e WebhookEndpoint
	err := s.inTx(ctx, func(tx *txn) error {
		// Switching an endpoint off ends the rotation overlap: a token that leaked and was rotated away must not come back to life
		// when the endpoint is switched on again within the day.
		row := tx.QueryRowContext(ctx, `
			UPDATE webhook_endpoints SET enabled = ?, auto_diagnose = ?, resolve_after_seconds = ?, rate_per_minute = ?, new_per_hour = ?,
				previous_token_hash = CASE WHEN ? = 0 THEN NULL ELSE previous_token_hash END,
				previous_valid_until = CASE WHEN ? = 0 THEN NULL ELSE previous_valid_until END
			WHERE id = ? RETURNING `+webhookCols,
			flagInt(f.Enabled), flagInt(f.AutoDiagnose), int(f.ResolveAfter/time.Second), f.RatePerMinute, f.NewPerHour,
			flagInt(f.Enabled), flagInt(f.Enabled), id)
		var err error
		e, err = scanWebhook(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return e, err
}

// DeleteWebhook removes an endpoint. Its incidents stay.
func (s *Store) DeleteWebhook(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_endpoints WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListWebhooks returns every endpoint, oldest first.
func (s *Store) ListWebhooks(ctx context.Context) ([]WebhookEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+webhookCols+` FROM webhook_endpoints ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		e, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// WebhookByName finds an endpoint by name, ignoring case, with its hashes. It returns ErrNotFound for an unknown name.
func (s *Store) WebhookByName(ctx context.Context, name string) (WebhookAuth, error) {
	var (
		a    WebhookAuth
		prev []byte
	)
	row := s.db.QueryRowContext(ctx, `SELECT `+webhookCols+`, token_hash, previous_token_hash FROM webhook_endpoints WHERE lower(name) = lower(?)`, name)
	var (
		enabled, auto, sec int
		created            string
		used, until        sql.NullString
	)
	err := row.Scan(&a.ID, &a.Name, &enabled, &auto, &sec, &a.RatePerMinute, &a.NewPerHour, &created, &used, &until, &a.TokenHash, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookAuth{}, ErrNotFound
	}
	if err != nil {
		return WebhookAuth{}, err
	}
	a.Enabled, a.AutoDiagnose, a.ResolveAfter, a.PreviousTokenHash = enabled != 0, auto != 0, time.Duration(sec)*time.Second, prev
	if until.Valid {
		t, err := parseTS(until.String)
		if err != nil {
			return WebhookAuth{}, err
		}
		a.PreviousValidUntil = &t
	}
	return a, nil
}

// TouchWebhook records that the endpoint was used. It writes at most once a minute per endpoint.
func (s *Store) TouchWebhook(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE webhook_endpoints SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)`,
		formatTS(at), id, formatTS(at.Add(-time.Minute)))
	return err
}
