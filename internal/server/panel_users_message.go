package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/telegram"
)

// Telegram's own caps: a plain message, and the shorter caption a message carrying
// media is allowed. Refused here rather than per send, where the operator would only
// see a raw API error.
const (
	messageUserMax = 4096
	captionUserMax = 1024
)

// messageUser sends one message to one user's Telegram chat — a broadcast of one,
// without the machinery: the operator wants to know right now whether it arrived,
// not to watch a progress bar for a single recipient.
//
// It goes through the USER bot, the same one the person already talks to, so the
// message lands in a conversation they recognise rather than from a stranger.
func (rt *Router) messageUser(w http.ResponseWriter, r *http.Request, id int64) {
	// Same multipart shape as a broadcast, and the same parser: whether a file goes
	// out as a photo or a document should not depend on which screen sent it.
	b, file, _, ok := parseBroadcastForm(w, r)
	if !ok {
		return
	}
	if file != nil {
		defer file.Close()
	}
	text := strings.TrimSpace(b.Text)
	if text == "" && b.MediaKind == "" {
		writeErr(w, http.StatusBadRequest, "нечего отправлять — добавьте текст или вложение")
		return
	}
	limit := messageUserMax
	if b.MediaKind != "" {
		limit = captionUserMax
	}
	if n := utf8.RuneCountInString(text); n > limit {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf(
			"текст длиннее %d символов (сейчас %d) — Telegram его не примет", limit, n))
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

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	client := telegram.NewClient(token)
	var sendErr error
	switch {
	case file == nil:
		sendErr = client.SendMessage(ctx, u.TgChatID, text)
	case b.MediaKind == "photo":
		_, sendErr = client.UploadPhoto(ctx, u.TgChatID, b.MediaName, text, nil, file)
	default:
		_, sendErr = client.UploadDocument(ctx, u.TgChatID, b.MediaName, text, nil, file)
	}
	if err := sendErr; err != nil {
		msg := "не удалось отправить: " + err.Error()
		if telegram.IsUnreachable(err) {
			msg = "пользователь заблокировал бота или удалил аккаунт — сообщение не доставлено"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeOK(w)
}
