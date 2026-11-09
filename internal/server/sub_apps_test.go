package server

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// The operator orders the page's client buttons and switches some off in the panel:
// the order survives the round trip, the page follows it, and the /app/<n> hand-off
// opens the n-th button of that same list — not the n-th app of the stock one.
func TestSubAppsSetting(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "apps", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cookie := signIn(t, st, "owner", model.RoleOwner, false)
	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/secretpath/api/settings", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
		}
		var form map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &form); err != nil {
			t.Fatal(err)
		}
		return form
	}
	post := func(form map[string]any) (int, string) {
		enc, _ := json.Marshal(form)
		req := httptest.NewRequest(http.MethodPost, "/secretpath/api/settings/subscription", bytes.NewReader(enc))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-RosPanel-CSRF", "1")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	save := func(form map[string]any) {
		enc, _ := json.Marshal(form)
		req := httptest.NewRequest(http.MethodPost, "/secretpath/api/settings/subscription", bytes.NewReader(enc))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-RosPanel-CSRF", "1")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
	}

	form := get()
	// Before any save the wizard is Happ, whose stock links the form shows.
	happ := form["sub_apps"].([]any)[0].(map[string]any)
	if links, _ := happ["links"].(map[string]any); len(links) != 6 || len(form["sub_wizard_apps"].(map[string]any)) != 6 ||
		form["sub_wizard_apps"].(map[string]any)["ios"] != "happ" {
		t.Fatalf("stock wizard: %v %v", form["sub_wizard_apps"], happ["links"])
	}
	save(form)
	if set, _ := st.GetSettings(); set.SubAppLinks != nil {
		t.Fatalf("Happ's untouched stock links were stored: %v", set.SubAppLinks)
	}
	apps, _ := form["sub_apps"].([]any)
	if len(apps) != 12 || apps[0].(map[string]any)["key"] != "happ" {
		t.Fatalf("stock list: %v", form["sub_apps"])
	}
	// Shadowrocket first, Happ and INCY off.
	body := maps.Clone(form)
	body["sub_apps"] = []sub.AppPref{{Key: "shadowrocket", On: true}, {Key: "happ", On: false}, {Key: "incy", On: false}}
	save(body)

	got := get()["sub_apps"].([]any)
	first := got[0].(map[string]any)
	if first["key"] != "shadowrocket" || first["label"] != "Shadowrocket" || first["on"] != true ||
		got[1].(map[string]any)["on"] != false || len(got) != 12 {
		t.Fatalf("after save: %v", got)
	}

	page := "/sub/" + u.SubToken
	html := fetchSubPath(h, page)
	if !strings.Contains(html, "shadowrocket://") || strings.Contains(html, "happ://") || strings.Contains(html, "incy://") {
		t.Fatal("the page does not follow the saved list")
	}
	if strings.Contains(html, `id="wizard"`) {
		t.Fatal("Happ is off but the wizard is still there")
	}
	if hand := fetchSubPath(h, page+"/app/0"); !strings.Contains(hand, "shadowrocket://") {
		t.Fatalf("/app/0 does not open the first button:\n%.400s", hand)
	}

	// The operator's own client goes first; its link carries the subscription.
	body = get()
	body["sub_apps"] = append([]any{map[string]any{"key": "", "custom": true, "label": "MyVPN", "platforms": "iOS", "link": "myvpn://import/{url}", "on": true}}, body["sub_apps"].([]any)...)
	save(body)
	if hand := fetchSubPath(h, page+"/app/0"); !strings.Contains(hand, "myvpn://import/https://") {
		t.Fatalf("/app/0 does not open the operator's client:\n%.400s", hand)
	}
	first = get()["sub_apps"].([]any)[0].(map[string]any)
	if first["custom"] != true || !strings.HasPrefix(first["key"].(string), "x") || first["link"] != "myvpn://import/{url}" {
		t.Fatalf("custom client after save: %v", first)
	}
	// An unsafe link is refused and the saved list stays.
	body = get()
	body["sub_apps"] = append([]any{map[string]any{"custom": true, "label": "Bad", "link": "javascript:alert('{url}')", "on": true}}, body["sub_apps"].([]any)...)
	if code, msg := post(body); code != http.StatusBadRequest {
		t.Fatalf("unsafe link: %d %s", code, msg)
	}
	if got := get()["sub_apps"].([]any)[0].(map[string]any)["label"]; got != "MyVPN" {
		t.Fatalf("a refused save changed the list: first is %v", got)
	}

	// The wizard on another client: its link, refused bad values, and a client the
	// wizard points at cannot be removed out from under it.
	body = get()
	body["sub_wizard_apps"] = map[string]string{"ios": "karing", "android": "karing", "windows": "karing", "macos": "karing", "linux": "karing", "tv": "karing"}
	for _, a := range body["sub_apps"].([]any) {
		if a := a.(map[string]any); a["key"] == "karing" {
			a["links"] = map[string]any{
				"android": []any{map[string]any{"label": "APK", "url": "https://dl.example/karing.apk"}, map[string]any{"label": "empty", "url": ""}},
				"bogus":   []any{map[string]any{"url": "https://x.example"}},
			}
		}
	}
	save(body)
	f := get()
	var karing map[string]any
	for _, a := range f["sub_apps"].([]any) {
		if a := a.(map[string]any); a["key"] == "karing" {
			karing = a
		}
	}
	if f["sub_wizard_apps"].(map[string]any)["android"] != "karing" || len(karing["links"].(map[string]any)) != 1 ||
		len(karing["links"].(map[string]any)["android"].([]any)) != 1 {
		t.Fatalf("wizard after save: %v %v", f["sub_wizard_apps"], karing["links"])
	}
	// The link sits in an onclick, where html/template escapes its slashes.
	if html := fetchSubPath(h, page); !strings.Contains(html, `dl.example\/karing.apk`) || !strings.Contains(html, "Добавить в Karing") {
		t.Fatal("the page's wizard does not follow the setting")
	}
	// A platform of its own: Android on v2rayNG, the rest on Karing.
	body = get()
	body["sub_wizard_apps"] = map[string]string{"android": "v2rayng", "bogus": "happ", "ios": "", "windows": "karing", "macos": "karing", "linux": "karing", "tv": "karing"}
	save(body)
	if f := get(); f["sub_wizard_apps"].(map[string]any)["android"] != "v2rayng" || f["sub_wizard_apps"].(map[string]any)["ios"] != "happ" {
		t.Fatalf("per platform after save: %v", f["sub_wizard_apps"])
	}
	if set, _ := st.GetSettings(); len(set.SubWizardApps) != 5 {
		t.Fatalf("stored: %v (Happ is not stored)", set.SubWizardApps)
	}
	// Karing kept only its Android link, which v2rayNG took over, so Karing drops
	// out; iOS went back to Happ, which is switched off, so iOS is gone too.
	if html := fetchSubPath(h, page); !strings.Contains(html, "Добавить в v2rayNG") || strings.Contains(html, "Добавить в Karing") || strings.Contains(html, `chip" data-plat="ios"`) {
		t.Fatal("the page's wizard is not per platform")
	}
	for _, bad := range []map[string]any{
		{"sub_wizard_apps": map[string]string{"windows": "nope"}},
		{"sub_apps": []sub.AppPref{{Key: "happ", On: true, Links: map[string][]model.SubAppLink{"ios": {{URL: "javascript:alert(1)"}}}}}},
	} {
		body = get()
		maps.Copy(body, bad)
		if code, _ := post(body); code != http.StatusBadRequest {
			t.Fatalf("%v accepted: %d", bad, code)
		}
	}
	body = get()
	body["sub_wizard_apps"] = map[string]any{"ios": get()["sub_apps"].([]any)[0].(map[string]any)["key"]}
	save(body)
	body = get()
	body["sub_apps"] = body["sub_apps"].([]any)[1:]
	if code, _ := post(body); code != http.StatusBadRequest {
		t.Fatalf("removing the wizard's client was accepted: %d", code)
	}

	// A form that does not know the field keeps the saved list.
	body = get()
	delete(body, "sub_apps")
	save(body)
	if got := get()["sub_apps"].([]any)[0].(map[string]any)["label"]; got != "MyVPN" {
		t.Fatalf("a save without sub_apps reset the list: first is %v", got)
	}
}
