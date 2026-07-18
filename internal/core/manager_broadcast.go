package core

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Broadcast composition and control. Delivery itself lives in internal/telegram
// (BroadcastService), the same split the bots use: core never talks to Telegram, it
// only decides what a broadcast is and who is in it.

// Telegram's own limits. Exceeding either is rejected per recipient, so the whole
// broadcast would fail one message at a time — worth refusing up front, where the
// operator can still fix the text.
const (
	broadcastTextMax    = 4096 // plain message
	broadcastCaptionMax = 1024 // message carrying media
	broadcastButtonsMax = 8
)

// CreateBroadcast validates a composed broadcast, resolves its audience to a fixed
// recipient list, and starts it. The audience is snapshotted here and never
// recomputed: a run that re-evaluated itself would pick up people who arrived
// halfway and give a progress total that moves under the operator's feet.
func (m *Manager) CreateBroadcast(ctx context.Context, b *model.Broadcast) (*model.Broadcast, error) {
	b.Text = strings.TrimSpace(b.Text)
	b.Audience = strings.TrimSpace(b.Audience)
	if b.Audience == "" {
		b.Audience = model.AudienceAll
	}
	if err := validateBroadcast(b); err != nil {
		return nil, err
	}

	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	// Delivery runs on the user bot's token; without it nothing would ever be sent
	// and the broadcast would sit at 0 % with no explanation.
	if !set.TGUserBotEnabled || strings.TrimSpace(set.TGUserBotToken) == "" {
		return nil, invalid("сначала включите пользовательского бота — рассылка идёт через него")
	}

	chats, err := m.audienceChats(b.Audience)
	if err != nil {
		return nil, err
	}
	if len(chats) == 0 {
		return nil, invalid("в выбранной аудитории нет получателей")
	}

	b.CreatedBy = actor.From(ctx).Name
	now := time.Now().Unix()
	id, err := m.store.CreateBroadcast(b, now)
	if err != nil {
		return nil, err
	}
	if err := m.store.AddBroadcastTargets(id, chats); err != nil {
		return nil, err
	}
	// Left paused: the caller starts it once anything else it needs is in place
	// (an attachment is written to disk under the id this call just produced).
	return m.store.GetBroadcast(id)
}

// StartBroadcast begins delivery of a freshly created broadcast.
func (m *Manager) StartBroadcast(id int64) error {
	return m.store.SetBroadcastStatus(id, model.BroadcastRunning, time.Now().Unix())
}

func validateBroadcast(b *model.Broadcast) error {
	switch b.Audience {
	case model.AudienceAll, model.AudienceLinked, model.AudienceUnlinked,
		model.AudienceActive, model.AudienceExpired:
	default:
		return invalid("неизвестная аудитория")
	}
	switch b.MediaKind {
	case "", "photo", "document":
	default:
		return invalid("неизвестный тип вложения")
	}
	if b.Text == "" && b.MediaKind == "" {
		return invalid("нечего отправлять — добавьте текст или вложение")
	}
	limit := broadcastTextMax
	if b.MediaKind != "" {
		limit = broadcastCaptionMax
	}
	if n := utf8.RuneCountInString(b.Text); n > limit {
		return invalid("текст длиннее %d символов (сейчас %d) — Telegram его не примет", limit, n)
	}
	if len(b.Buttons) > broadcastButtonsMax {
		return invalid("слишком много кнопок (максимум %d)", broadcastButtonsMax)
	}
	for i := range b.Buttons {
		b.Buttons[i].Text = strings.TrimSpace(b.Buttons[i].Text)
		b.Buttons[i].URL = strings.TrimSpace(b.Buttons[i].URL)
		if b.Buttons[i].Text == "" || b.Buttons[i].URL == "" {
			return invalid("у кнопки должны быть и текст, и ссылка")
		}
		u, err := url.Parse(b.Buttons[i].URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return invalid("ссылка кнопки «%s» должна начинаться с http:// или https://", b.Buttons[i].Text)
		}
	}
	return nil
}

// audienceChats resolves an audience to chat ids. The user-status filters are
// applied here rather than in SQL because status is derived on read (see
// store.deriveStatus) and does not exist as a queryable column.
func (m *Manager) audienceChats(audience string) ([]int64, error) {
	subs, err := m.store.ListReachableSubscribers()
	if err != nil {
		return nil, err
	}
	var byID map[int64]model.User
	if audience == model.AudienceActive || audience == model.AudienceExpired {
		users, err := m.store.ListUsers()
		if err != nil {
			return nil, err
		}
		byID = make(map[int64]model.User, len(users))
		for _, u := range users {
			byID[u.ID] = u
		}
	}
	out := make([]int64, 0, len(subs))
	for _, s := range subs {
		keep := false
		switch audience {
		case model.AudienceAll:
			keep = true
		case model.AudienceLinked:
			keep = s.UserID != 0
		case model.AudienceUnlinked:
			keep = s.UserID == 0
		case model.AudienceActive:
			keep = byID[s.UserID].Status == model.StatusActive
		case model.AudienceExpired:
			keep = byID[s.UserID].Status == model.StatusExpired
		}
		if keep {
			out = append(out, s.ChatID)
		}
	}
	return out, nil
}

// AudiencePreview reports how many recipients an audience currently resolves to, so
// the operator sees the size before launching rather than after.
func (m *Manager) AudiencePreview(audience string) (int, error) {
	chats, err := m.audienceChats(audience)
	if err != nil {
		return 0, err
	}
	return len(chats), nil
}

// ListBroadcasts returns the most recent broadcasts with their progress.
func (m *Manager) ListBroadcasts(limit int) ([]model.Broadcast, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return m.store.ListBroadcasts(limit)
}

// GetBroadcast returns one broadcast with its progress.
func (m *Manager) GetBroadcast(id int64) (*model.Broadcast, error) {
	return m.store.GetBroadcast(id)
}

// SetBroadcastStatus applies an operator control (pause / resume / cancel). Terminal
// states are refused a second transition so a cancelled run can't be revived into
// sending the rest of a message the operator has already thought better of.
func (m *Manager) SetBroadcastStatus(id int64, status string) error {
	b, err := m.store.GetBroadcast(id)
	if err != nil {
		return err
	}
	if b.Status == model.BroadcastDone || b.Status == model.BroadcastCancelled {
		return invalid("рассылка уже завершена")
	}
	switch status {
	case model.BroadcastPaused, model.BroadcastRunning, model.BroadcastCancelled:
		return m.store.SetBroadcastStatus(id, status, time.Now().Unix())
	default:
		return invalid("неизвестное состояние рассылки")
	}
}

// RetryBroadcast re-queues the recipients that failed for a transient reason.
// Blocked chats are left alone — Telegram will refuse them again identically.
//
// A cancelled run is refused outright. Cancelling does not clear the recipients that
// were still queued, so resuming one would not send "the failures again" — it would
// send the whole remainder of a message the operator has already stopped, from a
// button labelled as a retry of a handful.
func (m *Manager) RetryBroadcast(id int64) (int, error) {
	b, err := m.store.GetBroadcast(id)
	if err != nil {
		return 0, err
	}
	if b.Status != model.BroadcastDone {
		return 0, invalid("повторить можно только завершённую рассылку")
	}
	n, err := m.store.RetryFailedBroadcast(id, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, invalid("нет неудачных отправок для повтора")
	}
	return n, nil
}
