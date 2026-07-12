package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ErrAdminNotFound is returned when an admin id matches no row.
var ErrAdminNotFound = errors.New("admin not found")

// CountAdmins returns the number of admin accounts.
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM admins`).Scan(&n)
	return n, err
}

// CreateAdmin inserts an admin with the given username, password hash and role.
// mustChange gates the account on a password change at first login — set for every
// account created with a password someone else picked.
func (s *Store) CreateAdmin(username, passwordHash, role string, mustChange bool) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO admins (username, password_hash, role, must_change_password)
		 VALUES (?, ?, ?, ?)`,
		username, passwordHash, role, boolToInt(mustChange),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAdmins returns the roster, owner first, then by creation order.
func (s *Store) ListAdmins() ([]model.Admin, error) {
	rows, err := s.db.Query(`
		SELECT id, username, role, must_change_password, created_at, last_login_at
		FROM admins
		ORDER BY (role = 'owner') DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Admin
	for rows.Next() {
		var a model.Admin
		var mustChange int
		if err := rows.Scan(
			&a.ID, &a.Username, &a.Role, &mustChange, &a.CreatedAt, &a.LastLoginAt,
		); err != nil {
			return nil, err
		}
		a.MustChangePassword = mustChange != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAdmin returns one admin by id, or ErrAdminNotFound.
func (s *Store) GetAdmin(id int64) (model.Admin, error) {
	var a model.Admin
	var mustChange int
	err := s.db.QueryRow(`
		SELECT id, username, role, must_change_password, created_at, last_login_at
		FROM admins WHERE id = ?`, id,
	).Scan(&a.ID, &a.Username, &a.Role, &mustChange, &a.CreatedAt, &a.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrAdminNotFound
	}
	a.MustChangePassword = mustChange != 0
	return a, err
}

// DeleteAdmin removes an admin. Their sessions go with them: admin_sessions has
// ON DELETE CASCADE, so a deleted admin's live cookies stop resolving immediately.
func (s *Store) DeleteAdmin(id int64) error {
	res, err := s.db.Exec(`DELETE FROM admins WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrAdminNotFound
	}
	return nil
}

// SetAdminRole changes an admin's role.
func (s *Store) SetAdminRole(id int64, role string) error {
	_, err := s.db.Exec(`UPDATE admins SET role = ? WHERE id = ?`, role, id)
	return err
}

// UpdateAdminUsername changes an admin's login name.
func (s *Store) UpdateAdminUsername(id int64, username string) error {
	_, err := s.db.Exec(`UPDATE admins SET username = ? WHERE id = ?`, username, id)
	return err
}

// UpdateAdminPassword replaces an admin's password hash and sets or clears the
// forced-change gate in the same statement: the two always move together — an admin
// choosing their own password clears it, an owner assigning one raises it.
func (s *Store) UpdateAdminPassword(id int64, passwordHash string, mustChange bool) error {
	_, err := s.db.Exec(
		`UPDATE admins SET password_hash = ?, must_change_password = ? WHERE id = ?`,
		passwordHash, boolToInt(mustChange), id,
	)
	return err
}

// GetAdminAuth returns the id, password hash and role for a username.
func (s *Store) GetAdminAuth(username string) (id int64, hash, role string, err error) {
	err = s.db.QueryRow(
		`SELECT id, password_hash, role FROM admins WHERE username = ?`, username,
	).Scan(&id, &hash, &role)
	return id, hash, role, err
}

// GetAdminHash returns an admin's current password hash by id (for re-authenticating
// a credential change against the current password).
func (s *Store) GetAdminHash(id int64) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM admins WHERE id = ?`, id).Scan(&hash)
	return hash, err
}

// TouchAdminLogin records a successful sign-in, so the roster can show who is still
// actually using their account.
func (s *Store) TouchAdminLogin(id int64) error {
	_, err := s.db.Exec(
		`UPDATE admins SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), id,
	)
	return err
}
