package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The operator writes to a user without Telegram: refused while no webhook takes
// user.message, handed to the external system once one does — text only, since the
// file goes only through the bot.
func TestMessageUserWithoutTelegram(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)
	u, err := rt.mgr.CreateUser(t.Context(), "webonly", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalID(u.ID, "site-42"); err != nil {
		t.Fatal(err)
	}
	send := func(text string, file bool) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		payload, _ := json.Marshal(map[string]any{"text": text,
			"buttons": []map[string]string{{"text": "Открыть", "url": "https://example.com/c"}}})
		_ = mw.WriteField("payload", string(payload))
		if file {
			fw, _ := mw.CreateFormFile("media", "a.txt")
			_, _ = fw.Write([]byte("hello"))
		}
		mw.Close()
		req := httptest.NewRequest("POST", "/api/users/"+itoa64(u.ID)+"/telegram/message", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := send("Привет", false); rec.Code != http.StatusBadRequest {
		t.Fatalf("no Telegram, no webhook: %d %s", rec.Code, rec.Body.String())
	}

	got := make(chan []byte, 4)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
	}))
	t.Cleanup(recv.Close)
	if _, err := st.CreateWebhook(recv.URL, []string{model.WebhookUserMessage}, true); err != nil {
		t.Fatal(err)
	}
	if rec := send("Привет", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("an attachment with no bot to carry it: %d %s", rec.Code, rec.Body.String())
	}
	rec := send("<b>Привет</b>", false)
	var resp struct{ Telegram, Webhook bool }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp.Telegram || !resp.Webhook {
		t.Fatalf("through the webhook: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case body := <-got:
		var p struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatal(err)
		}
		if p.Event != model.WebhookUserMessage || p.Data["text"] != "<b>Привет</b>" || p.Data["telegram_sent"] != false ||
			p.Data["external_id"] != "site-42" || len(p.Data["buttons"].([]any)) != 1 {
			t.Fatalf("user.message = %s", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no user.message delivery")
	}
}
