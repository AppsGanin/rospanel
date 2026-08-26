package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Shu1t3/rospanel-shu1t3/internal/telegram"
)

// Telegram's own caps: a plain message, and the shorter caption a message carrying
// media is allowed. Refused here rather than per send, where the operator would only
// see a raw API error.
const (
	messageUserMax = 4096
	captionUserMax = 1024
)

// Deliberately operator tier, unlike broadcasts (admin): writing to one customer is
// support work, and support staff are exactly who does it. The broadcast gate exists
// because that surface reaches everyone at once.
//
// It also ignores tg_subscribers.opt_out on purpose. That flag means "no mass
// mailings" — the bot tells people so when they use it — not "never contact me";
// service messages and support replies are precisely what it promises will still
// arrive.
//
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
		writeErrCode(w, http.StatusBadRequest, "err.nothingToSend2", "нечего отправлять — добавьте текст или вложение")
		return
	}
	limit := messageUserMax
	if b.MediaKind != "" {
		limit = captionUserMax
	}
	if n := utf8.RuneCountInString(text); n > limit {
		writeCoded(w, "err.messageTooLong",
			map[string]any{"limit": limit, "count": n},
			fmt.Sprintf("текст длиннее %d символов (сейчас %d) — Telegram его не примет", limit, n))
		return
	}

	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, "err.userNotFound", "пользователь не найден")
		return
	}
	if u.TgChatID == 0 {
		writeErrCode(w, http.StatusBadRequest, "err.userHasNoTelegram", "у пользователя не привязан Telegram")
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	token := strings.TrimSpace(set.TGUserBotToken)
	if !set.TGUserBotEnabled || token == "" {
		writeErrCode(w, http.StatusBadRequest, "err.enableUserBotToMessage", "включите пользовательского бота — сообщение идёт через него")
		return
	}

	// The row records who was written to. The body is deliberately never stored (it
	// would put customer correspondence in the admin trail), so without the name the
	// entry cannot answer the only question it exists for.
	auditTarget(r, u.Name)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	client := telegram.NewClient(token, set.TelegramProxyURL())
	// Buttons ride along when a caller sends them, rather than being parsed and
	// dropped: accepting a field and ignoring it answers 200 for a message that isn't
	// what was asked for.
	rows := telegram.BroadcastButtonRows(b.Buttons)
	var sendErr error
	switch {
	case file == nil:
		sendErr = client.SendMenu(ctx, u.TgChatID, text, rows)
	case b.MediaKind == "photo":
		_, sendErr = client.UploadPhoto(ctx, u.TgChatID, b.MediaName, text, rows, file)
	default:
		_, sendErr = client.UploadDocument(ctx, u.TgChatID, b.MediaName, text, rows, file)
	}
	if err := sendErr; err != nil {
		if telegram.IsUnreachable(err) {
			writeErrCode(w, http.StatusBadGateway, "err.userUnreachable",
				"пользователь заблокировал бота или удалил аккаунт — сообщение не доставлено")
			return
		}
		writeErrDetail(w, http.StatusBadGateway, "err.sendFailed", "не удалось отправить: ", err.Error())
		return
	}
	writeOK(w)
}
