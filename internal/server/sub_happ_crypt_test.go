package server

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// fetchSubPath fetches a path under a subscription as a browser would.
func fetchSubPath(h http.Handler, path string) string {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept", "text/html")
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

var happHrefRe = regexp.MustCompile(`happ://(add|crypt4)/[^"<\s]+`)

// happHref is the one Happ link a rendered page or redirect carries.
func happHref(t *testing.T, body string) string {
	t.Helper()
	m := happHrefRe.FindAllString(html.UnescapeString(body), -1)
	if len(m) == 0 {
		t.Fatalf("no Happ link in the response:\n%.600s", body)
	}
	for _, other := range m[1:] {
		if strings.HasPrefix(other, "happ://add/") != strings.HasPrefix(m[0], "happ://add/") {
			t.Fatalf("the response carries both a plain and an encrypted Happ link: %v", m)
		}
	}
	return m[0]
}

// The page's Happ button, and the hand-off page the Telegram Mini App opens it
// through, give Happ an encrypted link once the operator asks for one — a link that
// opens to this user's subscription address and to nothing else.
func TestSubPageHandsHappAnEncryptedLink(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "happ", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	subURL := sub.URL(set, u.SubToken)
	page := "/sub/" + u.SubToken

	if got := happHref(t, fetchSubPath(h, page)); got != "happ://add/"+subURL {
		t.Fatalf("switched off, the page's Happ link is %q — want the plain one", got)
	}

	set.SubHappCrypt = true
	if err := mgr.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	if reread, _ := st.GetSettings(); !reread.SubHappCrypt {
		t.Fatal("the setting did not survive a save")
	}
	for name, body := range map[string]string{
		"page":     fetchSubPath(h, page),
		"hand-off": fetchSubPath(h, page+"/app/0"),
	} {
		link := happHref(t, body)
		if !strings.HasPrefix(link, "happ://crypt4/") {
			t.Errorf("%s: Happ link %q is not encrypted", name, link)
			continue
		}
		plain, err := extsub.DecryptHapp(link)
		if err != nil {
			t.Errorf("%s: the link does not decrypt: %v", name, err)
			continue
		}
		if string(plain) != subURL {
			t.Errorf("%s: the link opens %q, want %q", name, plain, subURL)
		}
	}
}

// The user's card asks for the link on its own; the endpoint answers "" while the
// setting is off, so the card shows nothing rather than a link that is not in use.
func TestUserHappLinkEndpoint(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	cookie := signIn(t, st, "op", model.RoleOperator, false)
	u, err := rt.mgr.CreateUser(t.Context(), "carded", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	get := func() (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/"+strconv.FormatInt(u.ID, 10)+"/happ-link", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		rt.panelMux().ServeHTTP(w, req)
		var body struct {
			Link string `json:"link"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Link
	}

	if code, link := get(); code != http.StatusOK || link != "" {
		t.Fatalf("switched off: %d %q — want 200 and no link", code, link)
	}
	set, _ := st.GetSettings()
	set.SubHappCrypt = true
	if err := rt.mgr.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	code, link := get()
	if code != http.StatusOK {
		t.Fatalf("switched on: %d", code)
	}
	plain, err := extsub.DecryptHapp(link)
	if err != nil || string(plain) != sub.URL(set, u.SubToken) {
		t.Errorf("the card's link %q opens %q (%v), want the user's subscription", link, plain, err)
	}
}
