package store

import (
	"database/sql"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Load history per server: five-minute rows plus an outage log. See migration 0146.

// RecordLoadTick stores one watch tick for every server sampled at `now`, in one
// transaction: each sample is folded into its five-minute row, an outage opens for a
// server found down and closes for one found up. A server missing from samples
// (switched off, deleted) is no longer watched, so whatever outage it had is over.
func (s *Store) RecordLoadTick(now int64, samples []model.LoadSample) error {
	bucket := now - now%model.LoadBucket
	return s.withTx(func(tx *sql.Tx) error {
		watched := make([]any, 0, len(samples))
		for _, sm := range samples {
			watched = append(watched, sm.NodeID)
			up, stats := 0, 0
			cpu, mem, disk, rx, txr := 0.0, 0.0, 0.0, 0.0, 0.0
			if sm.Up {
				up = 1
			}
			if sm.HasStats {
				stats, cpu, mem, disk, rx, txr = 1, sm.CPU, sm.Mem, sm.Disk, sm.RX, sm.TX
			}
			if _, err := tx.Exec(`
				INSERT INTO server_load (node_id, ts, cpu_sum, cpu_max, mem_sum, mem_max, stats, up, total,
				    ext, ext_stats, disk_sum, disk_max, rx_sum, rx_max, tx_sum, tx_max, online_sum, online_max)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(node_id, ts) DO UPDATE SET
				    cpu_sum    = cpu_sum + excluded.cpu_sum,
				    cpu_max    = MAX(cpu_max, excluded.cpu_max),
				    mem_sum    = mem_sum + excluded.mem_sum,
				    mem_max    = MAX(mem_max, excluded.mem_max),
				    stats      = stats + excluded.stats,
				    up         = up + excluded.up,
				    total      = total + 1,
				    ext        = ext + 1,
				    ext_stats  = ext_stats + excluded.ext_stats,
				    disk_sum   = disk_sum + excluded.disk_sum,
				    disk_max   = MAX(disk_max, excluded.disk_max),
				    rx_sum     = rx_sum + excluded.rx_sum,
				    rx_max     = MAX(rx_max, excluded.rx_max),
				    tx_sum     = tx_sum + excluded.tx_sum,
				    tx_max     = MAX(tx_max, excluded.tx_max),
				    online_sum = online_sum + excluded.online_sum,
				    online_max = MAX(online_max, excluded.online_max)`,
				sm.NodeID, bucket, cpu, cpu, mem, mem, stats, up,
				stats, disk, disk, rx, rx, txr, txr, sm.Online, sm.Online,
			); err != nil {
				return err
			}
			if sm.Up {
				if _, err := tx.Exec(`UPDATE server_outages SET ended_at = ? WHERE node_id = ? AND ended_at = 0`, now, sm.NodeID); err != nil {
					return err
				}
				continue
			}
			var id int64
			err := tx.QueryRow(`SELECT id FROM server_outages WHERE node_id = ? AND ended_at = 0`, sm.NodeID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				_, err = tx.Exec(`INSERT INTO server_outages (node_id, started_at) VALUES (?, ?)`, sm.NodeID, now)
			}
			if err != nil {
				return err
			}
		}
		q := `UPDATE server_outages SET ended_at = ? WHERE ended_at = 0`
		args := []any{now}
		if len(watched) > 0 {
			q += ` AND node_id NOT IN (` + placeholders(len(watched)) + `)`
			args = append(args, watched...)
		}
		_, err := tx.Exec(q, args...)
		return err
	})
}

// CloseStaleOutages ends the outages still open from before a panel restart that
// left a gap longer than `gap` seconds: nobody watched the servers meanwhile, so an
// outage ends where its server's last row does. A short gap (an update) keeps it
// open, and the next tick decides. Called once, before the first tick after boot.
func (s *Store) CloseStaleOutages(now, gap int64) error {
	_, err := s.db.Exec(`
		UPDATE server_outages SET ended_at = MAX(started_at, COALESCE(
		    (SELECT MAX(ts) + ? FROM server_load l WHERE l.node_id = server_outages.node_id),
		    started_at))
		WHERE ended_at = 0 AND ? - COALESCE(
		    (SELECT MAX(ts) + ? FROM server_load l WHERE l.node_id = server_outages.node_id),
		    started_at) > ?`,
		model.LoadBucket, now, model.LoadBucket, gap)
	return err
}

// LoadSeries folds a server's rows from `from` on into points `step` seconds wide
// (a multiple of model.LoadBucket), oldest first. Steps with no rows are absent.
func (s *Store) LoadSeries(nodeID, from, step int64) ([]model.LoadPoint, error) {
	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS b, SUM(cpu_sum), MAX(cpu_max), SUM(mem_sum), MAX(mem_max),
		       SUM(stats), SUM(up), SUM(total),
		       SUM(ext), SUM(ext_stats), SUM(disk_sum), MAX(disk_max), SUM(rx_sum), MAX(rx_max),
		       SUM(tx_sum), MAX(tx_max), SUM(online_sum), MAX(online_max)
		FROM server_load WHERE node_id = ? AND ts >= ?
		GROUP BY b ORDER BY b`, step, step, nodeID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.LoadPoint
	for rows.Next() {
		var p model.LoadPoint
		var cpuSum, memSum, diskSum float64
		var onlineSum int
		if err := rows.Scan(&p.TS, &cpuSum, &p.CPUMax, &memSum, &p.MemMax, &p.Stats, &p.Up, &p.Total,
			&p.Ext, &p.ExtStats, &diskSum, &p.DiskMax, &p.RXSum, &p.RXMax, &p.TXSum, &p.TXMax,
			&onlineSum, &p.OnlineMax); err != nil {
			return nil, err
		}
		if p.Stats > 0 {
			p.CPU = cpuSum / float64(p.Stats)
			p.Mem = memSum / float64(p.Stats)
		}
		if p.ExtStats > 0 {
			p.Disk = diskSum / float64(p.ExtStats)
			p.RX = p.RXSum / float64(p.ExtStats)
			p.TX = p.TXSum / float64(p.ExtStats)
		}
		if p.Ext > 0 {
			p.Online = float64(onlineSum) / float64(p.Ext)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OutagesSince lists a server's outages that were still going at `from` or started
// after it, oldest first.
func (s *Store) OutagesSince(nodeID, from int64) ([]model.Outage, error) {
	rows, err := s.db.Query(`
		SELECT id, node_id, started_at, ended_at FROM server_outages
		WHERE node_id = ? AND (ended_at = 0 OR ended_at >= ?)
		ORDER BY started_at`, nodeID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Outage
	for rows.Next() {
		var o model.Outage
		if err := rows.Scan(&o.ID, &o.NodeID, &o.StartedAt, &o.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// PurgeLoad drops load rows older than loadBefore and finished outages that ended
// before outageBefore (unix seconds).
func (s *Store) PurgeLoad(loadBefore, outageBefore int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM server_load WHERE ts < ?`, loadBefore); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM server_outages WHERE ended_at > 0 AND ended_at < ?`, outageBefore)
		return err
	})
}
