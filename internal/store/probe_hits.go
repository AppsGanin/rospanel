package store

import (
	"database/sql"

	"github.com/AppsGanin/rospanel/internal/model"
)

// maxProbeHits caps how many scanning IPs are kept. A public IP is scanned by
// internet background bots constantly, so this is deduped per IP and hard-capped:
// the newest scanners are what an operator acts on, older ones age out.
const maxProbeHits = 500

// RecordProbe folds one detected scan into the per-IP row: it bumps the hit count,
// advances last_seen, and remembers the largest distinct-miss burst. Then it trims
// the table to the newest maxProbeHits IPs so background scanning can't grow it
// without bound.
func (s *Store) RecordProbe(ip string, paths int, now int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO probe_hits (ip, first_seen, last_seen, hits, paths)
			 VALUES (?, ?, ?, 1, ?)
			 ON CONFLICT(ip) DO UPDATE SET
			   last_seen = excluded.last_seen,
			   hits = hits + 1,
			   paths = MAX(paths, excluded.paths)`,
			ip, now, now, paths,
		); err != nil {
			return err
		}
		_, err := tx.Exec(
			`DELETE FROM probe_hits WHERE ip NOT IN (
			   SELECT ip FROM probe_hits ORDER BY last_seen DESC, ip DESC LIMIT ?
			 )`, maxProbeHits)
		return err
	})
}

// ListProbes returns the scanning IPs, most recently seen first.
func (s *Store) ListProbes(limit int) ([]model.ProbeHit, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT ip, first_seen, last_seen, hits, paths FROM probe_hits
		 ORDER BY last_seen DESC, ip DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeHit
	for rows.Next() {
		var p model.ProbeHit
		if err := rows.Scan(&p.IP, &p.FirstSeen, &p.LastSeen, &p.Hits, &p.Paths); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProbesSince returns scanners whose FIRST sighting is at or after the cutoff — i.e.
// the IPs newly seen in a window, for the daily digest (not the ones merely still
// active). Newest first.
func (s *Store) ProbesSince(cutoff int64) ([]model.ProbeHit, error) {
	rows, err := s.db.Query(
		`SELECT ip, first_seen, last_seen, hits, paths FROM probe_hits
		 WHERE first_seen >= ? ORDER BY first_seen DESC, ip DESC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeHit
	for rows.Next() {
		var p model.ProbeHit
		if err := rows.Scan(&p.IP, &p.FirstSeen, &p.LastSeen, &p.Hits, &p.Paths); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PurgeProbes drops scanner rows last seen before the cutoff (unix seconds).
// Batched like every other sweep: the pool is a single connection, so one unbounded
// DELETE would stall every query behind it. This table is one row per scanning source
// IP, so an internet-wide scan is exactly what inflates it — the sweep must not seize
// the connection precisely when the box is under load.
func (s *Store) PurgeProbes(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM probe_hits WHERE rowid IN (
				SELECT rowid FROM probe_hits WHERE last_seen < ? LIMIT ?
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
