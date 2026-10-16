package store

import "github.com/AppsGanin/rospanel/internal/model"

// MonthReport gathers what the admin bot sums a month up with: [from, to) in unix
// seconds for what has a time, [fromDay, toDay] in 'YYYY-MM-DD' for the daily
// rollups, now for who is active today.
func (s *Store) MonthReport(from, to int64, fromDay, toDay string, now int64) (model.MonthReport, error) {
	var r model.MonthReport
	err := s.db.QueryRow(`SELECT
		(SELECT COALESCE(sum(amount_rub), 0) FROM payment_orders
		  WHERE status = 'paid' AND paid_at >= ? AND paid_at < ? AND refund_source <> 'provider'
		    AND provider <> 'balance'),
		(SELECT count(*) FROM payment_orders
		  WHERE status = 'paid' AND paid_at >= ? AND paid_at < ? AND refund_source <> 'provider'
		    AND provider <> 'balance'),
		(SELECT count(*) FROM users WHERE created_at >= ? AND created_at < ?),
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM users WHERE enabled != 0 AND (expire_at = 0 OR expire_at > ?)
		  AND (data_limit = 0 OR used_up + used_down < data_limit)),
		(SELECT COALESCE(sum(up + down), 0) FROM traffic_daily WHERE day >= ? AND day <= ?),
		(SELECT count(*) FROM incidents WHERE kind = 'incident' AND starts_at >= ? AND starts_at < ?),
		(SELECT count(*) FROM incidents WHERE kind = 'maintenance' AND starts_at >= ? AND starts_at < ?)`,
		from, to, from, to, from, to, now, fromDay, toDay, from, to, from, to,
	).Scan(&r.RevenueRub, &r.Payments, &r.NewUsers, &r.Users, &r.ActiveUsers, &r.Traffic,
		&r.Incidents, &r.Maintenance)
	if err != nil {
		return r, err
	}
	rows, err := s.db.Query(`SELECT node_id, sum(up), sum(total) FROM uptime_daily
		WHERE day >= ? AND day <= ? GROUP BY node_id ORDER BY node_id`, fromDay, toDay)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var u model.MonthUptime
		if err := rows.Scan(&u.NodeID, &u.Up, &u.Total); err != nil {
			return r, err
		}
		r.Uptime = append(r.Uptime, u)
	}
	return r, rows.Err()
}
