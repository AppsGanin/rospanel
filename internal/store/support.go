package store

import (
	"database/sql"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Support relay topic mapping: one forum topic per writer, both directions. The
// panel stores no message bodies — Telegram holds the conversation; this table only
// answers "which topic is this user's" and "whose topic is this".

// SupportTopicByChat returns the topic opened for a chat IN THIS GROUP, or 0 when
// none exists yet (the caller then creates one). A mapping made in another group does
// not match: thread ids are only unique within a chat, so honouring one across a
// group switch would address an unrelated conversation.
func (s *Store) SupportTopicByChat(groupID, chatID int64) (int64, error) {
	var topicID int64
	err := s.db.QueryRow(
		`SELECT topic_id FROM tg_support_topics WHERE group_id = ? AND chat_id = ?`,
		groupID, chatID).Scan(&topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return topicID, err
}

// SupportChatByTopic resolves an admin's reply back to the user who owns the topic,
// or 0 when the thread isn't one of ours (a topic opened by hand, or a leftover from
// a group support no longer points at).
func (s *Store) SupportChatByTopic(groupID, topicID int64) (int64, error) {
	var chatID int64
	err := s.db.QueryRow(
		`SELECT chat_id FROM tg_support_topics WHERE group_id = ? AND topic_id = ?`,
		groupID, topicID).Scan(&chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return chatID, err
}

// SetSupportTopic records (or re-points, after the old topic was deleted) the topic
// belonging to a chat.
//
// Any stale row already holding this (group, thread) is dropped first. Without that,
// the unique index rejects the insert and the caller is left having created a topic
// in Telegram that nothing can address — and since it retries on the next message,
// one unreachable topic per message, with that user's support dead for good.
func (s *Store) SetSupportTopic(groupID, chatID, topicID, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(
		`DELETE FROM tg_support_topics WHERE group_id = ? AND topic_id = ? AND chat_id <> ?`,
		groupID, topicID, chatID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO tg_support_topics (chat_id, group_id, topic_id, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET group_id   = excluded.group_id,
		                                    topic_id   = excluded.topic_id,
		                                    created_at = excluded.created_at`,
		chatID, groupID, topicID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSupportTopic drops a mapping whose topic no longer exists, so the next
// message opens a fresh thread instead of failing forever.
func (s *Store) DeleteSupportTopic(groupID, chatID int64) error {
	_, err := s.db.Exec(
		`DELETE FROM tg_support_topics WHERE group_id = ? AND chat_id = ?`, groupID, chatID)
	return err
}

// Discovered groups — see migrations/0031_telegram_support_broadcast.sql for why these are
// only ever candidates.

// UpsertSupportGroup records a group the bot is in, refreshing what is known about
// it (the operator may enable topics or grant admin after adding the bot).
func (s *Store) UpsertSupportGroup(chatID int64, title string, isForum, isAdmin bool, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO tg_support_groups (chat_id, title, is_forum, is_admin, seen_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     title    = excluded.title,
		     is_forum = excluded.is_forum,
		     is_admin = excluded.is_admin,
		     seen_at  = excluded.seen_at`,
		chatID, title, boolToInt(isForum), boolToInt(isAdmin), now)
	return err
}

// PruneSupportGroups forgets candidates not seen for a while, so a table anyone can
// write to doesn't grow forever.
func (s *Store) PruneSupportGroups(before int64) error {
	_, err := s.db.Exec(`DELETE FROM tg_support_groups WHERE seen_at < ?`, before)
	return err
}

// SeeSupportGroup refreshes what is known about a group WITHOUT touching is_admin.
// Used when the bot has seen the group but hasn't confirmed its own rights: writing
// a speculative "not an admin" would overwrite a verified true and send the operator
// to grant a permission the bot already holds.
func (s *Store) SeeSupportGroup(chatID int64, title string, isForum bool, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO tg_support_groups (chat_id, title, is_forum, is_admin, seen_at)
		 VALUES (?, ?, ?, 0, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     title    = excluded.title,
		     is_forum = excluded.is_forum,
		     seen_at  = excluded.seen_at`,
		chatID, title, boolToInt(isForum), now)
	return err
}

// SupportGroupSeenAt reports when a group was last recorded (0 = never). Used to
// debounce the rights lookup so a busy group can't cost an API call per message.
func (s *Store) SupportGroupSeenAt(chatID int64) (int64, error) {
	var seen int64
	err := s.db.QueryRow(
		`SELECT seen_at FROM tg_support_groups WHERE chat_id = ?`, chatID).Scan(&seen)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seen, err
}

// DeleteSupportGroup drops a group the bot was removed from, so the picker doesn't
// keep offering somewhere it can no longer post.
func (s *Store) DeleteSupportGroup(chatID int64) error {
	_, err := s.db.Exec(`DELETE FROM tg_support_groups WHERE chat_id = ?`, chatID)
	return err
}

// supportGroupsMax bounds the picker. Anyone can add a public bot to a group, so the
// candidate list is attacker-influenced: capping it keeps a seeded flood from burying
// the operator's real group in a dropdown.
const supportGroupsMax = 30

// ListSupportGroups returns the known candidates, most recently seen first.
func (s *Store) ListSupportGroups() ([]model.SupportGroup, error) {
	rows, err := s.db.Query(
		`SELECT chat_id, title, is_forum, is_admin FROM tg_support_groups
		 ORDER BY seen_at DESC LIMIT ?`, supportGroupsMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SupportGroup
	for rows.Next() {
		var g model.SupportGroup
		var isForum, isAdmin int
		if err := rows.Scan(&g.ChatID, &g.Title, &isForum, &isAdmin); err != nil {
			return nil, err
		}
		g.IsForum, g.IsAdmin = isForum != 0, isAdmin != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// Note: there is deliberately no "reset the mappings" call. Rows carry the group
// that issued them, so a mapping from another group simply never matches — which
// removes the need to get a reset exactly right on every transition, and with it the
// choice between leaking messages across customers and orphaning live conversations.
