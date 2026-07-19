package store

import (
	"database/sql"

	"github.com/AppsGanin/rospanel/internal/model"
)

// AddDailyTraffic adds up/down deltas to a user's row on the local server (node 0)
// for the given day.
func (s *Store) AddDailyTraffic(userID int64, day string, up, down int64) error {
	return s.AddDailyTrafficNode(userID, model.LocalNodeID, day, up, down)
}

// AddDailyTrafficNode adds up/down deltas attributed to a specific node.
func (s *Store) AddDailyTrafficNode(userID, nodeID int64, day string, up, down int64) error {
	return addDailyTrafficOn(s.db, userID, nodeID, day, up, down)
}

// addDailyTrafficOn books a day's traffic, skipping users that no longer exist.
//
// The EXISTS guard is load-bearing, not defensive noise. traffic_daily.user_id is a
// foreign key, so a plain INSERT for a deleted user raises a constraint error — and
// since these statements now run inside a batch transaction, that one bad row would
// roll back the whole batch, including the node's ingest watermark. Reporters
// legitimately carry stale users: a node's unacked batch can name someone the
// auto-delete sweep removed in the meantime. One departed user must cost their own
// row, never everyone else's.
func addDailyTrafficOn(ex execer, userID, nodeID int64, day string, up, down int64) error {
	if up == 0 && down == 0 {
		return nil
	}
	_, err := ex.Exec(`
		INSERT INTO traffic_daily (user_id, node_id, day, up, down)
		SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
		ON CONFLICT(user_id, node_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
		userID, nodeID, day, up, down, userID,
	)
	return err
}

// TrafficBaseline is the pair of raw Xray counters a delta was measured against.
type TrafficBaseline struct{ Up, Down int64 }

// TrafficDelta is one user's accounted traffic from a single reporting cycle.
type TrafficDelta struct {
	UserID  int64
	NodeID  int64
	Day     string // operator-local calendar day the traffic is booked against
	AddUp   int64  // delta to add to the lifetime totals
	AddDown int64
	// Baseline is the raw counters to remember as the next poll's reference point.
	// Only the LOCAL poller has them: it reads Xray's cumulative counters and
	// subtracts, so it must record where it read. A remote node subtracts on its own
	// side and ships the delta already computed, so it leaves this nil — and
	// last_up/last_down, which track the master's own Xray, stay untouched. Writing
	// a node's numbers there would corrupt the next local poll's arithmetic.
	Baseline *TrafficBaseline
	SeenAt   int64 // stamp last_seen with this; 0 leaves it alone
}

// ApplyTrafficDeltas books a whole poll cycle's traffic in one transaction.
//
// This is the panel's hottest write path: it used to run three separate statements
// per active user, each its own implicit transaction, each paying its own fsync on
// a single-connection pool. One commit for the batch turns a per-user cost into a
// per-cycle one — on the reference box that is the difference between ~70 users/sec
// and the whole cycle landing in a few milliseconds.
func (s *Store) ApplyTrafficDeltas(deltas []TrafficDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error { return applyTrafficDeltasOn(tx, deltas) })
}

func applyTrafficDeltasOn(ex execer, deltas []TrafficDelta) error {
	for _, d := range deltas {
		var err error
		if d.Baseline != nil {
			err = updateTrafficOn(ex, d.UserID, d.AddUp, d.AddDown, d.Baseline.Up, d.Baseline.Down)
		} else {
			err = addUsedTrafficOn(ex, d.UserID, d.AddUp, d.AddDown)
		}
		if err != nil {
			return err
		}
		if err := addDailyTrafficOn(ex, d.UserID, d.NodeID, d.Day, d.AddUp, d.AddDown); err != nil {
			return err
		}
		if d.SeenAt > 0 {
			if err := touchLastSeenOn(ex, d.UserID, d.SeenAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddUsedTraffic bumps a user's lifetime totals WITHOUT touching last_up/last_down
// (the raw Xray counter baseline for the local poller). Remote-node traffic ingest
// uses this: the node already computed the delta, so the panel just accumulates.
func (s *Store) AddUsedTraffic(userID, up, down int64) error {
	return addUsedTrafficOn(s.db, userID, up, down)
}

func addUsedTrafficOn(ex execer, userID, up, down int64) error {
	if up == 0 && down == 0 {
		return nil
	}
	_, err := ex.Exec(
		`UPDATE users SET used_up = used_up + ?, used_down = used_down + ? WHERE id = ?`,
		up, down, userID,
	)
	return err
}

// StatsSeriesNode returns per-day totals for a single node (nodeID, including 0
// for the local server) between from and to. userID 0 aggregates across users.
func (s *Store) StatsSeriesNode(userID, nodeID int64, from, to string) ([]model.DailyPoint, error) {
	query := `SELECT day, SUM(up), SUM(down) FROM traffic_daily WHERE node_id = ? AND day BETWEEN ? AND ?`
	args := []any{nodeID, from, to}
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

// NodeTrafficTotals returns each node's total up+down over the period, keyed by
// node_id (0 = local server). Used by the Nodes UI.
func (s *Store) NodeTrafficTotals(from, to string) (map[int64][2]int64, error) {
	rows, err := s.db.Query(
		`SELECT node_id, SUM(up), SUM(down) FROM traffic_daily WHERE day BETWEEN ? AND ? GROUP BY node_id`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][2]int64)
	for rows.Next() {
		var nodeID, up, down int64
		if err := rows.Scan(&nodeID, &up, &down); err != nil {
			return nil, err
		}
		out[nodeID] = [2]int64{up, down}
	}
	return out, rows.Err()
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
	return setResetPeriodOn(s.db, id, period, now)
}

func setResetPeriodOn(ex execer, id int64, period string, now int64) error {
	_, err := ex.Exec(
		`UPDATE users SET reset_period = ?, last_reset_at = ? WHERE id = ?`,
		period, now, id,
	)
	return err
}

// AddConnection records activity from a source IP for a user (upserting the
// per-IP row) and bumps the user's last_seen.
func (s *Store) AddConnection(userID int64, ip string, ts int64) error {
	return s.AddConnections([]ConnectionHit{{UserID: userID, IP: ip, SeenAt: ts, Hits: 1}})
}

// ConnectionHit is one user+IP sighting, with however many times it was seen
// folded into Hits.
type ConnectionHit struct {
	UserID int64
	IP     string
	SeenAt int64
	Hits   int64
}

// AddConnections records a batch of sightings in one transaction.
//
// The access-log tap is the panel's highest-frequency write source — it fires per
// user per source IP — and it used to do two separate statements per sighting.
// Folding a few seconds' worth into one commit is what stops the write rate from
// scaling with the number of connected devices.
func (s *Store) AddConnections(hits []ConnectionHit) error {
	if len(hits) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		seen := make(map[int64]int64, len(hits)) // user → newest sighting in the batch
		for _, h := range hits {
			if h.Hits <= 0 {
				h.Hits = 1
			}
			// EXISTS guard for the same reason addDailyTrafficOn has one: connections
			// .user_id is a foreign key, and RecordAccess reads user ids straight out of
			// the Xray access log — a deleted user with a still-live session keeps being
			// named. Without this, that one ghost would void everyone else's sightings
			// in the batch, every flush, until Xray reloads.
			if _, err := tx.Exec(`
				INSERT INTO connections (user_id, ip, last_seen, count)
				SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
				ON CONFLICT(user_id, ip) DO UPDATE SET
				    last_seen = MAX(last_seen, excluded.last_seen),
				    count = count + excluded.count`,
				h.UserID, h.IP, h.SeenAt, h.Hits, h.UserID,
			); err != nil {
				return err
			}
			if h.SeenAt > seen[h.UserID] {
				seen[h.UserID] = h.SeenAt
			}
		}
		// One last_seen write per user, not per sighting: a user on four devices
		// would otherwise stamp the same column four times in the same commit.
		for userID, ts := range seen {
			if err := touchLastSeenOn(tx, userID, ts); err != nil {
				return err
			}
		}
		return nil
	})
}

// TouchLastSeen updates a user's last activity time (used by the poller too).
func (s *Store) TouchLastSeen(userID, ts int64) error {
	return touchLastSeenOn(s.db, userID, ts)
}

func touchLastSeenOn(ex execer, userID, ts int64) error {
	_, err := ex.Exec(`UPDATE users SET last_seen = ? WHERE id = ?`, ts, userID)
	return err
}

// ActiveDeviceCounts returns how many distinct source IPs were seen per user
// since the given unix timestamp (typically now - DeviceOnlineWindow).
// INDEXED BY is deliberate. Left alone, SQLite picks the (user_id, ip) primary key
// so GROUP BY needs no sort, and scans the whole table — which grows a row per
// source IP per user. `since` is only DeviceOnlineWindow (120s) back, so the rows we
// want are a tiny slice of that: seeking them on last_seen and sorting the slice
// beats scanning everything, and the planner's row estimates (we never ANALYZE)
// don't know it. The clause also fails loudly if a migration ever drops the index.
func (s *Store) ActiveDeviceCounts(since int64) (map[int64]int, error) {
	rows, err := s.db.Query(
		`SELECT user_id, COUNT(DISTINCT ip) FROM connections INDEXED BY idx_connections_last_seen
		 WHERE last_seen > ? GROUP BY user_id`,
		since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// PurgeConnections drops connection rows not seen since the cutoff (unix seconds),
// returning how many were removed. Batched for the same reason PurgeUserEvents is:
// the pool is a single connection, so one unbounded DELETE would stall every query
// behind it. connections has no surrogate key, so this sweeps by rowid.
func (s *Store) PurgeConnections(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM connections WHERE rowid IN (
				SELECT rowid FROM connections WHERE last_seen < ? LIMIT ?
			)`, before, purgeBatch)
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

// PurgeTrafficDaily drops per-day traffic rows older than beforeDay (exclusive,
// 'YYYY-MM-DD'), returning how many were removed. The cutoff is a calendar day and
// not a timestamp because that is what the rows are keyed on — see AddDailyTraffic,
// which writes the operator's local day. Batched for the same reason the other
// sweeps are: one unbounded DELETE would hold the single connection for the whole
// statement and stall every request behind it.
func (s *Store) PurgeTrafficDaily(beforeDay string) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM traffic_daily WHERE rowid IN (
				SELECT rowid FROM traffic_daily WHERE day < ? LIMIT ?
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
