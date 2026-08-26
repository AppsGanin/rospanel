package store

import (
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// Uptime history for the public status page. See migration 0043 for the shape and
// why it is a daily rollup rather than an event log.

// RecordUptimeSample folds one liveness sample for a server into its day. Called
// on the node watch tick, so a day holds roughly one sample per minute.
func (s *Store) RecordUptimeSample(nodeID int64, day string, up bool) error {
	n := 0
	if up {
		n = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO uptime_daily (node_id, day, up, total) VALUES (?, ?, ?, 1)
		ON CONFLICT(node_id, day) DO UPDATE SET
		    up = up + excluded.up,
		    total = total + 1`,
		nodeID, day, n,
	)
	return err
}

// UptimeSince returns every sampled day from `from` (inclusive, 'YYYY-MM-DD')
// onwards, oldest first per server.
func (s *Store) UptimeSince(from string) ([]model.UptimeDay, error) {
	rows, err := s.db.Query(`
		SELECT node_id, day, up, total FROM uptime_daily
		WHERE day >= ? ORDER BY node_id, day`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.UptimeDay
	for rows.Next() {
		var d model.UptimeDay
		if err := rows.Scan(&d.NodeID, &d.Day, &d.Up, &d.Total); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PurgeUptime drops history older than beforeDay (exclusive, 'YYYY-MM-DD'),
// returning how many rows went. Bounded by servers × retention days, so unlike the
// other sweeps this one can afford a single statement.
func (s *Store) PurgeUptime(beforeDay string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM uptime_daily WHERE day < ?`, beforeDay)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
