package server

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func sendAs(t *testing.T, h http.Handler, c *http.Cookie, method, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func rowsByID(p usersPage) map[int64]userRow {
	out := map[int64]userRow{}
	for _, u := range p.Users {
		out[u.ID] = u
	}
	return out
}

// An edit to one user through that user's own route brings that row of the shared list
// up to date and leaves everyone else as read — a user deleted leaves the list, and the
// chips count the edit — while any other write, a create, still reads everyone again.
func TestAnEditToOneUserRereadsOnlyThatUser(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	op := signIn(t, st, "support", model.RoleOperator, false)
	var ids []int64
	for _, name := range []string{"a", "b", "c"} {
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	a, b, c := ids[0], ids[1], ids[2]
	page := func() usersPage {
		t.Helper()
		p, _ := getPage(t, h, op, "limit=50")
		return p
	}
	page() // the shared read

	// Behind the panel's back, as a bot or the traffic pass would write: not seen until
	// everyone is read again.
	if err := st.SetUserName(a, "a (behind)"); err != nil {
		t.Fatal(err)
	}
	if code := sendAs(t, h, op, "POST", fmt.Sprintf("/api/users/%d/name", b), `{"name":"b (edited)"}`); code >= 300 {
		t.Fatalf("rename: %d", code)
	}
	p := page()
	if rows := rowsByID(p); rows[b].Name != "b (edited)" || rows[a].Name != "a" {
		t.Fatalf("after one user's edit: a %q, b %q — want a as read, b as edited", rows[a].Name, rows[b].Name)
	}
	if got := []int64{p.Users[0].ID, p.Users[1].ID, p.Users[2].ID}; !slices.Equal(got, []int64{c, b, a}) {
		t.Errorf("the edited row moved: order %v, want newest first %v", got, []int64{c, b, a})
	}

	// The chips are counted over the list as it now stands.
	if code := sendAs(t, h, op, "POST", fmt.Sprintf("/api/users/%d/enabled", b), `{"enabled":false}`); code >= 300 {
		t.Fatalf("disable: %d", code)
	}
	if p = page(); p.Counts["disabled"] != 1 || rowsByID(p)[b].Status != model.StatusDisabled {
		t.Errorf("a disabled user was not counted: counts %v, status %q", p.Counts, rowsByID(p)[b].Status)
	}

	// Deleted through its own route: gone from the list, and still nobody else reread.
	if code := sendAs(t, h, op, "DELETE", fmt.Sprintf("/api/users/%d", c), ``); code >= 300 {
		t.Fatalf("delete: %d", code)
	}
	p = page()
	if _, ok := rowsByID(p)[c]; ok || p.All != 2 || rowsByID(p)[a].Name != "a" {
		t.Errorf("after a delete: all %d, c listed %v, a %q", p.All, ok, rowsByID(p)[a].Name)
	}

	// Any other write reads everyone again.
	if code := sendAs(t, h, op, "POST", "/api/users", `{"name":"d"}`); code >= 300 {
		t.Fatalf("create: %d", code)
	}
	if p = page(); p.All != 3 || rowsByID(p)[a].Name != "a (behind)" {
		t.Errorf("after a create: all %d, a %q — want everyone read again", p.All, rowsByID(p)[a].Name)
	}
}

// What counts as an edit to one user: a route under a plainly written id, on the panel
// or the API. Nothing else is claimed.
func TestSingleUserWritePaths(t *testing.T) {
	for path, want := range map[string]int64{
		"/api/users/42":             42,
		"/api/users/42/limits":      42,
		"/v1/users/7":               7,
		"/v1/users/7/plan/cancel":   7,
		"/api/users":                0,
		"/api/users/":               0,
		"/api/users/bulk":           0,
		"/api/users/import/marzban": 0,
		"/api/users/042":            0,
		"/api/users/0":              0,
		"/api/users/-3":             0,
		"/api/users/+3":             0,
		"/api/usersx/3":             0,
		"/api/groups/3/members":     0,
		"/v1/users":                 0,
	} {
		got, ok := singleUserWrite(path)
		if (want != 0) != ok || got != want {
			t.Errorf("%s: %d %v, want %d", path, got, ok, want)
		}
	}
}

// Every write route under a user's id is claimed as an edit to that user alone, and the
// list is then brought up to date one row at a time. That is only right for a route that
// changes nothing on the list for anyone else, so each one is listed here by hand: a new
// one fails until someone has looked at what it does to other users' rows.
func TestEveryWriteUnderAUsersIDEditsOnlyThatUser(t *testing.T) {
	rt, _ := rolesTestRouter(t)
	rt.apiKeys = newAPIKeyGuard()
	rt.apiLimiter = newIPRateLimiter(600, time.Minute)
	rt.panelMux()
	rt.apiHandler()
	reviewed := map[string]string{
		"POST /api/users/{id}/groups":           "membership: the groups column is read per page, not kept",
		"DELETE /api/users/{id}":                "the user leaves the list",
		"POST /api/users/{id}/reset":            "that user's usage",
		"POST /api/users/{id}/limits":           "that user's limits",
		"POST /api/users/{id}/enabled":          "that user's switch",
		"POST /api/users/{id}/name":             "that user's name",
		"POST /api/users/{id}/note":             "that user's note",
		"POST /api/users/{id}/tags":             "that user's tags",
		"POST /api/users/{id}/devices/unbind":   "that user's devices, counted per page",
		"POST /api/users/{id}/rotate-sub":       "a token the list does not show",
		"POST /api/users/{id}/telegram/unlink":  "chat fields the list does not show",
		"POST /api/users/{id}/telegram/link":    "chat fields the list does not show (another user losing the chat included)",
		"POST /api/users/{id}/telegram/message": "a message, no row changes",
		"POST /api/users/{id}/reset-period":     "that user's reset period",
		"POST /api/users/{id}/plan":             "that user's plan, term and limits",
		"PATCH /v1/users/{id}":                  "that user's fields",
		"DELETE /v1/users/{id}":                 "the user leaves the list",
		"POST /v1/users/{id}/reset":             "that user's usage",
		"POST /v1/users/{id}/reset-period":      "that user's reset period",
		"POST /v1/users/{id}/rotate-sub":        "a token the list does not show",
		"POST /v1/users/{id}/plan":              "that user's plan, term and limits",
		"POST /v1/users/{id}/plan/cancel":       "that user's plan, term and limits",
		"POST /v1/users/{id}/devices/unbind":    "that user's devices, counted per page",
		"POST /v1/users/{id}/groups":            "membership: the groups column is read per page, not kept",
	}
	seen := map[string]bool{}
	for _, pattern := range append(slices.Clone(rt.routes), rt.apiRoutes...) {
		method, path, _ := strings.Cut(pattern, " ")
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			continue
		}
		if !strings.HasPrefix(path, "/api/users/{id}") && !strings.HasPrefix(path, "/v1/users/{id}") {
			continue
		}
		seen[pattern] = true
		if _, ok := reviewed[pattern]; !ok {
			t.Errorf("%s writes under a user's id and is not reviewed: does it change any other user's row on the list?", pattern)
		}
	}
	for pattern := range reviewed {
		if !seen[pattern] {
			t.Errorf("%s is reviewed but no longer registered", pattern)
		}
	}
}

// Several admins at once — renaming, switching users off and on, deleting, creating —
// while the list is read in every order and something outside the panel writes notes.
// Every page is whole while it happens, and once the admins are done the next page shows
// exactly what they did; what was written outside the panel shows once the list ages.
func TestUsersListUnderConcurrentAdminsAndOutsideWrites(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admins := make([]*http.Cookie, 4)
	for i := range admins {
		admins[i] = signIn(t, st, fmt.Sprintf("op%d", i), model.RoleOperator, false)
	}
	var ids []int64
	for i := range 60 {
		u, err := st.CreateUser(fmt.Sprintf("user%02d", i), fmt.Sprintf("uuid-%d", i), "pw", fmt.Sprintf("tok-%d", i), 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	sorts := []string{"", "name", "traffic", "expiry", "online"}
	stop := make(chan struct{})
	var outside sync.WaitGroup
	outside.Add(1)
	go func() { // a bot and the traffic pass, writing past the panel
		defer outside.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := ids[rand.IntN(len(ids))]
			_ = st.SetUserNote(id, fmt.Sprintf("outside %d", i))
			_ = st.UpdateTraffic(id, int64(rand.IntN(1<<20)), int64(rand.IntN(1<<20)), 0, 0)
		}
	}()

	var wg sync.WaitGroup
	for a, cookie := range admins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 60 {
				id := ids[rand.IntN(len(ids))]
				switch j % 6 {
				case 0:
					sendAs(t, h, cookie, "POST", fmt.Sprintf("/api/users/%d/name", id), fmt.Sprintf(`{"name":"a%d-%d"}`, a, j))
				case 1:
					sendAs(t, h, cookie, "POST", fmt.Sprintf("/api/users/%d/enabled", id), fmt.Sprintf(`{"enabled":%v}`, j%4 == 1))
				case 2:
					if a == 0 && j%12 == 2 {
						sendAs(t, h, cookie, "DELETE", fmt.Sprintf("/api/users/%d", id), ``)
					}
				case 3:
					if a == 1 && j%18 == 3 {
						sendAs(t, h, cookie, "POST", "/api/users", fmt.Sprintf(`{"name":"new-%d"}`, j))
					}
				default:
					q := fmt.Sprintf("limit=20&ids=1&sort=%s&lang=ru&offset=%d", sorts[rand.IntN(len(sorts))], rand.IntN(40))
					if j%5 == 0 {
						q += "&filter=disabled"
					}
					req := httptest.NewRequest("GET", "/api/users/page?"+q, nil)
					req.AddCookie(cookie)
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					var p usersPage
					if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &p) != nil {
						t.Errorf("page %s: %d", q, rec.Code)
						continue
					}
					// Whole: every matching id once, the window drawn from them.
					if len(p.IDs) != p.Total {
						t.Errorf("page %s: %d ids for a total of %d", q, len(p.IDs), p.Total)
					}
					seen := map[int64]bool{}
					for _, id := range p.IDs {
						if seen[id] {
							t.Errorf("page %s: user %d listed twice", q, id)
						}
						seen[id] = true
					}
					for _, u := range p.Users {
						if !seen[u.ID] {
							t.Errorf("page %s: row %d is not among the matching ids", q, u.ID)
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	outside.Wait()

	compare := func(when string, field func(store.UserSummary) string, fromRow func(userRow) string) {
		t.Helper()
		want, err := st.ListUserSummaries()
		if err != nil {
			t.Fatal(err)
		}
		p, _ := getPage(t, h, admins[0], "limit=1000")
		if p.All != len(want) {
			t.Errorf("%s: the list has %d users, the database %d", when, p.All, len(want))
		}
		rows := rowsByID(p)
		for _, u := range want {
			row, ok := rows[u.ID]
			if !ok {
				t.Errorf("%s: user %d is missing from the list", when, u.ID)
				continue
			}
			if got, w := fromRow(row), field(u); got != w {
				t.Errorf("%s: user %d shows %q, the database has %q", when, u.ID, got, w)
			}
		}
	}
	// What the admins did shows at once: names, switches, who exists.
	compare("after the admins",
		func(u store.UserSummary) string { return fmt.Sprintf("%s/%v", u.Name, u.Enabled) },
		func(r userRow) string { return fmt.Sprintf("%s/%v", r.Name, r.Enabled) })
	// What was written outside the panel shows once the list ages.
	rt.usersSnap.mu.Lock()
	rt.usersSnap.at = time.Now().Add(-usersSnapshotTTL)
	rt.usersSnap.mu.Unlock()
	compare("after the list aged",
		func(u store.UserSummary) string {
			return fmt.Sprintf("%s/%v/%d", u.Name, u.Enabled, u.UsedUp+u.UsedDown)
		},
		func(r userRow) string { return fmt.Sprintf("%s/%v/%d", r.Name, r.Enabled, r.UsedUp+r.UsedDown) })
}

// The edit is recognised on the path a real request has once the panel's secret segment
// is taken off — through the whole router, CSRF guard included — and a write the router
// does not hand to a user's route records nothing.
func TestAUserEditIsRecordedThroughTheWholeRouter(t *testing.T) {
	rt, st := rolesTestRouter(t)
	rt.decoy = http.NotFoundHandler()
	rt.panel = securityHeaders(csrfGuard(rt.panelMux()))
	rt.mu.Lock()
	rt.secret = "panel-secret"
	rt.mu.Unlock()
	op := signIn(t, st, "support", model.RoleOperator, false)
	u, err := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-RosPanel-CSRF", "1")
		req.AddCookie(op)
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			t.Fatalf("POST %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	pending := func() map[uint64]int64 {
		rt.usersSnap.mu.Lock()
		defer rt.usersSnap.mu.Unlock()
		return maps.Clone(rt.usersSnap.pending)
	}
	post(fmt.Sprintf("/panel-secret/api/users/%d/note", u.ID), `{"note":"seen"}`)
	got := pending()
	if len(got) != 1 {
		t.Fatalf("recorded %v after one user's edit through the router, want that edit", got)
	}
	for n, id := range got {
		if id != u.ID || n != rt.writes.Load() {
			t.Errorf("recorded write %d for user %d, want write %d for user %d", n, id, rt.writes.Load(), u.ID)
		}
	}
	post("/panel-secret/api/users", `{"name":"b"}`)
	if got := pending(); len(got) != 1 {
		t.Errorf("a create recorded an edit: %v", got)
	}
}
