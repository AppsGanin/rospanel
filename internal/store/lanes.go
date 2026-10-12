package store

import (
	"database/sql"
)

// LaneDelta is what one lane of one server carried on one day since the last report.
type LaneDelta struct {
	NodeID int64
	Lane   string
	Day    string
	Up     int64
	Down   int64
}

// ApplyLaneTraffic books lane deltas (the panel's own server: its reads are already
// deltas, so there is no report to claim).
func (s *Store) ApplyLaneTraffic(deltas []LaneDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error { return applyLaneDeltasOn(tx, deltas) })
}

func applyLaneDeltasOn(tx *sql.Tx, deltas []LaneDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO lane_traffic_daily (node_id, lane, day, up, down)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (node_id, lane, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range deltas {
		if d.Lane == "" || (d.Up <= 0 && d.Down <= 0) {
			continue
		}
		if _, err := stmt.Exec(d.NodeID, d.Lane, d.Day, max(d.Up, 0), max(d.Down, 0)); err != nil {
			return err
		}
	}
	return nil
}

// LaneTotal is one lane of one server over a period.
type LaneTotal struct {
	NodeID int64
	Lane   string
	Up     int64
	Down   int64
}

// LaneTrafficTotals sums each server's lanes between from and to (inclusive).
func (s *Store) LaneTrafficTotals(from, to string) ([]LaneTotal, error) {
	rows, err := s.rdb.Query(`SELECT node_id, lane, SUM(up), SUM(down) FROM lane_traffic_daily
		WHERE day BETWEEN ? AND ? GROUP BY node_id, lane`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LaneTotal
	for rows.Next() {
		var t LaneTotal
		if err := rows.Scan(&t.NodeID, &t.Lane, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
