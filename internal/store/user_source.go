package store

import (
	"database/sql"

	"github.com/AppsGanin/rospanel/internal/model"
)

// SetSubscriberSource remembers the tag a chat's first /start link carried. A chat
// that already has one keeps it.
func (s *Store) SetSubscriberSource(chatID int64, source string, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO tg_subscribers (chat_id, source, started_at) VALUES (?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET source = excluded.source
		 WHERE tg_subscribers.source = ''`,
		chatID, source, now)
	return err
}

// AttachSourceFromChat gives a freshly registered user the tag its chat arrived
// with, unless the user already has one.
func (s *Store) AttachSourceFromChat(userID, chatID int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET source = (SELECT source FROM tg_subscribers WHERE chat_id = ?)
		 WHERE id = ? AND source = ''
		   AND EXISTS (SELECT 1 FROM tg_subscribers WHERE chat_id = ? AND source <> '')`,
		chatID, userID, chatID)
	return err
}

// SetUserSource sets a user's tag outright (” clears it).
func (s *Store) SetUserSource(userID int64, source string) error {
	res, err := s.db.Exec(`UPDATE users SET source = ? WHERE id = ?`, source, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UserSource reads a user's tag.
func (s *Store) UserSource(userID int64) string {
	var src string
	_ = s.db.QueryRow(`SELECT source FROM users WHERE id = ?`, userID).Scan(&src)
	return src
}

// FunnelBySource is the funnel split by where the users came from, for users who
// joined at or after since: the tag, "~ref" for an invite link without one, "" for
// neither. revenue_rub is the money they paid in (provider refunds excluded). The
// biggest limit groups by joined come back.
func (s *Store) FunnelBySource(since int64, limit int) ([]model.SourceFunnel, error) {
	rows, err := s.rdb.Query(
		`SELECT CASE WHEN u.source <> '' THEN u.source WHEN u.referrer_id <> 0 THEN '~ref' ELSE '' END AS src,
		        count(*), COALESCE(sum(u.trial_used <> 0), 0),
		        COALESCE(sum(p.n >= 1), 0), COALESCE(sum(p.n >= 2), 0), COALESCE(sum(m.rub), 0)
		 FROM users u
		 LEFT JOIN (SELECT user_id, count(*) AS n FROM payment_orders
		            WHERE status = 'paid' AND kind = 'plan' AND refund_source <> 'provider'
		            GROUP BY user_id) p ON p.user_id = u.id
		 LEFT JOIN (SELECT user_id, sum(amount_rub) AS rub FROM payment_orders
		            WHERE status = 'paid' AND refund_source <> 'provider'
		            GROUP BY user_id) m ON m.user_id = u.id
		 WHERE COALESCE(u.created_at, 0) >= ?
		 GROUP BY src ORDER BY count(*) DESC, src LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SourceFunnel{}
	for rows.Next() {
		var f model.SourceFunnel
		if err := rows.Scan(&f.Source, &f.Joined, &f.Trial, &f.Paid, &f.Renewed, &f.RevenueRub); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
