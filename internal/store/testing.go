package store

import "time"

// BackdateUserForTest moves a user's creation date. Test-only: the audience filters
// floor "never connected" by account age, and there is no other way to exercise the
// difference between a long-dormant account and one registered this morning.
func (s *Store) BackdateUserForTest(id int64, at time.Time) error {
	_, err := s.db.Exec(`UPDATE users SET created_at = ? WHERE id = ?`, at.Unix(), id)
	return err
}
