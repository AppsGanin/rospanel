package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// manualPanel is the little of the panel the manual-payment screen asks for: the
// instructions, and the operator's picture when there is one.
type manualPanel struct {
	Panel
	msg string
	img []byte
}

func (p manualPanel) RequestPlanPayment(context.Context, i18n.Lang, int64, int64, int) (*model.PaymentOrder, string, error) {
	return &model.PaymentOrder{ID: 7, AmountRub: 300}, p.msg, nil
}

func (p manualPanel) ManualPayImage() ([]byte, error) { return p.img, nil }

// botCall is one request the fake Telegram API received.
type botCall struct {
	method  string
	caption string
	photo   []byte
	markup  string
}

// fakeBot stands in for api.telegram.org, recording what the bot sent.
func fakeBot(t *testing.T) (*httptest.Server, func() []botCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []botCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		c := botCall{method: method}
		if err := r.ParseMultipartForm(4 << 20); err == nil {
			c.caption = r.FormValue("caption")
			c.markup = r.FormValue("reply_markup")
			if fhs := r.MultipartForm.File["photo"]; len(fhs) > 0 {
				f, err := fhs[0].Open()
				if err == nil {
					c.photo, _ = io.ReadAll(f)
					f.Close()
				}
			}
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []botCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]botCall(nil), calls...)
	}
}

func manualPayService(t *testing.T, p Panel) *UserService {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "userbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &UserService{store: st, panel: p}
}

// A picture the operator uploaded rides the instructions as a photo — that is what a
// payer scans — with the same buttons the text screen carries. Without one the screen
// is edited into text, as before.
func TestManualPaymentSendsThePicture(t *testing.T) {
	t.Parallel()
	srv, calls := fakeBot(t)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	img := []byte("\x89PNG\r\n\x1a\npicture")
	s := manualPayService(t, manualPanel{msg: "Transfer 300 ₽ to the card", img: img})
	u := model.User{ID: 5, Name: "payer"}

	s.manualPayment(context.Background(), client, 4242, 99, u, 3, 1)

	got := calls()
	if len(got) != 1 || got[0].method != "sendPhoto" {
		t.Fatalf("sent %+v, want one sendPhoto", got)
	}
	if !strings.Contains(got[0].caption, "Transfer 300 ₽") {
		t.Errorf("the photo lost the instructions: %q", got[0].caption)
	}
	if string(got[0].photo) != string(img) {
		t.Error("the photo is not the operator's picture")
	}
	if !strings.Contains(got[0].markup, "vu:plans") {
		t.Errorf("the photo carries no buttons: %q", got[0].markup)
	}

	// No picture: the instructions are edited into the existing screen.
	srv2, calls2 := fakeBot(t)
	s2 := manualPayService(t, manualPanel{msg: "Transfer 300 ₽ to the card"})
	s2.manualPayment(context.Background(), newTestClient(srv2.URL+"/bot", "111:AAA"), 4242, 99, u, 3, 1)
	got = calls2()
	if len(got) != 1 || got[0].method != "editMessageText" {
		t.Fatalf("without a picture: %+v, want one editMessageText", got)
	}
}
