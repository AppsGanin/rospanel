package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// An incident posted in the panel is on the public page at once — not after the
// page's half-minute cache — and leaves it the same way.
func TestIncidentReachesTheStatusPage(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	rt := h.(*Router)
	if err := st.SetStatusPage(true, "status"); err != nil {
		t.Fatal(err)
	}
	rt.setStatusPath("status")
	c := signIn(t, st, "owner", model.RoleOwner, false)

	// The page is rendered (and cached) before anything is posted.
	if body := getFrom(h, "/status").Body.String(); strings.Contains(body, "Works on NL") {
		t.Fatal("incident on the page before it was posted")
	}

	w := panelJSON(t, rt, http.MethodPost, "/api/incidents",
		`{"kind":"maintenance","title":"Works on NL","body":"back by 15:00","node_ids":[0]}`, c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var saved struct {
		Incident    model.Incident `json:"incident"`
		NotifyError any            `json:"notify_error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Incident.ID == 0 || saved.NotifyError != nil {
		t.Fatalf("create answer: %s (%v)", w.Body.String(), err)
	}
	body := getFrom(h, "/status").Body.String()
	if !strings.Contains(body, "Works on NL") || !strings.Contains(body, "back by 15:00") {
		t.Error("the posted incident is not on the page")
	}
	// (No Xray in a test process: the server itself reads as down, which outranks
	// maintenance in the headline — the badge on its row is what shows the works.)
	if !strings.Contains(body, `class="tag srv-tag maintenance-tone"`) {
		t.Error("maintenance under way does not mark its server")
	}

	list := panelJSON(t, rt, http.MethodGet, "/api/incidents", "", c)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"servers":[{"id":0`) {
		t.Errorf("list: %d %s", list.Code, list.Body.String())
	}

	id := saved.Incident.ID
	if w := panelJSON(t, rt, http.MethodPost, fmt.Sprintf("/api/incidents/%d/resolve", id), `{}`, c); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	if body := getFrom(h, "/status").Body.String(); !strings.Contains(body, `<details class="past `) || strings.Contains(body, `class="tag srv-tag maintenance-tone"`) {
		t.Error("a resolved entry did not move to the history")
	}

	if w := panelJSON(t, rt, http.MethodDelete, fmt.Sprintf("/api/incidents/%d", id), "", c); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if body := getFrom(h, "/status").Body.String(); strings.Contains(body, "Works on NL") {
		t.Error("a deleted entry is still on the page")
	}
	if w := panelJSON(t, rt, http.MethodPost, fmt.Sprintf("/api/incidents/%d", id), `{"kind":"incident","title":"x"}`, c); w.Code != http.StatusBadRequest {
		t.Errorf("editing a deleted entry: %d", w.Code)
	}
}

// A message to every subscriber is a broadcast: an admin who may edit the status
// page but not send broadcasts can post an incident, not announce it.
func TestIncidentNotifyNeedsBroadcasts(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	role, err := st.CreateAdminRole("Status", []string{model.PermSettingsView, model.PermSettingsManage})
	if err != nil {
		t.Fatal(err)
	}
	c := signIn(t, st, "editor", role.Key, false)

	if w := panelJSON(t, rt, http.MethodPost, "/api/incidents", `{"kind":"incident","title":"x","notify":true}`, c); w.Code != http.StatusForbidden {
		t.Errorf("notify without broadcasts: %d %s", w.Code, w.Body.String())
	}
	list, _ := st.ListIncidents(10)
	if len(list) != 0 {
		t.Error("a refused request still saved the incident")
	}
	if w := panelJSON(t, rt, http.MethodPost, "/api/incidents", `{"kind":"incident","title":"x"}`, c); w.Code != http.StatusOK {
		t.Errorf("post without notify: %d %s", w.Code, w.Body.String())
	}

	// The owner may announce; with no user bot the incident is saved and the reason
	// the message did not go comes back beside it.
	owner := signIn(t, st, "owner", model.RoleOwner, false)
	w := panelJSON(t, rt, http.MethodPost, "/api/incidents", `{"kind":"incident","title":"y","notify":true,"lang":"en"}`, owner)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":"err.enableUserBotFirst"`) {
		t.Errorf("notify with no bot: %d %s", w.Code, w.Body.String())
	}
	if list, _ := st.ListIncidents(10); len(list) != 2 {
		t.Errorf("incidents saved: %d, want 2", len(list))
	}
}

func panelJSON(t *testing.T, rt *Router, method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	return w
}

// The same over /v1: created with 201, resolved by a bare POST, and a key without
// the broadcasts permission may post but not announce.
func TestAPIIncidents(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, full := apiFixture(t, h, st)
	limited, err := st.CreateAPIKey("status", false, []string{model.PermSettingsView, model.PermSettingsManage}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if w := apiDo(t, h, http.MethodPost, base+"/v1/incidents", limited.RawKey, `{"kind":"incident","title":"x","notify":true}`); w.Code != http.StatusForbidden {
		t.Errorf("notify without broadcasts: %d %s", w.Code, w.Body.String())
	}
	w := apiDo(t, h, http.MethodPost, base+"/v1/incidents", limited.RawKey, `{"kind":"maintenance","title":"NL works","node_ids":[0]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			Incident model.Incident `json:"incident"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Data.Incident.ID == 0 {
		t.Fatalf("create answer: %s", w.Body.String())
	}
	id := created.Data.Incident.ID

	if w := apiDo(t, h, http.MethodPost, fmt.Sprintf("%s/v1/incidents/%d", base, id), limited.RawKey, `{"kind":"maintenance","title":"x","bogus":1}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown field accepted: %d", w.Code)
	}
	if w := apiDo(t, h, http.MethodPost, fmt.Sprintf("%s/v1/incidents/%d/resolve", base, id), limited.RawKey, ""); w.Code != http.StatusOK {
		t.Errorf("bare resolve: %d %s", w.Code, w.Body.String())
	}
	if got, _ := st.GetIncident(id); got == nil || got.ResolvedAt == 0 {
		t.Errorf("not resolved: %+v", got)
	}

	// A full key may announce; with no user bot the reason comes back in English.
	w = apiDo(t, h, http.MethodPost, base+"/v1/incidents", full, `{"kind":"incident","title":"y","notify":true}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"key":"err.enableUserBotFirst"`) ||
		!strings.Contains(w.Body.String(), `"message":"`) || strings.Contains(w.Body.String(), "сначала") {
		t.Errorf("notify with no bot: %d %s", w.Code, w.Body.String())
	}

	list := apiGet(t, h, base+"/v1/incidents", limited.RawKey)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"servers":[{"id":0`) {
		t.Errorf("list: %d %s", list.Code, list.Body.String())
	}
	if w := apiDo(t, h, http.MethodDelete, fmt.Sprintf("%s/v1/incidents/%d", base, id), limited.RawKey, ""); w.Code != http.StatusOK {
		t.Errorf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := apiDo(t, h, http.MethodDelete, fmt.Sprintf("%s/v1/incidents/%d", base, id), limited.RawKey, ""); w.Code != http.StatusBadRequest {
		t.Errorf("delete twice: %d", w.Code)
	}
}

// What is going on reaches the subscription page, linked to the status page; a step
// of the course posted in the panel reaches the status page.
func TestIncidentOnSubPageAndCourse(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	rt := h.(*Router)
	if err := st.SetStatusPage(true, "status"); err != nil {
		t.Fatal(err)
	}
	rt.setStatusPath("status")
	u, err := mgr.CreateUser(t.Context(), "reader", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	subPage := func() string {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if strings.Contains(subPage(), `class="notices"`) {
		t.Fatal("a notice block with nothing to say")
	}
	c := signIn(t, st, "owner", model.RoleOwner, false)
	w := panelJSON(t, rt, http.MethodPost, "/api/incidents", `{"kind":"incident","title":"Slow <NL>","body":"use Main"}`, c)
	var saved struct {
		Incident model.Incident `json:"incident"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Incident.ID == 0 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	page := subPage()
	for _, want := range []string{`class="notice-card incident"`, "Slow &lt;NL&gt;", "use Main", `href="/status"`} {
		if !strings.Contains(page, want) {
			t.Errorf("subscription page lacks %q", want)
		}
	}

	w = panelJSON(t, rt, http.MethodPost, fmt.Sprintf("/api/incidents/%d/updates", saved.Incident.ID),
		`{"stage":"identified","body":"provider link overloaded"}`, c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"stage":"identified"`) {
		t.Fatalf("post update: %d %s", w.Code, w.Body.String())
	}
	status := getFrom(h, "/status").Body.String()
	if !strings.Contains(status, `class="course"`) || !strings.Contains(status, "provider link overloaded") {
		t.Error("the course is not on the status page")
	}
	if w := panelJSON(t, rt, http.MethodPost, fmt.Sprintf("/api/incidents/%d/resolve", saved.Incident.ID), `{"note":"fixed"}`, c); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(subPage(), `class="notices"`) {
		t.Error("a resolved incident is still on the subscription page")
	}
}

// The subscription page shows an entry about the whole service or about a server
// the reader can use — not one about a server their groups keep them off.
func TestConcernsAny(t *testing.T) {
	t.Parallel()
	reached := map[int64]bool{0: true}
	for _, c := range []struct {
		ids  []int64
		want bool
	}{{nil, true}, {[]int64{0}, true}, {[]int64{2, 0}, true}, {[]int64{2}, false}} {
		if got := concernsAny(&model.Incident{NodeIDs: c.ids}, reached); got != c.want {
			t.Errorf("%v: %v, want %v", c.ids, got, c.want)
		}
	}
}
