package store

import (
	"context"
	"time"
)

// KindSettingChanged is the activity entry of a setting that was set in the Setup page or reset.
const KindSettingChanged = "setting_changed"

// Setting is one override: the key, its value as JSON and when it was written.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// ListSettings returns every override, by key.
func (s *Store) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, updated_at FROM settings ORDER BY key COLLATE "C"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var st Setting
		var at string
		if err := rows.Scan(&st.Key, &st.Value, &at); err != nil {
			return nil, err
		}
		if st.UpdatedAt, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SetSetting writes an override, and the activity entry that says so, in one transaction.
func (s *Store) SetSetting(ctx context.Context, key, value string, act NewActivity) error {
	return s.inTx(ctx, func(tx *txn) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, value, formatTS(time.Now())); err != nil {
			return err
		}
		return insertActivity(ctx, tx, act)
	})
}

// DeleteSetting removes an override. It reports whether there was one; only then is the activity entry written.
func (s *Store) DeleteSetting(ctx context.Context, key string, act NewActivity) (bool, error) {
	removed := false
	err := s.inTx(ctx, func(tx *txn) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		removed = true
		return insertActivity(ctx, tx, act)
	})
	return removed, err
}
