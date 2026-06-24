package store

import (
	"database/sql"
	"time"

	"github.com/msTimofeev/rospanel/internal/model"
)

const userCols = `id, name, uuid, password, sub_token, enabled,
	data_limit, expire_at, used_up, used_down, last_up, last_down, created_at,
	reset_period, last_reset_at, last_seen`

// CreateUser inserts a user with one credential set (UUID for VLESS, password
// for Trojan + Hysteria2), a subscription token, and optional quota/expiry.
func (s *Store) CreateUser(name, uuid, password, subToken string, dataLimit, expireAt int64) (*model.User, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO users (name, uuid, password, sub_token, data_limit, expire_at)
		 VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		name, uuid, password, subToken, dataLimit, expireAt,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	users, err := s.queryUsers(`SELECT `+userCols+` FROM users WHERE id = ?`, id)
	if err != nil || len(users) == 0 {
		return nil, err
	}
	return &users[0], nil
}

// ListUsers returns all users, newest first.
func (s *Store) ListUsers() ([]model.User, error) {
	return s.queryUsers(`SELECT ` + userCols + ` FROM users ORDER BY id DESC`)
}

// WorkingUsers returns users that should be in the proxy config right now:
// manually enabled AND not expired AND within their data limit. enabled is an
// independent manual flag — expiry/quota never change it, they just exclude the
// user from the config here.
func (s *Store) WorkingUsers(now int64) ([]model.User, error) {
	return s.queryUsers(`SELECT `+userCols+` FROM users
		WHERE enabled = 1
		  AND (expire_at = 0 OR expire_at > ?)
		  AND (data_limit = 0 OR used_up + used_down < data_limit)
		ORDER BY id ASC`, now)
}

// GetUserBySubToken resolves a subscription token to its user.
func (s *Store) GetUserBySubToken(token string) (*model.User, error) {
	if token == "" {
		return nil, sql.ErrNoRows
	}
	users, err := s.queryUsers(`SELECT `+userCols+` FROM users WHERE sub_token = ? LIMIT 1`, token)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, sql.ErrNoRows
	}
	return &users[0], nil
}

// UpdateTraffic adds deltas to lifetime totals and records the raw counters.
func (s *Store) UpdateTraffic(id, addUp, addDown, lastUp, lastDown int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET used_up = used_up + ?, used_down = used_down + ?,
		 last_up = ?, last_down = ? WHERE id = ?`,
		addUp, addDown, lastUp, lastDown, id,
	)
	return err
}

// SetUserLimits sets the data limit (bytes) and expiry (unix, 0 = none). Does
// not touch the manual enabled flag; status is derived on read.
func (s *Store) SetUserLimits(id, dataLimit, expireAt int64) error {
	_, err := s.db.Exec(`UPDATE users SET data_limit = ?, expire_at = ? WHERE id = ?`, dataLimit, expireAt, id)
	return err
}

// SetUserName updates a user's display name.
func (s *Store) SetUserName(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE users SET name = ? WHERE id = ?`, name, id)
	return err
}

// ResetTraffic zeroes a user's usage (so a "limited" user works again) and
// re-baselines the raw counters to the supplied live Xray values, so the next
// stats poll measures the delta from now. Passing 0/0 would make the poll re-add
// the user's whole lifetime Xray total straight back onto the freshly-zeroed
// usage. Does not touch enabled or expiry — an expired user stays expired.
func (s *Store) ResetTraffic(id, lastUp, lastDown int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET used_up=0, used_down=0, last_up=?, last_down=? WHERE id = ?`,
		lastUp, lastDown, id,
	)
	return err
}

// SetUserEnabled sets the independent manual on/off flag. Expiry/quota are
// separate and never change this.
func (s *Store) SetUserEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE users SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	return err
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(id int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// deriveStatus computes the display status from the independent enabled flag and
// the expiry/quota conditions. Order: disabled (manual) > expired > limited >
// active.
func deriveStatus(enabled bool, expireAt, used, limit, now int64) string {
	switch {
	case !enabled:
		return "disabled"
	case expireAt > 0 && expireAt <= now:
		return "expired"
	case limit > 0 && used >= limit:
		return "limited"
	default:
		return "active"
	}
}

func (s *Store) queryUsers(query string, args ...any) ([]model.User, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().Unix()
	var out []model.User
	for rows.Next() {
		var u model.User
		var created int64
		var enabled int
		if err := rows.Scan(
			&u.ID, &u.Name, &u.UUID, &u.Password, &u.SubToken, &enabled,
			&u.DataLimit, &u.ExpireAt, &u.UsedUp, &u.UsedDown, &u.LastUp, &u.LastDown, &created,
			&u.ResetPeriod, &u.LastResetAt, &u.LastSeen,
		); err != nil {
			return nil, err
		}
		u.Enabled = enabled != 0
		u.CreatedAt = time.Unix(created, 0)
		u.Status = deriveStatus(u.Enabled, u.ExpireAt, u.UsedUp+u.UsedDown, u.DataLimit, now)
		out = append(out, u)
	}
	return out, rows.Err()
}
