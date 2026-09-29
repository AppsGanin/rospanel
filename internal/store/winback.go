package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Win-back codes for users whose paid term lapsed, and the sales funnel.

// SetLapsed records when a user's paid term ended without renewal.
func (s *Store) SetLapsed(userID, at int64) error {
	_, err := s.db.Exec(`UPDATE users SET lapsed_at = ? WHERE id = ?`, at, userID)
	return err
}

// WinbackCandidate is a user whose paid term lapsed at Lapse.
type WinbackCandidate struct {
	UserID int64
	Lapse  int64
}

// WinbackCandidates returns enabled users who once paid for a plan and whose paid
// term lapsed in (floor, cut] with no win-back code sent for that lapse yet. The lapse
// is the recorded one (a downgrade to the free plan, a cancellation), or — with no
// free plan to fall to — the end of the term the user still sits on.
//
// It walks the whole users table, so it reads through the writer: the read pool is for
// bounded lookups (a long scan there holds back the WAL checkpoint).
func (s *Store) WinbackCandidates(floor, cut int64, limit int) ([]WinbackCandidate, error) {
	rows, err := s.db.Query(
		`SELECT id, lapse FROM (
		     SELECT id, enabled, winback_at,
		            max(lapsed_at, CASE WHEN expire_at > 0 AND expire_at <= ? THEN expire_at ELSE 0 END) AS lapse
		     FROM users) u
		 WHERE u.enabled = 1 AND u.lapse > ? AND u.lapse <= ? AND u.winback_at < u.lapse
		   AND EXISTS (SELECT 1 FROM payment_orders o
		               WHERE o.user_id = u.id AND o.status = 'paid' AND o.kind = 'plan'
		                 AND o.refunded_at = 0)
		   AND NOT EXISTS (SELECT 1 FROM payment_orders o
		                   WHERE o.user_id = u.id AND o.refund_source = 'provider')
		 LIMIT ?`, cut, floor, cut, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WinbackCandidate
	for rows.Next() {
		var c WinbackCandidate
		if err := rows.Scan(&c.UserID, &c.Lapse); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SkipWinback marks a lapse as handled without a code — the user came back first.
func (s *Store) SkipWinback(userID, lapse int64) error {
	_, err := s.db.Exec(`UPDATE users SET winback_at = ? WHERE id = ? AND winback_at < ?`, lapse, userID, lapse)
	return err
}

// ErrWinbackNowhere is a win-back code that could neither be attached nor told to
// its user — minting it would only count a code nobody can use.
var ErrWinbackNowhere = errors.New("win-back code has nowhere to go")

// IssueWinback mints the user's personal code for a lapse and attaches it to their
// next payment when no other live code is attached (an earlier win-back code that
// expired or was used gives way), all or nothing. It reports whether the code was
// attached; ErrPromoUsed when a code for this lapse went out already, and — when
// mustAttach — ErrWinbackNowhere if it could not be attached.
func (s *Store) IssueWinback(userID, lapse int64, p *model.PromoCode, code func() string, mustAttach bool, now int64) (attached bool, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		r, err := tx.Exec(`UPDATE users SET winback_at = ? WHERE id = ? AND winback_at < ?`, lapse, userID, lapse)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrPromoUsed
		}
		for range 5 {
			p.Code = code()
			err = tx.QueryRow(
				`INSERT INTO promo_codes (code, kind, value, max_uses, expires_at, enabled, note,
				     created_at, winback_user)
				 VALUES (?, ?, ?, 1, ?, 1, ?, ?, ?) RETURNING id`,
				p.Code, p.Kind, p.Value, p.ExpiresAt, p.Note, now, userID,
			).Scan(&p.ID)
			if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
				break
			}
		}
		if err != nil {
			return err
		}
		r, err = tx.Exec(
			`UPDATE users SET promo_id = ?
			 WHERE id = ? AND (promo_id = 0 OR promo_id IN (
			     SELECT id FROM promo_codes WHERE winback_user <> 0
			       AND ((expires_at > 0 AND expires_at <= ?) OR uses >= max_uses)))`,
			p.ID, userID, now)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		attached = n > 0
		if mustAttach && !attached {
			return ErrWinbackNowhere
		}
		return nil
	})
	return attached, err
}

// WinbackStats sums up the win-back codes sent so far.
func (s *Store) WinbackStats() (model.WinbackStats, error) {
	var st model.WinbackStats
	err := s.rdb.QueryRow(
		`SELECT count(*), COALESCE(sum(uses), 0),
		        (SELECT COALESCE(sum(o.amount_rub), 0) FROM promo_codes pc
		         JOIN payment_orders o ON o.promo_id = pc.id
		         WHERE pc.winback_user <> 0 AND pc.auto_rule = 0 AND o.status = 'paid' AND o.kind = 'plan'
		           AND o.refund_source <> 'provider')
		 FROM promo_codes WHERE winback_user <> 0 AND auto_rule = 0`,
	).Scan(&st.Sent, &st.Used, &st.RevenueRub)
	return st, err
}

// Funnel follows the users who joined at or after since (0 = everyone): how many took
// a trial, paid for a plan, and paid for one again. A plan bought from the balance
// counts; an order the payment system refunded does not.
func (s *Store) Funnel(since int64) (model.Funnel, error) {
	var f model.Funnel
	err := s.db.QueryRow(
		`SELECT count(*), COALESCE(sum(u.trial_used <> 0), 0),
		        COALESCE(sum(p.n >= 1), 0), COALESCE(sum(p.n >= 2), 0)
		 FROM users u
		 LEFT JOIN (SELECT user_id, count(*) AS n FROM payment_orders
		            WHERE status = 'paid' AND kind = 'plan' AND refund_source <> 'provider'
		            GROUP BY user_id) p ON p.user_id = u.id
		 WHERE COALESCE(u.created_at, 0) >= ?`, since,
	).Scan(&f.Joined, &f.Trial, &f.Paid, &f.Renewed)
	return f, err
}
