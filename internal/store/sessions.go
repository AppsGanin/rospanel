package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
)

// sessionPepper returns the per-install HMAC pepper mixed into session token
// hashes, generating and persisting one on first use.
func (s *Store) sessionPepper() (string, error) {
	var pepper string
	err := s.db.QueryRow(`SELECT session_pepper FROM settings WHERE id = 1`).Scan(&pepper)
	if err != nil {
		return "", err
	}
	if pepper != "" {
		return pepper, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	pepper = hex.EncodeToString(b)
	_, err = s.db.Exec(`UPDATE settings SET session_pepper = ? WHERE id = 1`, pepper)
	return pepper, err
}

// tokenHash returns the HMAC-SHA256 of a raw session token under the install
// pepper — what's stored in admin_sessions (the raw token never is).
func (s *Store) tokenHash(token string) (string, error) {
	pepper, err := s.sessionPepper()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// MaxSessionUserAgent caps what is kept of the User-Agent header. A browser's is a
// hundred-odd characters; anything longer is a client making a point, and the
// column is for telling a phone from a laptop, not for archiving the header.
const MaxSessionUserAgent = 256

// CreateSession issues a new opaque session token for an admin and stores only
// its HMAC hash. The raw token is returned to set as a cookie. The session records
// no address or client; login uses CreateSessionFrom, which does.
func (s *Store) CreateSession(adminID int64, ttl time.Duration) (string, error) {
	return s.CreateSessionFrom(adminID, ttl, "", "")
}

// CreateSessionFrom is CreateSession with where the login came from — the client
// address and User-Agent — so the admin can later recognise the session in their
// own list, and end one they did not open.
func (s *Store) CreateSessionFrom(adminID int64, ttl time.Duration, ip, userAgent string) (string, error) {
	token, err := auth.RandomToken()
	if err != nil {
		return "", err
	}
	hash, err := s.tokenHash(token)
	if err != nil {
		return "", err
	}
	if len(userAgent) > MaxSessionUserAgent {
		userAgent = userAgent[:MaxSessionUserAgent]
	}
	now := time.Now().Unix()
	expires := now + int64(ttl.Seconds())
	if _, err := s.db.Exec(
		`INSERT INTO admin_sessions (token_hash, admin_id, expires_at, created_at, ip, user_agent, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hash, adminID, expires, now, ip, userAgent, now,
	); err != nil {
		return "", err
	}
	// Opportunistically drop expired sessions on each new login. LookupSession only
	// purges a session lazily when its own token is presented, so without this a
	// session whose owner never returns would linger forever; logins are rare and
	// admin-only, so this keeps admin_sessions bounded to live sessions.
	_, _ = s.db.Exec(`DELETE FROM admin_sessions WHERE expires_at < ?`, now)
	return token, nil
}

// SessionAdmin is who a session belongs to — resolved fresh on every authenticated
// request, so a role change or a raised password gate takes effect on the admin's
// next request rather than on their next login.
type SessionAdmin struct {
	ID                 int64
	Username           string
	Role               string
	MustChangePassword bool

	// SessionID identifies the session itself (the row, never the token), so a
	// handler can mark it as "this one" in the admin's own list and keep it out of a
	// "sign out everywhere else". LastSeenAt is when the row was last stamped; the
	// middleware uses it to stamp no more often than once a minute.
	SessionID  int64
	LastSeenAt int64
}

// LookupSession resolves a raw session token to its admin. Expired sessions are
// deleted and treated as invalid.
func (s *Store) LookupSession(token string) (SessionAdmin, bool) {
	hash, err := s.tokenHash(token)
	if err != nil {
		return SessionAdmin{}, false
	}
	var a SessionAdmin
	var mustChange int
	var expires int64
	err = s.db.QueryRow(`
		SELECT a.id, a.username, a.role, a.must_change_password, s.expires_at, s.id, s.last_seen_at
		FROM admin_sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = ?`, hash,
	).Scan(&a.ID, &a.Username, &a.Role, &mustChange, &expires, &a.SessionID, &a.LastSeenAt)
	if err != nil {
		return SessionAdmin{}, false
	}
	if time.Now().Unix() > expires {
		_ = s.DeleteSession(token)
		return SessionAdmin{}, false
	}
	a.MustChangePassword = mustChange != 0
	return a, true
}

// TouchSession records that a session was just used, and from which address.
//
// The address is overwritten, not kept from login: the question the session list
// answers is "where is this session being used from NOW", and a cookie that moved
// to another machine is exactly what a changed address reveals. Callers throttle
// this (see SessionTouchInterval) — one write per request would put the panel's
// own chatter on the same SQLite writer as everything else.
func (s *Store) TouchSession(id, now int64, ip string) error {
	_, err := s.db.Exec(`UPDATE admin_sessions SET last_seen_at = ?, ip = ? WHERE id = ?`, now, ip, id)
	return err
}

// SessionTouchInterval is how long a session's last-seen stamp is allowed to lag
// behind reality. A minute is coarse enough that a busy panel tab (the dashboard
// streams every couple of seconds) costs one write a minute, and fine enough that
// "last used" reads as live.
const SessionTouchInterval = 60 * time.Second

// AdminSession is one open session as the admin sees it in their own list. There
// is no token in it, and no hash: the id is the only handle, and it is only ever
// accepted together with the admin it belongs to.
type AdminSession struct {
	ID         int64  `json:"id"`
	IP         string `json:"ip"`         // last address it was used from
	UserAgent  string `json:"user_agent"` // the browser, as it introduced itself
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
	// Current marks the session that made the request, so the list can say "this
	// device" and the revoke button can stay off it. Set by the handler.
	Current bool `json:"current"`
}

// ListAdminSessions returns an admin's live sessions, most recently used first.
func (s *Store) ListAdminSessions(adminID int64) ([]AdminSession, error) {
	rows, err := s.db.Query(`
		SELECT id, ip, user_agent, created_at, last_seen_at, expires_at
		FROM admin_sessions
		WHERE admin_id = ? AND expires_at >= ?
		ORDER BY last_seen_at DESC, id DESC`, adminID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminSession{}
	for rows.Next() {
		var x AdminSession
		if err := rows.Scan(&x.ID, &x.IP, &x.UserAgent, &x.CreatedAt, &x.LastSeenAt, &x.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// DeleteAdminSessionByID revokes one session by id, but only if it belongs to
// adminID: the id comes from the wire, and an admin must not be able to end a
// colleague's session by guessing a small integer. Reports whether a row went.
func (s *Store) DeleteAdminSessionByID(adminID, id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM admin_sessions WHERE id = ? AND admin_id = ?`, id, adminID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteOtherAdminSessions revokes every session of an admin except keepID — "sign
// out everywhere else". Returns how many were ended.
func (s *Store) DeleteOtherAdminSessions(adminID, keepID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM admin_sessions WHERE admin_id = ? AND id <> ?`, adminID, keepID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteAllAdminSessions ends every session of an admin — the response to "that
// sign-in was not me": whoever holds the password is signed out everywhere, the
// rightful owner included, until the password is changed. Returns how many went.
func (s *Store) DeleteAllAdminSessions(adminID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM admin_sessions WHERE admin_id = ?`, adminID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteSession revokes a session by its raw token.
func (s *Store) DeleteSession(token string) error {
	hash, err := s.tokenHash(token)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM admin_sessions WHERE token_hash = ?`, hash)
	return err
}

// DeleteSessionsForAdmin revokes every session for an admin.
func (s *Store) DeleteSessionsForAdmin(adminID int64) error {
	_, err := s.db.Exec(`DELETE FROM admin_sessions WHERE admin_id = ?`, adminID)
	return err
}

// DeleteSessionsForAdminExcept revokes every session belonging to an admin except
// the one identified by keepToken — used after a credential change so a previously
// stolen cookie can't outlive the change, while the admin doing the change stays
// logged in.
func (s *Store) DeleteSessionsForAdminExcept(adminID int64, keepToken string) error {
	if keepToken == "" {
		return s.DeleteSessionsForAdmin(adminID)
	}
	keep, err := s.tokenHash(keepToken)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`DELETE FROM admin_sessions WHERE admin_id = ? AND token_hash <> ?`,
		adminID, keep,
	)
	return err
}
