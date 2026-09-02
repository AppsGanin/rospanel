package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

const userCols = `id, name, uuid, password, sub_token, enabled,
	data_limit, expire_at, used_up, used_down, last_up, last_down, created_at,
	reset_period, last_reset_at, last_seen, device_limit, speed_limit, tg_chat_id,
	plan_id, trial_used, tg_link_code, tg_link_code_at, notified_status,
	notified_expire_at, notified_quota_at, device_over_since, note, tags, wg_private_key,
	abuse_action, abuse_until, abuse_prev_speed, abuse_warned_day`

// errTagsInvalid is returned by SetUserTags for a list model.NormalizeTags refuses.
// Callers validate before writing, so reaching this means a bug, not user input.
var errTagsInvalid = errors.New("store: invalid user tags")

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

// ImportedUser is everything an importer knows about a user coming from another
// panel: the credentials to keep, the limits and the usage so far.
type ImportedUser struct {
	Name        string
	UUID        string
	Password    string
	SubToken    string
	DataLimit   int64
	ExpireAt    int64
	UsedUp      int64
	UsedDown    int64
	DeviceLimit int
	SpeedLimit  int
	ResetPeriod string
	Enabled     bool
	Note        string
	Tags        []string // already in model.NormalizeTags form
	// WGPrivateKey carries the user's AmneziaWG identity over, so the configs they
	// already hold keep working. Empty mints one on first use, as for a new user.
	WGPrivateKey string
}

// ImportUser inserts a user with the credentials and counters another panel had
// for them, in one statement, so a half-imported user cannot exist. The UUID
// column is UNIQUE: an id already present fails the insert, which is how a second
// import of the same file is kept from doubling everyone.
func (s *Store) ImportUser(in ImportedUser) (*model.User, error) {
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	var id int64
	period := in.ResetPeriod
	if period == "" {
		period = "none"
	}
	err := s.db.QueryRow(
		`INSERT INTO users (name, uuid, password, sub_token, enabled, data_limit, expire_at,
		   used_up, used_down, device_limit, speed_limit, reset_period, note, tags, wg_private_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		in.Name, in.UUID, encField(in.Password), in.SubToken, enabled, in.DataLimit, in.ExpireAt,
		in.UsedUp, in.UsedDown, in.DeviceLimit, in.SpeedLimit, period, in.Note,
		model.EncodeTags(in.Tags), encField(in.WGPrivateKey),
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetUser(id)
}

// UserUUIDs returns every UUID in use, for an importer to tell "already here"
// from "new" before it writes anything.
func (s *Store) UserUUIDs() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT id, uuid FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var u string
		if err := rows.Scan(&id, &u); err != nil {
			return nil, err
		}
		out[strings.ToLower(u)] = id
	}
	return out, rows.Err()
}

// SubTokens returns every subscription token in use. The column is uniquely
// indexed, so an importer carrying tokens from another panel has to know which
// ones are taken before it writes — a collision is an insert error, and the user
// it would fail is one nobody could then reach.
func (s *Store) SubTokens() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT sub_token FROM users WHERE sub_token != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[t] = struct{}{}
	}
	return out, rows.Err()
}

// ListUsers returns all users, newest first.
func (s *Store) ListUsers() ([]model.User, error) {
	return s.queryUsers(`SELECT ` + userCols + ` FROM users ORDER BY id DESC`)
}

// UserIDs returns the set of existing user ids.
//
// For callers that only need to know whether an id is real — validating what a node
// reported, say. ListUsers would answer the same question by building every column
// of every row, which is a lot of work to throw away.
func (s *Store) UserIDs() (map[int64]struct{}, error) {
	rows, err := s.db.Query(`SELECT id FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// UserCounts is the dashboard's view of the users table: how many there are, how
// many are working, how many are connected right now, and the lifetime traffic
// totals.
type UserCounts struct {
	Total     int
	Active    int
	Online    int
	TotalUp   int64
	TotalDown int64
}

// CountUsers computes UserCounts in one aggregate query.
//
// The dashboard used to get these four numbers by loading every user row into Go
// and folding over the slice — on a 2s stream tick, per connected admin, which also
// meant decrypting every user's stored password (see decField) just to count them.
// This does the same arithmetic in SQLite and returns four scalars, so the cost
// stops scaling with the number of users AND the number of open panel tabs.
//
// The WHERE clause is deriveStatus's "active" case spelled out in SQL, in the same
// order: enabled, not expired, inside the quota, inside the device cap. The two must
// agree — TestCountUsersMatchesDeriveStatus holds them to it.
//
// Online rides on the device-count join that was already there: a user with at
// least one IP seen inside DeviceOnlineWindow is connected right now. Deliberately
// the same window that defines an "active device" everywhere else, so the dashboard
// and a user's device count can never tell different stories about who is on. It
// counts the whole fleet, not just this server — a node's sightings land in the
// same connections table when it syncs.
func (s *Store) CountUsers(now int64) (UserCounts, error) {
	var c UserCounts
	// The device clause is WorkingUsers' clause, term for term, because this number sits
	// next to the user list and any disagreement reads as the panel contradicting itself.
	// It previously omitted both the mode and the grace, so "hwid" and a user inside
	// DeviceLimitGrace each made this count one active user fewer than the list showed.
	err := s.db.QueryRow(`WITH device_count AS (`+deviceCountCTE+`)
		SELECT COUNT(*),
		       COALESCE(SUM(u.used_up), 0),
		       COALESCE(SUM(u.used_down), 0),
		       COALESCE(SUM(CASE WHEN u.enabled != 0
		            AND (u.expire_at = 0 OR u.expire_at > ?)
		            AND (u.data_limit = 0 OR u.used_up + u.used_down < u.data_limit)
		            AND (u.device_limit = 0
		                 OR NOT (SELECT ip_counts FROM device_count)
		                 OR COALESCE(d.n, 0) <= u.device_limit
		                 OR u.device_over_since = 0
		                 OR u.device_over_since > ?)
		           THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN COALESCE(d.n, 0) > 0 THEN 1 ELSE 0 END), 0)
		FROM users u
		LEFT JOIN (
		    SELECT user_id, COUNT(DISTINCT ip) AS n
		    FROM connections INDEXED BY idx_connections_last_seen
		    WHERE last_seen > ? GROUP BY user_id
		) d ON d.user_id = u.id`,
		now, now-model.DeviceLimitGrace, now-model.DeviceOnlineWindow,
	).Scan(&c.Total, &c.TotalUp, &c.TotalDown, &c.Active, &c.Online)
	return c, err
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

// ipCountsAsDevice answers, for Go-side callers, the same question deviceCountCTE
// answers in SQL. A read failure keeps the historical answer: enforcing a limit the
// operator set is the safer side of an unreadable settings row.
func (s *Store) ipCountsAsDevice() bool {
	var n int
	if err := s.db.QueryRow(`WITH device_count AS (` + deviceCountCTE + `)
		SELECT ip_counts FROM device_count`).Scan(&n); err != nil {
		return true
	}
	return n != 0
}

// deviceCountCTE yields one row, ip_counts, saying whether distinct source addresses
// enforce users.device_limit. It mirrors model.Settings.CountsIPAsDevice exactly; the two
// exist because the decision is needed on both sides of the wire, and any change has to
// land in both (a test pins that they agree).
const deviceCountCTE = `SELECT CASE
	WHEN device_count_mode = 'hwid' THEN 0
	ELSE 1
	END AS ip_counts
	FROM settings WHERE id = 1`

// WorkingUsers returns users that should be in the proxy config right now:
// manually enabled AND not expired AND within their data limit AND within their
// device limit. enabled is an independent manual flag — expiry/quota/devices
// never change it, they just exclude the user from the config here.
func (s *Store) WorkingUsers(now int64) ([]model.User, error) {
	since := now - model.DeviceOnlineWindow
	// device_count decides, once, whether source addresses still enforce the limit — the
	// same rule model.Settings.CountsIPAsDevice states, kept in SQL so every caller of
	// this query and the status derivation agree without threading a flag through six of
	// them. See migration 0055 and issue #66.
	return s.queryUsers(`WITH device_count AS (`+deviceCountCTE+`)
		SELECT `+userCols+` FROM users
		WHERE enabled = 1
		  AND (expire_at = 0 OR expire_at > ?)
		  AND (data_limit = 0 OR used_up + used_down < data_limit)
		  AND (device_limit = 0 OR NOT (SELECT ip_counts FROM device_count)
		       -- Not over the limit right now. Checked as well as the stamp, not instead
		       -- of it, so a user who has fallen back under is admitted even if nothing
		       -- has run to clear their stamp yet: an out-of-date stamp must never be
		       -- able to hold someone out.
		       OR (SELECT COUNT(DISTINCT c.ip) FROM connections c
		           WHERE c.user_id = users.id AND c.last_seen > ?) <= device_limit
		       -- Over, but not for long enough yet. See DeviceLimitGrace: an address
		       -- left behind by a network change or a carrier's address rotation leaves
		       -- the window before this expires, so it never costs anyone a cut.
		       OR device_over_since = 0
		       OR device_over_since > ?)
		ORDER BY id ASC`, now, since, now-model.DeviceLimitGrace)
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
	return updateTrafficOn(s.db, id, addUp, addDown, lastUp, lastDown)
}

func updateTrafficOn(ex execer, id, addUp, addDown, lastUp, lastDown int64) error {
	_, err := ex.Exec(
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
	return setUserLimitsOn(s.db, id, dataLimit, expireAt, deviceLimit)
}

func setUserLimitsOn(ex execer, id, dataLimit, expireAt int64, deviceLimit int) error {
	_, err := ex.Exec(
		`UPDATE users SET data_limit = ?, expire_at = ?, device_limit = ? WHERE id = ?`,
		dataLimit, expireAt, deviceLimit, id,
	)
	return err
}

// SetUserSpeedLimit sets the per-user bandwidth cap in kbit/s (0 = unlimited).
//
// Its own setter rather than a fourth argument to SetUserLimits: that signature is
// reached from the API, the bots and the plan machinery, and widening it would make
// every caller state a speed they have no opinion about.
func (s *Store) SetUserSpeedLimit(id int64, kbps int) error {
	return setUserSpeedLimitOn(s.db, id, kbps)
}

func setUserSpeedLimitOn(ex execer, id int64, kbps int) error {
	_, err := ex.Exec(`UPDATE users SET speed_limit = ? WHERE id = ?`, kbps, id)
	return err
}

// ShapedUsers returns every user with a speed cap, paired with the source addresses
// they have been seen on since `since` — everything internal/shaper needs, in one
// query rather than one per user.
//
// A capped user with no recent address is returned with none: the caller drops them
// (nothing to match on), and their absence from the result would otherwise be
// indistinguishable from having no cap.
//
// Addresses come newest-first and are capped at MaxShapedIPsPerUser. Every address
// becomes a classifier the kernel walks per packet, and the number of addresses one
// account can accrue inside the window has no natural bound — a roaming phone
// collects them honestly, and anyone sharing an account collects them faster. The
// newest few are also the right ones: they are where the traffic being shaped is.
func (s *Store) ShapedUsers(since int64) (map[int64]SpeedTarget, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.speed_limit, COALESCE(c.ip, '')
		FROM users u
		LEFT JOIN connections c ON c.user_id = u.id AND c.last_seen > ?
		WHERE u.speed_limit > 0 AND u.enabled = 1
		ORDER BY u.id, c.last_seen DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]SpeedTarget{}
	for rows.Next() {
		var id int64
		var kbps int
		var ip string
		if err := rows.Scan(&id, &kbps, &ip); err != nil {
			return nil, err
		}
		t := out[id]
		t.Kbps = kbps
		if ip != "" && len(t.IPs) < model.MaxShapedIPsPerUser {
			t.IPs = append(t.IPs, ip)
		}
		out[id] = t
	}
	return out, rows.Err()
}

// CappedUsers returns the speed cap of every user who currently has one and can
// actually connect, keyed by user id.
//
// Deliberately not WorkingUsers: this is read on every node's sync poll, and that
// one loads every user row and decrypts every stored password (see decField) to
// answer a question about a handful of them. The conditions below are the same ones
// WorkingUsers applies, minus the device cap — a user over their device limit is
// still worth shaping, since the limit is derived on read and they keep connecting.
func (s *Store) CappedUsers(now int64) (map[int64]int, error) {
	rows, err := s.db.Query(`
		SELECT id, speed_limit FROM users
		WHERE speed_limit > 0 AND enabled = 1
		  AND (expire_at = 0 OR expire_at > ?)
		  AND (data_limit = 0 OR used_up + used_down < data_limit)`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var kbps int
		if err := rows.Scan(&id, &kbps); err != nil {
			return nil, err
		}
		out[id] = kbps
	}
	return out, rows.Err()
}

// SpeedTarget is one user's cap and the addresses it applies to.
type SpeedTarget struct {
	Kbps int
	IPs  []string
}

// SetUserName updates a user's display name.
func (s *Store) SetUserName(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE users SET name = ? WHERE id = ?`, name, id)
	return err
}

// SetUserWGKey stores a user's AmneziaWG private key (encrypted at rest). Written
// once, when the first tunnel config is built for them; never rotated on its own.
func (s *Store) SetUserWGKey(id int64, priv string) error {
	_, err := s.db.Exec(`UPDATE users SET wg_private_key = ? WHERE id = ?`, encField(priv), id)
	return err
}

// SetUserNote replaces the operator's note on a user.
func (s *Store) SetUserNote(id int64, note string) error {
	_, err := s.db.Exec(`UPDATE users SET note = ? WHERE id = ?`, note, id)
	return err
}

// SetUserTags replaces a user's tag list. Tags are stored in model.NormalizeTags
// form regardless of what the caller hands in, so a read never sees a variant
// spelling. The caller is expected to have validated the input first; the check
// here is a safety net, not the place a bad tag gets reported to a person.
func (s *Store) SetUserTags(id int64, tags []string) error {
	norm, ok := model.NormalizeTags(tags)
	if !ok {
		return errTagsInvalid
	}
	_, err := s.db.Exec(`UPDATE users SET tags = ? WHERE id = ?`, model.EncodeTags(norm), id)
	return err
}

// AllUserTags returns every distinct tag in use with how many users carry it —
// what the list page's tag filter and the tag editor's suggestions are built from.
func (s *Store) AllUserTags() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT tags FROM users WHERE tags != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		for _, t := range model.DecodeTags(raw) {
			out[t]++
		}
	}
	return out, rows.Err()
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

// SetAbuseMeasure records the measure the panel imposed on a user for blocklist
// traffic, together with what it changed, so the lift can undo exactly that.
func (s *Store) SetAbuseMeasure(id int64, action string, until int64, prevSpeed int) error {
	_, err := s.db.Exec(`UPDATE users SET abuse_action = ?, abuse_until = ?, abuse_prev_speed = ? WHERE id = ?`,
		action, until, prevSpeed, id)
	return err
}

// ClearAbuseMeasure forgets the measure — after it was lifted, or after an operator
// overruled it by hand.
func (s *Store) ClearAbuseMeasure(id int64) error {
	_, err := s.db.Exec(`UPDATE users SET abuse_action = '', abuse_until = 0, abuse_prev_speed = 0 WHERE id = ?`, id)
	return err
}

// SetAbuseWarnedDay marks the day the user was last warned, so one day's traffic
// produces one warning however many flushes carry it.
func (s *Store) SetAbuseWarnedDay(id int64, day string) error {
	_, err := s.db.Exec(`UPDATE users SET abuse_warned_day = ? WHERE id = ?`, day, id)
	return err
}

// AbuseMeasuresDue returns the users whose measure has run its course by now.
func (s *Store) AbuseMeasuresDue(now int64) ([]model.User, error) {
	return s.queryUsers(`SELECT `+userCols+` FROM users
		WHERE abuse_action <> '' AND abuse_until > 0 AND abuse_until <= ? ORDER BY id`, now)
}

// DeleteUser removes a user and detaches them from the broadcast audience.
//
// The subscriber row survives on purpose — someone whose account was deleted is
// still in the bot, and reaching them is exactly what the "without an account" audience is
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
	var n int64
	err := s.withTx(func(tx *sql.Tx) error {
		// Detach the Telegram subscribers first, exactly as the single-user delete does
		// and for the same reason: tg_subscribers.user_id has no foreign key, so a row
		// deleted out from under it keeps pointing at an id that no longer exists — and
		// ids are AUTOINCREMENT, so nothing reclaims it. This path (bulk delete and the
		// retention sweep) removes the most users of any.
		if _, err := tx.Exec(
			`UPDATE tg_subscribers SET user_id = NULL WHERE user_id IN (`+placeholders(len(ids))+`)`,
			args...,
		); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM users WHERE id IN (`+placeholders(len(ids))+`)`, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
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
		var tags string
		if err := rows.Scan(
			&u.ID, &u.Name, &u.UUID, &u.Password, &u.SubToken, &enabled,
			&u.DataLimit, &u.ExpireAt, &u.UsedUp, &u.UsedDown, &u.LastUp, &u.LastDown, &created,
			&u.ResetPeriod, &u.LastResetAt, &u.LastSeen, &u.DeviceLimit, &u.SpeedLimit, &u.TgChatID,
			&u.PlanID, &trialUsed, &u.TgLinkCode, &u.TgLinkCodeAt, &u.NotifiedStatus,
			&u.NotifiedExpireAt, &u.NotifiedQuotaAt, &u.DeviceOverSince, &u.Note, &tags, &u.WGPrivateKey,
			&u.AbuseAction, &u.AbuseUntil, &u.AbusePrevSpeed, &u.AbuseWarnedDay,
		); err != nil {
			return nil, err
		}
		u.Enabled = enabled != 0
		u.TrialUsed = trialUsed != 0
		u.Tags = model.DecodeTags(tags)
		u.Password = decField(u.Password)
		u.WGPrivateKey = decField(u.WGPrivateKey)
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
	// The displayed count stays honest — it is how many addresses were seen — but it only
	// DRIVES the status while addresses are what enforces the limit. In "hwid" mode they
	// do not, and a phone changing network still read as "device limit exceeded" (issue
	// #66), the bot said so, and the HWID roster it is actually capped by showed one
	// device. Showing the number and enforcing it are separate decisions.
	countIP := s.ipCountsAsDevice()
	for i := range users {
		u := &users[i]
		active := counts[u.ID]
		u.ActiveDevices = active
		limit := u.DeviceLimit
		switch {
		case !countIP:
			limit = 0 // this counter does not enforce in "hwid" mode
		case u.DeviceOverSince == 0 || u.DeviceOverSince > now-model.DeviceLimitGrace:
			// Over the limit, but the grace has not run out, so nothing has happened to
			// them yet — and it usually never will, because this is what a network
			// change looks like. Saying "device limit exceeded" here would put the
			// panel, the API and the bot in the position of announcing a cut that the
			// enforcement query is not making.
			limit = 0
		}
		u.Status = deriveStatus(
			u.Enabled, u.ExpireAt, u.UsedUp+u.UsedDown, u.DataLimit, now,
			active, limit,
		)
	}
}

// StampDeviceOverLimit records, for every user, when they first went over their device
// limit — the clock DeviceLimitGrace runs against. Called after new sightings land.
//
// Kept out of WorkingUsers because that is a read, called from six places, and this is
// the one write the rule needs: the grace has to measure from a moment, and a moment
// cannot be derived from the connections table after the fact.
//
// Arming and disarming are separate statements over an id list rather than one UPDATE
// with a correlated count, so the cost is one grouped pass (ActiveDeviceCounts) plus at
// most two writes that touch only the rows actually changing state — usually none.
func (s *Store) StampDeviceOverLimit(now int64) error {
	// In "hwid" mode addresses do not enforce anything, so nothing should carry a stamp:
	// left armed, it would cut people the moment an operator switched back to "auto".
	if !s.ipCountsAsDevice() {
		_, err := s.db.Exec(`UPDATE users SET device_over_since = 0 WHERE device_over_since <> 0`)
		return err
	}
	counts, err := s.ActiveDeviceCounts(now - model.DeviceOnlineWindow)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT id, device_limit, device_over_since FROM users
		WHERE device_limit > 0 OR device_over_since <> 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var arm, disarm []int64
	for rows.Next() {
		var id, since int64
		var limit int
		if err := rows.Scan(&id, &limit, &since); err != nil {
			return err
		}
		over := limit > 0 && counts[id] > limit
		switch {
		case over && since == 0:
			arm = append(arm, id)
		case !over && since != 0:
			disarm = append(disarm, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(arm) == 0 && len(disarm) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		if len(arm) > 0 {
			args := make([]any, 0, len(arm)+1)
			args = append(args, now)
			for _, id := range arm {
				args = append(args, id)
			}
			if _, err := tx.Exec(`UPDATE users SET device_over_since = ?
				WHERE id IN (`+placeholders(len(arm))+`)`, args...); err != nil {
				return err
			}
		}
		if len(disarm) > 0 {
			args := make([]any, len(disarm))
			for i, id := range disarm {
				args[i] = id
			}
			if _, err := tx.Exec(`UPDATE users SET device_over_since = 0
				WHERE id IN (`+placeholders(len(disarm))+`)`, args...); err != nil {
				return err
			}
		}
		return nil
	})
}
