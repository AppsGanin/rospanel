package store

import (
	"database/sql"
	"encoding/json"
	"errors"

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
	AddUp   int64  // real bytes — booked to the per-node/day stats
	AddDown int64
	// QuotaUp/QuotaDown are the bytes charged to the USER'S quota, which is the real
	// bytes scaled by the node's traffic coefficient (see model.Node.TrafficCoefficient).
	// They are separate from AddUp/AddDown on purpose: the per-node statistics must stay
	// the true byte count for infrastructure monitoring, while a coefficient of 2.0
	// (expensive node) or 0.5 (promo node) only bends how fast the user's allowance
	// drains. Left zero by callers that don't scale, in which case the real bytes are
	// charged — see quotaBytes.
	QuotaUp   int64
	QuotaDown int64

	// Baseline is the raw counters to remember as the next poll's reference point.
	// Only the LOCAL poller has them: it reads Xray's cumulative counters and
	// subtracts, so it must record where it read. A remote node subtracts on its own
	// side and ships the delta already computed, so it leaves this nil — and
	// last_up/last_down, which track the master's own Xray, stay untouched. Writing
	// a node's numbers there would corrupt the next local poll's arithmetic.
	Baseline *TrafficBaseline
	SeenAt   int64 // stamp last_seen with this; 0 leaves it alone
}

// quotaBytes is the up/down charged to the user's quota: the scaled QuotaUp/QuotaDown
// when a caller set them, else the real bytes (an unscaled path, e.g. the local
// poller, or a test that predates the coefficient).
func (d TrafficDelta) quotaBytes() (up, down int64) {
	if d.QuotaUp != 0 || d.QuotaDown != 0 {
		return d.QuotaUp, d.QuotaDown
	}
	return d.AddUp, d.AddDown
}

// ApplyTrafficDeltas books a whole poll cycle's traffic in one transaction.
//
// This is the panel's hottest write path: it used to run three separate statements
// per active user, each its own implicit transaction, each paying its own fsync on
// a single-connection pool. One commit for the batch turns a per-user cost into a
// per-cycle one — on the reference box that is the difference between ~70 users/sec
// and the whole cycle landing in a few milliseconds. Inside that commit the batch is
// two statements, not three per user (see applyTrafficDeltasOn).
func (s *Store) ApplyTrafficDeltas(deltas []TrafficDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error { return applyTrafficDeltasOn(tx, deltas) })
}

// applyTrafficDeltasOn books a batch in two statements, whatever its size: one UPDATE
// of every user it touches and one upsert of every day's row, each reading its rows
// from a JSON array.
//
// It used to run up to three statements per user, and SQLite prepared each afresh:
// a 4,000-user node report took ~210ms at 20,000 users, over half of it parsing the
// same three statements again. Two statements over the whole batch take ~50ms.
//
// The deltas are folded per user first, which is what keeps the result identical to
// applying them one after another: quota adds up, the last baseline and the last
// non-zero sighting win, and a day's bytes add up per node. An UPDATE ... FROM that
// met one user twice would apply only one of the rows.
func applyTrafficDeltasOn(tx *sql.Tx, deltas []TrafficDelta) error {
	type userSum struct {
		up, down int64
		base     *TrafficBaseline
		seen     int64
	}
	type dayKey struct {
		user, node int64
		day        string
	}
	users := make(map[int64]*userSum, len(deltas))
	userOrder := make([]int64, 0, len(deltas))
	days := make(map[dayKey]*[2]int64, len(deltas))
	dayOrder := make([]dayKey, 0, len(deltas))
	for _, d := range deltas {
		// Quota is charged the scaled bytes; the per-node/day stats get the real ones.
		qUp, qDown := d.quotaBytes()
		u := users[d.UserID]
		if u == nil {
			u = &userSum{}
			users[d.UserID] = u
			userOrder = append(userOrder, d.UserID)
		}
		u.up += qUp
		u.down += qDown
		if d.Baseline != nil {
			b := *d.Baseline
			u.base = &b
		}
		if d.SeenAt > 0 {
			u.seen = d.SeenAt
		}
		if d.AddUp != 0 || d.AddDown != 0 {
			k := dayKey{d.UserID, d.NodeID, d.Day}
			sum := days[k]
			if sum == nil {
				sum = &[2]int64{}
				days[k] = sum
				dayOrder = append(dayOrder, k)
			}
			sum[0] += d.AddUp
			sum[1] += d.AddDown
		}
	}

	userRows := make([][7]int64, 0, len(userOrder))
	for _, id := range userOrder {
		u := users[id]
		// Nothing to charge, no baseline and no sighting: the row would be rewritten
		// unchanged.
		if u.up == 0 && u.down == 0 && u.base == nil && u.seen == 0 {
			continue
		}
		row := [7]int64{id, u.up, u.down, 0, 0, 0, u.seen}
		if u.base != nil {
			row[3], row[4], row[5] = 1, u.base.Up, u.base.Down
		}
		userRows = append(userRows, row)
	}
	if len(userRows) > 0 {
		raw, err := json.Marshal(userRows)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE users SET
			    used_up = used_up + v.up,
			    used_down = used_down + v.down,
			    last_up = CASE WHEN v.base = 1 THEN v.base_up ELSE last_up END,
			    last_down = CASE WHEN v.base = 1 THEN v.base_down ELSE last_down END,
			    last_seen = CASE WHEN v.seen > 0 THEN v.seen ELSE last_seen END
			FROM (SELECT j.value->>0 AS id, j.value->>1 AS up, j.value->>2 AS down,
			             j.value->>3 AS base, j.value->>4 AS base_up, j.value->>5 AS base_down,
			             j.value->>6 AS seen
			      FROM json_each(?) AS j) AS v
			WHERE users.id = v.id`, string(raw)); err != nil {
			return err
		}
	}

	if len(dayOrder) == 0 {
		return nil
	}
	dayRows := make([][5]any, 0, len(dayOrder))
	for _, k := range dayOrder {
		sum := days[k]
		dayRows = append(dayRows, [5]any{k.user, k.node, k.day, sum[0], sum[1]})
	}
	raw, err := json.Marshal(dayRows)
	if err != nil {
		return err
	}
	// The EXISTS guard, as in addDailyTrafficOn: a departed user costs their own row,
	// never the batch.
	_, err = tx.Exec(`
		INSERT INTO traffic_daily (user_id, node_id, day, up, down)
		SELECT j.value->>0, j.value->>1, j.value->>2, j.value->>3, j.value->>4
		FROM json_each(?) AS j
		WHERE EXISTS (SELECT 1 FROM users WHERE id = j.value->>0)
		ON CONFLICT(user_id, node_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
		string(raw))
	return err
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

// NodeDailyRow is one (day, node) bucket of the traffic table.
type NodeDailyRow struct {
	Day    string
	NodeID int64
	Up     int64
	Down   int64
}

// NodeTrafficSeries returns per-day totals split by node between from and to.
// userID 0 aggregates across users, matching StatsSeries.
func (s *Store) NodeTrafficSeries(userID int64, from, to string) ([]NodeDailyRow, error) {
	query := `SELECT day, node_id, SUM(up), SUM(down) FROM traffic_daily WHERE day BETWEEN ? AND ?`
	args := []any{from, to}
	if userID > 0 {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` GROUP BY day, node_id ORDER BY day, node_id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeDailyRow
	for rows.Next() {
		var r NodeDailyRow
		if err := rows.Scan(&r.Day, &r.NodeID, &r.Up, &r.Down); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NodeTrafficTotals returns each node's total up+down over the period, keyed by
// node_id (0 = local server). Used by the Nodes UI.
// userID 0 aggregates across all users, matching StatsSeries.
func (s *Store) NodeTrafficTotals(userID int64, from, to string) (map[int64][2]int64, error) {
	query := `SELECT node_id, SUM(up), SUM(down) FROM traffic_daily WHERE day BETWEEN ? AND ?`
	args := []any{from, to}
	if userID > 0 {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` GROUP BY node_id`
	rows, err := s.db.Query(query, args...)
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

// TermStart is a held term that a connection just started.
type TermStart struct {
	UserID      int64
	HoldSeconds int64 // the term that started
	ExpireAt    int64 // its end, as now stored
}

// AddConnections records a batch of sightings in one transaction.
func (s *Store) AddConnections(hits []ConnectionHit) error {
	_, err := s.RecordConnections(hits)
	return err
}

// RecordConnections is AddConnections that also reports the held terms the batch
// started: a user on hold who appears in it is connected, so their term begins at
// their sighting, in the same commit as the sighting itself.
//
// The access-log tap is the panel's highest-frequency write source — it fires per
// user per source IP — and it used to do two separate statements per sighting.
// Folding a few seconds' worth into one commit is what stops the write rate from
// scaling with the number of connected devices.
func (s *Store) RecordConnections(hits []ConnectionHit) ([]TermStart, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	var started []TermStart
	err := s.withTx(func(tx *sql.Tx) error {
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
		var err error
		started, err = startHeldTermsOn(tx, seen)
		return err
	})
	if err != nil {
		return nil, err
	}
	return started, nil
}

// startHeldTermsOn starts the term of every user in seen who is still on hold, from
// their sighting. The users on hold are read first — through their own partial index,
// so the cost follows how many are waiting, not how many connected — and only those
// who also connected are written.
//
// expire_at = 0 in the guard is part of the rule, not decoration: a user given a date
// by hand or by a plan while a stale hold survived must never have that date
// overwritten by the next connection.
func startHeldTermsOn(tx *sql.Tx, seen map[int64]int64) ([]TermStart, error) {
	rows, err := tx.Query(`SELECT id FROM users INDEXED BY idx_users_held WHERE hold_seconds > 0`)
	if err != nil {
		return nil, err
	}
	var due []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := seen[id]; ok {
			due = append(due, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []TermStart
	for _, id := range due {
		var t TermStart
		err := tx.QueryRow(`
			UPDATE users SET expire_at = ? + hold_seconds, hold_seconds = 0
			WHERE id = ? AND hold_seconds > 0 AND expire_at = 0
			RETURNING id, expire_at`, seen[id], id).Scan(&t.UserID, &t.ExpireAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		t.HoldSeconds = t.ExpireAt - seen[id]
		out = append(out, t)
	}
	return out, nil
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
	// Pinned to the window index: connections keys on (user_id, ip) and keeps a row per
	// address for ConnectionRetentionDays, so left to itself SQLite reads the whole
	// thirty-day table to answer a question about the last two minutes.
	rows, err := s.db.Query(
		`SELECT user_id, COUNT(DISTINCT ip)
		 FROM connections INDEXED BY idx_connections_last_seen
		 WHERE last_seen > ? GROUP BY user_id`, since)
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

// ConnectionIPStat is one source IP's activity across all users, for the geo
// breakdown (the country is resolved from the IP in the manager, not stored).
type ConnectionIPStat struct {
	IP   string
	Hits int64 // summed sighting count
}

// ConnectionIPStats aggregates the connections table by source IP for rows seen since
// the cutoff (unix seconds). One row per distinct IP.
func (s *Store) ConnectionIPStats(since int64) ([]ConnectionIPStat, error) {
	rows, err := s.db.Query(
		`SELECT ip, SUM(count) FROM connections
		 WHERE last_seen >= ? GROUP BY ip`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectionIPStat
	for rows.Next() {
		var st ConnectionIPStat
		if err := rows.Scan(&st.IP, &st.Hits); err != nil {
			return nil, err
		}
		out = append(out, st)
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
