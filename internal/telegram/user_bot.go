package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/core"
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
	pending     map[int64]string // chatID → "reg" (awaiting display name), "promo", "topup"

	// The bot's own @username (for invite links), and when and for which token it
	// was looked up.
	meName  string
	meAt    time.Time
	meToken string

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
	// Let the panel push messages to a user's chat via this bot — off the goroutine
	// that raised them (see notifyqueue.go): these come from the traffic poll and the
	// payment path, where a sweep can touch many users at once.
	q := newNotifyQueue("user bot")
	q.run(ctx, 4)
	s.panel.SetUserNotifier(func(chatID int64, html string) {
		q.submit(func(ctx context.Context) {
			set, err := s.store.GetSettings()
			if err != nil || strings.TrimSpace(set.TGUserBotToken) == "" {
				return
			}
			c := NewClient(strings.TrimSpace(set.TGUserBotToken), set.TelegramProxyURL())
			if err := c.SendMessage(ctx, chatID, html); err != nil {
				log.Printf("telegram: user notify to %d failed: %v", chatID, err)
			}
		})
	})
	s.panel.SetUserMessenger(func(chatID int64, html string, buttons []model.BroadcastButton) error {
		set, err := s.store.GetSettings()
		if err != nil {
			return err
		}
		if strings.TrimSpace(set.TGUserBotToken) == "" {
			return fmt.Errorf("the user bot has no token")
		}
		c := NewClient(strings.TrimSpace(set.TGUserBotToken), set.TelegramProxyURL())
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err = c.SendMenu(sendCtx, chatID, html, broadcastRows(buttons))
		if err != nil && isBlockedByUser(err) {
			_ = s.store.SetSubscriberBlocked(chatID, time.Now().Unix())
			return nil
		}
		return err
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
		updates, err := client.GetUpdatesFor(ctx, s.offset, pollTimeout, userBotUpdates)
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

// userBotUpdates adds the Stars pre-checkout to what a bot gets by default.
var userBotUpdates = []string{"message", "callback_query", "pre_checkout_query"}

func (s *UserService) handle(ctx context.Context, client *Client, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram user: handler panic recovered: %v", r)
		}
	}()
	// Payments before the rate gate: a paid invoice must never be dropped as a flood,
	// and a pre-checkout left unanswered for ten seconds fails the payment.
	if u.PreCheckout != nil {
		// Answered off the loop: Telegram gives ten seconds, and the update before it
		// in the batch may be a provider call that takes longer.
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("telegram user: pre-checkout panic recovered: %v", r)
				}
			}()
			s.preCheckout(ctx, client, u.PreCheckout)
		}()
		return
	}
	if u.Message != nil && u.Message.SuccessfulPayment != nil {
		s.starsPaid(ctx, client, u.Message)
		return
	}
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
		switch pending {
		case "reg":
			s.doRegister(ctx, client, chatID, set, text)
		case "promo":
			s.doPromo(ctx, client, chatID, set, u, text)
		case "topup":
			s.doTopupAmount(ctx, client, chatID, set, u, text)
		default:
			s.sendUserMenu(ctx, client, chatID, set, u)
		}
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
	// /start is the way out of any prompt (an amount, a promo code): the next text is
	// not an answer to it any more.
	s.clearPending(chatID)
	if len(args) >= 1 {
		if code := userStartLinkCode(args[0]); code != "" {
			s.linkUserFromCode(ctx, client, set, chatID, code)
			return
		}
		// An invite link: remembered for when this chat registers. A chat that already
		// has an account keeps the referrer it had (or none). Any other payload is a
		// source tag — which ad or post the person came from — kept the same way.
		if _, linked := s.findLinkedUser(chatID); !linked {
			arg := strings.TrimSpace(args[0])
			if code, ok := strings.CutPrefix(arg, refStartPrefix); ok {
				if code != "" {
					s.panel.TrackReferral(chatID, code)
				}
			} else {
				s.panel.TrackSource(chatID, arg)
			}
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
	// A request already waiting: say so, and no "sign up" to press again.
	if set.RegMode() == model.RegModeration && s.panel.RegistrationPending(chatID) {
		s.sendMenu(ctx, client, chatID, i18n.T(lang, "user.requestPending"), supportOnlyRows(set, lang))
		return
	}
	hint := i18n.T(lang, "user.hintOpen")
	switch set.RegMode() {
	case model.RegModeration:
		hint = i18n.T(lang, "user.hintModeration")
	case model.RegInvite:
		hint = i18n.T(lang, "user.hintInvite")
	}
	text := i18n.T(lang, "user.welcome") + "\n\n" + hint
	if line := s.legalAcceptLine(set, lang); line != "" {
		text += "\n\n" + line
	}
	s.sendMenu(ctx, client, chatID, text, welcomeRows(set, lang))
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
	// A new Telegram taking over an account (changed from the subscription page).
	if code, ok := strings.CutPrefix(cb.Data, relinkPrefix); ok {
		s.linkByCode(ctx, client, set, chatID, code, true)
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
		creating := "user.creatingAccount"
		if set.RegMode() == model.RegModeration {
			creating = "user.sendingRequest" // no account yet: a request for the operator
		}
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, creating), [][]InlineButton{})
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
	if s.panel.RegistrationBlacklisted(chatID) {
		log.Printf("telegram user: registration refused to blacklisted chat %d", chatID)
		s.send(ctx, client, chatID, i18n.T(lang, "user.regRefused"))
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
			s.send(ctx, client, chatID, i18n.T(lang, "user.requestFailed", esc(core.UserError(err, lang))))
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
	// One trial per Telegram: a chat that signed up before — its account since moved
	// to another Telegram, deleted, whatever — gets an account and can buy, but a new
	// trial each time would never end.
	trial := !s.store.ChatHadTrial(chatID)
	u, err := s.panel.CreateRegisteredUser(ctx, name, trial)
	if err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.createFailed", esc(core.UserError(err, lang))))
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.linkFailed", esc(core.UserError(err, lang))))
		return
	}
	_ = s.store.MarkChatTrial(chatID)
	log.Printf("telegram user: registered user %d from chat %d", u.ID, chatID)
	s.panel.AuditTelegramLinked(ctx, u.ID, actorFromCtxName(ctx))
	s.panel.AttachReferrer(ctx, u.ID, chatID)
	u.TgChatID = chatID
	created := i18n.T(lang, "user.accountCreated")
	if !trial && set.BillingEnabled {
		created += "\n\n" + i18n.T(lang, "user.noTrialAgain")
	}
	s.sendMenu(ctx, client, chatID,
		created+"\n\n"+userSelfCard(*u, set, s.panel, lang),
		s.menuRows(set, *u, lang))
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
		s.send(ctx, client, chatID, i18n.T(lang, "user.restoreFailed", esc(core.UserError(err, lang))))
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
		s.menuRows(set, *u, lang))
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
	s.linkByCode(ctx, client, set, chatID, code, false)
}

// linkByCode binds the account the code belongs to to this chat. A move is asked
// about first (confirmed=false), both ways it can go:
//   - this chat belongs to another account: the move takes the bot away from that
//     one — its menu, its reminders, its payment notices — and a person with an old
//     bot account and a new one from the website would not notice;
//   - the account is linked to another Telegram (its owner changing Telegram from
//     the subscription page): the old Telegram loses the account, and is told so.
func (s *UserService) linkByCode(ctx context.Context, client *Client, set *model.Settings, chatID int64, code string, confirmed bool) {
	lang := s.lang(chatID)
	u, err := s.store.GetUserByTgLinkCode(code)
	if err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.codeInvalid"))
		return
	}
	oldChat := int64(0)
	if u.TgChatID != 0 && u.TgChatID != chatID {
		// Moving a linked account to another Telegram is the operator's to allow.
		if !set.SubTGRebind {
			s.send(ctx, client, chatID, i18n.T(lang, "user.alreadyLinked"))
			return
		}
		oldChat = u.TgChatID
	}
	cur, curOK := s.findLinkedUser(chatID)
	here := curOK && cur.ID != u.ID
	if !confirmed && (oldChat != 0 || here) {
		var text string
		switch {
		case oldChat != 0 && here:
			text = i18n.T(lang, "user.rebindAsk", esc(u.Name)) + "\n\n" + i18n.T(lang, "user.relinkAlsoHere", esc(cur.Name))
		case oldChat != 0:
			text = i18n.T(lang, "user.rebindAsk", esc(u.Name))
		default:
			text = i18n.T(lang, "user.relinkAsk", esc(cur.Name), esc(u.Name))
		}
		// "Keep it as it is" goes back to where this chat was: its account's menu, or
		// the welcome screen for a chat with none.
		keep := "vu:cancel"
		if curOK {
			keep = "vu:menu"
		}
		s.sendMenu(ctx, client, chatID, text, [][]InlineButton{
			{{Text: i18n.T(lang, "user.btnRelink", u.Name), CallbackData: relinkPrefix + code}},
			{{Text: i18n.T(lang, "user.btnRelinkKeep"), CallbackData: keep}},
		})
		return
	}
	// Already this chat's (the page's button pressed in the Telegram it is linked
	// to): nothing to link, and nothing to record.
	if u.TgChatID == chatID {
		s.sendUserMenu(ctx, client, chatID, set, *u)
		return
	}
	// Spend the code before acting on it: two chats confirming the same one at once
	// must not both move the account.
	if ok, err := s.store.ClaimUserTgLinkCode(u.ID, code); err != nil || !ok {
		s.send(ctx, client, chatID, i18n.T(lang, "user.codeInvalid"))
		return
	}
	if err := s.store.SetUserTelegramChat(u.ID, chatID); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "user.linkChatFailed", esc(core.UserError(err, lang))))
		return
	}
	// The broadcast audiences follow the account: this chat holds it now, the one it
	// left does not.
	_ = s.store.SetSubscriberUser(chatID, u.ID)
	if oldChat != 0 {
		_ = s.store.SetSubscriberUser(oldChat, 0)
	}
	log.Printf("telegram user: user %d linked to chat %d via link code", u.ID, chatID)
	// The account this chat held loses it: the store detached it in the same write.
	if here {
		s.panel.AuditTelegramDetached(ctx, cur.ID, chatID)
	}
	// Moved off another Telegram: that one is gone from the account.
	if oldChat != 0 {
		s.panel.AuditTelegramDetached(ctx, u.ID, oldChat)
	}
	s.panel.AuditTelegramLinked(ctx, u.ID, actorFromCtxName(ctx))
	// Taken from another Telegram: the subscription link is reissued, so a link that
	// leaked — the way someone else got to move the account — works no more, here or
	// in the apps. The new Telegram gets the new link; the one the account left is
	// told, which is where its owner learns of a move they did not make.
	if oldChat != 0 {
		if nu, err := s.panel.RotateSubToken(ctx, u.ID); err != nil {
			log.Printf("telegram user: reissuing the link of user %d after a move: %v", u.ID, err)
		} else {
			u = nu
			if link := subWebAppURL(set, *u); link != "" {
				s.send(ctx, client, chatID, i18n.T(lang, "user.rebindNewLink", link))
			}
		}
		s.send(ctx, client, oldChat, i18n.T(s.lang(oldChat), "user.rebindNotice", esc(u.Name)))
	}
	u.TgChatID = chatID
	s.sendUserMenu(ctx, client, chatID, set, *u)
}

// relinkPrefix is the callback that confirms moving this chat to the account a link
// code belongs to.
const relinkPrefix = "vu:relink:"

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
	} else if u.HoldSeconds > 0 {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "user.cardHold", i18n.TN(lang, "notify.days", int(u.HoldSeconds/86400))))
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
	s.sendMenu(ctx, client, chatID, userSelfCard(u, set, s.panel, lang), s.menuRows(set, u, lang))
}

func (s *UserService) editUserMenu(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID, userSelfCard(u, set, s.panel, lang), s.menuRows(set, u, lang))
}

func (s *UserService) handleUserCallback(ctx context.Context, client *Client, cb *CallbackQuery, set *model.Settings, u model.User) {
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	// A button pressed leaves whatever prompt was open (a promo code, an amount);
	// the buttons that open one set it again below.
	s.clearPending(chatID)
	switch cb.Data {
	case "vu:menu":
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
	case "vu:legal":
		s.showLegal(ctx, client, chatID, msgID, set)
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
		if code, ok := strings.CutPrefix(cb.Data, relinkPrefix); ok {
			s.linkByCode(ctx, client, set, chatID, code, true)
			return
		}
		if s.handleWalletCallback(ctx, client, chatID, msgID, set, u, cb.Data) {
			return
		}
		if s.handlePurchaseCallback(ctx, client, chatID, msgID, set, u, cb.Data) {
			return
		}
		if planStr, ok := strings.CutPrefix(cb.Data, "vu:buy:"); ok {
			// "vu:buy:<plan>" asks for the term when several are sold;
			// "vu:buy:<plan>:<periods>" is the term chosen, and
			// "vu:buy:<plan>:<periods>:<devices>" the extra devices too.
			parts := strings.Split(planStr, ":")
			planID, _ := strconv.ParseInt(parts[0], 10, 64)
			periods, devices := 1, -1
			if len(parts) >= 2 {
				periods, _ = strconv.Atoi(parts[1])
			}
			if len(parts) >= 3 {
				devices, _ = strconv.Atoi(parts[2])
			}
			if devices > 0 {
				s.offerPurchase(ctx, client, chatID, msgID, u, core.Purchase{
					Kind: model.OrderPlan, PlanID: planID, Periods: max(periods, 1), Devices: devices})
				return
			}
			s.handleBuyPlan(ctx, client, chatID, msgID, set, u, planID, max(periods, 1), len(parts) < 2, devices == 0)
		} else if rest, ok := strings.CutPrefix(cb.Data, "vu:pay:"); ok {
			// rest = "<method>:<planID>[:<periods>]", the method being a provider key
			// or manual
			parts := strings.Split(rest, ":")
			if len(parts) >= 2 {
				planID, err := strconv.ParseInt(parts[1], 10, 64)
				periods := 1
				if len(parts) >= 3 {
					periods, _ = strconv.Atoi(parts[2])
				}
				if err == nil && planID > 0 {
					s.startPayment(ctx, client, chatID, msgID, u, planID, max(periods, 1), parts[0])
				}
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
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}}})
		return
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "user.subCancelled")+"\n\n"+userSelfCard(u, set, s.panel, lang),
		s.menuRows(set, u, lang))
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
		var rows [][]InlineButton
		// A lifetime plan has nothing to renew.
		if active.PeriodDays > 0 {
			rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnRenewPlan", active.Name), CallbackData: fmt.Sprintf("vu:buy:%d", active.ID)}})
		}
		if len(s.panel.ChangeOffers(u)) > 0 {
			rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnChangePlan"), CallbackData: "vu:chg"}})
		}
		if s.panel.Addons(u).Any() {
			rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnAddons"), CallbackData: "vu:add"}})
		}
		rows = append(rows,
			[]InlineButton{{Text: i18n.T(lang, "user.btnCancelSub"), CallbackData: "vu:cancelplan"}},
			[]InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}},
		)
		s.edit(ctx, client, chatID, msgID,
			i18n.T(lang, "user.planActive", esc(active.Name), planActiveUntil(u, s.panel, lang)), rows)
		return
	}
	plans, err := s.panel.ListTariffPlans(false)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}}})
		return
	}
	// With no way to pay, only what the balance covers can be bought.
	canPay := len(s.payMethods()) > 0
	var rows [][]InlineButton
	for _, p := range plans {
		if p.IsFree() {
			continue // paid plans only
		}
		q := s.panel.QuotePlan(u, &p)
		if !canPay && q.MoneyRub > 0 {
			continue
		}
		// A discount code the user entered, or devices held, show in the price it buys.
		label := planButtonLabel(p, lang)
		if q.DiscountRub > 0 || q.Devices > 0 {
			shown := p
			shown.PriceRub = q.TotalRub
			label = "🏷 " + planButtonLabel(shown, lang)
		}
		rows = append(rows, []InlineButton{{
			Text:         label,
			CallbackData: fmt.Sprintf("vu:buy:%d", p.ID),
		}})
	}
	if len(rows) == 0 {
		empty := i18n.T(lang, "user.noPlans")
		if !canPay {
			empty = i18n.T(lang, "user.noPayMethod")
		}
		s.edit(ctx, client, chatID, msgID, empty,
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}}})
		return
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}})
	msg := i18n.T(lang, "user.plansTitle") + "\n\n"
	if set.WalletEnabled {
		if w, err := s.panel.Wallet(u.ID); err == nil && w.BalanceKop > 0 {
			msg += i18n.T(lang, "user.plansBalance", kop(w.BalanceKop)) + "\n\n"
		}
	}
	switch {
	case len(s.panel.PaymentMethods()) > 0:
		msg += i18n.T(lang, "user.plansAuto")
	case s.panel.ManualPayment():
		msg += i18n.T(lang, "user.plansManual")
	}
	s.edit(ctx, client, chatID, msgID, strings.TrimSpace(msg), rows)
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
func (s *UserService) providerButton(lang i18n.Lang, key string) string {
	if key == sub.ManualPayKey {
		return "💳 " + s.panel.ManualPaymentLabel(lang)
	}
	return "💳 " + s.panel.ProviderLabel(key)
}

// payMethods are the methods the operator offers, manual first — the one that needs
// no setup, and the one a user falls back to when a provider refuses them.
func (s *UserService) payMethods() []string {
	methods := s.panel.PaymentMethods()
	if !s.panel.ManualPayment() {
		return methods
	}
	return append([]string{sub.ManualPayKey}, methods...)
}

// startPayment runs whichever method was chosen: the manual instructions, or the
// provider's own checkout.
func (s *UserService) startPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planID int64, periods int, method string) {
	if method == sub.ManualPayKey {
		s.manualPayment(ctx, client, chatID, msgID, u, planID, periods)
		return
	}
	s.startProviderPayment(ctx, client, chatID, msgID, u, planID, periods, method)
}

// devicesPicked: the extra devices were chosen (none), so they are not asked again.
func (s *UserService) handleBuyPlan(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, planID int64, periods int, pickTerm, devicesPicked bool) {
	lang := s.lang(chatID)
	if planID <= 0 {
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
		return
	}
	// The balance (and a discount code) may cover the whole price — then there is
	// nothing to pay with money, only a confirmation.
	extra := ""
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	if plan, err := s.store.GetTariffPlan(planID); err == nil && !plan.IsFree() {
		// Several terms on sale: ask which first. With nothing to pay money with, only
		// the terms the balance covers.
		offers := s.panel.PeriodOffers(u, plan)
		if len(s.payMethods()) == 0 {
			covered := offers[:0]
			for _, o := range offers {
				if o.MoneyRub == 0 {
					covered = append(covered, o)
				}
			}
			offers = covered
		}
		if pickTerm && len(offers) == 1 {
			periods = offers[0].Periods
		}
		// A new plan that sells extra devices asks how many, once the term is known; a
		// renewal keeps the ones held.
		if cur := s.panel.ActivePaidPlan(u); !(pickTerm && len(offers) > 1) && plan.SellsDevices() && !devicesPicked &&
			(cur == nil || cur.ID != plan.ID) {
			s.askDevices(ctx, client, chatID, msgID, plan, periods)
			return
		}
		if pickTerm && len(offers) > 1 {
			var rows [][]InlineButton
			for _, o := range offers {
				rows = append(rows, []InlineButton{{
					Text:         core.PeriodLabel(lang, plan, o),
					CallbackData: fmt.Sprintf("vu:buy:%d:%d", plan.ID, o.Periods),
				}})
			}
			rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}})
			s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.pickTerm", esc(plan.Name)), rows)
			return
		}
		q := s.panel.QuotePlanFor(u, plan, periods)
		if q.Periods != max(periods, 1) {
			// A term button from before the operator changed the offers: ask again
			// rather than sell one period for the price of the term tapped.
			s.handleBuyPlan(ctx, client, chatID, msgID, set, u, planID, 1, true, devicesPicked)
			return
		}
		if q.MoneyRub == 0 {
			s.confirmBalancePay(ctx, client, chatID, msgID, u, plan, q)
			return
		}
		extra = quoteLines(q, lang)
	}
	methods := s.payMethods()
	switch len(methods) {
	case 0:
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(i18n.T(lang, "user.noPayMethod")),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}}})
	case 1:
		s.startPayment(ctx, client, chatID, msgID, u, planID, periods, methods[0])
	default:
		var rows [][]InlineButton
		for _, p := range methods {
			rows = append(rows, []InlineButton{{Text: s.providerButton(lang, p), CallbackData: fmt.Sprintf("vu:pay:%s:%d:%d", p, planID, periods)}})
		}
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}})
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.pickPayMethod")+extra, rows)
	}
}

// startProviderPayment creates a provider payment and shows the pay button. The
// tariff is applied automatically once the provider confirms (webhook/poll).
func (s *UserService) startProviderPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planID int64, periods int, provider string) {
	lang := s.lang(chatID)
	if planID <= 0 {
		return
	}
	order, err := s.panel.StartPlanPayment(ctx, lang, u.ID, planID, provider, periods)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)),
			[][]InlineButton{
				{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}},
				{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
			})
		return
	}
	msg := i18n.T(lang, "user.orderPay", order.ID, order.AmountRub)
	if order.DiscountRub > 0 {
		msg += "\n" + i18n.T(lang, "user.quoteDiscount", esc(order.PromoCode), order.DiscountRub)
	}
	if order.BalanceKop > 0 {
		msg += "\n" + i18n.T(lang, "user.quoteBalance", kop(order.BalanceKop))
	}
	s.edit(ctx, client, chatID, msgID, msg,
		[][]InlineButton{
			{{Text: i18n.T(lang, "user.btnPay"), URL: order.PayURL},
				{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
		})
}

func (s *UserService) manualPayment(ctx context.Context, client *Client, chatID, msgID int64, u model.User, planID int64, periods int) {
	lang := s.lang(chatID)
	_, msg, err := s.panel.RequestPlanPayment(ctx, lang, u.ID, planID, periods)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)),
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
	if !s.publishMenuButton(ctx, client) {
		return // retried next cycle
	}
	s.mu.Lock()
	s.commandsFor = token
	s.mu.Unlock()
}

// publishMenuButton points the bot's menu button at the Mini App, so a user opens
// their subscription from any chat screen without a link carrying their token. A
// web_app button the operator set to somewhere else is left alone.
//
// Ours is the address this panel last set (settings.TGMenuURL), so a move to another
// host or path updates it; any other web_app address is the operator's own.
func (s *UserService) publishMenuButton(ctx context.Context, client *Client) bool {
	set, err := s.store.GetSettings()
	if err != nil {
		return false
	}
	url := sub.MiniAppURL(set)
	if url == "" {
		return true
	}
	cur, err := client.GetChatMenuButton(ctx)
	if err != nil {
		log.Printf("telegram user: read the menu button: %v", err)
		return false
	}
	if cur.Type == "web_app" && cur.WebApp != nil {
		if cur.WebApp.URL == url {
			return true
		}
		// Ours is the address last set, or anything under this panel's subscription
		// path (an older build set a fixed one there); anything else is the operator's.
		ours := cur.WebApp.URL == set.TGMenuURL ||
			strings.HasPrefix(cur.WebApp.URL, "https://"+set.Host+"/"+set.SubPathOr()+"/")
		if !ours {
			return true
		}
	}
	if err := client.SetChatMenuButton(ctx, i18n.T(i18n.Normalize(set.BotLang()), "user.menuApp"), url); err != nil {
		log.Printf("telegram user: set the menu button: %v", err)
		return false
	}
	_ = s.store.SetTelegramMenuURL(url)
	return true
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

// preCheckout approves a Stars payment for an order the panel still waits on.
func (s *UserService) preCheckout(ctx context.Context, client *Client, q *PreCheckoutQuery) {
	err := s.panel.StarsPreCheckout(q.InvoicePayload, q.Currency, q.TotalAmount)
	reason := ""
	if err != nil {
		var chat int64
		if q.From != nil {
			chat = q.From.ID
		}
		reason = core.UserError(err, s.lang(chat))
	}
	if e := client.AnswerPreCheckout(ctx, q.ID, err == nil, reason); e != nil {
		log.Printf("telegram user: answer pre-checkout: %v", e)
	}
}

// starsPaid applies a Stars payment. The user hears about it the way every paid
// order is announced.
func (s *UserService) starsPaid(ctx context.Context, client *Client, m *Message) {
	p := m.SuccessfulPayment
	raw, _ := json.Marshal(m)
	if err := s.panel.ConfirmStarsPayment(p.InvoicePayload, p.Currency, p.TotalAmount, raw); err != nil {
		log.Printf("telegram user: stars payment %q: %v", p.InvoicePayload, err)
		s.send(ctx, client, m.Chat.ID, i18n.T(s.lang(m.Chat.ID), "user.starsNotApplied"))
	}
}
