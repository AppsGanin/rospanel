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
	"github.com/AppsGanin/rospanel/internal/i18n"
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
	clientProxy string // proxy the cached client was built with; a change rebuilds it
	commandsFor string // token whose command menu was already published
	offset      int64
	pending     map[int64]string // chatID → "reg" (awaiting display name)

	regMu     sync.Mutex
	regWindow time.Time // start of the current registration rate-limit window
	regCount  int       // successful registrations in the current window

	// rate bounds how much work one chat can drive; codeRate bounds invite-code
	// guessing specifically. Both are per-chat — regWindow above is a global cap on
	// successful sign-ups and does nothing for a chat that never succeeds.
	rate     *chatLimiter
	codeRate *chatLimiter
}

// Open-registration rate limit: the user bot is public, and each sign-up creates a
// DB row + an Xray reconcile, so cap how many accounts can be minted per window
// across ALL chats (the one-account-per-chat guard already bounds a single chat).
const (
	regWindow       = time.Minute
	maxRegPerWindow = 20
)

// Per-chat limits for the public user bot. The updates loop is a single goroutine
// and every reply waits on the outbound one-second-per-chat slot, so an unbounded
// chat stalls the bot for everyone — see chatLimiter.
//
// Invite codes get their own, far tighter budget: they are operator-chosen and
// usually short, the comparison is constant-time but nothing bounded how many
// guesses a chat could make, and a hit mints a real account.
const (
	userRateWindow    = time.Minute
	maxUserPerWindow  = 20
	codeRateWindow    = 10 * time.Minute
	maxCodesPerWindow = 5
)

// NewUser builds the public user bot. Call Run to start polling.
func NewUser(panel Panel, st *store.Store) *UserService {
	return &UserService{
		panel:    panel,
		store:    st,
		pending:  map[int64]string{},
		rate:     newChatLimiter(userRateWindow, maxUserPerWindow),
		codeRate: newChatLimiter(codeRateWindow, maxCodesPerWindow),
	}
}

func (s *UserService) clientFor(token, proxy string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.clientToken != token || s.clientProxy != proxy {
		s.client = NewClient(token, proxy)
		s.clientToken, s.clientProxy = token, proxy
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
		_ = NewClient(strings.TrimSpace(set.TGUserBotToken), set.TelegramProxyURL()).SendMessage(context.Background(), chatID, html)
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
		client := s.clientFor(token, set.TelegramProxyURL())
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
	// Gate before anything else, trackSubscriber included: it writes a row per
	// update, and the handlers below answer synchronously on this one goroutine while
	// each reply waits for the outbound per-chat slot. One chat is otherwise enough to
	// stall registration, menus and payments for every other user.
	var chatID int64
	switch {
	case u.Callback != nil && u.Callback.Message != nil:
		chatID = u.Callback.Message.Chat.ID
	case u.Message != nil:
		chatID = u.Message.Chat.ID
	}
	if chatID != 0 {
		switch allowed, first := s.rate.allow(chatID, time.Now()); {
		case !allowed && first:
			// Said once per window. Answering every rejected update would make a flood
			// produce more outbound traffic than it did inbound.
			s.send(ctx, client, chatID, i18n.T(s.lang(chatID), "user.tooManyMessages"))
			return
		case !allowed:
			return
		}
	}

	switch {
	case u.Callback != nil:
		if u.Callback.Message != nil {
			s.trackSubscriber(u.Callback.From, u.Callback.Message.Chat.ID)
		}
		// The VPN user is acting on their own account — stamp them as the actor so the
		// audit log tells self-service apart from an admin doing it for them.
		s.handleCallback(selfActorCtx(ctx, u.Callback.From), client, u.Callback)
	case u.Message != nil && strings.TrimSpace(u.Message.Text) != "":
		// Private chats only. Added to a group, this bot would bind a VPN account to the
		// GROUP's chat id — every member would then see the card and be able to cancel
		// the plan or start a purchase on it. The Mini App button is invalid outside a
		// private chat anyway, so the menu could never arrive there.
		if u.Message.Chat.Type != "" && u.Message.Chat.Type != "private" {
			return
		}
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

// lang resolves one chat's language from the subscriber record. Telegram hands us
// the client's interface language on first contact and trackSubscriber stores it,
// so every reply — including one sent long after that contact — can be written in
// it without asking. An unknown chat falls back to the reference language.
func (s *UserService) lang(chatID int64) i18n.Lang {
	sub, err := s.store.SubscriberByChat(chatID)
	if err != nil || sub == nil {
		return i18n.Default
	}
	return i18n.Normalize(sub.Lang)
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
	// Handled before the pending-state machine so an explicit command always wins:
	// someone half-way through registration must still be able to open this, and
	// doing so must not eat the step they were on.
	if cmd == "/mailing" {
		s.showMailing(ctx, client, chatID, 0)
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
	lang := s.lang(chatID)
	want := strings.TrimSpace(set.TGUserRegCode)
	if !set.RegistrationOpen() || set.RegMode() != model.RegInvite || want == "" {
		s.sendWelcome(ctx, client, set, chatID)
		return
	}
	// Spend an attempt before checking. Guessing must cost something: the comparison
	// below is constant-time, but nothing else bounded how many codes one chat could
	// try, and a hit mints a real account on the trial plan. Charged on every attempt
	// rather than only on failures, so a correct guess mixed into a run of wrong ones
	// doesn't buy the attacker a fresh budget.
	if allowed, _ := s.codeRate.allow(chatID, time.Now()); !allowed {
		s.send(ctx, client, chatID, i18n.T(lang, "user.tooManyAttempts"))
		s.clearPending(chatID)
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(want)) != 1 {
		s.send(ctx, client, chatID, i18n.T(lang, "user.badInvite"))
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
	lang := s.lang(chatID)
	if !set.RegistrationOpen() {
		s.sendMenu(ctx, client, chatID,
			i18n.T(lang, "user.regClosedWelcome"),
			supportOnlyRows(set, lang))
		return
	}
	hint := i18n.T(lang, "user.hintOpen")
	switch set.RegMode() {
	case model.RegModeration:
		hint = i18n.T(lang, "user.hintModeration")
	case model.RegInvite:
		hint = i18n.T(lang, "user.hintInvite")
	}
	s.sendMenu(ctx, client, chatID, i18n.T(lang, "user.welcome")+"\n\n"+hint, welcomeRows(set, lang))
}

// welcomeRows is the pre-registration keyboard. Support is offered here too: someone
// who can't get past registration — wrong invite code, waiting on moderation — is
// exactly the person who needs to reach a human, and they have no menu to reach it
// from.
func welcomeRows(set *model.Settings, lang i18n.Lang) [][]InlineButton {
	rows := [][]InlineButton{{{Text: i18n.T(lang, "user.btnRegister"), CallbackData: "vu:reg"}}}
	return append(rows, supportOnlyRows(set, lang)...)
}

// supportOnlyRows is the support link on its own, or no rows at all when support
// isn't configured.
func supportOnlyRows(set *model.Settings, lang i18n.Lang) [][]InlineButton {
	if link := set.SupportLink(); link != "" {
		return [][]InlineButton{{{Text: i18n.T(lang, "user.btnSupport"), URL: link}}}
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
	// The nil check comes FIRST: a callback can arrive without a message (an inline
	// result has no chat), and the language lookup below dereferences it. The panic was
	// caught by the loop's recover, so the only symptom was a dropped update.
	if cb.Message == nil {
		_ = client.AnswerCallback(ctx, cb.ID, "")
		return
	}
	lang := s.lang(cb.Message.Chat.ID)
	_ = client.AnswerCallback(ctx, cb.ID, "")
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
				i18n.T(lang, "user.regClosed"), [][]InlineButton{})
			return
		}
		// Invite mode: ask for the code first; the account is created only once it matches.
		if set.RegMode() == model.RegInvite {
			s.setPending(chatID, "regcode")
			s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.enterInvite"),
				[][]InlineButton{{{Text: i18n.T(lang, "user.btnCancel"), CallbackData: "vu:cancel"}}})
			return
		}
		// Name is taken automatically from the Telegram profile (first name, or the
		// numeric Telegram id when it's empty) — no manual entry needed.
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.creatingAccount"), [][]InlineButton{})
		s.doRegister(ctx, client, chatID, set, tgDisplayName(cb.From, chatID))
	case "vu:cancel":
		s.clearPending(chatID)
		s.sendWelcome(ctx, client, set, chatID)
	}
}

func (s *UserService) doRegister(ctx context.Context, client *Client, chatID int64, set *model.Settings, name string) {
	lang := s.lang(chatID)
	name = strings.TrimSpace(name)
	if name == "" {
		s.send(ctx, client, chatID, i18n.T(lang, "user.emptyName"))
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
		s.send(ctx, client, chatID, i18n.T(lang, "user.regClosed"))
		return
	}
	// A chat that already has a pending moderated request must not re-tap its way
	// through the global rate limit (or spam admins) — short-circuit before both.
	if set.RegMode() == model.RegModeration && s.panel.RegistrationPending(chatID) {
		s.send(ctx, client, chatID, i18n.T(lang, "user.requestPending"))
		return
	}
	if !s.allowRegistration(time.Now()) {
		s.send(ctx, client, chatID, i18n.T(lang, "user.tooManySignups"))
		return
	}
	// Moderation: don't create an account — file a request an admin must approve. No
	// bot access is granted until then.
	if set.RegMode() == model.RegModeration {
		ok, err := s.panel.RequestRegistration(ctx, chatID, name)
		if err != nil {
			s.send(ctx, client, chatID, i18n.T(lang, "user.requestFailed", esc(err.Error())))
			return
		}
		if !ok {
			s.send(ctx, client, chatID, i18n.T(lang, "user.requestPending"))
			return
		}
		s.send(ctx, client, chatID,
			i18n.T(lang, "user.requestSent"))
		return
	}
	// Open / invite: create the account and show its menu right away. CreateRegistered
	// User applies the trial/free plan when billing is on, else a plain account.
	u, err := s.panel.CreateRegisteredUser(ctx, name)
	if err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.createFailed", esc(err.Error())))
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.linkFailed", esc(err.Error())))
		return
	}
	log.Printf("telegram user: registered user %d from chat %d", u.ID, chatID)
	s.panel.AuditTelegramLinked(ctx, u.ID, actorFromCtxName(ctx))
	u.TgChatID = chatID
	s.sendMenu(ctx, client, chatID,
		i18n.T(lang, "user.accountCreated")+"\n\n"+userSelfCard(*u, set, s.panel, lang),
		userMenuRows(set, *u, lang))
}

// restoreDetachedUser reattaches an account this chat previously unlinked (if any)
// and shows its menu, returning the restored user. Returns nil when the chat has no
// detached account to restore, so the caller falls through to normal registration.
func (s *UserService) restoreDetachedUser(ctx context.Context, client *Client, chatID int64, set *model.Settings) *model.User {
	lang := s.lang(chatID)
	u, err := s.store.GetDetachedUserByPrevChat(chatID)
	if err != nil || u == nil {
		return nil
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.restoreFailed", esc(err.Error())))
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
		i18n.T(lang, "user.welcomeBack")+"\n\n"+userSelfCard(*u, set, s.panel, lang),
		userMenuRows(set, *u, lang))
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
	lang := s.lang(chatID)
	u, err := s.store.GetUserByTgLinkCode(code)
	if err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.codeInvalid"))
		return
	}
	if u.TgChatID != 0 && u.TgChatID != chatID {
		s.send(ctx, client, chatID, i18n.T(lang, "user.alreadyLinked"))
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.linkChatFailed", esc(err.Error())))
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

func userMenuRows(set *model.Settings, u model.User, lang i18n.Lang) [][]InlineButton {
	var rows [][]InlineButton
	// A Mini App button opens the subscription page inside Telegram (QR, link,
	// import buttons — all on one page). Needs an https:// URL, so it's skipped
	// until the host is set.
	if url := subWebAppURL(set, u); url != "" {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnMySub"), WebApp: &WebAppInfo{URL: url}}})
	}
	if set.BillingEnabled {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnPlans"), CallbackData: "vu:plans"}})
	}
	// Support lives in its own bot, so this is a plain link out. Empty when support is
	// off or its @username never resolved — a dead button is worse than none.
	if link := set.SupportLink(); link != "" {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnSupport"), URL: link}})
	}
	// No self-service unlink. It only ever cost the person their access — the
	// account survives, but they land back on the welcome screen and write to
	// support to get it back — while an operator who genuinely needs to detach a
	// chat already has the button in the user's card in the panel.
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnRefresh"), CallbackData: "vu:menu"}})
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
func userSelfCard(u model.User, set *model.Settings, panel Panel, lang i18n.Lang) string {
	loc := panel.Location()
	now := time.Now().Unix()
	var b strings.Builder

	fmt.Fprintf(&b, "👤 <b>%s</b> · <code>#%d</code>\n\n", esc(u.Name), u.ID)
	fmt.Fprintf(&b, "%s\n", userStatusLine(u.Status, lang))

	// Plan (only when billing is in play).
	if u.PlanID != 0 {
		if name := panel.PlanName(u.PlanID); name != "" {
			fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardPlan", esc(name)))
		}
	} else if set.BillingEnabled {
		b.WriteString(i18n.T(lang, "user.cardPlanManual") + "\n")
	}

	// Expiry + remaining time.
	if u.ExpireAt > 0 {
		exp := time.Unix(u.ExpireAt, 0).In(loc).Format("02.01.2006")
		if u.ExpireAt > now {
			fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardUntil", exp, humanLeft(u.ExpireAt-now, lang)))
		} else {
			fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardExpiredOn", exp))
		}
	} else {
		b.WriteString(i18n.T(lang, "user.cardNoExpiry") + "\n")
	}

	// Traffic.
	used := formatBytes(u.UsedUp + u.UsedDown)
	if u.DataLimit > 0 {
		pct := int(min(100, (u.UsedUp+u.UsedDown)*100/u.DataLimit))
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardTraffic", used, formatBytes(u.DataLimit), pct))
	} else {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardTrafficUnlimited", used))
	}

	// Devices. The panel counts two independent kinds of "device" and either can be
	// in force: the IP-based limit (distinct source IPs, enforced when device_limit is
	// set) and HWID binding (distinct installs, enforced when the feature is on). Show
	// a line for each that applies, labelled so they don't read as one number — or, when
	// only one is active, just that one.
	ipLimited := u.DeviceLimit > 0
	if ipLimited && set.HWIDEnabled {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardDevicesIP", u.ActiveDevices, u.DeviceLimit))
		writeHWIDDeviceLine(&b, set, u, panel, lang, true)
	} else if ipLimited {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardDevices", u.ActiveDevices, u.DeviceLimit))
	} else if set.HWIDEnabled {
		writeHWIDDeviceLine(&b, set, u, panel, lang, false)
	}

	b.WriteString(userOnlineLine(u, now, loc, lang))
	return strings.TrimRight(b.String(), "\n")
}

// writeHWIDDeviceLine appends the HWID-bound device count. labeled distinguishes it
// as "(HWID)" when the IP line is shown alongside; on its own it reads as the plain
// "Devices" line. A zero cap (HWID on but no limit set) drops the "of N".
func writeHWIDDeviceLine(b *strings.Builder, set *model.Settings, u model.User, panel Panel, lang i18n.Lang, labeled bool) {
	count := panel.DeviceCount(u.ID)
	capacity := set.DeviceCap(u)
	var key string
	switch {
	case labeled && capacity > 0:
		key = "user.cardDevicesHWID"
	case labeled:
		key = "user.cardDevicesHWIDNoLimit"
	case capacity > 0:
		key = "user.cardDevices"
	default:
		key = "user.cardDevicesNoLimit"
	}
	if capacity > 0 {
		fmt.Fprintf(b, "%s\n", i18n.T(lang, key, count, capacity))
	} else {
		fmt.Fprintf(b, "%s\n", i18n.T(lang, key, count))
	}
}

// userStatusLine renders a friendly, emoji-led status for the user card.
func userStatusLine(status string, lang i18n.Lang) string {
	switch status {
	case model.StatusActive:
		return i18n.T(lang, "user.stActive")
	case model.StatusExpired:
		return i18n.T(lang, "user.stExpired")
	case model.StatusLimited:
		return i18n.T(lang, "user.stLimited")
	case model.StatusDeviceLimited:
		return i18n.T(lang, "user.stDeviceLimited")
	case model.StatusDisabled:
		return i18n.T(lang, "user.stDisabled")
	default:
		return "▫️ " + esc(status)
	}
}

// humanLeft renders remaining time as "N days/hours/minutes left".
func humanLeft(sec int64, lang i18n.Lang) string {
	if d := sec / 86400; d >= 1 {
		return i18n.T(lang, "user.leftDays", d)
	}
	if h := sec / 3600; h >= 1 {
		return i18n.T(lang, "user.leftHours", h)
	}
	return i18n.T(lang, "user.leftMinutes", sec/60)
}

// userOnlineLine renders the last-seen state in human terms.
func userOnlineLine(u model.User, now int64, loc *time.Location, lang i18n.Lang) string {
	if u.LastSeen == 0 {
		return i18n.T(lang, "user.neverConnected")
	}
	diff := now - u.LastSeen
	switch {
	case diff < 120:
		return i18n.T(lang, "user.onlineNow")
	case diff < 3600:
		return i18n.T(lang, "user.seenMinutes", diff/60)
	case diff < 86400:
		return i18n.T(lang, "user.seenHours", diff/3600)
	case diff < 7*86400:
		return i18n.T(lang, "user.seenDays", diff/86400)
	default:
		return i18n.T(lang, "user.seenOn", time.Unix(u.LastSeen, 0).In(loc).Format("02.01.2006"))
	}
}

func (s *UserService) sendUserMenu(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.sendMenu(ctx, client, chatID, userSelfCard(u, set, s.panel, lang), userMenuRows(set, u, lang))
}

func (s *UserService) editUserMenu(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID, userSelfCard(u, set, s.panel, lang), userMenuRows(set, u, lang))
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
	// "vu:unlink"/"vu:unlinkyes" are gone. Old menus still carrying those buttons
	// fall through to the default branch and do nothing, which is the intended
	// outcome — the alternative is honouring a detach the panel no longer offers.
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
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	active := s.panel.ActivePaidPlan(u)
	if active == nil {
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.noActiveSub"),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}}})
		return
	}
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "user.cancelConfirm", esc(active.Name)),
		[][]InlineButton{
			{{Text: i18n.T(lang, "user.btnCancelYes"), CallbackData: "vu:cancelyes"}},
			{{Text: i18n.T(lang, "user.btnCancel"), CallbackData: "vu:plans"}},
		})
}

// doCancelPlan cancels the active paid plan (→ free plan) and returns to the menu.
func (s *UserService) doCancelPlan(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
	if err := s.panel.CancelUserPlan(ctx, u.ID); err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}}})
		return
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "user.subCancelled")+"\n\n"+userSelfCard(u, set, s.panel, lang),
		userMenuRows(set, u, lang))
}

// showPlans presents the billing options. While a paid plan is active only renewal
// and cancellation are offered (no switching); otherwise the paid tariffs are listed
// for purchase. Free/trial plans are never self-selectable here.
func (s *UserService) showPlans(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
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
			i18n.T(lang, "user.planActive", esc(active.Name), planActiveUntil(u, s.panel, lang)),
			[][]InlineButton{
				{{Text: i18n.T(lang, "user.btnRenewPlan", active.Name), CallbackData: fmt.Sprintf("vu:buy:%d", active.ID)}},
				{{Text: i18n.T(lang, "user.btnCancelSub"), CallbackData: "vu:cancelplan"}},
				{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}},
			})
		return
	}
	plans, err := s.panel.ListTariffPlans(false)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}}})
		return
	}
	var rows [][]InlineButton
	for _, p := range plans {
		if p.IsFree() {
			continue // paid plans only
		}
		rows = append(rows, []InlineButton{{
			Text:         planButtonLabel(p, lang),
			CallbackData: fmt.Sprintf("vu:buy:%d", p.ID),
		}})
	}
	if len(rows) == 0 {
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.noPlans"),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}}})
		return
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}})
	msg := i18n.T(lang, "user.plansTitle") + "\n\n"
	if len(s.panel.PaymentMethods()) > 0 {
		msg += i18n.T(lang, "user.plansAuto")
	} else {
		msg += i18n.T(lang, "user.plansManual")
	}
	s.edit(ctx, client, chatID, msgID, msg, rows)
}

// planActiveUntil renders " until DD.MM.YYYY" for a user's paid expiry (empty if none).
func planActiveUntil(u model.User, panel Panel, lang i18n.Lang) string {
	if u.ExpireAt <= 0 {
		return ""
	}
	return " " + i18n.T(lang, "user.untilDate", time.Unix(u.ExpireAt, 0).In(panel.Location()).Format("02.01.2006"))
}

// providerButton is the pay-method button text: a wallet icon plus the provider's
// registry label (so a new provider needs no change here).
func (s *UserService) providerButton(key string) string {
	return "💳 " + s.panel.ProviderLabel(key)
}

func (s *UserService) handleBuyPlan(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, planIDStr string) {
	lang := s.lang(chatID)
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
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}})
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.pickPayMethod"), rows)
	}
}

// startProviderPayment creates a provider payment and shows the pay button. The
// tariff is applied automatically once the provider confirms (webhook/poll).
func (s *UserService) startProviderPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planIDStr, provider string) {
	lang := s.lang(chatID)
	var planID int64
	if _, err := fmt.Sscan(planIDStr, &planID); err != nil || planID <= 0 {
		return
	}
	order, err := s.panel.StartPlanPayment(ctx, lang, u.ID, planID, provider)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{
				{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}},
				{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
			})
		return
	}
	msg := i18n.T(lang, "user.orderPay", order.ID, order.AmountRub)
	s.edit(ctx, client, chatID, msgID, msg,
		[][]InlineButton{
			{{Text: i18n.T(lang, "user.btnPay"), URL: order.PayURL},
				{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
		})
}

func (s *UserService) manualPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planID int64) {
	lang := s.lang(chatID)
	_, msg, err := s.panel.RequestPlanPayment(ctx, lang, u.ID, planID)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()),
			[][]InlineButton{
				{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}},
				{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
			})
		return
	}
	s.edit(ctx, client, chatID, msgID, esc(msg),
		[][]InlineButton{
			{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}},
			{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
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
func mailingCard(optOut bool, lang i18n.Lang) (string, [][]InlineButton) {
	if optOut {
		return i18n.T(lang, "user.mailingOff"),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnSubscribe"), CallbackData: "vu:mail:on"}}}
	}
	return i18n.T(lang, "user.mailingOn"),
		[][]InlineButton{{{Text: i18n.T(lang, "user.btnUnsubscribe"), CallbackData: "vu:mail:off"}}}
}

// showMailing displays the toggle. msgID 0 sends a new message; otherwise the card
// is edited in place, like the rest of the bot's screens.
func (s *UserService) showMailing(ctx context.Context, client *Client, chatID, msgID int64) {
	lang := s.lang(chatID)
	optOut := false
	if sub, err := s.store.SubscriberByChat(chatID); err != nil {
		log.Printf("telegram user: mailing state for %d: %v", chatID, err)
	} else if sub != nil {
		optOut = sub.OptOut
	}
	text, rows := mailingCard(optOut, lang)
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

// userBotCommands is the command menu published to Telegram. One entry, not three:
// the card it opens shows the current state and the single button that flips it, so
// naming each direction as its own command only made the menu longer without telling
// anyone anything the card doesn't.
func userBotCommands(lang i18n.Lang) []BotCommand {
	return []BotCommand{
		{Command: "start", Description: i18n.T(lang, "user.cmdStart")},
		{Command: "mailing", Description: i18n.T(lang, "user.cmdMailing")},
	}
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
	// Telegram picks a command menu by the client's interface language, and falls
	// back to the unscoped one for a language nothing was published for. That has to
	// agree with i18n.Normalize, which words every message the bot sends: anything
	// not recognisably Russian is answered in English, so English is what the
	// fallback scope holds. Russian is published under the tags Normalize treats as
	// Russian, or a Belarusian user would read Russian messages under an English menu.
	//
	// The English set is published to its own scope as well as the fallback. Dropping
	// that call would leave whatever was last written to language_code=en in place —
	// a scope match beats the fallback, so a stale English menu would outrank the
	// fresh one, and only for English users.
	for _, pub := range []struct {
		lang  i18n.Lang
		scope string
	}{
		{i18n.EN, ""}, // fallback: every language nothing is published for
		{i18n.EN, "en"},
		{i18n.RU, "ru"},
		{i18n.RU, "be"},
		{i18n.RU, "uk"},
	} {
		if err := client.SetMyCommands(ctx, userBotCommands(pub.lang), pub.scope); err != nil {
			log.Printf("telegram user: publish commands (scope %q): %v", pub.scope, err)
			return // not latched: retried next cycle
		}
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
