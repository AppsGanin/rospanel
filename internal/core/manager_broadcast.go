package core

import (
	"context"
	"database/sql"
	"errors"
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
	// Delivery runs on the user bot's token, and — with a webhook on broadcast.sent —
	// through the external system to the accounts the bot does not reach. With
	// neither nothing would ever be sent and the broadcast would sit at 0 % with no
	// explanation.
	bot := set.TGUserBotEnabled && strings.TrimSpace(set.TGUserBotToken) != ""
	hook := m.webhookWanted(model.WebhookBroadcastSent)
	switch {
	case !bot && !hook:
		return nil, invalidCode("err.enableUserBotFirst", "сначала включите пользовательского бота — рассылка идёт через него")
	case !bot && b.MediaKind != "":
		return nil, invalidCode("err.attachmentNeedsBot", "вложение доставляет только бот в Telegram — отправьте текст")
	}

	var chats []int64
	if bot {
		if chats, err = m.audienceChats(b.Audience); err != nil {
			return nil, err
		}
	}
	var hookUsers []model.User
	if hook {
		if hookUsers, err = m.audienceHookUsers(b.Audience, bot); err != nil {
			return nil, err
		}
	}
	if len(chats) == 0 && len(hookUsers) == 0 {
		return nil, invalidCode("err.audienceEmpty", "в выбранной аудитории нет получателей")
	}

	b.CreatedBy = actor.From(ctx).Name
	b.HookUsers = len(hookUsers)
	now := time.Now().Unix()
	id, err := m.store.CreateBroadcast(b, now)
	if err != nil {
		return nil, err
	}
	if err := m.store.AddBroadcastTargets(id, chats); err != nil {
		return nil, err
	}
	if len(hookUsers) > 0 {
		m.broadcastHooks.Store(id, hookUsers)
	}
	// Left paused: the caller starts it once anything else it needs is in place
	// (an attachment is written to disk under the id this call just produced).
	return m.store.GetBroadcast(id)
}

// StartBroadcast begins delivery of a freshly created broadcast: the bot's run, and
// broadcast.sent to the external system. One with nothing for the bot is done then.
func (m *Manager) StartBroadcast(id int64) error {
	now := time.Now().Unix()
	if err := m.store.SetBroadcastStatus(id, model.BroadcastRunning, now); err != nil {
		return err
	}
	b, err := m.store.GetBroadcast(id)
	if err != nil {
		return err
	}
	if users, ok := m.broadcastHooks.LoadAndDelete(id); ok {
		m.emitBroadcast(b, users.([]model.User))
	}
	if b.Total == 0 {
		return m.store.SetBroadcastStatus(id, model.BroadcastDone, now)
	}
	return nil
}

// AbandonBroadcast cancels a created broadcast that never started, and lets go of
// the accounts it held for the external system.
func (m *Manager) AbandonBroadcast(id int64) {
	m.broadcastHooks.Delete(id)
	_ = m.SetBroadcastStatus(id, model.BroadcastCancelled)
}

// emitBroadcast hands a broadcast to the external system: the message (Telegram
// HTML, its URL buttons, the attachment's kind and name — the file goes only to
// Telegram) and the accounts it is for, by the panel's id and the system's own.
func (m *Manager) emitBroadcast(b *model.Broadcast, users []model.User) {
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	ext, err := m.store.UserExternalIDs(ids)
	if err != nil {
		logErr("broadcast: reading the external ids failed", "broadcast", b.ID, "err", err)
	}
	type recipient struct {
		ID         int64  `json:"id"`
		ExternalID string `json:"external_id"`
	}
	rcpt := make([]recipient, len(users))
	for i, u := range users {
		rcpt[i] = recipient{u.ID, ext[u.ID]}
	}
	buttons := b.Buttons
	if buttons == nil {
		buttons = []model.BroadcastButton{}
	}
	d := map[string]any{
		"id": b.ID, "text": b.Text, "buttons": buttons, "audience": b.Audience,
		"telegram_recipients": b.Total, "users": rcpt,
	}
	if b.MediaKind != "" {
		d["media_kind"], d["media_name"] = b.MediaKind, b.MediaName
	}
	m.EmitWebhook(model.WebhookBroadcastSent, d)
}

func validateBroadcast(b *model.Broadcast) error {
	if !model.ValidAudience(b.Audience) {
		return invalidCode("err.unknownAudience", "неизвестная аудитория")
	}
	switch b.MediaKind {
	case "", "photo", "document":
	default:
		return invalidCode("err.unknownAttachment", "неизвестный тип вложения")
	}
	if b.Text == "" && b.MediaKind == "" {
		return invalidCode("err.nothingToSend", "нечего отправлять — добавьте текст или вложение")
	}
	limit := broadcastTextMax
	if b.MediaKind != "" {
		limit = broadcastCaptionMax
	}
	if n := utf8.RuneCountInString(b.Text); n > limit {
		return invalidCode("err.textTooLong", "текст длиннее {{limit}} символов (сейчас {{count}}) — Telegram его не примет", map[string]any{"limit": limit, "count": n})
	}
	if len(b.Buttons) > broadcastButtonsMax {
		return invalidCode("err.tooManyButtons", "слишком много кнопок (максимум {{max}})", map[string]any{"max": broadcastButtonsMax})
	}
	for i := range b.Buttons {
		b.Buttons[i].Text = strings.TrimSpace(b.Buttons[i].Text)
		b.Buttons[i].URL = strings.TrimSpace(b.Buttons[i].URL)
		if b.Buttons[i].Text == "" || b.Buttons[i].URL == "" {
			return invalidCode("err.buttonNeedsBoth", "у кнопки должны быть и текст, и ссылка")
		}
		u, err := url.Parse(b.Buttons[i].URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return invalidCode("err.buttonLinkScheme", "ссылка кнопки «{{label}}» должна начинаться с http:// или https://", map[string]any{"label": b.Buttons[i].Text})
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
	// Every filter except the two membership ones needs the account behind the chat,
	// so it is cheaper to load the roster once than to branch on which ones do.
	users, err := m.store.ListUsers()
	if err != nil {
		return nil, err
	}
	byID, byChat := rosterMaps(users)

	now := time.Now().Unix()
	out := make([]int64, 0, len(subs))
	for _, s := range subs {
		// Presence in the roster, not a non-zero id: a deleted account leaves its id
		// behind on the subscriber row, and treating that as "linked" fed the filters
		// a zero-value user — so "never connected" collected ex-customers who
		// had connected yesterday, while the audience documented to hold them
		// ("without an account") excluded them.
		u, linked := chatAccount(s, byID, byChat)
		if linked && u.MailingOff {
			continue // the account said no to mailings (the API, the operator)
		}
		keep := false
		switch audience {
		case model.AudienceAll:
			keep = true
		case model.AudienceUnlinked:
			keep = !linked
		default:
			keep = linked && audienceKeeps(audience, u, now)
		}
		if keep {
			out = append(out, s.ChatID)
		}
	}
	return out, nil
}

func rosterMaps(users []model.User) (byID, byChat map[int64]model.User) {
	byID = make(map[int64]model.User, len(users))
	byChat = make(map[int64]model.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
		if u.TgChatID != 0 {
			byChat[u.TgChatID] = u
		}
	}
	return byID, byChat
}

// chatAccount finds the account behind a chat: the one holding it (users.tg_chat_id).
// The subscriber row's user_id only when no account holds the chat and that account
// holds no other: the bot alone keeps that column, so an approval, an API link or a
// move to another Telegram leaves it behind.
func chatAccount(s model.Subscriber, byID, byChat map[int64]model.User) (model.User, bool) {
	if u, ok := byChat[s.ChatID]; ok {
		return u, true
	}
	u, ok := byID[s.UserID]
	return u, ok && u.TgChatID == 0
}

// audienceKeeps reports whether an account belongs to an audience.
func audienceKeeps(audience string, u model.User, now int64) bool {
	switch audience {
	case model.AudienceAll, model.AudienceLinked:
		return true
	case model.AudienceUnlinked:
		return false
	case model.AudienceActive:
		return u.Status == model.StatusActive
	case model.AudienceExpired:
		return u.Status == model.StatusExpired
	case model.AudienceNever:
		return u.LastSeen == 0
	}
	if n, ok := model.AudienceDays(audience, model.AudienceSeenPrefix); ok {
		return u.LastSeen > 0 && now-u.LastSeen <= int64(n)*86400
	}
	if n, ok := model.AudienceDays(audience, model.AudienceUnseenPrefix); ok {
		// "Never connected" counts as not seen — someone who never arrived is the
		// clearest case of what this filter looks for — but only once the account is
		// itself older than the horizon. Otherwise "you have not been online for 90
		// days" lands on people who registered this morning.
		old := now-u.CreatedAt.Unix() > int64(n)*86400
		stale := u.LastSeen > 0 && now-u.LastSeen > int64(n)*86400
		return stale || (u.LastSeen == 0 && old)
	}
	if n, ok := model.AudienceDays(audience, model.AudienceExpiringPrefix); ok {
		return u.ExpireAt > now && u.ExpireAt-now <= int64(n)*86400
	}
	return false
}

// audienceHookUsers resolves an audience to the accounts the external system writes
// to: every one in it the bot does not reach (no Telegram, a Telegram that blocked
// the bot or never started it, or the bot off), except who turned mailings off in
// the bot.
func (m *Manager) audienceHookUsers(audience string, bot bool) ([]model.User, error) {
	users, err := m.store.ListUsers()
	if err != nil {
		return nil, err
	}
	optedOut, err := m.store.OptedOutChats()
	if err != nil {
		return nil, err
	}
	// The accounts the bot's own run reaches, found as audienceChats finds them.
	reached := map[int64]bool{}
	if bot {
		subs, err := m.store.ListReachableSubscribers()
		if err != nil {
			return nil, err
		}
		byID, byChat := rosterMaps(users)
		for _, s := range subs {
			if u, ok := chatAccount(s, byID, byChat); ok {
				reached[u.ID] = true
			}
		}
	}
	now := time.Now().Unix()
	var out []model.User
	for _, u := range users {
		if reached[u.ID] || u.MailingOff || (u.TgChatID != 0 && optedOut[u.TgChatID]) || !audienceKeeps(audience, u, now) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// AudienceHookPreview is how many accounts an audience sends to the external system
// right now (0 while no webhook takes broadcast.sent).
func (m *Manager) AudienceHookPreview(audience string) (int, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		audience = model.AudienceAll
	}
	if !model.ValidAudience(audience) || !m.webhookWanted(model.WebhookBroadcastSent) {
		return 0, nil
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return 0, err
	}
	users, err := m.audienceHookUsers(audience, set.TGUserBotEnabled && strings.TrimSpace(set.TGUserBotToken) != "")
	return len(users), err
}

// AudiencePreview reports how many recipients an audience currently resolves to, so
// the operator sees the size before launching rather than after.
//
// It normalises and validates exactly as CreateBroadcast does. Without that, an
// unrecognised audience resolved to an empty list and previewed as "0 recipients" —
// while the launch itself would either refuse it or, for an empty string, fall back
// to everyone. A preview that disagrees with the send is worse than no preview.
func (m *Manager) AudiencePreview(audience string) (int, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		audience = model.AudienceAll
	}
	if !model.ValidAudience(audience) {
		return 0, invalidCode("err.unknownAudience", "неизвестная аудитория")
	}
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

// ErrBroadcastNotFound lets the API answer 404 instead of leaking a raw SQL error
// as a 500. A sentinel is compared, never shown — the handler words the 404 with a
// dictionary code — so its text is English like every other sentinel here.
var ErrBroadcastNotFound = errors.New("broadcast not found")

// GetBroadcast returns one broadcast with its progress.
func (m *Manager) GetBroadcast(id int64) (*model.Broadcast, error) {
	b, err := m.store.GetBroadcast(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBroadcastNotFound
	}
	return b, err
}

// ValidateBroadcast checks a composed message without storing it — used by the test
// send, so a preview is refused for the same reasons the real run would be rather
// than failing later with a raw Telegram error.
func (m *Manager) ValidateBroadcast(b *model.Broadcast) error {
	b.Text = strings.TrimSpace(b.Text)
	if strings.TrimSpace(b.Audience) == "" {
		b.Audience = model.AudienceAll
	}
	return validateBroadcast(b)
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
		return invalidCode("err.broadcastFinished", "рассылка уже завершена")
	}
	switch status {
	case model.BroadcastPaused, model.BroadcastRunning, model.BroadcastCancelled:
		return m.store.SetBroadcastStatus(id, status, time.Now().Unix())
	default:
		return invalidCode("err.unknownBroadcastState", "неизвестное состояние рассылки")
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
		return 0, invalidCode("err.retryOnlyFinished", "повторить можно только завершённую рассылку")
	}
	n, err := m.store.RetryFailedBroadcast(id, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, invalidCode("err.noFailedToRetry", "нет неудачных отправок для повтора")
	}
	return n, nil
}
