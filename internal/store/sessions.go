package store

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession issues a new opaque session token for an admin and stores only
// its hash. The raw token is returned to set as a cookie.
func (s *Store) CreateSession(adminID int64, ttl time.Duration) (string, error) {
	token, err := auth.RandomToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(ttl).Unix()
	if _, err := s.db.Exec(
		`INSERT INTO admin_sessions (token_hash, admin_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), adminID, expires,
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

// LookupSession resolves a raw session token to its admin. Expired sessions are
// deleted and treated as invalid.
func (s *Store) LookupSession(token string) (adminID int64, username string, ok bool) {
	var expires int64
	err := s.db.QueryRow(`
		SELECT a.id, a.username, s.expires_at
		FROM admin_sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = ?`, hashToken(token),
	).Scan(&adminID, &username, &expires)
	if err != nil {
		return 0, "", false
	}
	if time.Now().Unix() > expires {
		_ = s.DeleteSession(token)
		return 0, "", false
	}
	return adminID, username, true
}

// DeleteSession revokes a session by its raw token.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM admin_sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// DeleteSessionsForAdminExcept revokes every session belonging to an admin except
// the one identified by keepToken — used after a credential change so a previously
// stolen cookie can't outlive the change, while the admin doing the change stays
// logged in.
func (s *Store) DeleteSessionsForAdminExcept(adminID int64, keepToken string) error {
	_, err := s.db.Exec(
		`DELETE FROM admin_sessions WHERE admin_id = ? AND token_hash <> ?`,
		adminID, hashToken(keepToken),
	)
	return err
}
