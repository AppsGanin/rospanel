package telegram

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// UserService is the public VPN user bot: open registration, personal subscription
// menu, and optional deep-link binding for accounts created in the panel.
type UserService struct {
	panel Panel
	store *store.Store

	mu          sync.Mutex
	client      *Client
	clientToken string
	offset      int64
	pending     map[int64]string // chatID → "reg" (awaiting display name)
}

// NewUser builds the public user bot. Call Run to start polling.
func NewUser(panel Panel, st *store.Store) *UserService {
	return &UserService{
		panel:   panel,
		store:   st,
		pending: map[int64]string{},
	}
}

func (s *UserService) clientFor(token string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.clientToken != token {
		s.client = NewClient(token)
		s.clientToken = token
	}
	return s.client
}

func (s *UserService) setPending(chatID int64, state string) {
	s.mu.Lock()
	s.pending[chatID] = state
	s.mu.Unlock()
}

func (s *UserService) takePending(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.pending[chatID]
	delete(s.pending, chatID)
	return st
}

func (s *UserService) clearPending(chatID int64) {
	s.mu.Lock()
	delete(s.pending, chatID)
	s.mu.Unlock()
}

// Run long-polls the user bot until ctx is cancelled.
func (s *UserService) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		set, err := s.store.GetSettings()
		if err != nil || !set.TGUserBotEnabled || strings.TrimSpace(set.TGUserBotToken) == "" {
			if !sleep(ctx, 10*time.Second) {
				return
			}
			continue
		}
		client := s.clientFor(strings.TrimSpace(set.TGUserBotToken))
		updates, err := client.GetUpdates(ctx, s.offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !sleep(ctx, 15*time.Second) {
				return
			}
			continue
		}
		for _, u := range updates {
			s.offset = u.UpdateID + 1
			s.handle(ctx, client, u)
		}
	}
}

func (s *UserService) handle(ctx context.Context, client *Client, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram user: handler panic recovered: %v", r)
		}
	}()
	switch {
	case u.Callback != nil:
		s.handleCallback(ctx, client, u.Callback)
	case u.Message != nil && strings.TrimSpace(u.Message.Text) != "":
		s.handleMessage(ctx, client, u.Message)
	}
}

func (s *UserService) handleMessage(ctx context.Context, client *Client, m *Message) {
	set, err := s.store.GetSettings()
	if err != nil {
		return
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	cmd, args := splitCmd(text)

	if cmd == "/start" {
		s.handleStart(ctx, client, set, chatID, args)
		return
	}
	if u, ok := s.findLinkedUser(chatID); ok {
		if s.takePending(chatID) == "reg" {
			s.doRegister(ctx, client, chatID, set, text)
			return
		}
		s.sendUserMenu(ctx, client, chatID, set, u)
		return
	}
	if s.takePending(chatID) == "reg" {
		s.doRegister(ctx, client, chatID, set, text)
		return
	}
	s.sendWelcome(ctx, client, set, chatID)
}

func (s *UserService) handleStart(ctx context.Context, client *Client, set *model.Settings, chatID int64, args []string) {
	if len(args) >= 1 {
		if token := userStartToken(args[0]); token != "" {
			s.linkUserFromToken(ctx, client, set, chatID, token)
			return
		}
	}
	if u, ok := s.findLinkedUser(chatID); ok {
		s.sendUserMenu(ctx, client, chatID, set, u)
		return
	}
	s.sendWelcome(ctx, client, set, chatID)
}

func (s *UserService) sendWelcome(ctx context.Context, client *Client, set *model.Settings, chatID int64) {
	if !set.TGUserRegEnabled {
		s.send(ctx, client, chatID,
			"👋 Это бот VPN-подписки.\n\nРегистрация новых пользователей закрыта. Обратитесь к администратору.")
		return
	}
	s.sendMenu(ctx, client, chatID,
		"👋 <b>Добро пожаловать!</b>\n\nНажмите «Зарегистрироваться», затем отправьте имя — вам будет создана VPN-подписка.",
		welcomeRows())
}

func welcomeRows() [][]InlineButton {
	return [][]InlineButton{{{Text: "✨ Зарегистрироваться", CallbackData: "vu:reg"}}}
}

func (s *UserService) handleCallback(ctx context.Context, client *Client, cb *CallbackQuery) {
	_ = client.AnswerCallback(ctx, cb.ID, "")
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	set, err := s.store.GetSettings()
	if err != nil {
		return
	}
	s.clearPending(chatID)

	if u, ok := s.findLinkedUser(chatID); ok {
		s.handleUserCallback(ctx, client, cb, set, u)
		return
	}

	switch cb.Data {
	case "vu:reg":
		if !set.TGUserRegEnabled {
			s.edit(ctx, client, chatID, msgID,
				"Регистрация закрыта. Обратитесь к администратору.", [][]InlineButton{})
			return
		}
		s.setPending(chatID, "reg")
		s.edit(ctx, client, chatID, msgID,
			"✨ <b>Регистрация</b>\n\nОтправьте имя сообщением (как вас показывать в панели).",
			[][]InlineButton{{{Text: "⬅️ Отмена", CallbackData: "vu:cancel"}}})
	case "vu:cancel":
		s.sendWelcome(ctx, client, set, chatID)
	}
}

func (s *UserService) doRegister(ctx context.Context, client *Client, chatID int64, set *model.Settings, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		s.send(ctx, client, chatID, "Имя не может быть пустым. Отправьте имя ещё раз.")
		s.setPending(chatID, "reg")
		return
	}
	if u, ok := s.findLinkedUser(chatID); ok {
		s.sendUserMenu(ctx, client, chatID, set, u)
		return
	}
	if !set.TGUserRegEnabled {
		s.send(ctx, client, chatID, "Регистрация закрыта. Обратитесь к администратору.")
		return
	}
	u, err := s.panel.CreateUser(name, 0, 0)
	if err != nil {
		s.send(ctx, client, chatID, "⚠️ Не удалось создать аккаунт: "+esc(err.Error()))
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, "⚠️ Аккаунт создан, но не удалось привязать чат: "+esc(err.Error()))
		return
	}
	log.Printf("telegram user: registered user %d from chat %d", u.ID, chatID)
	u.TgChatID = chatID
	s.sendMenu(ctx, client, chatID,
		"✅ Аккаунт создан!\n\n"+userSelfCard(*u, s.panel.Location()),
		userMenuRows())
}

func (s *UserService) findLinkedUser(chatID int64) (model.User, bool) {
	u, err := s.store.GetUserByTelegramChatID(chatID)
	if err != nil || u == nil {
		return model.User{}, false
	}
	return *u, true
}

func (s *UserService) linkUserFromToken(ctx context.Context, client *Client, set *model.Settings, chatID int64, token string) {
	u, err := s.store.GetUserBySubToken(token)
	if err != nil {
		s.send(ctx, client, chatID, "⚠️ Ссылка недействительна или устарела.")
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, "⚠️ Не удалось привязать чат: "+esc(err.Error()))
		return
	}
	log.Printf("telegram user: user %d linked to chat %d", u.ID, chatID)
	u.TgChatID = chatID
	s.sendUserMenu(ctx, client, chatID, set, *u)
}

func userMenuRows() [][]InlineButton {
	return [][]InlineButton{
		{{Text: "📲 Подписка", CallbackData: "vu:sub"}},
		{{Text: "🔄 Обновить", CallbackData: "vu:menu"}},
		{{Text: "🔓 Отвязать", CallbackData: "vu:unlink"}},
	}
}

func userSelfCard(u model.User, loc *time.Location) string {
	card := userCard(u, loc)
	if u.DeviceLimit > 0 {
		card += fmt.Sprintf("\nУстройства: %d / %d", u.ActiveDevices, u.DeviceLimit)
	}
	return card
}

func (s *UserService) sendUserMenu(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User) {
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.sendMenu(ctx, client, chatID, userSelfCard(u, s.panel.Location()), userMenuRows())
}

func (s *UserService) editUserMenu(ctx context.Context, client *Client, chatID, msgID int64, u model.User) {
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID, userSelfCard(u, s.panel.Location()), userMenuRows())
}

func (s *UserService) handleUserCallback(ctx context.Context, client *Client, cb *CallbackQuery, set *model.Settings, u model.User) {
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	switch cb.Data {
	case "vu:menu":
		s.editUserMenu(ctx, client, chatID, msgID, u)
	case "vu:sub":
		s.sendSubscription(ctx, client, chatID, set, u)
	case "vu:unlink":
		s.edit(ctx, client, chatID, msgID,
			"Отвязать этот Telegram от VPN-подписки?\nПосле отвязки можно зарегистрироваться снова или привязать другой аккаунт.",
			[][]InlineButton{
				{{Text: "🔓 Да, отвязать", CallbackData: "vu:unlinkyes"}},
				{{Text: "⬅️ Отмена", CallbackData: "vu:menu"}},
			})
	case "vu:unlinkyes":
		_ = s.store.ClearUserTelegramChat(u.ID)
		s.edit(ctx, client, chatID, msgID, "Чат отвязан.", [][]InlineButton{})
		s.sendWelcome(ctx, client, set, chatID)
	}
}

func (s *UserService) sendSubscription(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User) {
	caption := subCaption(u, set)
	png, err := subQR(u, set)
	if err != nil {
		s.send(ctx, client, chatID, caption)
		return
	}
	if perr := client.SendPhoto(ctx, chatID, "subscription.png", caption, bytes.NewReader(png)); perr != nil {
		log.Printf("telegram user: sendPhoto to %d: %v", chatID, perr)
		s.send(ctx, client, chatID, caption)
	}
}

// UserDeepLink builds a t.me link that binds an existing panel user on /start.
func UserDeepLink(botUsername, subToken string) string {
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	subToken = strings.TrimSpace(subToken)
	if botUsername == "" || subToken == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/%s?start=u_%s", botUsername, subToken)
}

// UserBotLink is the public bot URL (open /start, no payload).
func UserBotLink(botUsername string) string {
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if botUsername == "" {
		return ""
	}
	return "https://t.me/" + botUsername
}

// userStartToken extracts a subscription token from a /start argument.
func userStartToken(arg string) string {
	arg = strings.TrimSpace(arg)
	if strings.HasPrefix(arg, "u_") {
		return strings.TrimPrefix(arg, "u_")
	}
	if len(arg) >= 32 {
		return arg
	}
	return ""
}

func (s *UserService) send(ctx context.Context, client *Client, chatID int64, html string) {
	if err := client.SendMessage(ctx, chatID, html); err != nil {
		log.Printf("telegram user: send to %d: %v", chatID, err)
	}
}

func (s *UserService) sendMenu(ctx context.Context, client *Client, chatID int64, html string, rows [][]InlineButton) {
	if err := client.SendMenu(ctx, chatID, html, rows); err != nil {
		log.Printf("telegram user: send menu to %d: %v", chatID, err)
	}
}

func (s *UserService) edit(ctx context.Context, client *Client, chatID, msgID int64, html string, rows [][]InlineButton) {
	if err := client.EditMenu(ctx, chatID, msgID, html, rows); err != nil {
		log.Printf("telegram user: edit %d/%d: %v", chatID, msgID, err)
	}
}
