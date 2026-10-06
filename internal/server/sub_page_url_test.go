package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
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
	// The operator's preview link from the user card opens the panel's own page; a
	// forged, another user's or an expired one is redirected like any visit.
	set, _ := st.GetSettings()
	now := time.Now()
	preview := subPreviewURL(set, u.SubToken, now)
	_, q, _ := strings.Cut(preview, "?")
	if rec := fetch(page+"?"+q, true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("preview: %d → %q, want the panel's page", rec.Code, rec.Header().Get("Location"))
	}
	other, _ := mgr.CreateUser(t.Context(), "other-cabinet", 0, 0)
	exp := now.Add(-time.Minute).Unix()
	for name, v := range map[string]string{
		"forged":  "preview=9999999999.deadbeef",
		"another": strings.Replace(q, "preview=", "preview=", 1) + "x",
		"expired": "preview=" + strconv.FormatInt(exp, 10) + "." + subPreviewSig(u.SubToken, exp),
		"theirs":  "preview=" + strings.TrimPrefix(strings.SplitN(subPreviewURL(set, other.SubToken, now), "?", 2)[1], "preview="),
	} {
		if rec := fetch(page+"?"+v, true); rec.Code != http.StatusFound {
			t.Errorf("%s preview: %d, want the redirect", name, rec.Code)
		}
	}

	// Stored pointing back at this panel under the name the request came by (the
	// save-time check knows only the configured host): served, not looped.
	loop, _ := st.GetSettings()
	loop.SubPageURL = "https://example.com/sub/{token}" // httptest's request host
	if err := st.SetSubSettings(loop); err != nil {
		t.Fatal(err)
	}
	if rec := fetch(page, true); rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Errorf("a page URL back at this panel: %d → %q, want the panel's page", rec.Code, rec.Header().Get("Location"))
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

// The subscription view keeps the settings the panel's page keeps: no configs while
// the page would list none (switched off, or under a required HWID, where a raw link
// would bypass the device cap), no Clash download while its button is off or the
// download is refused, and the maintenance mode.
func TestAPISubscriptionFollowsSubSettings(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	u, err := mgr.CreateUser(t.Context(), "settings-view", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	type view struct {
		Links       []any  `json:"links"`
		ShowConfigs bool   `json:"show_configs"`
		ClashURL    string `json:"clash_url"`
		Maintenance bool   `json:"maintenance"`
	}
	get := func(query string) view {
		t.Helper()
		var got struct {
			Data view `json:"data"`
		}
		rec := apiGet(t, h, base+"/v1/users/"+itoa64(u.ID)+"/subscription"+query, key)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("view%s: %d %s", query, rec.Code, rec.Body.String())
		}
		return got.Data
	}
	set := func(change func(*model.Settings), save func(*model.Settings) error) {
		t.Helper()
		s, _ := st.GetSettings()
		change(s)
		if err := save(s); err != nil {
			t.Fatal(err)
		}
	}

	set(func(s *model.Settings) { s.SubShowConfigs = true }, st.SetSubSettings)
	if v := get(""); !v.ShowConfigs || len(v.Links) == 0 || !strings.HasSuffix(v.ClashURL, "?format=clash&dl=1") || v.Maintenance {
		t.Fatalf("configs on: %+v", v)
	}
	set(func(s *model.Settings) { s.SubShowConfigs = false }, st.SetSubSettings)
	if v := get(""); v.ShowConfigs || len(v.Links) != 0 || v.ClashURL == "" {
		t.Fatalf("configs off: %+v — want no links, the download kept", v)
	}
	// The Clash button, on the page and in the view; Clash clients still get theirs.
	page := "/sub/" + u.SubToken
	const button = "?format=clash&amp;dl=1"
	if !strings.Contains(fetchSubPath(h, page), button) {
		t.Fatal("the page lost its Clash button with the setting on")
	}
	set(func(s *model.Settings) { s.SubShowConfigs, s.SubShowClash = true, false }, st.SetSubSettings)
	if v := get(""); v.ClashURL != "" || len(v.Links) == 0 {
		t.Fatalf("Clash button off: %+v — want no clash_url, the links kept", v)
	}
	if strings.Contains(fetchSubPath(h, page), button) {
		t.Error("the page still offers the Clash download with the button off")
	}
	if rec := fetchSubUA(h, u.SubToken, "clash-verge/v2.0"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "proxies") {
		t.Errorf("a Clash client lost its subscription with the button off: %d", rec.Code)
	}
	set(func(s *model.Settings) { s.SubShowClash = true }, st.SetSubSettings)
	set(func(s *model.Settings) { s.HWIDEnabled, s.HWIDRequire = true, true }, st.SetHWIDSettings)
	if v := get(""); v.ShowConfigs || len(v.Links) != 0 || v.ClashURL != "" {
		t.Fatalf("HWID required: %+v — want neither links nor the download", v)
	}
	if err := st.SetMaintenanceMode(true); err != nil {
		t.Fatal(err)
	}
	if v := get(""); !v.Maintenance {
		t.Fatalf("maintenance: %+v", v)
	}
}

// The user card's button signs the preview at the click: with the operator's own
// page set it lands on a live preview link, without one on the plain link; and it
// is an operator's, not anyone's.
func TestUserCardOpensSubPage(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "card-admin", model.RoleOperator, false)
	u, err := rt.mgr.CreateUser(t.Context(), "card-user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	open := func(c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/users/"+itoa64(u.ID)+"/sub-page", nil)
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := open(nil); rec.Code == http.StatusFound {
		t.Fatal("no session, and still a redirect to the user's page")
	}
	set, _ := st.GetSettings()
	if rec := open(admin); rec.Code != http.StatusFound || rec.Header().Get("Location") != sub.URL(set, u.SubToken) {
		t.Fatalf("no own page: %d → %q", rec.Code, rec.Header().Get("Location"))
	}
	set.SubPageURL = "https://example.com/c?sub={token}"
	if err := st.SetSubSettings(set); err != nil {
		t.Fatal(err)
	}
	rec := open(admin)
	loc := rec.Header().Get("Location")
	_, q, _ := strings.Cut(loc, "?preview=")
	if rec.Code != http.StatusFound || !strings.HasPrefix(loc, sub.URL(set, u.SubToken)+"?preview=") ||
		!validSubPreview(u.SubToken, q, time.Now()) {
		t.Fatalf("own page set: %d → %q", rec.Code, loc)
	}
}
