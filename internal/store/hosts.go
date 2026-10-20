package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type HostStatus string

const (
	HostNew           HostStatus = "new"           // never tested
	HostUntrusted     HostStatus = "untrusted"     // the host key is not pinned
	HostOK            HostStatus = "ok"            // the last test worked
	HostError         HostStatus = "error"         // the last test failed
	HostKeyChanged    HostStatus = "key_changed"   // the host shows another key than the pinned one
	HostUndecryptable HostStatus = "undecryptable" // the master key does not open the stored login
)

// HostFields is what the maintainer edits.
type HostFields struct {
	Name          string
	Address       string
	Port          int
	Account       string
	Notes         string
	DockerPattern string
	SystemdUnits  []string
	Enabled       bool
	// Matchers are the rules that map an alert to this host: Alertmanager matcher syntax, one rule per line, canonical.
	// Empty means no alert names the host.
	Matchers string
}

type Host struct {
	ID int64
	HostFields
	PinnedKey    string // the host key as an authorized_keys-style line; empty until pinned
	PinnedAt     *time.Time
	Status       HostStatus
	StatusDetail string
	Report       string // the last test as JSON; empty before the first one
	CheckedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// HostCredential is the stored login of a host. The ciphertext is sealed; see HostCredentialAAD.
type HostCredential struct {
	HostID     int64
	Kind       string // "key" or "password"
	Ciphertext []byte
	Hint       string // a key's fingerprint, or "set" for a password
	PublicKey  string // for a key: the authorized_keys line
	UpdatedAt  time.Time
}

// HostEntry is a host with its login, if it has one.
type HostEntry struct {
	Host       Host
	Credential *HostCredential
}

const hostSelect = `
	SELECT h.id, h.name, h.address, h.port, h.account, h.notes, h.docker_pattern, h.systemd_units, h.enabled, h.matchers,
	       h.pinned_key, h.pinned_at, h.status, h.status_detail, h.report, h.checked_at, h.created_at, h.updated_at,
	       c.host_id, c.kind, c.ciphertext, c.hint, c.public_key, c.updated_at
	FROM hosts h LEFT JOIN host_credentials c ON c.host_id = h.id`

func nullTS(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTS(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanHostEntry(sc scanner) (HostEntry, error) {
	var (
		h                               Host
		units, status, created, updated string
		enabled                         int
		pinnedAt, checked, credUpdated  sql.NullString
		credHost                        sql.NullInt64
		kind, hint, pub                 sql.NullString
		ciphertext                      []byte
	)
	if err := sc.Scan(&h.ID, &h.Name, &h.Address, &h.Port, &h.Account, &h.Notes, &h.DockerPattern, &units, &enabled, &h.Matchers,
		&h.PinnedKey, &pinnedAt, &status, &h.StatusDetail, &h.Report, &checked, &created, &updated,
		&credHost, &kind, &ciphertext, &hint, &pub, &credUpdated); err != nil {
		return HostEntry{}, err
	}
	h.Enabled = enabled != 0
	h.Status = HostStatus(status)
	if err := json.Unmarshal([]byte(units), &h.SystemdUnits); err != nil {
		return HostEntry{}, err
	}
	var err error
	if h.PinnedAt, err = nullTS(pinnedAt); err != nil {
		return HostEntry{}, err
	}
	if h.CheckedAt, err = nullTS(checked); err != nil {
		return HostEntry{}, err
	}
	if h.CreatedAt, err = parseTS(created); err != nil {
		return HostEntry{}, err
	}
	if h.UpdatedAt, err = parseTS(updated); err != nil {
		return HostEntry{}, err
	}
	e := HostEntry{Host: h}
	if credHost.Valid {
		at, err := parseTS(credUpdated.String)
		if err != nil {
			return HostEntry{}, err
		}
		e.Credential = &HostCredential{HostID: credHost.Int64, Kind: kind.String, Ciphertext: ciphertext, Hint: hint.String, PublicKey: pub.String, UpdatedAt: at}
	}
	return e, nil
}

func unitsJSON(units []string) (string, error) {
	if units == nil {
		units = []string{}
	}
	b, err := json.Marshal(units)
	return string(b), err
}

func hostEnabledInt(f HostFields) int {
	if f.Enabled {
		return 1
	}
	return 0
}

func isUnique(err error) bool { return strings.Contains(err.Error(), "UNIQUE constraint failed") }

func (s *Store) GetHost(ctx context.Context, id int64) (HostEntry, error) {
	e, err := scanHostEntry(s.db.QueryRowContext(ctx, hostSelect+` WHERE h.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return HostEntry{}, ErrNotFound
	}
	return e, err
}

// ListHosts returns every host with its login, ordered by name.
func (s *Store) ListHosts(ctx context.Context) ([]HostEntry, error) {
	rows, err := s.db.QueryContext(ctx, hostSelect+` ORDER BY h.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostEntry{}
	for rows.Next() {
		e, err := scanHostEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateHost adds a host. It returns ErrExists if the name is taken, ignoring case.
func (s *Store) CreateHost(ctx context.Context, f HostFields) (Host, error) {
	units, err := unitsJSON(f.SystemdUnits)
	if err != nil {
		return Host{}, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		now := formatTS(time.Now())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO hosts (name, address, port, account, notes, docker_pattern, systemd_units, enabled, matchers, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.Name, f.Address, f.Port, f.Account, f.Notes, f.DockerPattern, units, hostEnabledInt(f), f.Matchers, now, now)
		if err != nil {
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host " + f.Name + " created"})
	})
	if err != nil {
		return Host{}, err
	}
	e, err := s.GetHost(ctx, id)
	return e.Host, err
}

// UpdateHost replaces the editable fields. A new address or port clears the pinned key, because a pin belongs to an endpoint;
// the bool says whether it did.
func (s *Store) UpdateHost(ctx context.Context, id int64, f HostFields) (Host, bool, error) {
	units, err := unitsJSON(f.SystemdUnits)
	if err != nil {
		return Host{}, false, err
	}
	cleared := false
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var oldAddress, pinned string
		var oldPort int
		err := tx.QueryRowContext(ctx, `SELECT address, port, pinned_key FROM hosts WHERE id = ?`, id).Scan(&oldAddress, &oldPort, &pinned)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cleared = pinned != "" && (oldAddress != f.Address || oldPort != f.Port)
		q := `UPDATE hosts SET name = ?, address = ?, port = ?, account = ?, notes = ?, docker_pattern = ?, systemd_units = ?, enabled = ?, matchers = ?, updated_at = ?`
		if cleared {
			q += `, pinned_key = '', pinned_at = NULL, status = 'untrusted', status_detail = '', report = ''`
		}
		q += ` WHERE id = ?`
		if _, err := tx.ExecContext(ctx, q, f.Name, f.Address, f.Port, f.Account, f.Notes, f.DockerPattern, units, hostEnabledInt(f), f.Matchers, formatTS(time.Now()), id); err != nil {
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		summary := "Host " + f.Name + " changed"
		if cleared {
			summary += "; its pinned key was cleared"
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: summary})
	})
	if err != nil {
		return Host{}, false, err
	}
	e, err := s.GetHost(ctx, id)
	return e.Host, cleared, err
}

// hostName reads a host's name inside a transaction; ErrNotFound if it does not exist.
func hostName(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM hosts WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// DeleteHost removes a host and, through the foreign keys, its login and its skills (proposals included). The activity entry says how
// many skills went with it.
func (s *Store) DeleteHost(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, id)
		if err != nil {
			return err
		}
		var skills int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE host_id = ?`, id).Scan(&skills); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id); err != nil {
			return err
		}
		summary := "Host " + name + " removed"
		if skills > 0 {
			summary += fmt.Sprintf("; %s removed with it", plural(skills, "skill"))
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: summary})
	})
}

// touchHost moves updated_at of a host. An approval that was asked for before is stale after it.
func touchHost(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE hosts SET updated_at = ? WHERE id = ?`, formatTS(time.Now()), id)
	return err
}

// SaveHostCredential sets or replaces a host's login. A host whose last test was ok, failed or could not decrypt the old login
// is untested again: that result does not describe the new login.
func (s *Store) SaveHostCredential(ctx context.Context, c HostCredential) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, c.HostID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO host_credentials (host_id, kind, ciphertext, hint, public_key, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (host_id) DO UPDATE SET kind = excluded.kind, ciphertext = excluded.ciphertext, hint = excluded.hint,
				public_key = excluded.public_key, updated_at = excluded.updated_at`,
			c.HostID, c.Kind, c.Ciphertext, c.Hint, c.PublicKey, formatTS(time.Now())); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, resetStaleStatus, c.HostID); err != nil {
			return err
		}
		if err := touchHost(ctx, tx, c.HostID); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Credential of " + name + " replaced"})
	})
}

// DeleteHostCredential removes a host's login. The last test no longer describes the host, as with a replaced login.
func (s *Store) DeleteHostCredential(ctx context.Context, hostID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, hostID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM host_credentials WHERE host_id = ?`, hostID)
		if err := oneRow(res, err); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, resetStaleStatus, hostID); err != nil {
			return err
		}
		if err := touchHost(ctx, tx, hostID); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Credential of " + name + " removed"})
	})
}

// PinHostKey stores the host key (an authorized_keys-style line). The host is untested again.
func (s *Store) PinHostKey(ctx context.Context, id int64, line string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE hosts SET pinned_key = ?, pinned_at = ?, status = 'new', status_detail = '', report = '' WHERE id = ?`,
			line, formatTS(at), id); err != nil {
			return err
		}
		if err := touchHost(ctx, tx, id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host key of " + name + " pinned"})
	})
}

func (s *Store) UnpinHostKey(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		name, err := hostName(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE hosts SET pinned_key = '', pinned_at = NULL, status = 'untrusted', status_detail = '', report = '' WHERE id = ?`, id); err != nil {
			return err
		}
		if err := touchHost(ctx, tx, id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindHostChanged, Summary: "Host key of " + name + " unpinned"})
	})
}

// ErrStale says a connection test finished after the pin it ran against was changed or removed (the address or port was
// edited, or the key was pinned or unpinned meanwhile). Its result belongs to a situation that no longer exists and is dropped.
var ErrStale = errors.New("the result belongs to an earlier state of the host")

// resetStaleStatus makes a host whose last test no longer describes it untested: a changed or removed login invalidates an
// ok, error or undecryptable result and the report. untrusted and key_changed are about the host key, not the login.
const resetStaleStatus = `UPDATE hosts SET status = 'new', status_detail = '', report = ''
	WHERE id = ? AND status IN ('ok', 'error', 'undecryptable')`

// SaveHostTest records the result of a connection test. It writes no activity: tests are frequent. pinnedKey is the pin the test
// ran against (empty for the probe of an unpinned host): if the host's pin is another one now, nothing is written and ErrStale
// is returned; ErrNotFound if the host is gone.
func (s *Store) SaveHostTest(ctx context.Context, id int64, pinnedKey string, status HostStatus, detail, report string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE hosts SET status = ?, status_detail = ?, report = ?, checked_at = ? WHERE id = ? AND pinned_key = ?`,
			string(status), detail, report, formatTS(at), id, pinnedKey)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return nil
		}
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM hosts WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return ErrStale
	})
}
