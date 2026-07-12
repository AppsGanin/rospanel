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

// CreateSession issues a new opaque session token for an admin and stores only
// its HMAC hash. The raw token is returned to set as a cookie.
func (s *Store) CreateSession(adminID int64, ttl time.Duration) (string, error) {
	token, err := auth.RandomToken()
	if err != nil {
		return "", err
	}
	hash, err := s.tokenHash(token)
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(ttl).Unix()
	if _, err := s.db.Exec(
		`INSERT INTO admin_sessions (token_hash, admin_id, expires_at) VALUES (?, ?, ?)`,
		hash, adminID, expires,
	); err != nil {
		return "", err
	}
	// Opportunistically drop expired sessions on each new login. LookupSession only
	// purges a session lazily when its own token is presented, so without this a
	// session whose owner never returns would linger forever; logins are rare and
	// admin-only, so this keeps admin_sessions bounded to live sessions.
	_, _ = s.db.Exec(`DELETE FROM admin_sessions WHERE expires_at < ?`, time.Now().Unix())
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
		SELECT a.id, a.username, a.role, a.must_change_password, s.expires_at
		FROM admin_sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = ?`, hash,
	).Scan(&a.ID, &a.Username, &a.Role, &mustChange, &expires)
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
