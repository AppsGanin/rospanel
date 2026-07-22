package store

import "database/sql"

// AbuseHit is one pre-aggregated blocklist match, as the flush hands it over.
type AbuseHit struct {
	UserID   int64
	NodeID   int64
	Domain   string
	Category string
	Day      string
	Count    int64
	SeenAt   int64
}

// AbuseMatch is a stored match, for the panel's views.
type AbuseMatch struct {
	UserID   int64  `json:"user_id"`
	UserName string `json:"user_name,omitempty"`
	NodeID   int64  `json:"node_id"`
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Day      string `json:"day"`
	Count    int64  `json:"count"`
	LastSeen int64  `json:"last_seen"`
}

// AddAbuseMatches folds a batch of matches into the daily rollup, in one
// transaction.
//
// The EXISTS guard is there for the same reason AddConnections has one: user ids
// come out of the Xray access log, so a deleted user with a still-live session
// keeps being named, and without the guard that one ghost would void everyone
// else's matches in the batch on every flush.
func (s *Store) AddAbuseMatches(hits []AbuseHit) error {
	if len(hits) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		for _, h := range hits {
			if h.Count <= 0 {
				h.Count = 1
			}
			// category is deliberately NOT in the conflict target. One address resolves to
			// one category for a given config (the custom list is checked first), so a row
			// only sees two categories if the operator edits their list mid-day — then it
			// keeps the first category written and sums the counts. Best-effort by design;
			// the address and the fact of a match are what the operator acts on.
			if _, err := tx.Exec(`
				INSERT INTO abuse_matches (user_id, node_id, domain, category, day, count, last_seen)
				SELECT ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
				ON CONFLICT(user_id, node_id, domain, day) DO UPDATE SET
				    count = count + excluded.count,
				    last_seen = MAX(last_seen, excluded.last_seen)`,
				h.UserID, h.NodeID, h.Domain, h.Category, h.Day, h.Count, h.SeenAt, h.UserID,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// AbuseByUser returns a user's matches, most recent first.
func (s *Store) AbuseByUser(userID int64, limit int) ([]AbuseMatch, error) {
	return s.queryAbuse(`
		SELECT user_id, '', node_id, domain, category, day, count, last_seen
		FROM abuse_matches WHERE user_id = ?
		ORDER BY last_seen DESC LIMIT ?`, userID, limit)
}

// AbuseRecent returns the fleet's matches, most recent first, with user names so
// the operator can act without a second lookup per row.
func (s *Store) AbuseRecent(limit int) ([]AbuseMatch, error) {
	return s.queryAbuse(`
		SELECT a.user_id, COALESCE(u.name, ''), a.node_id, a.domain, a.category,
		       a.day, a.count, a.last_seen
		FROM abuse_matches a LEFT JOIN users u ON u.id = a.user_id
		ORDER BY a.last_seen DESC LIMIT ?`, limit)
}

// AbuseUserCountsForDay returns each user's match count on one day, keyed by user
// id.
//
// Scoped to a day, not the whole retention window, because the alert it feeds fires
// once per user per day: a window-wide sum would re-alert today about matches that
// happened last week, which is the opposite of "this account is a problem right
// now".
func (s *Store) AbuseUserCountsForDay(day string) (map[int64]int64, error) {
	rows, err := s.db.Query(
		`SELECT user_id, SUM(count) FROM abuse_matches WHERE day = ? GROUP BY user_id`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Store) queryAbuse(q string, args ...any) ([]AbuseMatch, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AbuseMatch
	for rows.Next() {
		var m AbuseMatch
		if err := rows.Scan(&m.UserID, &m.UserName, &m.NodeID, &m.Domain,
			&m.Category, &m.Day, &m.Count, &m.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PurgeAbuseMatches drops rows older than the cutoff day ('YYYY-MM-DD'), returning
// how many went.
//
// Batched by rowid for the same reason every other sweep here is: the pool is a
// single connection, so one unbounded DELETE would hold it for the whole statement
// and stall every request behind it.
func (s *Store) PurgeAbuseMatches(beforeDay string) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(`
			DELETE FROM abuse_matches WHERE rowid IN (
				SELECT rowid FROM abuse_matches WHERE day < ? LIMIT ?
			)`, beforeDay, purgeBatch)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeBatch {
			return total, nil
		}
	}
}
