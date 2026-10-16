package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// A user reissues their own link from its page only when the operator allows it, and
// no more often than the interval set.
func TestSubSelfRotate(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "reader", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	rotate := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/sub/"+token+"/rotate", nil)
		req.Header.Set("X-RosPanel-Sub", "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	page := func(token string) string {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}

	if strings.Contains(page(u.SubToken), "rotateLink(this)") {
		t.Error("the button is offered while the operator does not allow it")
	}
	if rec := rotate(u.SubToken); rec.Code == http.StatusOK {
		t.Fatalf("reissued while not allowed: %s", rec.Body.String())
	}

	set, _ := st.GetSettings()
	set.SubSelfRotateHours = 24
	if err := mgr.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page(u.SubToken), "rotateLink(this)") {
		t.Error("no button while allowed")
	}
	rec := rotate(u.SubToken)
	var out struct {
		URL string `json:"url"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("reissue: %d %s", rec.Code, rec.Body.String())
	}
	nu, _ := st.GetUser(u.ID)
	if nu.SubToken == u.SubToken || out.URL != sub.URL(set, nu.SubToken) {
		t.Errorf("new link %q, token %q (was %q)", out.URL, nu.SubToken, u.SubToken)
	}
	if nu.UUID == u.UUID {
		t.Error("the keys stayed: configs imported from the leaked link keep working")
	}
	if strings.Contains(page(u.SubToken), "rotateLink(this)") {
		t.Error("the old link still opens the page")
	}
	if rec := rotate(nu.SubToken); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a second reissue within the day: %d", rec.Code)
	}

	set.SubSelfRotateHours = 5
	if err := mgr.SaveSubSettings(set); err == nil {
		t.Error("an interval outside the choices was saved")
	}
}

// Servers under maintenance leave a subscription, unless that would empty it.
func TestWithoutMaintenance(t *testing.T) {
	t.Parallel()
	all := model.Access{All: true}
	servers := []sub.Server{{Set: &model.Settings{ServerID: 0}, Access: all}, {Set: &model.Settings{ServerID: 2}, Access: all}}
	if got := withoutMaintenance(servers, map[int64]bool{2: true}); len(got) != 1 || got[0].Set.ServerID != 0 {
		t.Errorf("kept %v", got)
	}
	if got := withoutMaintenance(servers, map[int64]bool{0: true, 2: true}); len(got) != 2 {
		t.Errorf("every server under works emptied the subscription: %v", got)
	}
	if got := withoutMaintenance(servers, nil); len(got) != 2 {
		t.Errorf("nothing under works: %v", got)
	}
	// The only server this user's groups give them is under works: what remains is
	// nothing to them, so the subscription stays as it is.
	only2 := model.Access{Tokens: map[string]bool{model.BuiltinToken(2, "vless"): true}}
	restricted := []sub.Server{{Set: &model.Settings{ServerID: 0}, Access: only2}, {Set: &model.Settings{ServerID: 2}, Access: only2}}
	if got := withoutMaintenance(restricted, map[int64]bool{2: true}); len(got) != 2 {
		t.Errorf("a restricted user was left with nothing usable: %v", got)
	}
}

// A Telegram moved more recently than the operator allows: the page says how often
// it may change instead of offering the bot's link.
func TestSubPageTelegramChangeWait(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "reader", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(u.ID, 4242); err != nil {
		t.Fatal(err)
	}
	if err := st.ExecForTest(`UPDATE settings SET tg_user_bot_enabled = 1, tg_user_bot_token = '111:AAA',
		sub_tg_rebind = 1, sub_tg_rebind_hours = 168`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimTGLink(u.ID, time.Now().Unix(), 0, false); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "at most once every week") {
		t.Errorf("no wait note on the page:\n%.400s", body)
	}
}

// The page switched off: a browser gets the decoy, an app the subscription, and the
// Mini App is gone too.
func TestSubPageSwitchedOff(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "reader", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, accept, ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if body := get("/sub/"+u.SubToken, "text/html", "").Body.String(); !strings.Contains(body, "access-card") {
		t.Fatal("the page is not served while on")
	}
	set, _ := st.GetSettings()
	set.SubPageEnabled = false
	if err := mgr.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	page := get("/sub/"+u.SubToken, "text/html", "")
	unknown := get("/no-such-path", "text/html", "")
	if page.Code != unknown.Code || page.Body.String() != unknown.Body.String() {
		t.Error("a browser opening the link gets something other than the decoy")
	}
	app := get("/sub/"+u.SubToken, "", "v2rayNG/1.9")
	if app.Code != http.StatusOK || strings.Contains(app.Body.String(), "<html") || app.Body.Len() == 0 {
		t.Errorf("an app lost its subscription: %d %.120s", app.Code, app.Body.String())
	}
}

// With the page off, nothing hands a browser the bare link: the user card offers no
// button, the apps get no web-page button, a hosted payment does not return to it.
func TestSubPageOffLeavesNoWayToTheDecoy(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "reader", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetSettings()
	cur.SubPageEnabled = false
	if err := mgr.SaveSubSettings(cur); err != nil {
		t.Fatal(err)
	}
	rt := h.(*Router)
	owner := signIn(t, st, "owner", model.RoleOwner, false)
	card := panelJSON(t, rt, http.MethodGet, fmt.Sprintf("/api/users/%d", u.ID), "", owner)
	if !strings.Contains(card.Body.String(), `"no_sub_page":true`) {
		t.Errorf("the card is not told there is no page: %.200s", card.Body.String())
	}
	if got := rt.apiReturnURL("", u.ID); got != "" {
		t.Errorf("a payment returns to the decoy: %q", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
	req.Header.Set("User-Agent", "Happ/1.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if v := rec.Header().Get("Profile-Web-Page-Url"); v != "" {
		t.Errorf("apps are offered a web page that is the decoy: %q", v)
	}
}
