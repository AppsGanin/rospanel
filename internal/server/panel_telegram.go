package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/telegram"
)

// telegramConfig is the bot configuration returned to the settings UI. The token
// is returned in clear (admin-only, behind the secret path + auth + TLS — the same
// treatment as the system proxy's password) so the form can round-trip it.
func (rt *Router) getTelegram(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	chats := set.TelegramChatIDs()
	if chats == nil {
		chats = []int64{}
	}
	userEvents, expiringDays := rt.mgr.UserNotifyPrefs()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":            set.TGBotEnabled,
		"token":              set.TGBotToken,
		"backup_cron":        set.TGBackupCron,
		"lang":               set.BotLang(),
		"proxy":              set.TGProxy,
		"proxy_mode":         set.TGProxyModeOr(),
		"chat_ids":           chats,
		"link_code":          set.TGLinkCode,
		"bot_username":       botUsername(r.Context(), set.TGBotToken, set.TelegramProxyURL()),
		"user_enabled":       set.TGUserBotEnabled,
		"user_token":         set.TGUserBotToken,
		"user_reg_enabled":   set.TGUserRegEnabled,
		"user_reg_mode":      set.RegMode(),
		"user_reg_code":      set.TGUserRegCode,
		"user_bot_username":  botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL()),
		"admin_events":       rt.mgr.AdminEventPrefs(),
		"user_events":        userEvents,
		"user_expiring_days": expiringDays,

		"support_enabled":      set.TGSupportEnabled,
		"support_token":        set.TGSupportBotToken,
		"support_group_id":     set.TGSupportGroupID,
		"support_greeting":     set.TGSupportGreeting,
		"support_bot_username": set.TGSupportBotUsername,
	})
}

// or returns the field the client sent, or the value already stored when it sent
// nothing. Absent must not read as empty: this endpoint rewrites all three bots at
// once, so a body from a stale browser tab that predates a field would otherwise wipe
// a bot token or the whole support relay and report success.
func or[T any](sent *T, current T) T {
	if sent != nil {
		return *sent
	}
	return current
}

func (rt *Router) saveTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled     *bool           `json:"enabled"`
		Token       *string         `json:"token"`
		BackupCron  *string         `json:"backup_cron"`
		Lang        *string         `json:"lang"`
		UserEnabled *bool           `json:"user_enabled"`
		UserToken   *string         `json:"user_token"`
		UserRegMode *string         `json:"user_reg_mode"`
		UserRegCode *string         `json:"user_reg_code"`
		AdminEvents map[string]bool `json:"admin_events"`
		UserEvents  map[string]bool `json:"user_events"`
		// UserExpiringDays is a pointer like the rest: absent means "leave it", and
		// zero is a value the operator can never have meant.
		UserExpiringDays *int `json:"user_expiring_days"`

		// Proxy is panel-wide for Telegram, not per bot: it is the one setting that
		// decides whether ANY of this is reachable.
		Proxy     *string `json:"proxy"`
		ProxyMode *string `json:"proxy_mode"`

		SupportEnabled  *bool   `json:"support_enabled"`
		SupportToken    *string `json:"support_token"`
		SupportGroupID  *int64  `json:"support_group_id"`
		SupportGreeting *string `json:"support_greeting"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cur, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	supportToken := or(req.SupportToken, cur.TGSupportBotToken)
	proxy := or(req.Proxy, cur.TGProxy)
	proxyMode := or(req.ProxyMode, cur.TGProxyModeOr())
	// Resolve the route being SAVED, not the stored one: an operator fixing an
	// unreachable panel picks the route and enters the bot tokens in one submit, and
	// checking through the old (broken) route would fail the save that fixes it.
	pending := *cur
	pending.TGProxyMode, pending.TGProxy = proxyMode, proxy
	proxyURL := pending.TelegramProxyURL()

	// getMe only when there is something new to check. Re-resolving an unchanged
	// token made every save depend on Telegram being reachable.
	supportUser := cur.TGSupportBotUsername
	switch {
	case supportToken != cur.TGSupportBotToken || supportUser == "":
		// A different bot, so whatever comes back is the truth — including "". Keeping
		// the previous bot's @username would aim the support button at a stranger.
		supportUser = botUsername(r.Context(), supportToken, proxyURL)
	case proxyURL != cur.TelegramProxyURL():
		// Same bot, new route: worth re-checking, but a failure must NOT clear a
		// username we already have. A route just pointed at a local egress may need
		// seconds before it carries anything — the WARP tunnel is not up the instant
		// its address exists — and clearing on that would make the route impossible to
		// save while support is on, since SaveTelegramSupport refuses an enabled relay
		// with no username.
		if u := botUsername(r.Context(), supportToken, proxyURL); u != "" {
			supportUser = u
		}
	}

	cfg := core.TelegramConfig{
		Enabled:     or(req.Enabled, cur.TGBotEnabled),
		Token:       or(req.Token, cur.TGBotToken),
		BackupCron:  or(req.BackupCron, cur.TGBackupCron),
		Lang:        or(req.Lang, cur.TGLang),
		Proxy:       proxy,
		ProxyMode:   proxyMode,
		UserEnabled: or(req.UserEnabled, cur.TGUserBotEnabled),
		UserToken:   or(req.UserToken, cur.TGUserBotToken),
		UserRegMode: or(req.UserRegMode, cur.RegMode()),
		UserRegCode: or(req.UserRegCode, cur.TGUserRegCode),

		SupportEnabled:  or(req.SupportEnabled, cur.TGSupportEnabled),
		SupportToken:    supportToken,
		SupportUsername: supportUser,
		SupportGroupID:  or(req.SupportGroupID, cur.TGSupportGroupID),
		SupportGreeting: or(req.SupportGreeting, cur.TGSupportGreeting),
	}
	// One call, because all three bots are checked before any of them is written.
	// Saving them in sequence meant a failure on the third — a support token that
	// couldn't be verified while Telegram was unreachable, say — left the first two
	// committed while the request reported failure, and the audit trail recorded
	// nothing at all.
	if err := rt.mgr.SaveTelegramConfig(cfg); err != nil {
		writeManagerErr(w, err)
		return
	}
	if req.AdminEvents != nil {
		if err := rt.mgr.SaveAdminEventPrefs(req.AdminEvents); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	// Either field alone is enough to save: every other field in this handler is
	// independently optional, and a body carrying just the horizon returning 200
	// while changing nothing is the kind of silence that costs an hour to diagnose.
	if req.UserEvents != nil || req.UserExpiringDays != nil {
		prefs := req.UserEvents
		if prefs == nil {
			prefs, _ = rt.mgr.UserNotifyPrefs()
		}
		if err := rt.mgr.SaveUserNotifyPrefs(prefs,
			or(req.UserExpiringDays, cur.ExpiringDays())); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	writeOK(w)
}

// listSupportGroups returns the groups the support bot is in, so the settings page
// can offer a picker instead of asking for a numeric chat id — which otherwise means
// reading one out of a Telegram Web URL (and remembering the -100 prefix) or letting
// a stranger's id-printing bot into the group where customer conversations will live.
func (rt *Router) listSupportGroups(w http.ResponseWriter, _ *http.Request) {
	groups, err := rt.mgr.ListSupportGroups()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if groups == nil {
		groups = []model.SupportGroup{}
	}
	writeJSON(w, http.StatusOK, groups)
}

// checkTelegramSupport verifies the support group end to end before the operator
// relies on it. The failure it exists for is silent: a bot added as a plain member
// still receives what users write, but Telegram's group privacy mode hides the
// admins' replies from it, so the relay half-works with no symptom anyone can see
// from outside.
func (rt *Router) checkTelegramSupport(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	token := strings.TrimSpace(set.TGSupportBotToken)
	if token == "" {
		writeErrCode(w, http.StatusBadRequest, "err.setSupportTokenFirst", "сначала укажите токен бота поддержки и сохраните настройки")
		return
	}
	if set.TGSupportGroupID == 0 {
		writeErrCode(w, http.StatusBadRequest, "err.setSupportGroupFirst", "сначала укажите ID группы поддержки и сохраните настройки")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := telegram.NewClient(token, set.TelegramProxyURL())

	me, err := client.GetMe(ctx)
	if err != nil {
		writeErrDetail(w, http.StatusBadGateway, "err.supportTokenRejected", "токен бота поддержки не принят: ", err.Error())
		return
	}
	chat, err := client.GetChat(ctx, set.TGSupportGroupID)
	if err != nil {
		writeCoded(w, "err.supportGroupUnreachable",
			map[string]any{"detail": err.Error(), "bot": me.Username},
			"группа недоступна: "+err.Error()+" — добавьте @"+me.Username+" в группу и проверьте её ID")
		return
	}
	if chat.Type != "supergroup" {
		writeErrCode(w, http.StatusBadRequest, "err.notSupergroup",
			"указанный чат не является супергруппой — создайте группу и включите в ней «Темы»")
		return
	}
	if !chat.IsForum {
		writeErrCode(w, http.StatusBadRequest, "err.topicsOff",
			"в группе не включены «Темы» — включите их в настройках группы, иначе диалоги не разделить")
		return
	}
	member, err := client.GetChatMember(ctx, set.TGSupportGroupID, me.ID)
	if err != nil {
		writeErrDetail(w, http.StatusBadGateway, "err.botRightsCheckFailed", "не удалось проверить права бота: ", err.Error())
		return
	}
	if member.Status != "administrator" && member.Status != "creator" {
		writeErrCode(w, http.StatusBadRequest, "err.botNotGroupAdmin",
			"бот должен быть администратором группы — иначе он не увидит ответы админов")
		return
	}
	if member.Status == "administrator" && !member.CanManageTopics {
		writeErrCode(w, http.StatusBadRequest, "err.botCannotManageTopics",
			"у бота нет права «Управление темами» — без него он не сможет завести тему на пользователя")
		return
	}
	// Persist the freshly resolved @username. It is otherwise cached forever, so
	// renaming the bot in BotFather left the user bot pointing at a dead t.me link
	// with no way to refresh short of changing the token.
	if me.Username != set.TGSupportBotUsername {
		if err := rt.mgr.SaveTelegramSupport(set.TGSupportEnabled, set.TGSupportBotToken,
			me.Username, set.TGSupportGroupID, set.TGSupportGreeting); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"bot_username": me.Username,
		"group_title":  chat.Title,
	})
}

// genTelegramLink issues a fresh one-time linking code and returns it together
// with the bot's @username so the UI can show "open @bot and send /start <code>".
func (rt *Router) genTelegramLink(w http.ResponseWriter, r *http.Request) {
	code, err := rt.mgr.GenerateTelegramLinkCode()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	username := ""
	if set, err := rt.mgr.Settings(); err == nil {
		username = botUsername(r.Context(), set.TGBotToken, set.TelegramProxyURL())
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "bot_username": username})
}

// telegramLinkStatus is a cheap poll (no Telegram call) the settings page hits
// while a link code is pending, so the UI reflects a just-linked chat without a
// manual page reload. pending=false means the code was consumed (chat linked).
func (rt *Router) telegramLinkStatus(w http.ResponseWriter, _ *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	chats := set.TelegramChatIDs()
	if chats == nil {
		chats = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"chat_ids": chats,
		"pending":  set.TGLinkCode != "",
	})
}

// cancelTelegramLink clears a pending link code (the "✕" on the code box).
func (rt *Router) cancelTelegramLink(w http.ResponseWriter, _ *http.Request) {
	if err := rt.mgr.CancelTelegramLink(); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) unlinkTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.UnlinkTelegramChat(req.ChatID); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// testTelegramBackup sends a backup to every linked chat right now, so the operator
// can confirm delivery works before relying on the schedule.
func (rt *Router) testTelegramBackup(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if set.TGBotToken == "" {
		writeErrCode(w, http.StatusBadRequest, "err.setBotTokenFirst", "сначала укажите токен бота")
		return
	}
	chats := set.TelegramChatIDs()
	if len(chats) == 0 {
		writeErrCode(w, http.StatusBadRequest, "err.noLinkedChats", "нет привязанных чатов — сначала привяжите чат кодом")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	client := telegram.NewClient(set.TGBotToken, set.TelegramProxyURL())
	if err := telegram.SendBackup(ctx, client, chats, rt.dataDir, rt.mgr.BackupManifest(),
		rt.mgr.Store().Checkpoint, i18n.T(rt.mgr.BotLang(), "bot.testBackup")); err != nil {
		writeErrDetail(w, http.StatusBadGateway, "err.sendFailed", "не удалось отправить: ", err.Error())
		return
	}
	writeOK(w)
}

// botUsername fetches the bot's @username (best-effort, short timeout) so the UI
// can render a clickable t.me link. Returns "" when no token is set or the call
// fails (e.g. an invalid token, or Telegram being unreachable through proxy).
//
// proxy is the value being SAVED, not the stored one, on the save path: an operator
// fixing an unreachable panel sets the proxy and the bot tokens in one submit, and
// resolving the username through the old (direct, broken) route would fail the save
// that was about to fix it.
func botUsername(ctx context.Context, token, proxy string) string {
	if token == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if u, err := telegram.NewClient(token, proxy).GetMe(ctx); err == nil {
		return u.Username
	}
	return ""
}
