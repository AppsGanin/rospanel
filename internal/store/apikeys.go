package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// apiKeyPrefix is the human-visible marker every raw API key starts with, so a
// leaked key is recognizably a RosPanel credential (and greppable in logs).
const apiKeyPrefix = "rp_"

// prefixLen is how many leading characters of a key (after "rp_") are kept in
// clear as the display prefix. Long enough to disambiguate keys in the UI,
// short enough to leak nothing useful about the 256-bit secret.
const prefixLen = 6

// generateAPIKey mints a raw key ("rp_<43 url-safe chars>") and its display
// prefix. The raw key is 256 bits of entropy — the whole thing is the secret.
func generateAPIKey() (raw, prefix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	body := base64.RawURLEncoding.EncodeToString(b)
	raw = apiKeyPrefix + body
	prefix = apiKeyPrefix + body[:prefixLen]
	return raw, prefix, nil
}

// CreateAPIKey mints a new named key, stores only its HMAC hash, and returns the
// model record with RawKey populated (shown to the operator exactly once). role is
// the admin role the key acts with; "" is full access.
func (s *Store) CreateAPIKey(name, role string) (*model.APIKey, error) {
	raw, prefix, err := generateAPIKey()
	if err != nil {
		return nil, err
	}
	hash, err := s.tokenHash(raw)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	// "" (full access) or a role row that exists as the key is written. Not
	// roleExists: that one lets "owner" through, which is an admin's role, never a key's.
	res, err := s.db.Exec(
		`INSERT INTO api_keys (name, key_hash, prefix, created_at, role)
		 SELECT ?, ?, ?, ?, ? WHERE ? = '' OR EXISTS (SELECT 1 FROM admin_roles WHERE key = ?)`,
		name, hash, prefix, now, role, role, role,
	)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return nil, ErrRoleNotFound
	}
	id, _ := res.LastInsertId()
	return &model.APIKey{
		ID:        id,
		Name:      name,
		Role:      role,
		Prefix:    prefix,
		CreatedAt: now,
		RawKey:    raw,
	}, nil
}

// LookupAPIKey resolves a raw key to its record, ignoring revoked keys. The
// lookup is by HMAC hash (a UNIQUE-indexed column) so the DB does the
// comparison; the raw key never touches storage. last_used_at is bumped on a
// successful, non-revoked match. Returns (nil, nil) when no active key matches.
func (s *Store) LookupAPIKey(raw string) (*model.APIKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	hash, err := s.tokenHash(raw)
	if err != nil {
		return nil, err
	}
	var k model.APIKey
	var perms sql.NullString
	err = s.rdb.QueryRow(
		`SELECT k.id, k.name, k.prefix, k.created_at, k.last_used_at, k.revoked_at, k.role, r.perms
		 FROM api_keys k LEFT JOIN admin_roles r ON r.key = k.role
		 WHERE k.key_hash = ?`, hash,
	).Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &k.Role, &perms)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !k.Active() {
		return nil, nil
	}
	// No role is full access — every key issued before roles existed. A role that no
	// longer resolves grants nothing (permsFor), never everything.
	if k.Role == "" {
		k.Perms = model.OwnerPermSet()
	} else {
		k.Perms = permsFor(k.Role, perms)
	}
	now := time.Now().Unix()
	_, _ = s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, k.ID)
	k.LastUsedAt = now
	return &k, nil
}

// ListAPIKeys returns all keys (active and revoked), newest first. RawKey is
// never populated here — it exists only in the CreateAPIKey response.
func (s *Store) ListAPIKeys() ([]model.APIKey, error) {
	rows, err := s.db.Query(
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at, role
		 FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIKey
	for rows.Next() {
		var k model.APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix,
			&k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &k.Role); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey marks a key revoked (idempotent). The row is kept so a revoked
// key still shows in the UI with its metadata; the hash stays so the same raw
// key can never be silently re-lived.
func (s *Store) RevokeAPIKey(id int64) error {
	_, err := s.db.Exec(
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at = 0`,
		time.Now().Unix(), id,
	)
	return err
}

// APIKeyPerms is what the key with this id may do — its role's set, every
// permission for a key without one — whether or not it is still active. ok is false
// when no key has the id.
func (s *Store) APIKeyPerms(id int64) (model.PermSet, bool, error) {
	var role string
	var perms sql.NullString
	err := s.db.QueryRow(
		`SELECT k.role, r.perms FROM api_keys k LEFT JOIN admin_roles r ON r.key = k.role
		 WHERE k.id = ?`, id,
	).Scan(&role, &perms)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if role == "" {
		return model.OwnerPermSet(), true, nil
	}
	return permsFor(role, perms), true, nil
}
