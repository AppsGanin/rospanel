package store

import (
	"database/sql"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The bot's audience registry — see migrations/0032_tg_subscribers.sql for why it is
// kept apart from the user roster.

// UpsertSubscriber records a chat that just interacted with the bot, refreshing the
// profile fields and the linked account.
//
// Two rules the ON CONFLICT clause encodes. Contact re-activates: a message proves
// Telegram will deliver to this chat again, so an earlier block is stale. Opt-out is
// never touched: it is the person's own decision, and a broadcast quietly resuming
// because they typed /start is the behaviour that makes people block the bot instead.
func (s *Store) UpsertSubscriber(chatID, userID int64, username, firstName, lang string, now int64) error {
	var uid any
	if userID != 0 {
		uid = userID
	}
	_, err := s.db.Exec(
		`INSERT INTO tg_subscribers (chat_id, user_id, username, first_name, lang, started_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     user_id    = excluded.user_id,
		     username   = excluded.username,
		     first_name = excluded.first_name,
		     lang       = excluded.lang,
		     active     = 1,
		     blocked_at = 0`,
		chatID, uid, username, firstName, lang, now)
	return err
}

// SetSubscriberBlocked marks a chat undeliverable after Telegram refused permanently
// (blocked the bot, deactivated account). Rows are kept rather than deleted: the
// person may unblock, and the next message they send re-activates them.
func (s *Store) SetSubscriberBlocked(chatID, at int64) error {
	_, err := s.db.Exec(
		`UPDATE tg_subscribers SET active = 0, blocked_at = ? WHERE chat_id = ?`, at, chatID)
	return err
}

// SetSubscriberOptOut records the /stop or /mailing choice. It inserts when the chat
// isn't known yet, so an opt-out is never lost to a missing row.
func (s *Store) SetSubscriberOptOut(chatID int64, out bool, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO tg_subscribers (chat_id, opt_out, started_at) VALUES (?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET opt_out = excluded.opt_out`,
		chatID, boolToInt(out), now)
	return err
}

// SubscriberByChat returns one subscriber, or nil when the chat is unknown.
func (s *Store) SubscriberByChat(chatID int64) (*model.Subscriber, error) {
	var sub model.Subscriber
	var userID sql.NullInt64
	var active, optOut int
	err := s.db.QueryRow(
		`SELECT chat_id, user_id, username, first_name, lang, active, opt_out, blocked_at, started_at
		 FROM tg_subscribers WHERE chat_id = ?`, chatID).
		Scan(&sub.ChatID, &userID, &sub.Username, &sub.FirstName, &sub.Lang,
			&active, &optOut, &sub.BlockedAt, &sub.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sub.UserID = userID.Int64
	sub.Active = active != 0
	sub.OptOut = optOut != 0
	return &sub, nil
}

// CountSubscribers reports how many chats are known in total and how many a
// broadcast would currently reach (neither blocked nor opted out).
func (s *Store) CountSubscribers() (total, reachable int, err error) {
	err = s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(active = 1 AND opt_out = 0), 0) FROM tg_subscribers`).
		Scan(&total, &reachable)
	return total, reachable, err
}
