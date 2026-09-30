package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"slices"
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
// model record with RawKey populated (shown to the operator exactly once). A key
// holds its own permissions: full access, or the set given (normalised as stored).
func (s *Store) CreateAPIKey(name string, full bool, perms, routes []string) (*model.APIKey, error) {
	raw, prefix, err := generateAPIKey()
	if err != nil {
		return nil, err
	}
	hash, err := s.tokenHash(raw)
	if err != nil {
		return nil, err
	}
	stored, rs := keyColumns(full, perms, routes)
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO api_keys (name, key_hash, prefix, created_at, perms, full_access, routes) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, hash, prefix, now, stored, boolToInt(full), rs,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.APIKey{
		ID:         id,
		Name:       name,
		FullAccess: full,
		Grants:     grantsOf(full, stored),
		Routes:     routesOf(full, rs),
		Prefix:     prefix,
		CreatedAt:  now,
		RawKey:     raw,
	}, nil
}

// grantsOf is a key's permission list as the panel shows it: none listed for full
// access, which is every permission and the owner's reach besides.
func grantsOf(full bool, stored string) []string {
	if full {
		return []string{}
	}
	if g := model.SplitPerms(stored); g != nil {
		return g
	}
	return []string{}
}

// keyColumns is what a key's perms and routes columns hold: nothing for full access.
func keyColumns(full bool, perms, routes []string) (string, string) {
	if full {
		return "", ""
	}
	rs := slices.Clone(routes)
	slices.Sort(rs)
	return model.JoinPerms(perms), strings.Join(slices.Compact(rs), ",")
}

// routesOf reads the routes column: the methods a key is held to, or none.
func routesOf(full bool, stored string) []string {
	if full || stored == "" {
		return []string{}
	}
	return strings.Split(stored, ",")
}

// keyPerms is what a key may do.
func keyPerms(full bool, stored string) model.PermSet {
	if full {
		return model.OwnerPermSet()
	}
	return permsFor("", sql.NullString{String: stored, Valid: stored != ""})
}

// ErrAPIKeyNotFound is an id no key has, or one already revoked.
var ErrAPIKeyNotFound = errors.New("api key not found")

// SetAPIKeyPerms replaces what an active key may do.
func (s *Store) SetAPIKeyPerms(id int64, full bool, perms, routes []string) error {
	stored, rs := keyColumns(full, perms, routes)
	res, err := s.db.Exec(
		`UPDATE api_keys SET perms = ?, full_access = ?, routes = ? WHERE id = ? AND revoked_at = 0`,
		stored, boolToInt(full), rs, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
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
	var perms, routes string
	err = s.rdb.QueryRow(
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at, perms, full_access, routes
		 FROM api_keys WHERE key_hash = ?`, hash,
	).Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &perms, &k.FullAccess, &routes)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !k.Active() {
		return nil, nil
	}
	k.Perms = keyPerms(k.FullAccess, perms)
	k.Grants = grantsOf(k.FullAccess, perms)
	k.Routes = routesOf(k.FullAccess, routes)
	if len(k.Routes) > 0 {
		k.Allowed = make(map[string]bool, len(k.Routes))
		for _, r := range k.Routes {
			k.Allowed[r] = true
		}
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
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at, perms, full_access, routes
		 FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIKey
	for rows.Next() {
		var k model.APIKey
		var perms, routes string
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix,
			&k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &perms, &k.FullAccess, &routes); err != nil {
			return nil, err
		}
		k.Grants = grantsOf(k.FullAccess, perms)
		k.Routes = routesOf(k.FullAccess, routes)
		k.Perms = keyPerms(k.FullAccess, perms)
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

// APIKeyPerms is what the key with this id may do, whether or not it is still
// active; revoked says which. ok is false when no key has the id.
func (s *Store) APIKeyPerms(id int64) (perms model.PermSet, revoked, ok bool, err error) {
	var stored string
	var full bool
	var revokedAt int64
	err = s.db.QueryRow(
		`SELECT perms, full_access, revoked_at FROM api_keys WHERE id = ?`, id,
	).Scan(&stored, &full, &revokedAt)
	if err == sql.ErrNoRows {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	return keyPerms(full, stored), revokedAt != 0, true, nil
}

// PermsOnlyAPIKey is an active key held to its permissions alone, with the list as
// stored — permissions later releases retired included, which reading it through
// the catalog would drop.
type PermsOnlyAPIKey struct {
	ID    int64
	Perms []string
}

// PermsOnlyAPIKeys lists the active keys not held to methods and not full access.
func (s *Store) PermsOnlyAPIKeys() ([]PermsOnlyAPIKey, error) {
	rows, err := s.db.Query(
		`SELECT id, perms FROM api_keys WHERE routes = '' AND full_access = 0 AND revoked_at = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PermsOnlyAPIKey
	for rows.Next() {
		var k PermsOnlyAPIKey
		var perms string
		if err := rows.Scan(&k.ID, &perms); err != nil {
			return nil, err
		}
		if perms != "" {
			k.Perms = strings.Split(perms, ",")
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
