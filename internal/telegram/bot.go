package telegram

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Panel is the slice of the core Manager the bot drives. Defining it here (rather
// than importing core) keeps the dependency one-way — core never imports telegram —
// and makes the bot trivially testable with a fake.
// Every mutating method takes a context: it carries the actor for the audit log, so
// a change made from a bot is attributed to the Telegram admin (or the VPN user)
// who made it rather than to "system". See the actor package.
type Panel interface {
	ListUsers() ([]model.User, error)
	CreateUser(ctx context.Context, name string, dataLimit, expireAt int64) (*model.User, error)
	DeleteUser(ctx context.Context, id int64) error
	SetUserEnabled(ctx context.Context, id int64, enabled bool) error
	ResetTraffic(ctx context.Context, id int64) error
	BackupManifest() backup.Manifest
	Location() *time.Location
	// DeviceCount is the number of HWID-bound devices a user has, for the card's
	// device line (only consulted when HWID binding is enabled).
	DeviceCount(userID int64) int

	// Billing (no-op surface unless tariffs are enabled).
	ListTariffPlans(includeDisabled bool) ([]model.TariffPlan, error)
	ApplyPlanToUser(ctx context.Context, userID, planID int64, extendFromCurrent bool) error
	PlanName(planID int64) string
	RequestPlanPayment(ctx context.Context, lang i18n.Lang, userID, planID int64) (*model.PaymentOrder, string, error)
	// CreateRegisteredUser signs a new user up (active, for the open/invite modes).
	CreateRegisteredUser(ctx context.Context, name string) (*model.User, error)
	// RequestRegistration records a moderated signup (no user yet); ApproveRegistration
	// Request creates the user, RejectRegistrationRequest drops it.
	RequestRegistration(ctx context.Context, chatID int64, name string) (bool, error)
	RegistrationPending(chatID int64) bool
	ApproveRegistrationRequest(ctx context.Context, reqID int64) error
	RejectRegistrationRequest(ctx context.Context, reqID int64) error
	// ActivePaidPlan reports the user's active paid plan (nil = none), and
	// CancelUserPlan drops it to the free plan. Together they gate plan switching:
	// while a paid plan is active only renewal or cancellation is allowed.
	ActivePaidPlan(u model.User) *model.TariffPlan
	CancelUserPlan(ctx context.Context, userID int64) error

	// Automatic payment providers (no-op surface unless configured).
	PaymentMethods() []string
	ProviderLabel(key string) string
	StartPlanPayment(ctx context.Context, lang i18n.Lang, userID, planID int64, provider string) (*model.PaymentOrder, error)
	SetUserNotifier(fn func(chatID int64, html string))
	SetAdminNotifier(fn func(html string))
	SetAdminModerationNotifier(fn func(reqID int64, name, plan string))
	// A sign-in from a new address, and the button under it: end every session of
	// that admin. The bot never touches admin_sessions itself — the panel records
	// who pressed it.
	SetAdminLoginNotifier(fn func(a core.LoginAlert))
	RevokeAdminSessions(ctx context.Context, adminID int64) (username string, n int64, err error)

	// Audit hooks for the actions the bots perform directly on the store.
	// (Unlinking is deliberately absent: it is an operator action in the panel, not
	// something a user can do to themselves from the bot.)
	AuditTelegramLinked(ctx context.Context, id int64, username string)
}

// pollTimeout is the long-poll window (seconds). A change to the bot token or
// enable flag is picked up within this window since each cycle re-reads settings.
const pollTimeout = 25

// usersPerPage caps how many user buttons one menu page shows.
const usersPerPage = 8

// Service is the running bot: it long-polls for updates and pushes scheduled
// backups. The whole UI is inline buttons — the only text a user ever types is
// "/start <code>" to link, and (when prompted) a new user's name. It re-reads
// settings every cycle, so enabling/disabling, rotating the token, or changing the
// schedule all take effect without a restart.
type Service struct {
	panel   Panel
	store   *store.Store
	dataDir string

	mu          sync.Mutex
	client      *Client
	clientToken string
	clientProxy string // proxy the cached client was built with; a change rebuilds it
	offset      int64
	pending     map[int64]string // chatID → awaited text input ("add"), guarded by mu

	// rate throttles inbound updates per chat. The bot is reachable by @username, so a
	// stranger can message it; every update is handled SYNCHRONOUSLY in the single poll
	// loop and each reply waits for that chat's outbound slot, so one unauthenticated
	// chat sending steadily was enough to park the loop and stop the real operator's
	// buttons from responding.
	rate *chatLimiter

	lastFired   time.Time // last scheduled-backup minute (operator TZ); seeded in New
	lastPollErr string    // last getUpdates error (dedups log spam on a bad token)
}

// New builds the bot service. It does not start polling — call Run.
func New(panel Panel, st *store.Store, dataDir string) *Service {
	return &Service{
		panel:     panel,
		store:     st,
		dataDir:   dataDir,
		pending:   map[int64]string{},
		rate:      newChatLimiter(adminRateWindow, maxAdminPerWindow),
		lastFired: time.Now().In(panel.Location()).Truncate(time.Minute),
	}
}

// clientFor returns a cached client for token, rebuilding it when the token rotates
// or the operator repoints the Telegram proxy.
func (s *Service) clientFor(token, proxy string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.clientToken != token || s.clientProxy != proxy {
		s.client = NewClient(token, proxy)
		s.clientToken, s.clientProxy = token, proxy
	}
	return s.client
}

func (s *Service) setPending(chatID int64, state string) {
	s.mu.Lock()
	s.pending[chatID] = state
	s.mu.Unlock()
}

// takePending reads and clears a chat's awaited-input state.
func (s *Service) takePending(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.pending[chatID]
	delete(s.pending, chatID)
	return st
}

func (s *Service) clearPending(chatID int64) {
	s.mu.Lock()
	delete(s.pending, chatID)
	s.mu.Unlock()
}

// Run drives the bot until ctx is cancelled: it long-polls for updates and, in a
// sibling goroutine, fires scheduled backups. When the bot is disabled or has no
// token it idles, re-checking periodically.
func (s *Service) Run(ctx context.Context) {
	go s.backupLoop(ctx)
	// Broadcast admin events (payments, outages, blocklist hits) to the authorized
	// admin chats.
	s.panel.SetAdminNotifier(func(html string) {
		set, err := s.store.GetSettings()
		if err != nil {
			log.Printf("telegram: admin notify: settings: %v", err)
			return
		}
		token := strings.TrimSpace(set.TGBotToken)
		if token == "" {
			log.Printf("telegram: admin notify dropped — no bot token")
			return
		}
		chats := set.TelegramChatIDs()
		if len(chats) == 0 {
			// The whole feature is silent in this state and nothing else says so: every
			// alert the panel raises is built, gated, and then delivered to nobody.
			log.Printf("telegram: admin notify dropped — no linked admin chats")
			return
		}
		c := NewClient(strings.TrimSpace(set.TGBotToken), set.TelegramProxyURL())
		for _, id := range chats {
			// Logged, never swallowed: a chat that blocked the bot, a stale chat id or a
			// revoked token fails per send, and with the error discarded the panel looked
			// exactly like a panel that had nothing to say.
			if err := c.SendMessage(context.Background(), id, html); err != nil {
				log.Printf("telegram: admin notify to %d failed: %v", id, err)
			}
		}
	})
	// A signup awaiting moderation: post it with approve/reject buttons.
	s.panel.SetAdminModerationNotifier(func(reqID int64, name, plan string) {
		set, err := s.store.GetSettings()
		if err != nil || strings.TrimSpace(set.TGBotToken) == "" || !set.AdminEventEnabled(model.AdminEventRegistered) {
			return
		}
		lang := s.lang()
		msg := i18n.T(lang, "admin.regRequest", esc(name))
		if plan != "" {
			msg += "\n" + i18n.T(lang, "admin.planIs", esc(plan))
		}
		msg += "\n\n" + i18n.T(lang, "admin.approveAccess")
		rows := [][]InlineButton{{
			{Text: i18n.T(lang, "admin.btnApprove"), CallbackData: fmt.Sprintf("reg:%d:ok", reqID)},
			{Text: i18n.T(lang, "admin.btnReject"), CallbackData: fmt.Sprintf("reg:%d:no", reqID)},
		}}
		c := NewClient(strings.TrimSpace(set.TGBotToken), set.TelegramProxyURL())
		for _, id := range set.TelegramChatIDs() {
			if err := c.SendMenu(context.Background(), id, msg, rows); err != nil {
				log.Printf("telegram: moderation prompt to %d failed: %v", id, err)
			}
		}
	})
	// A sign-in from an address the admin had not used: say where from, on what,
	// and offer the one action that helps if it was somebody else.
	s.panel.SetAdminLoginNotifier(func(a core.LoginAlert) {
		set, err := s.store.GetSettings()
		if err != nil || strings.TrimSpace(set.TGBotToken) == "" {
			return
		}
		lang := s.lang()
		where := esc(a.IP)
		if a.Country != "" {
			where += " " + geo.Flag(a.Country) + " " + esc(a.Country)
		}
		if a.Org != "" {
			where += " · " + esc(a.Org)
		}
		client := esc(a.Client)
		if client == "" {
			client = i18n.T(lang, "admin.unknownClient")
		}
		when := time.Unix(a.At, 0).In(s.panel.Location()).Format("02.01.2006 15:04")
		msg := i18n.T(lang, "notify.adminLogin", esc(a.Username), where, client, when)
		rows := [][]InlineButton{{
			{Text: i18n.T(lang, "admin.btnNotMe"), CallbackData: fmt.Sprintf("sess:%d:kill", a.AdminID)},
		}}
		c := NewClient(strings.TrimSpace(set.TGBotToken), set.TelegramProxyURL())
		for _, id := range set.TelegramChatIDs() {
			if err := c.SendMenu(context.Background(), id, msg, rows); err != nil {
				log.Printf("telegram: login alert to %d failed: %v", id, err)
			}
		}
	})
	for {
		if ctx.Err() != nil {
			return
		}
		set, err := s.store.GetSettings()
		if err != nil || !set.TGBotEnabled || strings.TrimSpace(set.TGBotToken) == "" {
			if !sleep(ctx, 10*time.Second) {
				return
			}
			continue
		}
		client := s.clientFor(strings.TrimSpace(set.TGBotToken), set.TelegramProxyURL())
		updates, err := client.GetUpdates(ctx, s.offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Log a persistent error (e.g. a bad token, or a webhook conflict) once,
			// not every cycle — otherwise it floods the journal forever.
			if msg := err.Error(); msg != s.lastPollErr {
				log.Printf("telegram: getUpdates: %v", err)
				s.lastPollErr = msg
			}
			if !sleep(ctx, pollBackoff(err)) {
				return
			}
			continue
		}
		if s.lastPollErr != "" {
			log.Printf("telegram: polling recovered")
			s.lastPollErr = ""
		}
		for _, u := range updates {
			s.offset = u.UpdateID + 1
			s.handle(ctx, client, u)
		}
	}
}

// adminRateWindow / maxAdminPerWindow bound one chat's inbound updates. Generous — an
// operator clicking through the user list is a burst of callbacks — but low enough that
// a stranger cannot hold the single poll loop.
const (
	adminRateWindow   = time.Minute
	maxAdminPerWindow = 60
)

// handle dispatches one update, recovering from a panic so a single malformed
// update can't tear down the poll loop.
func (s *Service) handle(ctx context.Context, client *Client, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram: handler panic recovered: %v", r)
		}
	}()
	switch {
	case u.Callback != nil:
		// Stamp the tapping admin onto the context once, here: every panel mutation
		// made below records them as the actor in the audit log.
		s.handleCallback(actorCtx(ctx, u.Callback.From), client, u.Callback)
	case u.Message != nil && strings.TrimSpace(u.Message.Text) != "":
		s.handleMessage(actorCtx(ctx, u.Message.From), client, u.Message)
	}
}

// actorCtx marks the context as "this Telegram admin is acting".
func actorCtx(ctx context.Context, from *User) context.Context {
	return actor.With(ctx, actor.Telegram(actorName(from)))
}

// actorName is the most human identifier Telegram gave us for a person: their
// @username, else their first name, else their numeric id.
func actorName(u *User) string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	return strconv.FormatInt(u.ID, 10)
}

func (s *Service) handleMessage(ctx context.Context, client *Client, m *Message) {
	lang := s.lang()
	set, err := s.store.GetSettings()
	if err != nil {
		return
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	cmd, args := splitCmd(text)

	// /start is the only thing an un-linked chat may use (to link with a code).
	if cmd == "/start" {
		s.handleStart(ctx, client, set, chatID, args)
		return
	}
	if !set.TelegramAuthorized(chatID) {
		s.send(ctx, client, chatID, accessDenied(lang))
		return
	}
	// A pending prompt (e.g. "send the new user's name") consumes the next message.
	if s.takePending(chatID) == "add" {
		s.doAdd(ctx, client, chatID, set, text)
		return
	}
	// Any other text just opens the menu — the whole UI is buttons.
	s.sendMainMenu(ctx, client, chatID)
}

// handleStart links a chat when "/start CODE" carries the current one-time code,
// otherwise opens the menu (linked) or explains how to link (un-linked).
func (s *Service) handleStart(ctx context.Context, client *Client, set *model.Settings, chatID int64, args []string) {
	lang := s.lang()
	if len(args) >= 1 && set.TGLinkCode != "" &&
		subtle.ConstantTimeCompare([]byte(args[0]), []byte(set.TGLinkCode)) == 1 {
		ids := set.TelegramChatIDs()
		if !set.TelegramAuthorized(chatID) {
			ids = append(ids, chatID)
		}
		_ = s.store.SetTelegramChats(joinIDs(ids))
		_ = s.store.SetTelegramLinkCode("") // one-time: burn the code
		log.Printf("telegram: chat %d linked", chatID)
		s.sendMenu(ctx, client, chatID, i18n.T(lang, "admin.chatLinked")+"\n\n"+menuHeader(lang), mainMenuRows(lang))
		return
	}
	if set.TelegramAuthorized(chatID) {
		s.sendMainMenu(ctx, client, chatID)
		return
	}
	s.send(ctx, client, chatID, i18n.T(lang, "admin.startHint"))
}

// handleCallback routes an inline-button tap. Navigation edits the current message
// in place; actions that produce an artifact (links, a backup) send a new message.
func (s *Service) handleCallback(ctx context.Context, client *Client, cb *CallbackQuery) {
	lang := s.lang()
	_ = client.AnswerCallback(ctx, cb.ID, "")
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	set, err := s.store.GetSettings()
	if err != nil || !set.TelegramAuthorized(chatID) {
		return
	}
	s.clearPending(chatID) // navigating via buttons cancels any awaited text input

	data := cb.Data
	switch {
	case data == "menu":
		s.edit(ctx, client, chatID, msgID, menuHeader(lang), mainMenuRows(lang))
	case strings.HasPrefix(data, "users:"):
		s.showUsers(ctx, client, chatID, msgID, atoiOr(strings.TrimPrefix(data, "users:"), 0))
	case strings.HasPrefix(data, "u:"):
		s.handleUserAction(ctx, client, chatID, msgID, set, strings.TrimPrefix(data, "u:"))
	case strings.HasPrefix(data, "reg:"):
		s.handleModeration(ctx, client, chatID, msgID, strings.TrimPrefix(data, "reg:"))
	case data == "add":
		s.promptAdd(ctx, client, chatID, msgID)
	case data == "backup":
		s.cmdBackup(ctx, client, chatID, set)
	case strings.HasPrefix(data, "sess:"):
		s.handleSessionKill(ctx, client, chatID, msgID, cb.From, strings.TrimPrefix(data, "sess:"))
	}
}

// handleSessionKill is the login alert's button: "<adminID>:kill" ends every
// session of that admin. The message is edited to the outcome so a second tap on
// a stale alert has nothing left to press.
func (s *Service) handleSessionKill(ctx context.Context, client *Client, chatID, msgID int64, from *User, payload string) {
	lang := s.lang()
	idStr, action, _ := strings.Cut(payload, ":")
	adminID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || action != "kill" {
		return
	}
	who := "telegram"
	if from != nil && from.Username != "" {
		who = "@" + from.Username
	}
	username, n, err := s.panel.RevokeAdminSessions(actor.With(ctx, actor.Telegram(who)), adminID)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.sessionsRevokeFailed", esc(err.Error())), nil)
		return
	}
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.sessionsRevoked", esc(username), n), nil)
}

// handleModeration approves or rejects a signup awaiting moderation. payload is
// "<id>:ok" | "<id>:no". The prompt message is edited in place to the outcome.
func (s *Service) handleModeration(ctx context.Context, client *Client, chatID, msgID int64, payload string) {
	lang := s.lang()
	idStr, action, _ := strings.Cut(payload, ":")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return
	}
	switch action {
	case "ok":
		if err := s.panel.ApproveRegistrationRequest(ctx, id); err != nil {
			s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.approveFailed", esc(err.Error())), nil)
			return
		}
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.requestApproved"), nil)
	case "no":
		if err := s.panel.RejectRegistrationRequest(ctx, id); err != nil {
			s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.rejectFailed", esc(err.Error())), nil)
			return
		}
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.requestRejected"), nil)
	}
}

// handleUserAction handles the per-user buttons. payload is "<id>" (show card) or
// "<id>:<action>".
func (s *Service) handleUserAction(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, payload string) {
	lang := s.lang()
	idStr, action, _ := strings.Cut(payload, ":")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return
	}
	switch action {
	case "": // show the user card
		s.showUserCard(ctx, client, chatID, msgID, set, id)
	case "sub":
		u, ok := s.findUser(id)
		if ok {
			s.sendSubscription(ctx, client, chatID, set, u)
		}
	case "on":
		_ = s.panel.SetUserEnabled(ctx, id, true)
		s.showUserCard(ctx, client, chatID, msgID, set, id)
	case "off":
		_ = s.panel.SetUserEnabled(ctx, id, false)
		s.showUserCard(ctx, client, chatID, msgID, set, id)
	case "reset":
		_ = s.panel.ResetTraffic(ctx, id)
		s.showUserCard(ctx, client, chatID, msgID, set, id)
	case "plans":
		s.showUserPlans(ctx, client, chatID, msgID, set, id)
	case "del": // ask for confirmation in place
		u, ok := s.findUser(id)
		if !ok {
			s.showUsers(ctx, client, chatID, msgID, 0)
			return
		}
		s.edit(ctx, client, chatID, msgID,
			i18n.T(lang, "admin.deleteConfirm", u.ID, esc(u.Name)),
			[][]InlineButton{
				{{Text: i18n.T(lang, "admin.btnDeleteYes"), CallbackData: fmt.Sprintf("u:%d:delyes", id)}},
				{{Text: i18n.T(lang, "admin.btnCancel"), CallbackData: fmt.Sprintf("u:%d", id)}},
			})
	case "delyes":
		if derr := s.panel.DeleteUser(ctx, id); derr != nil {
			s.send(ctx, client, chatID, i18n.T(lang, "admin.error", esc(derr.Error())))
		}
		s.showUsers(ctx, client, chatID, msgID, 0)
	default:
		// "plan:<id>" — assign a tariff (planID 0 = manual, no limits).
		if planStr, ok := strings.CutPrefix(action, "plan:"); ok {
			planID, _ := strconv.ParseInt(planStr, 10, 64)
			if perr := s.panel.ApplyPlanToUser(ctx, id, planID, false); perr != nil {
				s.send(ctx, client, chatID, i18n.T(lang, "admin.planChangeFailed", esc(perr.Error())))
			}
			s.showUserCard(ctx, client, chatID, msgID, set, id)
		}
	}
}

// showUsers edits the message into a paginated list of user buttons.
func (s *Service) showUsers(ctx context.Context, client *Client, chatID, msgID int64, page int) {
	lang := s.lang()
	users, err := s.panel.ListUsers()
	if err != nil {
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.error", esc(err.Error())), backToMenu(lang))
		return
	}
	if len(users) == 0 {
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.noUsers"), [][]InlineButton{
			{{Text: i18n.T(lang, "admin.btnAdd"), CallbackData: "add"}},
			{{Text: i18n.T(lang, "admin.btnMenu"), CallbackData: "menu"}},
		})
		return
	}
	pages := (len(users) + usersPerPage - 1) / usersPerPage
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * usersPerPage
	end := min(start+usersPerPage, len(users))

	rows := make([][]InlineButton, 0, usersPerPage+2)
	for _, u := range users[start:end] {
		rows = append(rows, []InlineButton{{
			Text:         userButtonLabel(u),
			CallbackData: fmt.Sprintf("u:%d", u.ID),
		}})
	}
	if pages > 1 {
		var nav []InlineButton
		if page > 0 {
			nav = append(nav, InlineButton{Text: "◀", CallbackData: fmt.Sprintf("users:%d", page-1)})
		}
		nav = append(nav, InlineButton{Text: fmt.Sprintf("%d/%d", page+1, pages), CallbackData: "noop"})
		if page < pages-1 {
			nav = append(nav, InlineButton{Text: "▶", CallbackData: fmt.Sprintf("users:%d", page+1)})
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []InlineButton{
		{Text: i18n.T(lang, "admin.btnAdd"), CallbackData: "add"},
		{Text: i18n.T(lang, "admin.btnMenu"), CallbackData: "menu"},
	})
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "admin.usersTitle", len(users)), rows)
}

// showUserCard edits the message into a user's card with action buttons.
func (s *Service) showUserCard(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, id int64) {
	lang := s.lang()
	u, ok := s.findUser(id)
	if !ok {
		s.showUsers(ctx, client, chatID, msgID, 0)
		return
	}
	toggle := InlineButton{Text: i18n.T(lang, "admin.btnDisable"), CallbackData: fmt.Sprintf("u:%d:off", id)}
	if !u.Enabled {
		toggle = InlineButton{Text: i18n.T(lang, "admin.btnEnable"), CallbackData: fmt.Sprintf("u:%d:on", id)}
	}
	rows := [][]InlineButton{
		{{Text: i18n.T(lang, "admin.btnSub"), CallbackData: fmt.Sprintf("u:%d:sub", id)}},
		{toggle, {Text: i18n.T(lang, "admin.btnResetTraffic"), CallbackData: fmt.Sprintf("u:%d:reset", id)}},
	}
	if set.BillingEnabled {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "admin.btnPlan"), CallbackData: fmt.Sprintf("u:%d:plans", id)}})
	}
	rows = append(rows,
		[]InlineButton{{Text: i18n.T(lang, "admin.btnDelete"), CallbackData: fmt.Sprintf("u:%d:del", id)}},
		[]InlineButton{{Text: i18n.T(lang, "admin.btnToList"), CallbackData: "users:0"}},
	)
	planName := ""
	if u.PlanID != 0 {
		planName = s.panel.PlanName(u.PlanID)
	}
	s.edit(ctx, client, chatID, msgID, userCardWithPlan(u, s.panel.Location(), planName, set.BillingEnabled, lang), rows)
}

// showUserPlans lets the admin assign a tariff (or switch back to manual limits).
func (s *Service) showUserPlans(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, userID int64) {
	lang := s.lang()
	u, ok := s.findUser(userID)
	if !ok {
		s.showUsers(ctx, client, chatID, msgID, 0)
		return
	}
	plans, err := s.panel.ListTariffPlans(false)
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(err.Error()), [][]InlineButton{
			{{Text: i18n.T(lang, "admin.btnBack"), CallbackData: fmt.Sprintf("u:%d", userID)}},
		})
		return
	}
	rows := [][]InlineButton{{{
		Text:         i18n.T(lang, "admin.btnManual"),
		CallbackData: fmt.Sprintf("u:%d:plan:0", userID),
	}}}
	for _, p := range plans {
		rows = append(rows, []InlineButton{{
			Text:         planButtonLabel(p, lang),
			CallbackData: fmt.Sprintf("u:%d:plan:%d", userID, p.ID),
		}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "admin.btnBack"), CallbackData: fmt.Sprintf("u:%d", userID)}})
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "admin.pickPlan", u.ID, esc(u.Name)),
		rows)
}

// promptAdd asks for the new user's details and arms the pending-input state.
func (s *Service) promptAdd(ctx context.Context, client *Client, chatID, msgID int64) {
	lang := s.lang()
	s.setPending(chatID, "add")
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "admin.newUserPrompt"),
		backToMenu(lang))
}

// doAdd creates a user, without a data limit or expiry, from the prompted name.
func (s *Service) doAdd(ctx context.Context, client *Client, chatID int64, set *model.Settings, text string) {
	lang := s.lang()
	name := strings.TrimSpace(text)
	if name == "" {
		s.send(ctx, client, chatID, i18n.T(lang, "admin.emptyName"))
		return
	}
	u, err := s.panel.CreateUser(ctx, name, 0, 0)
	if err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "admin.createFailed", esc(err.Error())))
		return
	}
	s.sendMenu(ctx, client, chatID, i18n.T(lang, "admin.userCreated")+"\n\n"+userCard(*u, s.panel.Location(), lang),
		[][]InlineButton{
			{{Text: i18n.T(lang, "admin.btnSub"), CallbackData: fmt.Sprintf("u:%d:sub", u.ID)}},
			{{Text: i18n.T(lang, "admin.btnMenu"), CallbackData: "menu"}},
		})
}

// sendSubscription sends the user's subscription as a QR-code image with the URL
// in the caption (a fallback text message is sent if the QR can't be rendered or
// uploaded).
func (s *Service) sendSubscription(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User) {
	lang := s.lang()
	caption := subCaption(u, set, lang)
	png, err := subQR(u, set)
	if err != nil {
		s.send(ctx, client, chatID, caption) // fallback: URL only
		return
	}
	if perr := client.SendPhoto(ctx, chatID, "subscription.png", caption, bytes.NewReader(png)); perr != nil {
		log.Printf("telegram: sendPhoto to %d: %v", chatID, perr)
		s.send(ctx, client, chatID, caption) // fallback: URL only
	}
}

func (s *Service) cmdBackup(ctx context.Context, client *Client, chatID int64, set *model.Settings) {
	lang := s.lang()
	s.send(ctx, client, chatID, i18n.T(lang, "admin.preparingBackup"))
	if err := SendBackup(ctx, client, []int64{chatID}, s.dataDir,
		s.panel.BackupManifest(), s.store.Checkpoint, i18n.T(lang, "admin.backupCaption")); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "admin.backupFailed", esc(err.Error())))
	}
}

func (s *Service) sendMainMenu(ctx context.Context, client *Client, chatID int64) {
	lang := s.lang()
	s.sendMenu(ctx, client, chatID, menuHeader(lang), mainMenuRows(lang))
}

// findUser looks up a user by ID from the current list.
func (s *Service) findUser(id int64) (model.User, bool) {
	users, err := s.panel.ListUsers()
	if err != nil {
		return model.User{}, false
	}
	for _, u := range users {
		if u.ID == id {
			return u, true
		}
	}
	return model.User{}, false
}

// send / sendMenu / edit are logged best-effort wrappers (a delivery failure
// shouldn't abort a handler — the admin just doesn't see the update).
func (s *Service) send(ctx context.Context, client *Client, chatID int64, html string) {
	if err := client.SendMessage(ctx, chatID, html); err != nil {
		log.Printf("telegram: send to %d: %v", chatID, err)
	}
}

// lang is the language the admin bot writes in. Unlike the client and support
// bots it is not per-recipient: the admin bot also pushes unprompted alerts, which
// carry no Telegram update to read a language from, so a panel-wide setting decides
// it (Settings → Telegram).
func (s *Service) lang() i18n.Lang {
	set, err := s.store.GetSettings()
	if err != nil || set == nil {
		return i18n.Default
	}
	return i18n.Normalize(set.BotLang())
}

func (s *Service) sendMenu(ctx context.Context, client *Client, chatID int64, html string, rows [][]InlineButton) {
	if err := client.SendMenu(ctx, chatID, html, rows); err != nil {
		log.Printf("telegram: send menu to %d: %v", chatID, err)
	}
}

func (s *Service) edit(ctx context.Context, client *Client, chatID, msgID int64, html string, rows [][]InlineButton) {
	if err := client.EditMenu(ctx, chatID, msgID, html, rows); err != nil {
		// A "message is not modified" edit (re-tapping the current view) is benign.
		log.Printf("telegram: edit %d/%d: %v", chatID, msgID, err)
	}
}
