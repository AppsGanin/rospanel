package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/telegram"
)

// Mass broadcasts through the user bot. Composition is multipart rather than JSON
// because an attachment travels with it, and base64 in a JSON body would not fit the
// 256 KB decodeJSON cap.

// maxBroadcastUpload bounds what the panel will store on disk. Telegram's own limits
// are stricter for photos (10 MB) and looser for documents (50 MB); this is a cap on
// what the panel accepts, and the API reports its own refusal on top.
const maxBroadcastUpload = 20 << 20

// broadcastForm is the composed message as the SPA sends it, in the "payload" field.
type broadcastForm struct {
	Text     string                  `json:"text"`
	Audience string                  `json:"audience"`
	Buttons  []model.BroadcastButton `json:"buttons"`
}

// parseBroadcastForm reads the multipart body into a broadcast plus the attachment
// (nil when none was sent). The file is returned unread so the caller decides where
// it goes — straight to Telegram for a test, or to disk for a real run.
func parseBroadcastForm(w http.ResponseWriter, r *http.Request) (*model.Broadcast, multipart.File, string, bool) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, maxBroadcastUpload+64<<10)
	if err := r.ParseMultipartForm(maxBroadcastUpload); err != nil {
		writeErr(w, http.StatusBadRequest, "не удалось разобрать загрузку (возможно, файл слишком большой)")
		return nil, nil, "", false
	}
	var form broadcastForm
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &form); err != nil {
		writeErr(w, http.StatusBadRequest, "неверное тело запроса")
		return nil, nil, "", false
	}
	b := &model.Broadcast{
		Text:     form.Text,
		Audience: form.Audience,
		Buttons:  form.Buttons,
	}
	file, header, err := r.FormFile("media")
	if err != nil {
		return b, nil, "", true // no attachment — a text-only broadcast
	}
	b.MediaName = filepath.Base(header.Filename)
	b.MediaKind = "document"
	if isImageName(b.MediaName) {
		b.MediaKind = "photo"
	}
	return b, file, b.MediaKind, true
}

// isImageName decides between sendPhoto and sendDocument. Photos render inline,
// which is what an operator attaching a picture expects; anything else is a file.
func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

func (rt *Router) listBroadcasts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := rt.mgr.ListBroadcasts(limit)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if list == nil {
		list = []model.Broadcast{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (rt *Router) getBroadcast(w http.ResponseWriter, r *http.Request, id int64) {
	b, err := rt.mgr.GetBroadcast(id)
	if errors.Is(err, core.ErrBroadcastNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// broadcastAudience reports how many recipients an audience resolves to right now,
// so the operator sees the size before committing rather than after.
func (rt *Router) broadcastAudience(w http.ResponseWriter, r *http.Request) {
	n, err := rt.mgr.AudiencePreview(r.URL.Query().Get("audience"))
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// createBroadcast validates, snapshots the audience, stores the attachment, and only
// then starts delivery — the worker addresses the file by broadcast id, so it must
// exist before the run is visible as running.
func (rt *Router) createBroadcast(w http.ResponseWriter, r *http.Request) {
	b, file, _, ok := parseBroadcastForm(w, r)
	if !ok {
		return
	}
	if file != nil {
		defer file.Close()
	}
	created, err := rt.mgr.CreateBroadcast(r.Context(), b)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if file != nil {
		if err := saveBroadcastMedia(rt.dataDir, created.ID, file); err != nil {
			// The row exists but has no attachment, and it is still paused — cancel
			// it so nothing half-formed can be resumed into going out.
			_ = rt.mgr.SetBroadcastStatus(created.ID, model.BroadcastCancelled)
			writeErr(w, http.StatusInternalServerError, "не удалось сохранить вложение: "+err.Error())
			return
		}
	}
	if err := rt.mgr.StartBroadcast(created.ID); err != nil {
		// Same reasoning as the save-media failure above: a row left paused with an
		// attachment on disk is never swept (the sweep only looks at finished runs),
		// so an abandoned create would leak up to 20 MB into the backed-up data dir.
		_ = rt.mgr.SetBroadcastStatus(created.ID, model.BroadcastCancelled)
		writeManagerErr(w, err)
		return
	}
	created.Status = model.BroadcastRunning
	writeJSON(w, http.StatusOK, created)
}

func saveBroadcastMedia(dataDir string, id int64, src io.Reader) error {
	if err := os.MkdirAll(telegram.BroadcastMediaDir(dataDir), 0o700); err != nil {
		return err
	}
	dst, err := os.Create(telegram.BroadcastMediaPath(dataDir, id))
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Sync()
}

func (rt *Router) pauseBroadcast(w http.ResponseWriter, r *http.Request, id int64) {
	rt.setBroadcastStatus(w, id, model.BroadcastPaused)
}

func (rt *Router) resumeBroadcast(w http.ResponseWriter, r *http.Request, id int64) {
	rt.setBroadcastStatus(w, id, model.BroadcastRunning)
}

func (rt *Router) cancelBroadcast(w http.ResponseWriter, r *http.Request, id int64) {
	rt.setBroadcastStatus(w, id, model.BroadcastCancelled)
}

func (rt *Router) setBroadcastStatus(w http.ResponseWriter, id int64, status string) {
	if err := rt.mgr.SetBroadcastStatus(id, status); err != nil {
		writeManagerErr(w, err)
		return
	}
	b, err := rt.mgr.GetBroadcast(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (rt *Router) retryBroadcast(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := rt.mgr.RetryBroadcast(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	b, err := rt.mgr.GetBroadcast(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// testBroadcast sends the composed message to the operator's own linked chats before
// anyone else sees it. Broken HTML seen by the whole audience can only be corrected
// by another broadcast, so this is the one guard that actually prevents the mistake.
//
// It goes out through the ADMIN bot, whose linked chats exist for exactly this — the
// panel's established way to reach an admin. Sending it with the user bot's token
// (as this first did) worked only by accident: chat ids are global Telegram user ids,
// so the other bot's token lands in the same chat — but only if the admin had also
// started the user bot themselves, and Telegram refuses outright if they hadn't.
//
// Fidelity is not lost by the swap. What a preview has to prove is the markup, the
// buttons and the attachment, and every bot renders those identically; only the
// sender's name differs, which is not what anyone is checking.
func (rt *Router) testBroadcast(w http.ResponseWriter, r *http.Request) {
	b, file, _, ok := parseBroadcastForm(w, r)
	if !ok {
		return
	}
	if file != nil {
		defer file.Close()
	}
	// Validated like the real thing: a preview that Telegram rejects for a 5000-char
	// text or a javascript: button teaches the operator nothing about the message
	// they were about to send to everyone.
	if err := rt.mgr.ValidateBroadcast(b); err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	token := strings.TrimSpace(set.TGBotToken)
	if !set.TGBotEnabled || token == "" {
		writeErr(w, http.StatusBadRequest,
			"включите админ-бота — тест приходит через него, в привязанный чат")
		return
	}
	chats := set.TelegramChatIDs()
	if len(chats) == 0 {
		writeErr(w, http.StatusBadRequest,
			"нет привязанных админ-чатов — привяжите чат в разделе Telegram, чтобы получать тесты")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	client := telegram.NewClient(token)
	rows := telegram.BroadcastButtonRows(b.Buttons)
	var sendErr error
	for _, chatID := range chats {
		if file != nil {
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				sendErr = err
				break
			}
			// Same keyboard the real run sends: a preview without the buttons would
			// not be a preview of what the audience gets.
			if b.MediaKind == "photo" {
				_, sendErr = client.UploadPhoto(ctx, chatID, b.MediaName, b.Text, rows, file)
			} else {
				_, sendErr = client.UploadDocument(ctx, chatID, b.MediaName, b.Text, rows, file)
			}
		} else {
			sendErr = client.SendMenu(ctx, chatID, b.Text, rows)
		}
		if sendErr != nil {
			break
		}
	}
	if sendErr != nil {
		msg := "не удалось отправить: " + sendErr.Error()
		if telegram.IsUnreachable(sendErr) {
			msg = "привязанный чат недоступен — откройте админ-бота и убедитесь, что он не заблокирован"
		}
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeOK(w)
}
