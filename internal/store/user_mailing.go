package store

import (
	"database/sql"
	"errors"
)

// An account's mailing switch and language. Mailings reach an account while neither
// its own switch (users.mailing_off) nor its Telegram chat's opt-out says no; every
// write sets both, so the bot's switch and the API never disagree on what they show.

// UserContact is how an account is reached: its external system's id, whether
// mailings go to it, and its language — the account's own, else the one its
// Telegram reports (raw, as Telegram gives it; "" when neither is known).
type UserContact struct {
	ExternalID string
	MailingOff bool
	OwnLang    string
	ChatLang   string
}

const userContactSQL = `SELECT u.id, u.external_id, u.mailing_off, u.lang,
	       COALESCE(c.opt_out, 0), COALESCE(c.lang, '')
	  FROM users u LEFT JOIN tg_subscribers c ON c.chat_id = u.tg_chat_id AND u.tg_chat_id <> 0`

// UserContacts reads them for several accounts at once.
func (s *Store) UserContacts(ids []int64) (map[int64]UserContact, error) {
	out := make(map[int64]UserContact, len(ids))
	const chunk = 900
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := s.rdb.Query(userContactSQL+` WHERE u.id IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var c UserContact
			var own, chat int
			if err := rows.Scan(&id, &c.ExternalID, &own, &c.OwnLang, &chat, &c.ChatLang); err != nil {
				rows.Close()
				return nil, err
			}
			c.MailingOff = own != 0 || chat != 0
			out[id] = c
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetUserMailing switches mailings for an account, and for its Telegram chat with it.
func (s *Store) SetUserMailing(userID int64, off bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE users SET mailing_off = ? WHERE id = ?`, boolToInt(off), userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.Exec(`UPDATE tg_subscribers SET opt_out = ?
			 WHERE chat_id = (SELECT tg_chat_id FROM users WHERE id = ?) AND chat_id <> 0`, boolToInt(off), userID)
		return err
	})
}

// SetChatMailing is the bot's switch: the chat's opt-out and the account holding the
// chat, if one does — whose id it returns (0 for none).
func (s *Store) SetChatMailing(chatID int64, off bool, now int64) (int64, error) {
	var userID int64
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO tg_subscribers (chat_id, opt_out, started_at) VALUES (?, ?, ?)
			 ON CONFLICT(chat_id) DO UPDATE SET opt_out = excluded.opt_out`,
			chatID, boolToInt(off), now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET mailing_off = ? WHERE tg_chat_id = ? AND tg_chat_id <> 0`,
			boolToInt(off), chatID); err != nil {
			return err
		}
		err := tx.QueryRow(`SELECT id FROM users WHERE tg_chat_id = ? AND tg_chat_id <> 0 LIMIT 1`, chatID).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return userID, err
}

// ChatMailingOff reports whether mailings are off for a chat: its own opt-out, or
// that of the account holding it.
func (s *Store) ChatMailingOff(chatID int64) (bool, error) {
	var off int
	err := s.rdb.QueryRow(`SELECT
		    COALESCE((SELECT opt_out FROM tg_subscribers WHERE chat_id = ?), 0)
		  + COALESCE((SELECT mailing_off FROM users WHERE tg_chat_id = ? AND tg_chat_id <> 0 LIMIT 1), 0)`,
		chatID, chatID).Scan(&off)
	return off != 0, err
}

// SetUserLang sets an account's language ("" goes back to what Telegram reports).
func (s *Store) SetUserLang(userID int64, lang string) error {
	res, err := s.db.Exec(`UPDATE users SET lang = ? WHERE id = ?`, lang, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ChatLang is the language to write to a chat in: the account holding it, when its
// own is set, else the chat's (raw, as Telegram reports it); "" when unknown.
func (s *Store) ChatLang(chatID int64) string {
	var lang string
	_ = s.rdb.QueryRow(`SELECT COALESCE(
		    NULLIF((SELECT lang FROM users WHERE tg_chat_id = ? AND tg_chat_id <> 0 LIMIT 1), ''),
		    (SELECT lang FROM tg_subscribers WHERE chat_id = ?), '')`, chatID, chatID).Scan(&lang)
	return lang
}
