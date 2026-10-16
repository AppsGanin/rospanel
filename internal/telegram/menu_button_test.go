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
	"github.com/AppsGanin/rospanel/internal/model"
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

// The subscription page switched off: the menu button that opened it goes back to
// the default, and the user's card carries the link instead of a button.
func TestMenuButtonGoesWithThePage(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "menu-off.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	url := "https://vpn.example.com/sub/mini"
	if err := st.ExecForTest(`UPDATE settings SET host = 'vpn.example.com', miniapp_path = 'mini',
		sub_page_enabled = 0, tg_menu_url = ? WHERE id = 1`, url); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var reset bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getChatMenuButton"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"type":"web_app","text":"x","web_app":{"url":"` + url + `"}}}`))
		case strings.HasSuffix(r.URL.Path, "/setChatMenuButton"):
			reset = strings.Contains(string(body), `"type":"default"`)
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
	mu.Lock()
	if !reset {
		t.Error("the menu button still opens the switched-off page")
	}
	mu.Unlock()
	if set, _ := st.GetSettings(); set.TGMenuURL != "" {
		t.Errorf("menu address kept: %q", set.TGMenuURL)
	}

	set, _ := st.GetSettings()
	u := model.User{ID: 1, Name: "ann", SubToken: "tok-ann"}
	for _, row := range userMenuRows(set, u, i18n.EN) {
		for _, b := range row {
			if b.WebApp != nil {
				t.Errorf("a button opens the switched-off page: %+v", b)
			}
		}
	}
	if card := userSelfCard(u, set, relinkPanel{st: st}, i18n.EN); !strings.Contains(card, "/sub/tok-ann") {
		t.Errorf("the card does not carry the link:\n%s", card)
	}
}
