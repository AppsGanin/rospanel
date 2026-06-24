package store

import (
	"database/sql"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

const userCols = `id, name, uuid, password, sub_token, enabled,
	data_limit, expire_at, used_up, used_down, last_up, last_down, created_at,
	reset_period, last_reset_at, last_seen, device_limit, tg_chat_id,
	plan_id, trial_used, tg_link_code, tg_link_code_at`

// CreateUser inserts a user with one credential set (UUID for VLESS, password
// for Trojan + Hysteria2), a subscription token, and optional quota/expiry.
func (s *Store) CreateUser(name, uuid, password, subToken string, dataLimit, expireAt int64, deviceLimit int) (*model.User, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO users (name, uuid, password, sub_token, data_limit, expire_at, device_limit)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		name, uuid, encField(password), subToken, dataLimit, expireAt, deviceLimit,
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
// manually enabled AND not expired AND within their data limit AND within their
// device limit. enabled is an independent manual flag — expiry/quota/devices
// never change it, they just exclude the user from the config here.
func (s *Store) WorkingUsers(now int64) ([]model.User, error) {
	since := now - model.DeviceOnlineWindow
	return s.queryUsers(`SELECT `+userCols+` FROM users
		WHERE enabled = 1
		  AND (expire_at = 0 OR expire_at > ?)
		  AND (data_limit = 0 OR used_up + used_down < data_limit)
		  AND (device_limit = 0 OR (
		    SELECT COUNT(DISTINCT c.ip) FROM connections c
		    WHERE c.user_id = users.id AND c.last_seen > ?
		  ) <= device_limit)
		ORDER BY id ASC`, now, since)
}

// GetUser returns one user by id.
func (s *Store) GetUser(id int64) (*model.User, error) {
	users, err := s.queryUsers(`SELECT `+userCols+` FROM users WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, sql.ErrNoRows
	}
	return &users[0], nil
}

// GetUserByTgLinkCode resolves a pending one-time Telegram bind code to its user,
// rejecting codes that are blank, unknown, or expired.
func (s *Store) GetUserByTgLinkCode(code string) (*model.User, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, sql.ErrNoRows
	}
	users, err := s.queryUsers(`SELECT `+userCols+` FROM users WHERE tg_link_code = ? LIMIT 1`, code)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, sql.ErrNoRows
	}
	u := &users[0]
	if !u.UserTgLinkCodeValid() {
		return nil, sql.ErrNoRows
	}
	return u, nil
}

// SetUserTgLinkCode stores (or clears, with "") a user's pending Telegram bind
// code, stamping the issue time so it can expire.
func (s *Store) SetUserTgLinkCode(userID int64, code string) error {
	at := int64(0)
	if strings.TrimSpace(code) != "" {
		at = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`UPDATE users SET tg_link_code = ?, tg_link_code_at = ? WHERE id = ?`,
		code, at, userID,
	)
	return err
}

// ClearUserTgLinkCode burns a user's pending Telegram bind code (after a
// successful link or on demand).
func (s *Store) ClearUserTgLinkCode(userID int64) error {
	return s.SetUserTgLinkCode(userID, "")
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

// SetUserLimits sets the data limit (bytes), expiry (unix, 0 = none), and the
// simultaneous device cap (0 = unlimited). Does not touch the manual enabled
// flag; status is derived on read.
func (s *Store) SetUserLimits(id, dataLimit, expireAt int64, deviceLimit int) error {
	_, err := s.db.Exec(
		`UPDATE users SET data_limit = ?, expire_at = ?, device_limit = ? WHERE id = ?`,
		dataLimit, expireAt, deviceLimit, id,
	)
	return err
}

// SetUserName updates a user's display name.
func (s *Store) SetUserName(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE users SET name = ? WHERE id = ?`, name, id)
	return err
}

// SetSubToken replaces a user's subscription capability token. The old URL stops
// working immediately; protocol credentials (UUID/password) are unchanged.
func (s *Store) SetSubToken(id int64, token string) error {
	_, err := s.db.Exec(`UPDATE users SET sub_token = ? WHERE id = ?`, token, id)
	return err
}

// GetUserByTelegramChatID resolves a linked Telegram chat to its VPN user.
func (s *Store) GetUserByTelegramChatID(chatID int64) (*model.User, error) {
	if chatID == 0 {
		return nil, sql.ErrNoRows
	}
	users, err := s.queryUsers(`SELECT `+userCols+` FROM users WHERE tg_chat_id = ? LIMIT 1`, chatID)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, sql.ErrNoRows
	}
	return &users[0], nil
}

// SetUserTelegramChat links a Telegram chat to a VPN user, first detaching the
// chat from any other user (one chat ⇒ at most one account).
func (s *Store) SetUserTelegramChat(userID, chatID int64) error {
	if _, err := s.db.Exec(`UPDATE users SET tg_chat_id = 0 WHERE tg_chat_id = ?`, chatID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE users SET tg_chat_id = ? WHERE id = ?`, chatID, userID)
	return err
}

// ClearUserTelegramChat unlinks a VPN user's Telegram chat.
func (s *Store) ClearUserTelegramChat(userID int64) error {
	_, err := s.db.Exec(`UPDATE users SET tg_chat_id = 0 WHERE id = ?`, userID)
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
// the expiry/quota/device conditions. Order: disabled (manual) > expired >
// limited (traffic) > device_limited > active.
func deriveStatus(enabled bool, expireAt, used, limit, now int64, activeDevices, deviceLimit int) string {
	switch {
	case !enabled:
		return model.StatusDisabled
	case expireAt > 0 && expireAt <= now:
		return model.StatusExpired
	case limit > 0 && used >= limit:
		return model.StatusLimited
	case deviceLimit > 0 && activeDevices > deviceLimit:
		return model.StatusDeviceLimited
	default:
		return model.StatusActive
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
		var enabled, trialUsed int
		if err := rows.Scan(
			&u.ID, &u.Name, &u.UUID, &u.Password, &u.SubToken, &enabled,
			&u.DataLimit, &u.ExpireAt, &u.UsedUp, &u.UsedDown, &u.LastUp, &u.LastDown, &created,
			&u.ResetPeriod, &u.LastResetAt, &u.LastSeen, &u.DeviceLimit, &u.TgChatID,
			&u.PlanID, &trialUsed, &u.TgLinkCode, &u.TgLinkCodeAt,
		); err != nil {
			return nil, err
		}
		u.Enabled = enabled != 0
		u.TrialUsed = trialUsed != 0
		u.Password = decField(u.Password)
		u.CreatedAt = time.Unix(created, 0)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.applyUserStatus(out, now)
	return out, nil
}

// applyUserStatus fills each user's ActiveDevices (distinct source IPs seen
// within DeviceOnlineWindow) and derives their display status.
func (s *Store) applyUserStatus(users []model.User, now int64) {
	if len(users) == 0 {
		return
	}
	counts, _ := s.ActiveDeviceCounts(now - model.DeviceOnlineWindow)
	for i := range users {
		u := &users[i]
		active := counts[u.ID]
		u.ActiveDevices = active
		u.Status = deriveStatus(
			u.Enabled, u.ExpireAt, u.UsedUp+u.UsedDown, u.DataLimit, now,
			active, u.DeviceLimit,
		)
	}
}
