package store

import (
	"database/sql"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The churn survey: why users whose paid term lapsed did not renew (migration 0133).

// MarkSurveyed records that the bot asked about the lapse, and reports whether this
// call did (false: asked already).
func (s *Store) MarkSurveyed(userID, lapse int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE users SET survey_at = ? WHERE id = ? AND survey_at < ?`, lapse, userID, lapse)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SkipSurvey records a lapse that is no churn (the term ended by a refund): neither
// the bot nor the page asks about it.
func (s *Store) SkipSurvey(userID, lapse int64) error {
	_, err := s.db.Exec(`UPDATE users SET survey_skip_at = ? WHERE id = ? AND survey_skip_at < ?`, lapse, userID, lapse)
	return err
}

// SurveyLapse is the lapse a user can answer the survey for at now — 0 when there is
// none, the user never paid for a plan, or a refund ended the term. One already asked
// about (survey_at) stays the answer's until a payment since: a later end of a free
// plan's term is the same departure.
func (s *Store) SurveyLapse(userID, now int64) (int64, error) {
	var lapse, asked, skip int64
	var paidAfter bool
	err := s.rdb.QueryRow(`SELECT max(lapsed_at, CASE WHEN expire_at > 0 AND expire_at <= ? THEN expire_at ELSE 0 END),
		       survey_at, survey_skip_at, `+paidSince("u.survey_at")+`
		FROM users u WHERE u.id = ? AND `+paidOnce, now, userID).Scan(&lapse, &asked, &skip, &paidAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	switch {
	case lapse == 0 || lapse <= skip:
		return 0, nil
	case asked > 0 && (lapse <= asked || !paidAfter):
		return asked, nil
	}
	return lapse, nil
}

// ChurnAnswered reports whether the user answered for the lapse.
func (s *Store) ChurnAnswered(userID, lapse int64) (bool, error) {
	var n int
	err := s.rdb.QueryRow(`SELECT count(*) FROM churn_answers WHERE user_id = ? AND lapse_at = ?`, userID, lapse).Scan(&n)
	return n > 0, err
}

// SaveChurnAnswer stores a user's answer for a lapse and reports whether it is the
// first (fresh) and whether it brings the first words (worded). A later one replaces
// the reason; its comment is kept unless a new one is given — "other" is a button,
// then the words.
func (s *Store) SaveChurnAnswer(a model.ChurnAnswer, lapse int64) (fresh, worded bool, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		var prev sql.NullString
		err := tx.QueryRow(`SELECT comment FROM churn_answers WHERE user_id = ? AND lapse_at = ?`,
			a.UserID, lapse).Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		fresh = !prev.Valid
		worded = a.Comment != "" && prev.String == ""
		_, err = tx.Exec(`INSERT INTO churn_answers (user_id, lapse_at, reason, comment, source, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, lapse_at) DO UPDATE SET reason = excluded.reason,
			    comment = CASE WHEN excluded.comment <> '' THEN excluded.comment ELSE churn_answers.comment END,
			    source = excluded.source`,
			a.UserID, lapse, a.Reason, a.Comment, a.Source, a.CreatedAt)
		return err
	})
	return fresh, worded, err
}

// ChurnStats counts the answers given since `since` by reason, with the latest
// `limit` that came with a comment.
func (s *Store) ChurnStats(since int64, limit int) (model.ChurnStats, error) {
	out := model.ChurnStats{Reasons: map[string]int{}, Comments: []model.ChurnAnswer{}}
	rows, err := s.rdb.Query(`SELECT reason, count(*) FROM churn_answers WHERE created_at >= ? GROUP BY reason`, since)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var reason string
		var n int
		if err := rows.Scan(&reason, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Reasons[reason] = n
		out.Total += n
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	rows, err = s.rdb.Query(`SELECT a.user_id, coalesce(u.name, ''), a.reason, a.comment, a.source, a.created_at
		FROM churn_answers a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.created_at >= ? AND a.comment <> ''
		ORDER BY a.created_at DESC, a.id DESC LIMIT ?`, since, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a model.ChurnAnswer
		if err := rows.Scan(&a.UserID, &a.UserName, &a.Reason, &a.Comment, &a.Source, &a.CreatedAt); err != nil {
			return out, err
		}
		out.Comments = append(out.Comments, a)
	}
	return out, rows.Err()
}
