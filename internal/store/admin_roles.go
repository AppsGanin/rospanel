package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Admin roles: named permission sets (see model/perms.go and migration 0085).

var (
	// ErrRoleNotFound is returned when a role key matches no row.
	ErrRoleNotFound = errors.New("role not found")
	// ErrRoleInUse refuses deleting a role an admin or an API key still holds: the
	// account would be left pointing at nothing, which resolves to no permissions —
	// safe, but a lockout nobody asked for.
	ErrRoleInUse = errors.New("role in use")
)

// ListAdminRoles returns every role, presets first, then by creation, with how many
// admins and API keys hold each.
func (s *Store) ListAdminRoles() ([]model.AdminRole, error) {
	rows, err := s.db.Query(`
		SELECT r.key, r.name, r.perms, r.created_at,
		       (SELECT COUNT(1) FROM admins a WHERE a.role = r.key),
		       (SELECT COUNT(1) FROM api_keys k WHERE k.role = r.key AND k.revoked_at = 0)
		FROM admin_roles r
		ORDER BY (r.key = 'admin') DESC, (r.key = 'operator') DESC, r.created_at, r.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AdminRole{}
	for rows.Next() {
		var r model.AdminRole
		var perms string
		if err := rows.Scan(&r.Key, &r.Name, &perms, &r.CreatedAt, &r.Admins, &r.APIKeys); err != nil {
			return nil, err
		}
		r.Perms = model.SplitPerms(perms)
		if r.Perms == nil {
			r.Perms = []string{}
		}
		r.Preset = model.IsPresetRole(r.Key)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetAdminRole returns one role by key, or ErrRoleNotFound.
func (s *Store) GetAdminRole(key string) (model.AdminRole, error) {
	var r model.AdminRole
	var perms string
	err := s.db.QueryRow(
		`SELECT key, name, perms, created_at FROM admin_roles WHERE key = ?`, key,
	).Scan(&r.Key, &r.Name, &perms, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrRoleNotFound
	}
	if err != nil {
		return r, err
	}
	r.Perms = model.SplitPerms(perms)
	if r.Perms == nil {
		r.Perms = []string{}
	}
	r.Preset = model.IsPresetRole(r.Key)
	return r, nil
}

// CreateAdminRole adds a role under a freshly generated key and returns it.
func (s *Store) CreateAdminRole(name string, perms []string) (model.AdminRole, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return model.AdminRole{}, err
	}
	// The "r-" prefix keeps a generated key from ever colliding with a preset or with
	// "owner", which are bare words.
	key := "r-" + hex.EncodeToString(b)
	if _, err := s.db.Exec(
		`INSERT INTO admin_roles (key, name, perms, created_at) VALUES (?, ?, ?, ?)`,
		key, name, model.JoinPerms(perms), time.Now().Unix(),
	); err != nil {
		return model.AdminRole{}, err
	}
	return s.GetAdminRole(key)
}

// UpdateAdminRole renames a role and replaces its permissions. Admins holding it get
// the new set on their next request: sessions resolve the role on every lookup.
func (s *Store) UpdateAdminRole(key, name string, perms []string) error {
	res, err := s.db.Exec(
		`UPDATE admin_roles SET name = ?, perms = ? WHERE key = ?`,
		name, model.JoinPerms(perms), key,
	)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrRoleNotFound
	}
	return nil
}

// DeleteAdminRole removes a role nobody holds. The check and the delete share a
// transaction, and every write of a role key checks the role exists in the same
// statement (see roleExists), so an assignment cannot land on a deleted role. A
// revoked API key does not count as holding it and keeps the key it was issued with
// — the record of what it could do; it can no longer do anything.
func (s *Store) DeleteAdminRole(key string) error {
	return s.withTx(func(tx *sql.Tx) error {
		var held int
		if err := tx.QueryRow(`
			SELECT (SELECT COUNT(1) FROM admins WHERE role = ?)
			     + (SELECT COUNT(1) FROM api_keys WHERE role = ? AND revoked_at = 0)`,
			key, key,
		).Scan(&held); err != nil {
			return err
		}
		if held > 0 {
			return ErrRoleInUse
		}
		res, err := tx.Exec(`DELETE FROM admin_roles WHERE key = ?`, key)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return ErrRoleNotFound
		}
		return nil
	})
}

// permsFor resolves a role key and the stored permission list read beside it to
// the set a request holds: everything for the owner, the stored list otherwise —
// and nothing for a key no role row answers to.
func permsFor(role string, stored sql.NullString) model.PermSet {
	if role == model.RoleOwner {
		return model.OwnerPermSet()
	}
	if !stored.Valid {
		return model.PermSet{}
	}
	if set, ok := permCache.Load(stored.String); ok {
		return set.(model.PermSet)
	}
	set := model.NewPermSet(strings.Split(stored.String, ",")) // NewPermSet normalises
	permCache.Store(stored.String, set)
	return set
}

// permCache holds each stored permission list already parsed: every authenticated
// request resolves one, and the lists are few (one per role, plus what an edit left
// behind). The sets are shared — callers only read them.
var permCache sync.Map // stored perms string → model.PermSet
