package server

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/telegram"
)

// messageUserMax matches Telegram's plain-message limit. Refused here rather than
// per send, where the operator would only see a raw API error.
const messageUserMax = 4096

// messageUser sends one message to one user's Telegram chat — a broadcast of one,
// without the machinery: the operator wants to know right now whether it arrived,
// not to watch a progress bar for a single recipient.
//
// It goes through the USER bot, the same one the person already talks to, so the
// message lands in a conversation they recognise rather than from a stranger.
func (rt *Router) messageUser(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "сообщение пустое")
		return
	}
	if n := utf8.RuneCountInString(text); n > messageUserMax {
		writeErr(w, http.StatusBadRequest,
			"сообщение длиннее 4096 символов — Telegram его не примет")
		return
	}

	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "пользователь не найден")
		return
	}
	if u.TgChatID == 0 {
		writeErr(w, http.StatusBadRequest, "у пользователя не привязан Telegram")
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	token := strings.TrimSpace(set.TGUserBotToken)
	if !set.TGUserBotEnabled || token == "" {
		writeErr(w, http.StatusBadRequest, "включите пользовательского бота — сообщение идёт через него")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := telegram.NewClient(token).SendMessage(ctx, u.TgChatID, text); err != nil {
		msg := "не удалось отправить: " + err.Error()
		if telegram.IsUnreachable(err) {
			msg = "пользователь заблокировал бота или удалил аккаунт — сообщение не доставлено"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeOK(w)
}
