package store

import (
	"database/sql"
	"errors"
	"strings"
)

// A website's own id for its client (see core.Manager.Signup). Kept off userCols like
// the source tag: only sign-up and the lookup read it.

// userByExternalIDSQL repeats the partial index's condition, or the planner cannot use
// it (see userBySubTokenSQL).
const userByExternalIDSQL = `SELECT id FROM users WHERE external_id = ? AND external_id <> '' LIMIT 1`

// UserIDByExternalID finds the account a website id belongs to; 0 when none.
func (s *Store) UserIDByExternalID(externalID string) (int64, error) {
	if externalID == "" {
		return 0, nil
	}
	var id int64
	err := s.db.QueryRow(userByExternalIDSQL, externalID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ErrExternalIDTaken is a website id another account already holds.
var ErrExternalIDTaken = errors.New("external id already belongs to another user")

// SetUserExternalID gives an account its website id.
func (s *Store) SetUserExternalID(userID int64, externalID string) error {
	res, err := s.db.Exec(`UPDATE users SET external_id = ? WHERE id = ?`, externalID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrExternalIDTaken
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UserExternalID reads an account's website id; "" when it has none.
func (s *Store) UserExternalID(userID int64) string {
	var id string
	_ = s.db.QueryRow(`SELECT external_id FROM users WHERE id = ?`, userID).Scan(&id)
	return id
}
