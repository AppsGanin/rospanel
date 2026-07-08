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
// model record with RawKey populated (shown to the operator exactly once).
func (s *Store) CreateAPIKey(name string) (*model.APIKey, error) {
	raw, prefix, err := generateAPIKey()
	if err != nil {
		return nil, err
	}
	hash, err := s.tokenHash(raw)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO api_keys (name, key_hash, prefix, created_at) VALUES (?, ?, ?, ?)`,
		name, hash, prefix, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.APIKey{
		ID:        id,
		Name:      name,
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
	err = s.db.QueryRow(
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at
		 FROM api_keys WHERE key_hash = ?`, hash,
	).Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !k.Active() {
		return nil, nil
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
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at
		 FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIKey
	for rows.Next() {
		var k model.APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix,
			&k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
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
