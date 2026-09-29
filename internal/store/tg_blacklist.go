package store

import "database/sql"

// ReplaceBlacklist swaps the whole shared blacklist for a freshly fetched one, in one
// transaction: a reader sees the old list or the new one, never half of each.
func (s *Store) ReplaceBlacklist(entries map[int64]string, at int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM tg_blacklist`); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO tg_blacklist (tg_id, reason) VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for id, reason := range entries {
			if _, err := stmt.Exec(id, reason); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE settings SET blacklist_synced_at = ?, blacklist_error = '' WHERE id = 1`, at)
		return err
	})
}

// SetBlacklistError records a failed fetch; the list already stored stays in force.
func (s *Store) SetBlacklistError(msg string) error {
	_, err := s.db.Exec(`UPDATE settings SET blacklist_error = ? WHERE id = 1`, msg)
	return err
}

// SetBlacklistSettings stores whether the list is enforced and where it comes from.
func (s *Store) SetBlacklistSettings(enabled bool, url string) error {
	_, err := s.db.Exec(`UPDATE settings SET blacklist_enabled = ?, blacklist_url = ?, updated_at = unixepoch()
		WHERE id = 1`, boolToInt(enabled), url)
	return err
}

// BlacklistReason reports whether a Telegram account is on the shared blacklist, and
// why ("" reason for a listed account that came without one).
func (s *Store) BlacklistReason(tgID int64) (string, bool) {
	if tgID == 0 {
		return "", false
	}
	var reason string
	err := s.rdb.QueryRow(`SELECT reason FROM tg_blacklist WHERE tg_id = ?`, tgID).Scan(&reason)
	return reason, err == nil
}

// BlacklistCount is how many accounts the stored list holds.
func (s *Store) BlacklistCount() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM tg_blacklist`).Scan(&n)
	return n
}
