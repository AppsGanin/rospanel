package store

// CountAdmins returns the number of admin accounts.
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM admins`).Scan(&n)
	return n, err
}

// CreateAdmin inserts an admin with the given username and password hash.
func (s *Store) CreateAdmin(username, passwordHash string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO admins (username, password_hash) VALUES (?, ?)`,
		username, passwordHash,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAdminUsername changes an admin's login name.
func (s *Store) UpdateAdminUsername(id int64, username string) error {
	_, err := s.db.Exec(`UPDATE admins SET username = ? WHERE id = ?`, username, id)
	return err
}

// UpdateAdminPassword replaces an admin's password hash.
func (s *Store) UpdateAdminPassword(id int64, passwordHash string) error {
	_, err := s.db.Exec(
		`UPDATE admins SET password_hash = ? WHERE id = ?`, passwordHash, id,
	)
	return err
}

// GetAdminAuth returns the admin id and password hash for a username.
func (s *Store) GetAdminAuth(username string) (id int64, hash string, err error) {
	err = s.db.QueryRow(
		`SELECT id, password_hash FROM admins WHERE username = ?`, username,
	).Scan(&id, &hash)
	return id, hash, err
}

// GetAdminHash returns an admin's current password hash by id (for re-authenticating
// a credential change against the current password).
func (s *Store) GetAdminHash(id int64) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM admins WHERE id = ?`, id).Scan(&hash)
	return hash, err
}
