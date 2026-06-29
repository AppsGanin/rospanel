package server

import (
	"context"
	"net/http"
	"time"

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
		"user_bot_username": botUsername(r.Context(), set.TGUserBotToken),
		"admin_events":      rt.mgr.AdminEventPrefs(),
	})
}

func (rt *Router) saveTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled        bool            `json:"enabled"`
		Token          string          `json:"token"`
		BackupCron     string          `json:"backup_cron"`
		UserEnabled    bool            `json:"user_enabled"`
		UserToken      string          `json:"user_token"`
		UserRegEnabled bool            `json:"user_reg_enabled"`
		AdminEvents    map[string]bool `json:"admin_events"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SaveTelegram(req.Enabled, req.Token, req.BackupCron); err != nil {
		writeManagerErr(w, err)
		return
	}
	if err := rt.mgr.SaveTelegramUserBot(req.UserEnabled, req.UserToken, req.UserRegEnabled); err != nil {
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
