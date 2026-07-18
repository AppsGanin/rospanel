package store

import (
	"database/sql"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Support relay topic mapping: one forum topic per writer, both directions. The
// panel stores no message bodies — Telegram holds the conversation; this table only
// answers "which topic is this user's" and "whose topic is this".

// SupportTopicByChat returns the topic id opened for a chat, or 0 when none exists
// yet (the caller then creates one).
func (s *Store) SupportTopicByChat(chatID int64) (int64, error) {
	var topicID int64
	err := s.db.QueryRow(
		`SELECT topic_id FROM tg_support_topics WHERE chat_id = ?`, chatID).Scan(&topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return topicID, err
}

// SupportChatByTopic resolves an admin's reply back to the user who owns the topic,
// or 0 when the thread isn't one of ours (a message in some unrelated topic).
func (s *Store) SupportChatByTopic(topicID int64) (int64, error) {
	var chatID int64
	err := s.db.QueryRow(
		`SELECT chat_id FROM tg_support_topics WHERE topic_id = ?`, topicID).Scan(&chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return chatID, err
}

// SetSupportTopic records (or re-points, after the old topic was deleted) the topic
// belonging to a chat.
func (s *Store) SetSupportTopic(chatID, topicID, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO tg_support_topics (chat_id, topic_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET topic_id = excluded.topic_id,
		                                    created_at = excluded.created_at`,
		chatID, topicID, now)
	return err
}

// DeleteSupportTopic drops a mapping whose topic no longer exists, so the next
// message opens a fresh thread instead of failing forever.
func (s *Store) DeleteSupportTopic(chatID int64) error {
	_, err := s.db.Exec(`DELETE FROM tg_support_topics WHERE chat_id = ?`, chatID)
	return err
}

// Discovered groups — see migrations/0034_tg_support_groups.sql for why these are
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

// DeleteSupportGroup drops a group the bot was removed from, so the picker doesn't
// keep offering somewhere it can no longer post.
func (s *Store) DeleteSupportGroup(chatID int64) error {
	_, err := s.db.Exec(`DELETE FROM tg_support_groups WHERE chat_id = ?`, chatID)
	return err
}

// ListSupportGroups returns the known candidates, most recently seen first.
func (s *Store) ListSupportGroups() ([]model.SupportGroup, error) {
	rows, err := s.db.Query(
		`SELECT chat_id, title, is_forum, is_admin FROM tg_support_groups
		 ORDER BY seen_at DESC`)
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

// ResetSupportTopics clears every mapping. Called when the operator points support at
// a different group: the stored thread ids belong to the old one and would otherwise
// address threads that don't exist (or, worse, unrelated threads in the new group).
func (s *Store) ResetSupportTopics() error {
	_, err := s.db.Exec(`DELETE FROM tg_support_topics`)
	return err
}
