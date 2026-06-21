package store

import "github.com/AppsGanin/rospanel/internal/model"

// AddDailyTraffic adds up/down deltas to a user's row for the given UTC day.
func (s *Store) AddDailyTraffic(userID int64, day string, up, down int64) error {
	if up == 0 && down == 0 {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO traffic_daily (user_id, day, up, down) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
		userID, day, up, down,
	)
	return err
}

// StatsSeries returns per-day totals between from and to (inclusive, YYYY-MM-DD).
// userID == 0 aggregates across all users.
func (s *Store) StatsSeries(userID int64, from, to string) ([]model.DailyPoint, error) {
	query := `SELECT day, SUM(up), SUM(down) FROM traffic_daily WHERE day BETWEEN ? AND ?`
	args := []any{from, to}
	if userID > 0 {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` GROUP BY day ORDER BY day`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DailyPoint
	for rows.Next() {
		var p model.DailyPoint
		if err := rows.Scan(&p.Day, &p.Up, &p.Down); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StatsByUser returns each user's traffic total over the period (users with no
// traffic appear with zeros), busiest first.
func (s *Store) StatsByUser(from, to string) ([]model.UserTotal, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.name,
		       COALESCE(SUM(td.up), 0), COALESCE(SUM(td.down), 0)
		FROM users u
		LEFT JOIN traffic_daily td ON td.user_id = u.id AND td.day BETWEEN ? AND ?
		GROUP BY u.id, u.name
		ORDER BY (COALESCE(SUM(td.up),0) + COALESCE(SUM(td.down),0)) DESC, u.id`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.UserTotal
	for rows.Next() {
		var t model.UserTotal
		if err := rows.Scan(&t.UserID, &t.Name, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ResetDailyStats clears the entire per-day traffic history.
func (s *Store) ResetDailyStats() error {
	_, err := s.db.Exec(`DELETE FROM traffic_daily`)
	return err
}

// SetResetPeriod sets a user's automatic quota-reset period and anchors the
// cycle at now.
func (s *Store) SetResetPeriod(id int64, period string, now int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET reset_period = ?, last_reset_at = ? WHERE id = ?`,
		period, now, id,
	)
	return err
}

// AddConnection records activity from a source IP for a user (upserting the
// per-IP row) and bumps the user's last_seen.
func (s *Store) AddConnection(userID int64, ip string, ts int64) error {
	if _, err := s.db.Exec(`
		INSERT INTO connections (user_id, ip, last_seen, count) VALUES (?, ?, ?, 1)
		ON CONFLICT(user_id, ip) DO UPDATE SET last_seen = excluded.last_seen, count = count + 1`,
		userID, ip, ts,
	); err != nil {
		return err
	}
	return s.TouchLastSeen(userID, ts)
}

// TouchLastSeen updates a user's last activity time (used by the poller too).
func (s *Store) TouchLastSeen(userID, ts int64) error {
	_, err := s.db.Exec(`UPDATE users SET last_seen = ? WHERE id = ?`, ts, userID)
	return err
}

// RecentConnections returns a user's source IPs, most recent first.
func (s *Store) RecentConnections(userID int64, limit int) ([]model.Connection, error) {
	rows, err := s.db.Query(`
		SELECT ip, last_seen, count FROM connections
		WHERE user_id = ? ORDER BY last_seen DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Connection
	for rows.Next() {
		var c model.Connection
		if err := rows.Scan(&c.IP, &c.LastSeen, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResetUserQuota zeroes a user's usage and records the reset time (automatic
// reset scheduler). It re-baselines the raw counters to the supplied live Xray
// values so the next stats poll measures the delta from now — passing 0/0 would
// make the poll re-add the whole lifetime total back. enabled/expiry untouched;
// status is derived on read.
func (s *Store) ResetUserQuota(id, now, lastUp, lastDown int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET used_up = 0, used_down = 0, last_up = ?, last_down = ?,
		 last_reset_at = ? WHERE id = ?`,
		lastUp, lastDown, now, id,
	)
	return err
}
