package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/telegram"
)

// telegramConfig is the bot configuration returned to the settings UI. The token
// is returned in clear (admin-only, behind the secret path + auth + TLS — the same
// treatment as proxy_mode_pass) so the form can round-trip it.
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
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":           set.TGBotEnabled,
		"token":             set.TGBotToken,
		"backup_cron":       set.TGBackupCron,
		"chat_ids":          chats,
		"link_code":         set.TGLinkCode,
		"bot_username":      botUsername(r.Context(), set.TGBotToken),
		"user_enabled":      set.TGUserBotEnabled,
		"user_token":        set.TGUserBotToken,
		"user_reg_enabled":  set.TGUserRegEnabled,
		"user_reg_mode":     set.RegMode(),
		"user_reg_code":     set.TGUserRegCode,
		"user_bot_username": botUsername(r.Context(), set.TGUserBotToken),
		"admin_events":      rt.mgr.AdminEventPrefs(),

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
		UserEnabled *bool           `json:"user_enabled"`
		UserToken   *string         `json:"user_token"`
		UserRegMode *string         `json:"user_reg_mode"`
		UserRegCode *string         `json:"user_reg_code"`
		AdminEvents map[string]bool `json:"admin_events"`

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
	// getMe only when there is something new to check. Re-resolving an unchanged
	// token made every save depend on Telegram being reachable.
	supportUser := cur.TGSupportBotUsername
	if supportToken != cur.TGSupportBotToken || supportUser == "" {
		supportUser = botUsername(r.Context(), supportToken)
	}

	cfg := core.TelegramConfig{
		Enabled:     or(req.Enabled, cur.TGBotEnabled),
		Token:       or(req.Token, cur.TGBotToken),
		BackupCron:  or(req.BackupCron, cur.TGBackupCron),
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
		writeErr(w, http.StatusBadRequest, "сначала укажите токен бота поддержки и сохраните настройки")
		return
	}
	if set.TGSupportGroupID == 0 {
		writeErr(w, http.StatusBadRequest, "сначала укажите ID группы поддержки и сохраните настройки")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := telegram.NewClient(token)

	me, err := client.GetMe(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "токен бота поддержки не принят: "+err.Error())
		return
	}
	chat, err := client.GetChat(ctx, set.TGSupportGroupID)
	if err != nil {
		writeErr(w, http.StatusBadGateway,
			"группа недоступна: "+err.Error()+" — добавьте @"+me.Username+" в группу и проверьте её ID")
		return
	}
	if chat.Type != "supergroup" {
		writeErr(w, http.StatusBadRequest,
			"указанный чат не является супергруппой — создайте группу и включите в ней «Темы»")
		return
	}
	if !chat.IsForum {
		writeErr(w, http.StatusBadRequest,
			"в группе не включены «Темы» — включите их в настройках группы, иначе диалоги не разделить")
		return
	}
	member, err := client.GetChatMember(ctx, set.TGSupportGroupID, me.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "не удалось проверить права бота: "+err.Error())
		return
	}
	if member.Status != "administrator" && member.Status != "creator" {
		writeErr(w, http.StatusBadRequest,
			"бот должен быть администратором группы — иначе он не увидит ответы админов")
		return
	}
	if member.Status == "administrator" && !member.CanManageTopics {
		writeErr(w, http.StatusBadRequest,
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
		username = botUsername(r.Context(), set.TGBotToken)
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
		writeErr(w, http.StatusBadRequest, "сначала укажите токен бота")
		return
	}
	chats := set.TelegramChatIDs()
	if len(chats) == 0 {
		writeErr(w, http.StatusBadRequest, "нет привязанных чатов — сначала привяжите чат кодом")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	client := telegram.NewClient(set.TGBotToken)
	if err := telegram.SendBackup(ctx, client, chats, rt.dataDir, rt.mgr.BackupManifest(),
		rt.mgr.Store().Checkpoint, "Тестовая резервная копия"); err != nil {
		writeErr(w, http.StatusBadGateway, "не удалось отправить: "+err.Error())
		return
	}
	writeOK(w)
}

// botUsername fetches the bot's @username (best-effort, short timeout) so the UI
// can render a clickable t.me link. Returns "" when no token is set or the call
// fails (e.g. an invalid token).
func botUsername(ctx context.Context, token string) string {
	if token == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if u, err := telegram.NewClient(token).GetMe(ctx); err == nil {
		return u.Username
	}
	return ""
}
