package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// SupportService is the support relay: a third bot that carries messages between a
// user's private chat and a per-user topic in the operator's forum supergroup.
//
// It is deliberately a separate bot from the user bot. Inside the user bot every
// incoming message would need a "support request, or just tapping around the menu?"
// decision; here there is no menu, no plans and no registration, so everything sent
// is unambiguously a request and nothing has to be guessed. Relaying by message id
// also means screenshots, documents and voice notes pass through without the bot
// parsing a single attachment.
type SupportService struct {
	panel Panel
	store *store.Store

	mu          sync.Mutex
	client      *Client
	clientToken string
	botID       int64 // getMe result for the current token, resolved once
	offset      int64

	rateMu sync.Mutex
	rate   map[int64]*rateWindow
}

// Per-chat flood limit. The support bot is public and everything it receives lands
// in the operator's admin group, so one chat must not be able to bury it.
const (
	supportRateWindow   = time.Minute
	maxSupportPerWindow = 20
	// rateGCThreshold caps the tracking map; stale windows are pruned past it so a
	// long-lived process doesn't accumulate an entry per chat that ever wrote.
	rateGCThreshold = 1024
)

// topicNameMax is Telegram's limit for a forum topic name.
const topicNameMax = 128

// internalNotePrefix marks an admin message in a topic as a note between admins —
// it is not relayed to the user. Without an escape like this, thinking out loud in
// the thread would be delivered to the person you're talking about.
const internalNotePrefix = "//"

// defaultSupportGreeting is used when the operator hasn't written one. It promises
// nothing about response time — that promise is the operator's to make.
const defaultSupportGreeting = "💬 <b>Поддержка</b>\n\nОпишите проблему сообщением в этот чат — можно приложить скриншот. Ответим здесь же."

type rateWindow struct {
	start time.Time
	count int
}

// NewSupport builds the support relay bot. Call Run to start polling.
func NewSupport(panel Panel, st *store.Store) *SupportService {
	return &SupportService{panel: panel, store: st, rate: map[int64]*rateWindow{}}
}

func (s *SupportService) clientFor(token string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.clientToken != token {
		s.client = NewClient(token)
		s.clientToken = token
		// Update ids are per-bot and a new bot starts from scratch. Carrying the old
		// offset over would ACK away the new bot's whole backlog and swallow every
		// message until its counter caught up — silently, with nothing logged.
		s.offset = 0
		s.botID = 0
	}
	return s.client
}

// allow rate-limits one chat (fixed window). allowed is false once the window is
// spent; first marks the single message that crossed the line, so the sender can be
// told exactly once instead of on every one of a hundred.
func (s *SupportService) allow(chatID int64, now time.Time) (allowed, first bool) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if len(s.rate) > rateGCThreshold {
		for id, w := range s.rate {
			if now.Sub(w.start) >= supportRateWindow {
				delete(s.rate, id)
			}
		}
	}
	w := s.rate[chatID]
	if w == nil || now.Sub(w.start) >= supportRateWindow {
		s.rate[chatID] = &rateWindow{start: now, count: 1}
		return true, false
	}
	w.count++
	if w.count > maxSupportPerWindow {
		return false, w.count == maxSupportPerWindow+1
	}
	return true, false
}

// supportAllowedUpdates adds my_chat_member on top of the default set: it is what
// lets the bot report which groups it is in, so the operator picks one from a list
// instead of digging a numeric chat id out of a Telegram Web URL.
var supportAllowedUpdates = []string{"message", "callback_query", "my_chat_member"}

// Run long-polls the support bot until ctx is cancelled. Settings are re-read every
// cycle, so enabling/disabling or rotating the token takes effect without a restart.
//
// Polling starts as soon as a TOKEN exists — before support is enabled and before a
// group is chosen. That ordering is the whole point: the bot has to be listening to
// notice which groups it was added to, and requiring the group first made the one
// piece of information the operator was missing impossible for the bot to supply.
// Relaying still waits until support is fully configured.
func (s *SupportService) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		set, err := s.store.GetSettings()
		if err != nil || strings.TrimSpace(set.TGSupportBotToken) == "" {
			if !sleep(ctx, 10*time.Second) {
				return
			}
			continue
		}
		client := s.clientFor(strings.TrimSpace(set.TGSupportBotToken))
		updates, err := client.GetUpdatesFor(ctx, s.offset, pollTimeout, supportAllowedUpdates)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !sleep(ctx, pollBackoff(err)) {
				return
			}
			continue
		}
		for _, u := range updates {
			s.offset = u.UpdateID + 1
			s.handle(ctx, client, set, u)
		}
	}
}

func (s *SupportService) handle(ctx context.Context, client *Client, set *model.Settings, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram support: handler panic recovered: %v", r)
		}
	}()
	if u.MyChatMember != nil {
		s.trackGroup(u.MyChatMember)
		return
	}
	if u.Message == nil {
		return
	}
	m := u.Message
	switch {
	case m.Chat.Type == "private":
		s.handleUserMessage(ctx, client, set, m)
	case m.Chat.ID == set.TGSupportGroupID:
		s.handleAdminReply(ctx, client, set, m)
	case m.Chat.Type == "supergroup" || m.Chat.Type == "group":
		// Some other group. Nothing is relayed either way — that would leak one
		// operator's conversations into another's chat — but it is remembered as a
		// candidate, which also picks up groups the bot joined before this existed
		// and so never produced a my_chat_member event we saw.
		s.rememberGroupFromMessage(ctx, client, m.Chat)
	}
}

// botIdentity returns the bot's own user id, asking Telegram once per token. It is
// needed to look the bot up in a group's member list.
func (s *SupportService) botIdentity(ctx context.Context, client *Client) int64 {
	s.mu.Lock()
	id := s.botID
	s.mu.Unlock()
	if id != 0 {
		return id
	}
	me, err := client.GetMe(ctx)
	if err != nil || me == nil {
		log.Printf("telegram support: getMe: %v", err)
		return 0
	}
	s.mu.Lock()
	s.botID = me.ID
	s.mu.Unlock()
	return me.ID
}

// rememberGroupFromMessage records a group seen through a message. A message says
// nothing about the bot's own rights, so they are looked up rather than assumed:
// recording "not an admin" for a bot that IS one sends the operator off to fix a
// setting that was never wrong. Only reached for groups that aren't the configured
// one, so the extra call is rare.
func (s *SupportService) rememberGroupFromMessage(ctx context.Context, client *Client, chat Chat) {
	// Recorded first, on what the message itself proves. A failed rights lookup
	// then costs an accurate label, not the candidate.
	s.rememberGroup(chat, false)
	id := s.botIdentity(ctx, client)
	if id == 0 {
		return
	}
	member, err := client.GetChatMember(ctx, chat.ID, id)
	if err != nil {
		log.Printf("telegram support: rights in %d: %v", chat.ID, err)
		return
	}
	if member.Status == "administrator" || member.Status == "creator" {
		s.rememberGroup(chat, true)
	}
}

// trackGroup records or forgets a group from a membership change.
func (s *SupportService) trackGroup(ev *ChatMemberUpdated) {
	if !ev.InChat() {
		if err := s.store.DeleteSupportGroup(ev.Chat.ID); err != nil {
			log.Printf("telegram support: forget group %d: %v", ev.Chat.ID, err)
		}
		return
	}
	s.rememberGroup(ev.Chat, ev.IsAdmin())
}

// rememberGroup stores a group as a PICKER OPTION — never as the configured one. The
// bot is reachable by @username, so anyone may add it to a group and land here;
// applying that automatically would let a stranger redirect every support
// conversation to a chat they control. The choice stays with whoever holds the panel.
func (s *SupportService) rememberGroup(chat Chat, isAdmin bool) {
	if chat.ID == 0 {
		return
	}
	if err := s.store.UpsertSupportGroup(chat.ID, chat.Title, chat.IsForum, isAdmin, time.Now().Unix()); err != nil {
		log.Printf("telegram support: remember group %d: %v", chat.ID, err)
	}
}

// handleUserMessage relays what a user wrote into their topic, opening one on first
// contact.
func (s *SupportService) handleUserMessage(ctx context.Context, client *Client, set *model.Settings, m *Message) {
	chatID := m.Chat.ID
	// Counted before the /start branch: it is the one command every user sends
	// first, and leaving it outside the limit leaves the limit trivially bypassable.
	switch allowed, first := s.allow(chatID, time.Now()); {
	case !allowed && first:
		// Told once per window. Answering every rejected message would make a flood
		// produce MORE outbound traffic than it did inbound.
		s.reply(ctx, client, chatID, "⏳ Слишком много сообщений подряд. Подождите минуту.")
		return
	case !allowed:
		return
	}
	// The loop now runs on a bare token, so a user can reach the bot while the
	// operator is still setting it up. Say so — silently eating the message would
	// leave them waiting for an answer nobody will ever see.
	if !set.TGSupportEnabled || set.TGSupportGroupID == 0 {
		s.reply(ctx, client, chatID, "⚙️ Поддержка ещё не настроена. Загляните позже.")
		return
	}
	if cmd, _ := splitCmd(m.Text); cmd == "/start" {
		greeting := strings.TrimSpace(set.TGSupportGreeting)
		if greeting == "" {
			greeting = defaultSupportGreeting
		}
		if err := client.SendMessage(ctx, chatID, greeting); err != nil {
			log.Printf("telegram support: greeting to %d: %v", chatID, err)
		}
		return
	}
	topicID, created, err := s.ensureTopic(ctx, client, set, m)
	if err != nil {
		log.Printf("telegram support: ensure topic for %d: %v", chatID, err)
		s.reply(ctx, client, chatID, "⚠️ Не удалось передать сообщение. Попробуйте позже.")
		return
	}

	err = client.ForwardMessage(ctx, set.TGSupportGroupID, topicID, chatID, m.MessageID)
	switch {
	case isTopicClosed(err):
		// Closing a thread is how an admin marks an issue handled — it is not a
		// decision to stop talking to that person forever. Re-open and deliver.
		if err = client.ReopenForumTopic(ctx, set.TGSupportGroupID, topicID); err == nil {
			err = client.ForwardMessage(ctx, set.TGSupportGroupID, topicID, chatID, m.MessageID)
		}
	case isThreadGone(err):
		// The admins deleted the topic. Re-open one and retry once, otherwise this
		// user's conversation would be dead forever.
		if err = s.store.DeleteSupportTopic(chatID); err == nil {
			if topicID, created, err = s.ensureTopic(ctx, client, set, m); err == nil {
				err = client.ForwardMessage(ctx, set.TGSupportGroupID, topicID, chatID, m.MessageID)
			}
		}
	}
	if err != nil {
		// The bot was thrown out of the group, lost its rights, or the group is gone.
		// Never confirm in this case: a "✅ delivered" the operator will never see is
		// worse than an honest failure, because the user then waits for an answer.
		log.Printf("telegram support: forward from %d: %v", chatID, err)
		s.reply(ctx, client, chatID, "⚠️ Не удалось передать сообщение. Попробуйте позже.")
		return
	}
	if created {
		s.reply(ctx, client, chatID, "✅ Отправлено. Ответим здесь же — уведомление придёт в этот чат.")
	}
}

// handleAdminReply copies an admin's message in a topic back to its owner.
func (s *SupportService) handleAdminReply(ctx context.Context, client *Client, set *model.Settings, m *Message) {
	if m.MessageThreadID == 0 {
		return // the General thread — not anybody's conversation
	}
	if m.IsForumService() {
		// Renaming or closing a topic is not a reply. Relaying it would fail and post
		// an alarming "не доставлено" notice for routine housekeeping.
		return
	}
	// Body(), not Text: a note written as a photo caption is still a note, and the
	// escape hatch admins are told to trust must not be text-only.
	if strings.HasPrefix(strings.TrimSpace(m.Body()), internalNotePrefix) {
		return
	}
	chatID, err := s.store.SupportChatByTopic(m.MessageThreadID)
	if err != nil {
		log.Printf("telegram support: topic %d lookup: %v", m.MessageThreadID, err)
		return
	}
	if chatID == 0 {
		return // a topic the operator opened by hand
	}
	if err := client.CopyMessage(ctx, chatID, set.TGSupportGroupID, m.MessageID); err != nil {
		// Report into the thread rather than the log: the admin is standing right
		// there waiting, and silence reads as "delivered".
		note := "⚠️ Не доставлено: " + esc(err.Error())
		if isBlockedByUser(err) {
			note = "🚫 Пользователь заблокировал бота — ответ не доставлен."
		}
		if _, err := client.SendTopic(ctx, set.TGSupportGroupID, m.MessageThreadID, note); err != nil {
			log.Printf("telegram support: notice to topic %d: %v", m.MessageThreadID, err)
		}
	}
}

// ensureTopic returns the chat's topic, opening one (with a pinned user card) on
// first contact. created reports whether this call opened it.
func (s *SupportService) ensureTopic(ctx context.Context, client *Client, set *model.Settings, m *Message) (topicID int64, created bool, err error) {
	chatID := m.Chat.ID
	if topicID, err = s.store.SupportTopicByChat(chatID); err != nil || topicID != 0 {
		return topicID, false, err
	}
	u, linked := s.findUser(chatID)
	if topicID, err = client.CreateForumTopic(ctx, set.TGSupportGroupID, topicTitle(u, linked, m)); err != nil {
		return 0, false, err
	}
	if err = s.store.SetSupportTopic(chatID, topicID, time.Now().Unix()); err != nil {
		return 0, false, err
	}
	// Best-effort context for whoever answers: the subscription card if we know who
	// this is. A failure here must not cost the user their message.
	if msgID, err := client.SendTopic(ctx, set.TGSupportGroupID, topicID, topicCard(u, linked, m, set, s.panel)); err != nil {
		log.Printf("telegram support: card for %d: %v", chatID, err)
	} else if err := client.PinChatMessage(ctx, set.TGSupportGroupID, msgID); err != nil {
		log.Printf("telegram support: pin card for %d: %v", chatID, err)
	}
	return topicID, true, nil
}

func (s *SupportService) findUser(chatID int64) (model.User, bool) {
	u, err := s.store.GetUserByTelegramChatID(chatID)
	if err != nil || u == nil {
		return model.User{}, false
	}
	return *u, true
}

// topicTitle names the thread so the admin list is scannable: the panel user and id
// when we know them, otherwise the Telegram profile.
func topicTitle(u model.User, linked bool, m *Message) string {
	title := tgDisplayName(m.From, m.Chat.ID)
	if linked {
		title = fmt.Sprintf("%s · #%d", u.Name, u.ID)
	} else if m.From != nil && m.From.Username != "" {
		title = "@" + m.From.Username
	}
	// Telegram counts characters, not bytes, and rejects malformed UTF-8 outright.
	// A byte slice through a multi-byte name would 400 every time, permanently
	// breaking support for whoever picked that name.
	if r := []rune(title); len(r) > topicNameMax {
		title = string(r[:topicNameMax])
	}
	return title
}

// topicCard is the pinned first post of a topic: who this is, and how the thread
// behaves.
func topicCard(u model.User, linked bool, m *Message, set *model.Settings, panel Panel) string {
	var b strings.Builder
	if linked {
		b.WriteString(userSelfCard(u, set, panel))
	} else {
		b.WriteString("👤 <b>Не зарегистрирован</b>\n")
		if m.From != nil && m.From.Username != "" {
			fmt.Fprintf(&b, "Telegram: @%s\n", esc(m.From.Username))
		}
		fmt.Fprintf(&b, "Chat ID: <code>%d</code>", m.Chat.ID)
	}
	b.WriteString("\n\n<i>Ответьте в этой теме — сообщение уйдёт пользователю. Строка, начинающаяся с " +
		internalNotePrefix + ", остаётся между админами.</i>")
	return b.String()
}

func (s *SupportService) reply(ctx context.Context, client *Client, chatID int64, html string) {
	if err := client.SendMessage(ctx, chatID, html); err != nil {
		log.Printf("telegram support: reply to %d: %v", chatID, err)
	}
}

// isThreadGone reports whether the API refused because the topic no longer exists —
// the admins deleted it, and the mapping has to be re-pointed at a fresh one.
func isThreadGone(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == 400 &&
		strings.Contains(strings.ToLower(ae.Description), "thread not found")
}

// isTopicClosed reports whether the topic still exists but is closed to new posts.
// Distinct from isThreadGone: this one is repaired by reopening, not by recreating —
// recreating would strand the conversation history the admins just filed away.
func isTopicClosed(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == 400 &&
		strings.Contains(strings.ToUpper(ae.Description), "TOPIC_CLOSED")
}

// isBlockedByUser reports whether the user can no longer be written to at all, as
// opposed to a transient failure worth retrying. Telegram has no machine-readable
// code for these, so the description is matched — but only within the status codes
// that can actually carry them, so a 500 whose text happens to mention a chat is
// never mistaken for a permanent block.
func isBlockedByUser(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || (ae.Code != 403 && ae.Code != 400) {
		return false
	}
	d := strings.ToLower(ae.Description)
	return strings.Contains(d, "bot was blocked") ||
		strings.Contains(d, "user is deactivated") ||
		strings.Contains(d, "chat not found")
}
