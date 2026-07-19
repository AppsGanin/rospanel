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
	plan_id, trial_used, tg_link_code, tg_link_code_at, notified_status,
	notified_expire_at, notified_quota_at`

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

// ExpiredUsersBefore returns users whose expiry date is older than cutoff (unix
// seconds) — the candidates for the auto-delete sweep.
//
// It keys off expire_at rather than the `status` column on purpose. status is a
// derived value that a reset or a plan change can flip back to active, and a user
// who is active again must never be deleted because they were expired last week.
// expire_at is the fact: a date in the past that nobody extended. Users with no
// expiry (expire_at = 0) are excluded — there is nothing for them to be past.
func (s *Store) ExpiredUsersBefore(cutoff int64) ([]model.User, error) {
	return s.queryUsers(`SELECT `+userCols+` FROM users
		WHERE expire_at > 0 AND expire_at <= ?
		ORDER BY id ASC`, cutoff)
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
	// One transaction so the detach + attach are atomic: without it, a failure (or
	// crash) between the two statements would leave the chat unlinked from its old
	// owner and never attached to the new one.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET tg_chat_id = 0 WHERE tg_chat_id = ?`, chatID); err != nil {
		return err
	}
	// This chat is now actively owned, so the self-reattach slot it may have left on
	// a previously-unlinked account is consumed — drop any stale prev pointers to it
	// (including on this user) so a later unlink resolves to exactly one account.
	if _, err := tx.Exec(`UPDATE users SET tg_prev_chat_id = 0 WHERE tg_prev_chat_id = ?`, chatID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET tg_chat_id = ? WHERE id = ?`, chatID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearUserTelegramChat unlinks a VPN user's Telegram chat, remembering the chat
// in tg_prev_chat_id so the same chat can restore this exact account (keeping its
// plan and consumed trial) by registering again — instead of getting a brand-new
// trial user. Only overwrites tg_prev_chat_id when actually detaching a chat, so a
// redundant clear can't wipe a prior pointer.
func (s *Store) ClearUserTelegramChat(userID int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET tg_prev_chat_id = tg_chat_id, tg_chat_id = 0
		 WHERE id = ? AND tg_chat_id <> 0`,
		userID,
	)
	return err
}

// GetDetachedUserByPrevChat finds an account this chat was previously unlinked
// from and that is still detached (no active chat), so registration can restore
// it instead of creating a new trial user. Returns sql.ErrNoRows when none.
func (s *Store) GetDetachedUserByPrevChat(chatID int64) (*model.User, error) {
	if chatID == 0 {
		return nil, sql.ErrNoRows
	}
	users, err := s.queryUsers(
		`SELECT `+userCols+` FROM users
		 WHERE tg_prev_chat_id = ? AND tg_chat_id = 0
		 ORDER BY id DESC LIMIT 1`, chatID)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, sql.ErrNoRows
	}
	return &users[0], nil
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

// SetNotifiedExpireAt records the expiry a "runs out soon" warning was sent for.
func (s *Store) SetNotifiedExpireAt(id, expireAt int64) error {
	_, err := s.db.Exec(`UPDATE users SET notified_expire_at = ? WHERE id = ?`, expireAt, id)
	return err
}

// SetNotifiedQuotaAt marks (at != 0) or re-arms (0) the traffic warning.
func (s *Store) SetNotifiedQuotaAt(id, at int64) error {
	_, err := s.db.Exec(`UPDATE users SET notified_quota_at = ? WHERE id = ?`, at, id)
	return err
}

// SetNotifiedStatus records the status a user was last alerted about, so the
// transition detector's comparison survives a panel restart (see the 0020 migration).
func (s *Store) SetNotifiedStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE users SET notified_status = ? WHERE id = ?`, status, id)
	return err
}

// SetUserEnabled sets the independent manual on/off flag. Expiry/quota are
// separate and never change this.
func (s *Store) SetUserEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE users SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	return err
}

// DeleteUser removes a user and detaches them from the broadcast audience.
//
// The subscriber row survives on purpose — someone whose account was deleted is
// still in the bot, and reaching them is exactly what the "без аккаунта" audience is
// for — but it must stop naming an account that no longer exists, or the audience
// filters read a missing user's zero values as facts about a real one.
func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`UPDATE tg_subscribers SET user_id = NULL WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetUsersEnabled flips the manual enabled flag for many users in one statement,
// returning how many rows changed. Empty ids is a no-op.
func (s *Store) SetUsersEnabled(ids []int64, enabled bool) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, boolToInt(enabled))
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := s.db.Exec(
		`UPDATE users SET enabled = ? WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteUsers removes many users in one statement, returning how many were deleted.
func (s *Store) DeleteUsers(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := s.db.Exec(`DELETE FROM users WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// placeholders returns "?,?,…" with n terms for an IN clause.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
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
			&u.PlanID, &trialUsed, &u.TgLinkCode, &u.TgLinkCodeAt, &u.NotifiedStatus,
			&u.NotifiedExpireAt, &u.NotifiedQuotaAt,
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
