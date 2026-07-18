package telegram

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// UserService is the public VPN user bot: open registration, personal subscription
// menu, and optional deep-link binding for accounts created in the panel.
type UserService struct {
	panel Panel
	store *store.Store

	mu          sync.Mutex
	client      *Client
	clientToken string
	commandsFor string           // token whose command menu was already published
	offset      int64
	pending     map[int64]string // chatID → "reg" (awaiting display name)

	regMu     sync.Mutex
	regWindow time.Time // start of the current registration rate-limit window
	regCount  int       // successful registrations in the current window
}

// Open-registration rate limit: the user bot is public, and each sign-up creates a
// DB row + an Xray reconcile, so cap how many accounts can be minted per window
// across ALL chats (the one-account-per-chat guard already bounds a single chat).
const (
	regWindow       = time.Minute
	maxRegPerWindow = 20
)

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
		// Per-bot update ids: keeping the old offset across a token swap would ACK
		// away the new bot's backlog and drop messages until it caught up.
		s.offset = 0
	}
	return s.client
}

func (s *UserService) setPending(chatID int64, state string) {
	s.mu.Lock()
	s.pending[chatID] = state
	s.mu.Unlock()
}

// allowRegistration rate-limits open sign-ups globally (fixed window) so a flood of
// Telegram accounts can't mass-create VPN users. Returns false when the current
// window is exhausted.
func (s *UserService) allowRegistration(now time.Time) bool {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	if now.Sub(s.regWindow) >= regWindow {
		s.regWindow = now
		s.regCount = 0
	}
	if s.regCount >= maxRegPerWindow {
		return false
	}
	s.regCount++
	return true
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
	// Let the panel push payment confirmations to a user's chat via this bot.
	s.panel.SetUserNotifier(func(chatID int64, html string) {
		set, err := s.store.GetSettings()
		if err != nil || strings.TrimSpace(set.TGUserBotToken) == "" {
			return
		}
		_ = NewClient(strings.TrimSpace(set.TGUserBotToken)).SendMessage(context.Background(), chatID, html)
	})
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
		token := strings.TrimSpace(set.TGUserBotToken)
		client := s.clientFor(token)
		s.publishCommands(ctx, client, token)
		updates, err := client.GetUpdates(ctx, s.offset, pollTimeout)
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
		if u.Callback.Message != nil {
			s.trackSubscriber(u.Callback.From, u.Callback.Message.Chat.ID)
		}
		// The VPN user is acting on their own account — stamp them as the actor so the
		// audit log tells self-service apart from an admin doing it for them.
		s.handleCallback(selfActorCtx(ctx, u.Callback.From), client, u.Callback)
	case u.Message != nil && strings.TrimSpace(u.Message.Text) != "":
		s.trackSubscriber(u.Message.From, u.Message.Chat.ID)
		s.handleMessage(selfActorCtx(ctx, u.Message.From), client, u.Message)
	}
}

// trackSubscriber records the chat in the broadcast audience registry. It runs on
// every interaction, not just registration, so the roster also covers the people a
// broadcast most needs to reach and the user roster cannot name: someone waiting on
// moderation, someone who mistyped an invite code, someone whose account was deleted
// but who is still sitting in the bot.
func (s *UserService) trackSubscriber(from *User, chatID int64) {
	var userID int64
	if u, ok := s.findLinkedUser(chatID); ok {
		userID = u.ID
	}
	var username, firstName, lang string
	if from != nil {
		username, firstName, lang = from.Username, from.FirstName, from.LangCode
	}
	if err := s.store.UpsertSubscriber(chatID, userID, username, firstName, lang, time.Now().Unix()); err != nil {
		log.Printf("telegram user: track subscriber %d: %v", chatID, err)
	}
}

// selfActorCtx marks the context as "this VPN user is acting on themself".
func selfActorCtx(ctx context.Context, from *User) context.Context {
	return actor.With(ctx, actor.UserSelf(actorName(from)))
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
	// Broadcast subscription is handled before the pending-state machine so an
	// explicit command always wins: someone half-way through registration must still
	// be able to opt out, and doing so must not eat the step they were on.
	switch cmd {
	case "/mailing":
		s.showMailing(ctx, client, chatID, 0)
		return
	case "/mailing_on":
		s.setMailingByCommand(ctx, client, chatID, true)
		return
	case "/mailing_off":
		s.setMailingByCommand(ctx, client, chatID, false)
		return
	}
	pending := s.takePending(chatID)
	if u, ok := s.findLinkedUser(chatID); ok {
		if pending == "reg" {
			s.doRegister(ctx, client, chatID, set, text)
			return
		}
		s.sendUserMenu(ctx, client, chatID, set, u)
		return
	}
	switch pending {
	case "reg":
		s.doRegister(ctx, client, chatID, set, text)
	case "regcode":
		s.handleRegCode(ctx, client, chatID, set, text, tgDisplayName(m.From, chatID))
	default:
		s.sendWelcome(ctx, client, set, chatID)
	}
}

// handleRegCode checks an entered invite code and, on a match, registers the user.
func (s *UserService) handleRegCode(ctx context.Context, client *Client, chatID int64, set *model.Settings, code, name string) {
	want := strings.TrimSpace(set.TGUserRegCode)
	if !set.RegistrationOpen() || set.RegMode() != model.RegInvite || want == "" {
		s.sendWelcome(ctx, client, set, chatID)
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(want)) != 1 {
		s.send(ctx, client, chatID, "⚠️ Неверный код-приглашение. Попробуйте ещё раз или обратитесь к администратору.")
		s.setPending(chatID, "regcode")
		return
	}
	s.doRegister(ctx, client, chatID, set, name)
}

func (s *UserService) handleStart(ctx context.Context, client *Client, set *model.Settings, chatID int64, args []string) {
	if len(args) >= 1 {
		if code := userStartLinkCode(args[0]); code != "" {
			s.linkUserFromCode(ctx, client, set, chatID, code)
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
	if !set.RegistrationOpen() {
		s.sendMenu(ctx, client, chatID,
			"👋 Это бот VPN-подписки.\n\nРегистрация новых пользователей закрыта. Обратитесь к администратору.",
			supportOnlyRows(set))
		return
	}
	hint := "Нажмите «Зарегистрироваться» — VPN-подписка будет создана автоматически."
	switch set.RegMode() {
	case model.RegModeration:
		hint = "Нажмите «Зарегистрироваться» — заявку рассмотрит администратор, и мы откроем доступ."
	case model.RegInvite:
		hint = "Нажмите «Зарегистрироваться» и введите код-приглашение от администратора."
	}
	s.sendMenu(ctx, client, chatID, "👋 <b>Добро пожаловать!</b>\n\n"+hint, welcomeRows(set))
}

// welcomeRows is the pre-registration keyboard. Support is offered here too: someone
// who can't get past registration — wrong invite code, waiting on moderation — is
// exactly the person who needs to reach a human, and they have no menu to reach it
// from.
func welcomeRows(set *model.Settings) [][]InlineButton {
	rows := [][]InlineButton{{{Text: "✨ Зарегистрироваться", CallbackData: "vu:reg"}}}
	return append(rows, supportOnlyRows(set)...)
}

// supportOnlyRows is the support link on its own, or no rows at all when support
// isn't configured.
func supportOnlyRows(set *model.Settings) [][]InlineButton {
	if link := set.SupportLink(); link != "" {
		return [][]InlineButton{{{Text: "💬 Поддержка", URL: link}}}
	}
	return nil
}

// tgDisplayName derives a user's panel name from their Telegram profile: the
// first name, or the numeric Telegram id when it's empty (no manual entry).
func tgDisplayName(from *User, fallbackID int64) string {
	if from != nil {
		if name := strings.TrimSpace(from.FirstName); name != "" {
			return name
		}
		if from.ID != 0 {
			return fmt.Sprintf("%d", from.ID)
		}
	}
	return fmt.Sprintf("%d", fallbackID)
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
	// Before the linked-user split and before pending is cleared: the mailing toggle
	// belongs to everyone in the audience, registered or not, and tapping it must not
	// drop a registration step in progress.
	if on, ok := strings.CutPrefix(cb.Data, "vu:mail:"); ok {
		s.setMailing(ctx, client, chatID, msgID, on == "on")
		return
	}
	s.clearPending(chatID)

	if u, ok := s.findLinkedUser(chatID); ok {
		s.handleUserCallback(ctx, client, cb, set, u)
		return
	}

	switch cb.Data {
	case "vu:reg":
		if !set.RegistrationOpen() {
			s.edit(ctx, client, chatID, msgID,
				"Регистрация закрыта. Обратитесь к администратору.", [][]InlineButton{})
			return
		}
		// Invite mode: ask for the code first; the account is created only once it matches.
		if set.RegMode() == model.RegInvite {
			s.setPending(chatID, "regcode")
			s.edit(ctx, client, chatID, msgID, "🔑 Введите код-приглашение:",
				[][]InlineButton{{{Text: "⬅️ Отмена", CallbackData: "vu:cancel"}}})
			return
		}
		// Name is taken automatically from the Telegram profile (first name, or the
		// numeric Telegram id when it's empty) — no manual entry needed.
		s.edit(ctx, client, chatID, msgID, "✨ Создаю аккаунт…", [][]InlineButton{})
		s.doRegister(ctx, client, chatID, set, tgDisplayName(cb.From, chatID))
	case "vu:cancel":
		s.clearPending(chatID)
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
	// If this chat previously unlinked an account, restore that exact account rather
	// than minting a fresh trial user — otherwise unlink→register loops farm trials.
	// Allowed even when open registration is closed: it's a restore, not a new signup.
	if u := s.restoreDetachedUser(ctx, client, chatID, set); u != nil {
		return
	}
	if !set.RegistrationOpen() {
		s.send(ctx, client, chatID, "Регистрация закрыта. Обратитесь к администратору.")
		return
	}
	// A chat that already has a pending moderated request must not re-tap its way
	// through the global rate limit (or spam admins) — short-circuit before both.
	if set.RegMode() == model.RegModeration && s.panel.RegistrationPending(chatID) {
		s.send(ctx, client, chatID, "⏳ Ваша заявка уже на рассмотрении. Дождитесь ответа администратора.")
		return
	}
	if !s.allowRegistration(time.Now()) {
		s.send(ctx, client, chatID, "Сейчас слишком много регистраций. Попробуйте через минуту.")
		return
	}
	// Moderation: don't create an account — file a request an admin must approve. No
	// bot access is granted until then.
	if set.RegMode() == model.RegModeration {
		ok, err := s.panel.RequestRegistration(ctx, chatID, name)
		if err != nil {
			s.send(ctx, client, chatID, "⚠️ Не удалось отправить заявку: "+esc(err.Error()))
			return
		}
		if !ok {
			s.send(ctx, client, chatID, "⏳ Ваша заявка уже на рассмотрении. Дождитесь ответа администратора.")
			return
		}
		s.send(ctx, client, chatID,
			"✅ <b>Заявка отправлена!</b>\n\nОжидает одобрения администратора — мы сообщим, как только доступ откроют.")
		return
	}
	// Open / invite: create the account and show its menu right away. CreateRegistered
	// User applies the trial/free plan when billing is on, else a plain account.
	u, err := s.panel.CreateRegisteredUser(ctx, name)
	if err != nil {
		s.send(ctx, client, chatID, "⚠️ Не удалось создать аккаунт: "+esc(err.Error()))
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, "⚠️ Аккаунт создан, но не удалось привязать чат: "+esc(err.Error()))
		return
	}
	log.Printf("telegram user: registered user %d from chat %d", u.ID, chatID)
	s.panel.AuditTelegramLinked(ctx, u.ID, actorFromCtxName(ctx))
	u.TgChatID = chatID
	s.sendMenu(ctx, client, chatID,
		"✅ Аккаунт создан!\n\n"+userSelfCard(*u, set, s.panel),
		userMenuRows(set, *u))
}

// restoreDetachedUser reattaches an account this chat previously unlinked (if any)
// and shows its menu, returning the restored user. Returns nil when the chat has no
// detached account to restore, so the caller falls through to normal registration.
func (s *UserService) restoreDetachedUser(ctx context.Context, client *Client, chatID int64, set *model.Settings) *model.User {
	u, err := s.store.GetDetachedUserByPrevChat(chatID)
	if err != nil || u == nil {
		return nil
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, "⚠️ Не удалось восстановить аккаунт: "+esc(err.Error()))
		return u
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = &fresh
	}
	log.Printf("telegram user: restored user %d for chat %d (prev unlink)", u.ID, chatID)
	// The account is bound again — without this the trail would still claim it's
	// unlinked, since the unlink WAS recorded.
	s.panel.AuditTelegramLinked(ctx, u.ID, actorFromCtxName(ctx))
	s.sendMenu(ctx, client, chatID,
		"♻️ С возвращением! Ваш прежний аккаунт восстановлен.\n\n"+userSelfCard(*u, set, s.panel),
		userMenuRows(set, *u))
	return u
}

func (s *UserService) findLinkedUser(chatID int64) (model.User, bool) {
	u, err := s.store.GetUserByTelegramChatID(chatID)
	if err != nil || u == nil {
		return model.User{}, false
	}
	return *u, true
}

func (s *UserService) linkUserFromCode(ctx context.Context, client *Client, set *model.Settings, chatID int64, code string) {
	u, err := s.store.GetUserByTgLinkCode(code)
	if err != nil {
		s.send(ctx, client, chatID, "⚠️ Код недействителен или устарел. Откройте страницу подписки и получите новый.")
		return
	}
	if u.TgChatID != 0 && u.TgChatID != chatID {
		s.send(ctx, client, chatID, "⚠️ Этот аккаунт уже привязан к другому чату.")
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, "⚠️ Не удалось привязать чат: "+esc(err.Error()))
		return
	}
	_ = s.store.ClearUserTgLinkCode(u.ID) // one-time: burn the code
	log.Printf("telegram user: user %d linked to chat %d via link code", u.ID, chatID)
	s.panel.AuditTelegramLinked(ctx, u.ID, actorFromCtxName(ctx))
	u.TgChatID = chatID
	s.sendUserMenu(ctx, client, chatID, set, *u)
}

// actorFromCtxName is the Telegram identity stamped on ctx by selfActorCtx — the
// @username the audit row records as the account that was bound.
func actorFromCtxName(ctx context.Context) string { return actor.From(ctx).Name }

func userMenuRows(set *model.Settings, u model.User) [][]InlineButton {
	var rows [][]InlineButton
	// A Mini App button opens the subscription page inside Telegram (QR, link,
	// import buttons — all on one page). Needs an https:// URL, so it's skipped
	// until the host is set.
	if url := subWebAppURL(set, u); url != "" {
		rows = append(rows, []InlineButton{{Text: "🌐 Моя подписка", WebApp: &WebAppInfo{URL: url}}})
	}
	if set.BillingEnabled {
		rows = append(rows, []InlineButton{{Text: "💳 Тарифы", CallbackData: "vu:plans"}})
	}
	// Support lives in its own bot, so this is a plain link out. Empty when support is
	// off or its @username never resolved — a dead button is worse than none.
	if link := set.SupportLink(); link != "" {
		rows = append(rows, []InlineButton{{Text: "💬 Поддержка", URL: link}})
	}
	rows = append(rows,
		[]InlineButton{{Text: "🔄 Обновить", CallbackData: "vu:menu"}},
		[]InlineButton{{Text: "🔓 Отвязать", CallbackData: "vu:unlink"}},
	)
	return rows
}

// subWebAppURL is the https:// subscription-page URL for a web_app button, or ""
// when the host isn't configured yet (Telegram rejects a non-https web_app URL).
func subWebAppURL(set *model.Settings, u model.User) string {
	if strings.TrimSpace(set.Host) == "" || strings.TrimSpace(u.SubToken) == "" {
		return ""
	}
	url := sub.URL(set, u.SubToken)
	if !strings.HasPrefix(url, "https://") {
		return ""
	}
	return url
}

// userSelfCard is the friendly subscription card the user sees in the bot (no
// internal id, emoji labels, human-readable expiry / last-seen).
func userSelfCard(u model.User, set *model.Settings, panel Panel) string {
	loc := panel.Location()
	now := time.Now().Unix()
	var b strings.Builder

	fmt.Fprintf(&b, "👤 <b>%s</b> · <code>#%d</code>\n\n", esc(u.Name), u.ID)
	fmt.Fprintf(&b, "%s\n", userStatusLine(u.Status))

	// Plan (only when billing is in play).
	if u.PlanID != 0 {
		if name := panel.PlanName(u.PlanID); name != "" {
			fmt.Fprintf(&b, "💳 Тариф: <b>%s</b>\n", esc(name))
		}
	} else if set.BillingEnabled {
		b.WriteString("💳 Тариф: вручную\n")
	}

	// Expiry + remaining time.
	if u.ExpireAt > 0 {
		exp := time.Unix(u.ExpireAt, 0).In(loc).Format("02.01.2006")
		if u.ExpireAt > now {
			fmt.Fprintf(&b, "📅 До %s · %s\n", exp, humanLeft(u.ExpireAt-now))
		} else {
			fmt.Fprintf(&b, "📅 Срок истёк %s\n", exp)
		}
	} else {
		b.WriteString("📅 Бессрочно\n")
	}

	// Traffic.
	used := formatBytes(u.UsedUp + u.UsedDown)
	if u.DataLimit > 0 {
		pct := int(min(100, (u.UsedUp+u.UsedDown)*100/u.DataLimit))
		fmt.Fprintf(&b, "📊 %s из %s · %d%%\n", used, formatBytes(u.DataLimit), pct)
	} else {
		fmt.Fprintf(&b, "📊 %s · безлимит\n", used)
	}

	// Devices (only when limited).
	if u.DeviceLimit > 0 {
		fmt.Fprintf(&b, "📱 Устройства: %d из %d\n", u.ActiveDevices, u.DeviceLimit)
	}

	b.WriteString(userOnlineLine(u, now, loc))
	return strings.TrimRight(b.String(), "\n")
}

// userStatusLine renders a friendly, emoji-led status for the user card.
func userStatusLine(status string) string {
	switch status {
	case model.StatusActive:
		return "🟢 <b>Активна</b>"
	case model.StatusExpired:
		return "🔴 <b>Срок истёк</b>"
	case model.StatusLimited:
		return "🟠 <b>Лимит трафика</b>"
	case model.StatusDeviceLimited:
		return "🟠 <b>Лишние устройства</b>"
	case model.StatusDisabled:
		return "⚪ <b>Отключена</b>"
	default:
		return "▫️ " + esc(status)
	}
}

// humanLeft renders remaining time as "осталось N дн./ч./мин.".
func humanLeft(sec int64) string {
	if d := sec / 86400; d >= 1 {
		return fmt.Sprintf("осталось %d дн.", d)
	}
	if h := sec / 3600; h >= 1 {
		return fmt.Sprintf("осталось %d ч.", h)
	}
	return fmt.Sprintf("осталось %d мин.", sec/60)
}

// userOnlineLine renders the last-seen state in human terms.
func userOnlineLine(u model.User, now int64, loc *time.Location) string {
	if u.LastSeen == 0 {
		return "🕐 Ещё не подключались"
	}
	diff := now - u.LastSeen
	switch {
	case diff < 120:
		return "🟢 Сейчас в сети"
	case diff < 3600:
		return fmt.Sprintf("🕐 Был в сети %d мин назад", diff/60)
	case diff < 86400:
		return fmt.Sprintf("🕐 Был в сети %d ч назад", diff/3600)
	case diff < 7*86400:
		return fmt.Sprintf("🕐 Был в сети %d дн назад", diff/86400)
	default:
		return "🕐 Был в сети " + time.Unix(u.LastSeen, 0).In(loc).Format("02.01.2006")
	}
}

func (s *UserService) sendUserMenu(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User) {
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.sendMenu(ctx, client, chatID, userSelfCard(u, set, s.panel), userMenuRows(set, u))
}

func (s *UserService) editUserMenu(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID, userSelfCard(u, set, s.panel), userMenuRows(set, u))
}

func (s *UserService) handleUserCallback(ctx context.Context, client *Client, cb *CallbackQuery, set *model.Settings, u model.User) {
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	switch cb.Data {
	case "vu:menu":
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
	case "vu:plans":
		s.showPlans(ctx, client, chatID, msgID, set, u)
	case "vu:unlink":
		s.edit(ctx, client, chatID, msgID,
			"Отвязать этот Telegram от VPN-подписки?\nАккаунт сохранится: снова нажав «Зарегистрироваться», вы вернёте ту же подписку.",
			[][]InlineButton{
				{{Text: "🔓 Да, отвязать", CallbackData: "vu:unlinkyes"}},
				{{Text: "⬅️ Отмена", CallbackData: "vu:menu"}},
			})
	case "vu:unlinkyes":
		_ = s.panel.UnlinkUserTelegram(ctx, u.ID)
		s.edit(ctx, client, chatID, msgID, "Чат отвязан.", [][]InlineButton{})
		s.sendWelcome(ctx, client, set, chatID)
	case "vu:cancelplan":
		s.confirmCancelPlan(ctx, client, chatID, msgID, u)
	case "vu:cancelyes":
		s.doCancelPlan(ctx, client, chatID, msgID, set, u)
	default:
		if planStr, ok := strings.CutPrefix(cb.Data, "vu:buy:"); ok {
			s.handleBuyPlan(ctx, client, chatID, msgID, set, u, planStr)
		} else if rest, ok := strings.CutPrefix(cb.Data, "vu:pay:"); ok {
			// rest = "<provider>:<planID>"
			if prov, planStr, found := strings.Cut(rest, ":"); found {
				s.startProviderPayment(ctx, client, chatID, msgID, u, planStr, prov)
			}
		}
	}
}

// confirmCancelPlan asks the user to confirm cancelling their active paid plan.
func (s *UserService) confirmCancelPlan(ctx context.Context, client *Client, chatID, msgID int64, u model.User) {
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	active := s.panel.ActivePaidPlan(u)
	if active == nil {
		s.edit(ctx, client, chatID, msgID, "Активной подписки нет.",
			[][]InlineButton{{{Text: "🏠 Меню", CallbackData: "vu:menu"}}})
		return
	}
	s.edit(ctx, client, chatID, msgID,
		"Отменить подписку «"+esc(active.Name)+"»?\nВы перейдёте на бесплатный тариф, оставшееся оплаченное время сгорит.",
		[][]InlineButton{
			{{Text: "🚫 Да, отменить", CallbackData: "vu:cancelyes"}},
			{{Text: "⬅️ Отмена", CallbackData: "vu:plans"}},
		})
}

// doCancelPlan cancels the active paid plan (→ free plan) and returns to the menu.
func (s *UserService) doCancelPlan(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	if err := s.panel.CancelUserPlan(ctx, u.ID); err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{{{Text: "⬅️ К тарифам", CallbackData: "vu:plans"}}})
		return
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID,
		"✅ Подписка отменена — вы на бесплатном тарифе.\n\n"+userSelfCard(u, set, s.panel),
		userMenuRows(set, u))
}

// showPlans presents the billing options. While a paid plan is active only renewal
// and cancellation are offered (no switching); otherwise the paid tariffs are listed
// for purchase. Free/trial plans are never self-selectable here.
func (s *UserService) showPlans(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	if !set.BillingEnabled {
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
		return
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	// Active paid plan: renew the same plan or cancel it — switching is blocked.
	if active := s.panel.ActivePaidPlan(u); active != nil {
		s.edit(ctx, client, chatID, msgID,
			"💳 <b>Подписка</b>\n\nАктивен тариф «"+esc(active.Name)+"»"+planActiveUntil(u, s.panel)+
				".\nМожно продлить его или отменить. Сменить тариф можно только после отмены.",
			[][]InlineButton{
				{{Text: "🔄 Продлить «" + active.Name + "»", CallbackData: fmt.Sprintf("vu:buy:%d", active.ID)}},
				{{Text: "🚫 Отменить подписку", CallbackData: "vu:cancelplan"}},
				{{Text: "⬅️ Назад", CallbackData: "vu:menu"}},
			})
		return
	}
	plans, err := s.panel.ListTariffPlans(false)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{{{Text: "⬅️ Назад", CallbackData: "vu:menu"}}})
		return
	}
	var rows [][]InlineButton
	for _, p := range plans {
		if p.IsFree() {
			continue // paid plans only
		}
		rows = append(rows, []InlineButton{{
			Text:         planButtonLabel(p),
			CallbackData: fmt.Sprintf("vu:buy:%d", p.ID),
		}})
	}
	if len(rows) == 0 {
		s.edit(ctx, client, chatID, msgID, "Сейчас нет доступных тарифов.",
			[][]InlineButton{{{Text: "⬅️ Назад", CallbackData: "vu:menu"}}})
		return
	}
	rows = append(rows, []InlineButton{{Text: "⬅️ Назад", CallbackData: "vu:menu"}})
	msg := "💳 <b>Тарифы</b>\n\n"
	if len(s.panel.PaymentMethods()) > 0 {
		msg += "Оплата картой или криптой — тариф активируется автоматически."
	} else {
		msg += "Оплата и подтверждение админом."
	}
	s.edit(ctx, client, chatID, msgID, msg, rows)
}

// planActiveUntil renders " до DD.MM.YYYY" for a user's paid expiry (empty if none).
func planActiveUntil(u model.User, panel Panel) string {
	if u.ExpireAt <= 0 {
		return ""
	}
	return " до " + time.Unix(u.ExpireAt, 0).In(panel.Location()).Format("02.01.2006")
}

// providerButton is the pay-method button text: a wallet icon plus the provider's
// registry label (so a new provider needs no change here).
func (s *UserService) providerButton(key string) string {
	return "💳 " + s.panel.ProviderLabel(key)
}

func (s *UserService) handleBuyPlan(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, planIDStr string) {
	var planID int64
	if _, err := fmt.Sscan(planIDStr, &planID); err != nil || planID <= 0 {
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
		return
	}
	methods := s.panel.PaymentMethods()
	switch len(methods) {
	case 0:
		s.manualPayment(ctx, client, chatID, msgID, u, planID) // no provider → manual instructions
	case 1:
		s.startProviderPayment(ctx, client, chatID, msgID, u, planIDStr, methods[0])
	default:
		var rows [][]InlineButton
		for _, p := range methods {
			rows = append(rows, []InlineButton{{Text: s.providerButton(p), CallbackData: fmt.Sprintf("vu:pay:%s:%d", p, planID)}})
		}
		rows = append(rows, []InlineButton{{Text: "⬅️ К тарифам", CallbackData: "vu:plans"}})
		s.edit(ctx, client, chatID, msgID, "Выберите способ оплаты:", rows)
	}
}

// startProviderPayment creates a provider payment and shows the pay button. The
// tariff is applied automatically once the provider confirms (webhook/poll).
func (s *UserService) startProviderPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planIDStr, provider string) {
	var planID int64
	if _, err := fmt.Sscan(planIDStr, &planID); err != nil || planID <= 0 {
		return
	}
	order, err := s.panel.StartPlanPayment(ctx, u.ID, planID, provider)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{
				{{Text: "⬅️ К тарифам", CallbackData: "vu:plans"}},
				{{Text: "🏠 Меню", CallbackData: "vu:menu"}},
			})
		return
	}
	msg := fmt.Sprintf("💳 <b>Оплата заказа #%d</b>\nСумма: %d ₽\n\nНажмите кнопку, чтобы оплатить. Тариф активируется автоматически после оплаты.", order.ID, order.AmountRub)
	s.edit(ctx, client, chatID, msgID, msg,
		[][]InlineButton{
			{{Text: "💳 Оплатить", URL: order.PayURL}},
			{{Text: "🏠 Меню", CallbackData: "vu:menu"}},
		})
}

func (s *UserService) manualPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planID int64) {
	_, msg, err := s.panel.RequestPlanPayment(ctx, u.ID, planID)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{
				{{Text: "⬅️ К тарифам", CallbackData: "vu:plans"}},
				{{Text: "🏠 Меню", CallbackData: "vu:menu"}},
			})
		return
	}
	s.edit(ctx, client, chatID, msgID, esc(msg),
		[][]InlineButton{
			{{Text: "⬅️ К тарифам", CallbackData: "vu:plans"}},
			{{Text: "🏠 Меню", CallbackData: "vu:menu"}},
		})
}

// UserDeepLink builds a t.me link that binds an existing panel user via a
// one-time, short-lived bind code (see model.TelegramLinkCodeTTL).
func UserDeepLink(botUsername, linkCode string) string {
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	linkCode = strings.TrimSpace(linkCode)
	if botUsername == "" || linkCode == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/%s?start=l_%s", botUsername, linkCode)
}

// UserBotLink is the public bot URL (open /start, no payload).
func UserBotLink(botUsername string) string {
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if botUsername == "" {
		return ""
	}
	return "https://t.me/" + botUsername
}

// userStartLinkCode extracts a one-time bind code from a /start argument
// ("l_<code>"), the payload produced by UserDeepLink.
func userStartLinkCode(arg string) string {
	arg = strings.TrimSpace(arg)
	if code, ok := strings.CutPrefix(arg, "l_"); ok && len(code) >= 16 {
		return code
	}
	return ""
}

// Broadcast opt-out. Kept as its own command rather than a button under every
// broadcast: the alternative to a findable opt-out isn't a captive audience, it's
// people blocking the bot — and a block is irreversible and silently kills payment
// confirmations and support replies along with the newsletter.

// mailingCard renders the current state and the button that flips it.
func mailingCard(optOut bool) (string, [][]InlineButton) {
	if optOut {
		return "📣 <b>Рассылка</b>\n\nСейчас: <b>выключена</b>\n\n" +
				"Служебные уведомления — оплата и ответы поддержки — приходят в любом случае.",
			[][]InlineButton{{{Text: "🔔 Подписаться", CallbackData: "vu:mail:on"}}}
	}
	return "📣 <b>Рассылка</b>\n\nСейчас: <b>включена</b>\n\nНовости сервиса и важные объявления.",
		[][]InlineButton{{{Text: "🔕 Отписаться", CallbackData: "vu:mail:off"}}}
}

// showMailing displays the toggle. msgID 0 sends a new message; otherwise the card
// is edited in place, like the rest of the bot's screens.
func (s *UserService) showMailing(ctx context.Context, client *Client, chatID, msgID int64) {
	optOut := false
	if sub, err := s.store.SubscriberByChat(chatID); err != nil {
		log.Printf("telegram user: mailing state for %d: %v", chatID, err)
	} else if sub != nil {
		optOut = sub.OptOut
	}
	text, rows := mailingCard(optOut)
	if msgID == 0 {
		s.sendMenu(ctx, client, chatID, text, rows)
		return
	}
	s.edit(ctx, client, chatID, msgID, text, rows)
}

func (s *UserService) setMailing(ctx context.Context, client *Client, chatID, msgID int64, on bool) {
	if err := s.store.SetSubscriberOptOut(chatID, !on, time.Now().Unix()); err != nil {
		log.Printf("telegram user: set mailing for %d: %v", chatID, err)
		return
	}
	s.showMailing(ctx, client, chatID, msgID)
}

// setMailingByCommand applies an explicit /mailing_on or /mailing_off and answers
// with a fresh message rather than editing one, since the command may arrive with no
// card on screen to edit.
//
// Unsubscribing states what was NOT switched off. Without that line the next thing a
// person does is block the bot to be sure — and a block is irreversible and takes
// payment confirmations and support replies with it.
func (s *UserService) setMailingByCommand(ctx context.Context, client *Client, chatID int64, on bool) {
	if err := s.store.SetSubscriberOptOut(chatID, !on, time.Now().Unix()); err != nil {
		log.Printf("telegram user: set mailing for %d: %v", chatID, err)
		return
	}
	if on {
		s.sendMenu(ctx, client, chatID,
			"🔔 Вы подписаны на рассылку — новости сервиса и важные объявления.",
			[][]InlineButton{{{Text: "🔕 Отписаться", CallbackData: "vu:mail:off"}}})
		return
	}
	s.sendMenu(ctx, client, chatID,
		"🔕 Вы отписаны от рассылки.\n\nСлужебные уведомления — оплата и ответы поддержки — продолжат приходить.",
		[][]InlineButton{{{Text: "🔔 Подписаться обратно", CallbackData: "vu:mail:on"}}})
}

// userBotCommands is the command menu published to Telegram. An opt-out nobody can
// find is not an opt-out, so both directions are named explicitly instead of hiding
// behind one toggle; /mailing answers "am I subscribed right now?".
var userBotCommands = []BotCommand{
	{Command: "start", Description: "Моя подписка"},
	{Command: "mailing", Description: "Рассылка: текущее состояние"},
	{Command: "mailing_on", Description: "Подписаться на рассылку"},
	{Command: "mailing_off", Description: "Отписаться от рассылки"},
}

// publishCommands pushes the command menu once per token. Re-publishing on every
// poll would spend an API call a cycle to send Telegram what it already has.
func (s *UserService) publishCommands(ctx context.Context, client *Client, token string) {
	s.mu.Lock()
	done := s.commandsFor == token
	s.mu.Unlock()
	if done {
		return
	}
	if err := client.SetMyCommands(ctx, userBotCommands); err != nil {
		log.Printf("telegram user: publish commands: %v", err)
		return // not latched: retried next cycle
	}
	s.mu.Lock()
	s.commandsFor = token
	s.mu.Unlock()
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
