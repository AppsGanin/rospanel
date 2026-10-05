package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// With the operator's own page set, a browser opening a subscription link is sent
// there with the user's token, while apps — and the page's own ?format= downloads —
// keep getting the subscription from the panel.
func TestSubPageURLSendsBrowsersAway(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	u, err := mgr.CreateUser(t.Context(), "cabinet", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"javascript:alert(1)", "/cabinet", "ftp://x.example/", "https://", "https://a b.example/"} {
		body, _ := json.Marshal(map[string]string{"sub_page_url": bad})
		if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, string(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("sub_page_url %q: %d — want 400", bad, rec.Code)
		}
	}
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key,
		`{"sub_page_url":" https://my.example/cab?sub={token} "}`); rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}

	fetch := func(path string, browser bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if browser {
			req.Header.Set("Accept", "text/html")
		} else {
			req.Header.Set("User-Agent", "Happ/1.0")
		}
		req.RemoteAddr = testClientIP + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	page := "/sub/" + u.SubToken
	rec := fetch(page, true)
	if want := "https://my.example/cab?sub=" + u.SubToken; rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Fatalf("browser: %d → %q, want 302 → %q", rec.Code, rec.Header().Get("Location"), want)
	}
	if rec := fetch(page, false); rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Errorf("app: %d → %q, want the subscription itself", rec.Code, rec.Header().Get("Location"))
	}
	if rec := fetch(page+"?format=clash", true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "proxies") {
		t.Errorf("the page's Clash download: %d, want the YAML", rec.Code)
	}
	if rec := fetch("/sub/not-a-token", true); rec.Header().Get("Location") != "" {
		t.Error("an unknown token was redirected — the decoy must answer it")
	}

	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, `{"sub_page_url":""}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if rec := fetch(page, true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("cleared: %d, want the panel's page back", rec.Code)
	}
}

// The subscription view carries the bot's bind link the page's Telegram button
// opens, under the same switches as the button.
func TestAPISubscriptionTelegramLink(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	const token = "777:sub-view-tg-link"
	if err := st.SetTelegramUserBot(true, token, model.RegOff, ""); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	// The bot's @username, as a getMe would have cached it: no network in tests.
	botNameMu.Lock()
	botNameCache[token+"\x00"+set.TelegramProxyURL()] = botNameEntry{name: "view_bot", at: time.Now()}
	botNameMu.Unlock()
	u, err := mgr.CreateUser(t.Context(), "tg-view", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	view := func() (link string, linked bool) {
		t.Helper()
		var got struct {
			Data struct {
				TGLink   string `json:"tg_link"`
				TGLinked bool   `json:"tg_linked"`
			} `json:"data"`
		}
		rec := apiGet(t, h, base+"/v1/users/"+itoa64(u.ID)+"/subscription", key)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("view: %d %s", rec.Code, rec.Body.String())
		}
		return got.Data.TGLink, got.Data.TGLinked
	}
	link, linked := view()
	stored, _ := st.GetUser(u.ID)
	if linked || stored.TgLinkCode == "" || !strings.HasPrefix(link, "https://t.me/view_bot?start=") ||
		!strings.Contains(link, stored.TgLinkCode) {
		t.Fatalf("tg_link = %q (linked %v, code %q)", link, linked, stored.TgLinkCode)
	}
	if again, _ := view(); again != link {
		t.Errorf("a second read minted a new code: %q, then %q", link, again)
	}
	if err := st.SetSubTGBinding(false, false); err != nil {
		t.Fatal(err)
	}
	if link, _ := view(); link != "" {
		t.Errorf("binding switched off, the view still hands out %q", link)
	}
}
