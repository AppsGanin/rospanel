package store

import "time"

// BackdateUserForTest moves a user's creation date. Test-only: the audience filters
// floor "never connected" by account age, and there is no other way to exercise the
// difference between a long-dormant account and one registered this morning.
func (s *Store) BackdateUserForTest(id int64, at time.Time) error {
	_, err := s.db.Exec(`UPDATE users SET created_at = ? WHERE id = ?`, at.Unix(), id)
	return err
}

// ExecForTest runs a statement against the store. Test-only: for states no API
// reaches directly (a subscriber's first /start a week ago, a user's last traffic).
func (s *Store) ExecForTest(q string, args ...any) error {
	_, err := s.db.Exec(q, args...)
	return err
}
