package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// pageFixture is one user of each kind the users page tells apart.
type pageFixture struct {
	ids map[string]int64
}

func newPageFixture(t *testing.T, st *store.Store) pageFixture {
	t.Helper()
	now := time.Now().Unix()
	f := pageFixture{ids: map[string]int64{}}
	mk := func(name string, limit, expire int64) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, limit, expire, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.ids[name] = u.ID
		return u.ID
	}
	anna := mk("Анна", 0, 0)
	if err := st.SetUserTags(anna, []string{"sales"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserNote(anna, "VIP client"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchLastSeen(anna, now-30); err != nil {
		t.Fatal(err)
	}
	bob := mk("bob", 0, 0)
	if err := st.SetUserEnabled(bob, false); err != nil {
		t.Fatal(err)
	}
	alice := mk("alice", 100<<30, now+3*86400)
	if err := st.UpdateTraffic(alice, 5<<30, 5<<30, 0, 0); err != nil {
		t.Fatal(err)
	}
	mk("Ёж", 0, now-86400)
	capped := mk("_x", 1000, 0)
	if err := st.UpdateTraffic(capped, 600, 600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserOnHold("1a", "uuid-1a", "pw", "tok-1a", 0, 10*86400); err != nil {
		t.Fatal(err)
	}
	f.ids["1a"] = f.ids["_x"] + 1
	// Over a one-device limit for longer than the grace: limited by devices.
	crowded, err := st.CreateUser("crowd", "uuid-crowd", "pw", "tok-crowd", 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.ids["crowd"] = crowded.ID
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(crowded.ID, ip, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StampDeviceOverLimit(now - model.DeviceLimitGrace - 10); err != nil {
		t.Fatal(err)
	}
	zed := mk("Zed", 0, now+30*86400)
	if err := st.SetUserTags(zed, []string{"sales", "trial"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func getPage(t *testing.T, h http.Handler, c *http.Cookie, query string) (usersPage, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/users/page?"+query, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/users/page?%s = %d: %s", query, rec.Code, rec.Body.String())
	}
	var p usersPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p, rec.Body.String()
}

func pageNames(p usersPage) []string {
	out := []string{}
	for _, u := range p.Users {
		out = append(out, u.Name)
	}
	return out
}

// The list is one window of rows with everything the page's controls need beside it:
// how many match, how many there are, what each chip would find and every tag in use.
func TestUsersPageFiltersCountsAndWindows(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	op := signIn(t, st, "support", model.RoleOperator, false)
	f := newPageFixture(t, st)

	p, body := getPage(t, h, op, "")
	if p.All != 8 || p.Total != 8 || len(p.Users) != 8 {
		t.Fatalf("all=%d total=%d rows=%d", p.All, p.Total, len(p.Users))
	}
	wantCounts := map[string]int{"active": 4, "online": 2, "expiring": 1, "limited": 2, "disabled": 1, "expired": 1}
	if !reflect.DeepEqual(p.Counts, wantCounts) {
		t.Fatalf("counts %v, want %v", p.Counts, wantCounts)
	}
	if !reflect.DeepEqual(p.Tags, []tagCount{{"sales", 2}, {"trial", 1}}) {
		t.Fatalf("tags %v", p.Tags)
	}
	// Rows carry nothing a row does not draw.
	for _, leak := range []string{"sub_url", "links", "vless", "uuid", "password", "sub_token"} {
		if strings.Contains(body, `"`+leak+`"`) {
			t.Errorf("a list row carries %q", leak)
		}
	}
	if got := pageNames(p); got[0] != "Zed" || got[7] != "Анна" {
		t.Errorf("default order is not newest first: %v", got)
	}

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"filter=expiring", []string{"alice"}},
		{"filter=limited", []string{"crowd", "_x"}},
		{"filter=disabled", []string{"bob"}},
		{"filter=expired", []string{"Ёж"}},
		{"filter=online", []string{"crowd", "Анна"}},
		{"filter=bogus", []string{"Zed", "crowd", "1a", "_x", "Ёж", "alice", "bob", "Анна"}},
		{"q=vip", []string{"Анна"}},
		{"q=АННА", []string{"Анна"}},
		{"q=sal", []string{"Zed", "Анна"}},
		{fmt.Sprintf("q=u%d", f.ids["bob"]), []string{"bob"}},
		{fmt.Sprintf("q=%d", f.ids["alice"]), []string{"alice"}},
		{"tag=trial", []string{"Zed"}},
		{"tag=sales&filter=active", []string{"Zed", "Анна"}},
		{"sort=name&lang=ru", []string{"_x", "1a", "Анна", "Ёж", "alice", "bob", "crowd", "Zed"}},
		{"sort=name&lang=en", []string{"_x", "1a", "alice", "bob", "crowd", "Zed", "Анна", "Ёж"}},
		{"sort=traffic", []string{"alice", "_x", "Zed", "crowd", "1a", "Ёж", "bob", "Анна"}},
		{"sort=expiry", []string{"Ёж", "alice", "1a", "Zed", "crowd", "_x", "bob", "Анна"}},
		{"sort=online", []string{"crowd", "Анна", "Zed", "1a", "_x", "Ёж", "alice", "bob"}},
		{"offset=2&limit=3", []string{"1a", "_x", "Ёж"}},
		{"offset=50", []string{}},
		{"sort=new", []string{"Zed", "crowd", "1a", "_x", "Ёж", "alice", "bob", "Анна"}},
		// A filter and an order together: the page orders what matched, not everyone.
		{"filter=active&sort=name&lang=en", []string{"1a", "alice", "Zed", "Анна"}},
		{"filter=active&sort=name&lang=en&offset=2", []string{"Zed", "Анна"}},
		{"filter=active&sort=name&lang=en&offset=9", []string{}},
	} {
		p, _ := getPage(t, h, op, c.query)
		if got := pageNames(p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.query, got, c.want)
		}
		// The chips count every user whatever is selected: a count taken over the
		// current filter would read the same on every chip.
		if !reflect.DeepEqual(p.Counts, wantCounts) || p.All != 8 {
			t.Errorf("%s: counts %v over %d users, want %v over 8", c.query, p.Counts, p.All, wantCounts)
		}
	}

	// Counts only, and every matching id for "select all".
	p, _ = getPage(t, h, op, "limit=0&filter=active&ids=1")
	if len(p.Users) != 0 || p.Total != 4 || p.Counts["active"] != 4 {
		t.Fatalf("counts-only page: rows=%d total=%d", len(p.Users), p.Total)
	}
	wantIDs := []int64{f.ids["Zed"], f.ids["1a"], f.ids["alice"], f.ids["Анна"]}
	if !reflect.DeepEqual(p.IDs, wantIDs) {
		t.Fatalf("ids %v, want %v", p.IDs, wantIDs)
	}
	if p, _ := getPage(t, h, op, "limit=2"); p.IDs != nil {
		t.Error("ids were sent without being asked for")
	}
	// "Select all" over a filtered, ordered list selects them in the order shown.
	p, _ = getPage(t, h, op, "limit=0&filter=active&ids=1&sort=name&lang=en")
	wantIDs = []int64{f.ids["1a"], f.ids["alice"], f.ids["Zed"], f.ids["Анна"]}
	if !reflect.DeepEqual(p.IDs, wantIDs) {
		t.Fatalf("ordered ids %v, want %v", p.IDs, wantIDs)
	}
}

// The card fetches its one user whole, links included; the member picker gets names.
func TestUserCardAndBriefList(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	op := signIn(t, st, "support", model.RoleOperator, false)
	f := newPageFixture(t, st)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(op)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := get(fmt.Sprintf("/api/users/%d", f.ids["alice"]))
	if rec.Code != http.StatusOK {
		t.Fatalf("card = %d: %s", rec.Code, rec.Body.String())
	}
	var card map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &card)
	if card["name"] != "alice" || card["sub_url"] == nil || card["links"] == nil {
		t.Fatalf("card lacks the user's links: %v", card)
	}
	if rec := get("/api/users/999999"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user = %d, want 404", rec.Code)
	}

	rec = get("/api/users/brief")
	var brief []userBrief
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &brief) != nil || len(brief) != 8 {
		t.Fatalf("brief = %d (%d users): %s", rec.Code, len(brief), rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "tok-") || strings.Contains(rec.Body.String(), "uuid") {
		t.Error("the brief list carries credentials")
	}
	if call(h, "GET", "/api/users/page", nil) != http.StatusUnauthorized || call(h, "GET", "/api/users/brief", nil) != http.StatusUnauthorized {
		t.Error("the list answered without a session")
	}
}

// A summary says about a user exactly what the whole user says: the same status, the
// same device count, the same usage and tags. The page is built from summaries, so a
// summary that drifted from the user would be a page that lies.
func TestUserSummariesMatchWholeUsers(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/page.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := newPageFixture(t, st)
	check := func(when string) {
		t.Helper()
		whole, err := st.ListUsers()
		if err != nil {
			t.Fatal(err)
		}
		summaries, err := st.ListUserSummaries()
		if err != nil {
			t.Fatal(err)
		}
		want := make([]store.UserSummary, len(whole))
		ids := make([]int64, len(whole))
		for i, u := range whole {
			want[i] = store.UserSummary{
				ID: u.ID, Name: u.Name, Note: u.Note, Tags: u.Tags, Enabled: u.Enabled,
				DataLimit: u.DataLimit, ExpireAt: u.ExpireAt, HoldSeconds: u.HoldSeconds,
				UsedUp: u.UsedUp, UsedDown: u.UsedDown, LastSeen: u.LastSeen,
				DeviceLimit: u.DeviceLimit, Status: u.Status,
			}
			ids[i] = u.ID
		}
		if !reflect.DeepEqual(summaries, want) {
			t.Fatalf("%s: summaries differ from whole users:\n got  %+v\n want %+v", when, summaries, want)
		}
		// The rows' device counts, asked for separately, are the whole users' counts.
		counts, err := st.ActiveDeviceCountsOf(ids, time.Now().Unix()-model.DeviceOnlineWindow)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range whole {
			if counts[u.ID] != u.ActiveDevices {
				t.Fatalf("%s: user %d has %d devices as a whole user, %d counted for the row", when, u.ID, u.ActiveDevices, counts[u.ID])
			}
		}
		if counts[f.ids["crowd"]] != 2 {
			t.Fatalf("%s: the fixture's crowded user counts %d devices, want 2", when, counts[f.ids["crowd"]])
		}
	}
	check("addresses count as devices")
	// In "hwid" mode the address count is shown but decides no status.
	if err := st.SetDeviceCountMode(model.DeviceCountHWID); err != nil {
		t.Fatal(err)
	}
	check("hwid mode")
}

// A page with no rows reads neither groups nor devices; a page with rows asks for the
// devices of exactly those rows.
func TestUsersPageReadsLookupsOnlyForItsRows(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/page.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	newPageFixture(t, st)
	summaries, err := st.ListUserSummaries()
	if err != nil {
		t.Fatal(err)
	}
	var groupReads int
	var deviceIDs [][]int64
	look := pageLookups{
		groups: func() map[int64][]model.GroupRef { groupReads++; return nil },
		devices: func(ids []int64) map[int64]int {
			deviceIDs = append(deviceIDs, ids)
			return map[int64]int{ids[0]: 7}
		},
	}
	for _, window := range []string{"limit=0", "offset=100&limit=5"} {
		groupReads, deviceIDs = 0, nil
		q, _ := url.ParseQuery(window)
		p := buildUsersPage(summaries, newUsersIndex(summaries, time.Now().Unix()), q, time.Now().Unix(), look)
		if len(p.Users) != 0 || p.Total != len(summaries) || p.IDs != nil || groupReads != 0 || deviceIDs != nil {
			t.Fatalf("%s: rows=%d total=%d ids=%v, groups read %d times, devices for %v", window, len(p.Users), p.Total, p.IDs, groupReads, deviceIDs)
		}
	}
	groupReads, deviceIDs = 0, nil
	q, _ := url.ParseQuery("offset=1&limit=2")
	p := buildUsersPage(summaries, newUsersIndex(summaries, time.Now().Unix()), q, time.Now().Unix(), look)
	want := []int64{summaries[1].ID, summaries[2].ID}
	if groupReads != 1 || !reflect.DeepEqual(deviceIDs, [][]int64{want}) {
		t.Fatalf("groups read %d times, devices asked for %v, want once and %v", groupReads, deviceIDs, want)
	}
	if p.Users[0].ActiveDevices != 7 || p.Users[1].ActiveDevices != 0 {
		t.Fatalf("rows carry devices %d and %d, want 7 and 0", p.Users[0].ActiveDevices, p.Users[1].ActiveDevices)
	}
}

// Requests share one read of the users for a few seconds, until something is changed
// through the panel or the API: then the next read is fresh, so an operator who edits
// a user and reloads the list sees the edit. What changes by other means (traffic, a
// bot) shows once the snapshot ages out.
func TestUsersListIsSharedUntilAChange(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	op := signIn(t, st, "support", model.RoleOperator, false)
	mk := func(name string) {
		t.Helper()
		if _, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	all := func() int {
		t.Helper()
		p, _ := getPage(t, h, op, "limit=0")
		return p.All
	}
	brief := func() int {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/users/brief", nil)
		req.AddCookie(op)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out []userBrief
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("brief: %v (%s)", err, rec.Body.String())
		}
		return len(out)
	}

	mk("a")
	if got := all(); got != 1 {
		t.Fatalf("first read: %d users", got)
	}
	mk("b") // straight into the store, as a bot or the traffic pass would
	if got, gotBrief := all(), brief(); got != 1 || gotBrief != 1 {
		t.Fatalf("within the TTL the list was read again: page %d, picker %d", got, gotBrief)
	}

	// A change through the panel — even one that fails — makes the next read fresh.
	req := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"name":"c"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(op)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		t.Fatalf("create through the panel: %d %s", rec.Code, rec.Body.String())
	}
	if got := all(); got != 3 {
		t.Fatalf("after a change through the panel the list shows %d users, want 3", got)
	}

	// A plain read changes nothing, and what changed elsewhere shows once the snapshot
	// ages out.
	mk("d")
	if got := all(); got != 3 {
		t.Fatalf("a read counted as a change: %d users", got)
	}
	rt.usersSnap.mu.Lock()
	rt.usersSnap.at = time.Now().Add(-usersSnapshotTTL)
	rt.usersSnap.mu.Unlock()
	if got := all(); got != 4 {
		t.Fatalf("past the TTL the list shows %d users, want 4", got)
	}
}

// Every surface that changes users counts its requests: the panel, the external API
// and the payment callbacks. Reads do not count.
func TestWritingRequestsAreCounted(t *testing.T) {
	rt, st := rolesTestRouter(t)
	rt.apiKeys = newAPIKeyGuard()
	rt.apiLimiter = newIPRateLimiter(600, time.Minute)
	rt.subLimiter = newIPRateLimiter(120, time.Minute)
	rt.decoy = http.NotFoundHandler()
	op := signIn(t, st, "support", model.RoleOperator, false)
	counted := func(name string, want bool, serve func()) {
		t.Helper()
		before := rt.writes.Load()
		serve()
		if got := rt.writes.Load() != before; got != want {
			t.Errorf("%s: counted=%v, want %v", name, got, want)
		}
	}
	panel := rt.panelMux()
	api := rt.apiHandler()
	send := func(h http.Handler, method, path string) func() {
		return func() {
			req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			req.AddCookie(op)
			h.ServeHTTP(httptest.NewRecorder(), req)
		}
	}
	counted("panel read", false, send(panel, "GET", "/api/users/page"))
	counted("panel write", true, send(panel, "POST", "/api/users"))
	counted("API read", false, send(api, "GET", "/v1/users"))
	counted("API write", true, send(api, "PATCH", "/v1/users/1"))

	rt.mu.Lock()
	rt.paySecret = "pay-segment"
	rt.mu.Unlock()
	counted("payment callback", true, send(rt, "POST", "/pay-segment/nope"))
}

// Concurrent readers and writers of the shared list: run under -race.
func TestUsersListSnapshotUnderConcurrency(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	op := signIn(t, st, "support", model.RoleOperator, false)
	newPageFixture(t, st)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				if i%4 == 0 && j%5 == 0 {
					req := httptest.NewRequest("POST", "/api/users", strings.NewReader(fmt.Sprintf(`{"name":"w%d-%d"}`, i, j)))
					req.Header.Set("Content-Type", "application/json")
					req.AddCookie(op)
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					if rec.Code >= 300 {
						t.Errorf("create: %d %s", rec.Code, rec.Body.String())
					}
					continue
				}
				req := httptest.NewRequest("GET", "/api/users/page?sort=name&lang=ru&limit=5", nil)
				req.AddCookie(op)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("page: %d", rec.Code)
				}
			}
		}()
	}
	wg.Wait()
	// Two writers (i = 0, 4) creating at j = 0, 5, 10, 15: eight users on top of the
	// fixture's eight.
	if p, _ := getPage(t, h, op, "limit=0"); p.All != 8+8 {
		t.Fatalf("after the writes the list shows %d users, want %d", p.All, 8+8)
	}
}
