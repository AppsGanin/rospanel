package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/store"
)

// A renamed menu button reaches a bot already running: the button at our address
// with last release's label is set again, one with today's label is left alone.
func TestMenuButtonFollowsItsLabel(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "menu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ExecForTest(`UPDATE settings SET host = 'vpn.example.com', miniapp_path = 'mini', tg_lang = 'ru' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	url := "https://vpn.example.com/sub/mini"
	var mu sync.Mutex
	label := "Мой VPN"
	sets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getChatMenuButton"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"type":"web_app","text":"` + label + `","web_app":{"url":"` + url + `"}}}`))
		case strings.HasSuffix(r.URL.Path, "/setChatMenuButton"):
			var p struct {
				MenuButton struct {
					Text string `json:"text"`
				} `json:"menu_button"`
			}
			_ = json.Unmarshal(body, &p)
			label = p.MenuButton.Text
			sets++
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer srv.Close()
	s := &UserService{store: st}
	c := newTestClient(srv.URL+"/bot", "111:AAA")
	if !s.publishMenuButton(context.Background(), c) {
		t.Fatal("publish failed")
	}
	if want := i18n.T(i18n.RU, "user.menuApp"); sets != 1 || label != want {
		t.Fatalf("after the rename: %d sets, label %q (want %q)", sets, label, want)
	}
	if !s.publishMenuButton(context.Background(), c) || sets != 1 {
		t.Errorf("an up-to-date button was set again (%d sets)", sets)
	}
}
