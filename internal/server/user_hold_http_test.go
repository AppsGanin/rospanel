package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The panel's user routes carry the held term both ways, and refuse a save that
// states a date and a pending term at once — refused before anything is written, so
// the quota posted beside it does not land half of the save.
func TestPanelUserRoutesCarryAHeldTerm(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	c := signIn(t, st, "op", model.RoleOperator, false)

	code, errCode := send(t, rt, http.MethodPost, "/api/users", `{"name":"held","hold_seconds":2592000}`, c, nil)
	if code != http.StatusCreated {
		t.Fatalf("create on hold: %d %s", code, errCode)
	}
	users, _ := st.ListUsers()
	if len(users) != 1 || users[0].HoldSeconds != 2592000 || users[0].ExpireAt != 0 {
		t.Fatalf("created: %+v", users)
	}
	id := users[0].ID
	limits := fmt.Sprintf("/api/users/%d/limits", id)

	date := time.Now().Add(48 * time.Hour).Unix()
	body := fmt.Sprintf(`{"data_limit":1073741824,"expire_at":%d,"device_limit":0,"hold_seconds":604800}`, date)
	if code, errCode := send(t, rt, http.MethodPost, limits, body, c, nil); code != http.StatusBadRequest || errCode != "err.holdWithExpiry" {
		t.Errorf("date and hold together: %d %s — want 400 err.holdWithExpiry", code, errCode)
	}
	if u, _ := st.GetUser(id); u.DataLimit != 0 || u.HoldSeconds != 2592000 {
		t.Errorf("the refused save still wrote: quota %d hold %d", u.DataLimit, u.HoldSeconds)
	}

	// A quota save that says nothing about the hold keeps it (the bots post this body).
	if code, errCode := send(t, rt, http.MethodPost, limits, `{"data_limit":1073741824,"expire_at":0,"device_limit":0}`, c, nil); code != http.StatusOK {
		t.Fatalf("quota only: %d %s", code, errCode)
	}
	if u, _ := st.GetUser(id); u.DataLimit != 1073741824 || u.HoldSeconds != 2592000 {
		t.Errorf("quota only: quota %d hold %d — want the quota and the hold kept", u.DataLimit, u.HoldSeconds)
	}

	// Explicitly no hold, no date: the pending term is taken away.
	if code, errCode := send(t, rt, http.MethodPost, limits, `{"data_limit":0,"expire_at":0,"device_limit":0,"hold_seconds":0}`, c, nil); code != http.StatusOK {
		t.Fatalf("clear: %d %s", code, errCode)
	}
	if u, _ := st.GetUser(id); u.HoldSeconds != 0 || u.ExpireAt != 0 {
		t.Errorf("clear: hold %d expire %d", u.HoldSeconds, u.ExpireAt)
	}

	if code, errCode := send(t, rt, http.MethodPost, limits, `{"data_limit":0,"expire_at":0,"device_limit":0,"hold_seconds":-5}`, c, nil); code != http.StatusBadRequest || errCode != "err.badHold" {
		t.Errorf("negative hold: %d %s — want 400 err.badHold", code, errCode)
	}
}

// The panel's own form: a save that leaves the term out keeps whatever term the user
// has now — started or not — and a term change against a stale picture is refused.
func TestPanelLimitsFormProtectsAStartedTerm(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	c := signIn(t, st, "op", model.RoleOperator, false)
	if code, errCode := send(t, rt, http.MethodPost, "/api/users", `{"name":"held","hold_seconds":2592000}`, c, nil); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, errCode)
	}
	users, _ := st.ListUsers()
	id := users[0].ID
	limits := fmt.Sprintf("/api/users/%d/limits", id)

	// The first connection lands while the card is open.
	seen := time.Now().Unix()
	if _, err := st.RecordConnections([]store.ConnectionHit{{UserID: id, IP: "198.51.100.50", SeenAt: seen, Hits: 1}}); err != nil {
		t.Fatal(err)
	}

	if code, errCode := send(t, rt, http.MethodPost, limits, `{"data_limit":1073741824,"device_limit":2}`, c, nil); code != http.StatusOK {
		t.Fatalf("quota only: %d %s", code, errCode)
	}
	if u, _ := st.GetUser(id); u.ExpireAt != seen+2592000 || u.HoldSeconds != 0 || u.DataLimit != 1073741824 {
		t.Errorf("quota only: expire %d hold %d quota %d — want the started term kept", u.ExpireAt, u.HoldSeconds, u.DataLimit)
	}

	stale := `{"data_limit":1073741824,"device_limit":2,"expire_at":0,"hold_seconds":3888000,"seen_expire_at":0,"seen_hold_seconds":2592000}`
	if code, errCode := send(t, rt, http.MethodPost, limits, stale, c, nil); code != http.StatusBadRequest || errCode != "err.userTermChanged" {
		t.Errorf("stale term: %d %s — want 400 err.userTermChanged", code, errCode)
	}
	if u, _ := st.GetUser(id); u.ExpireAt != seen+2592000 || u.HoldSeconds != 0 {
		t.Errorf("the stale save wrote: expire %d hold %d", u.ExpireAt, u.HoldSeconds)
	}
}

// PATCH /v1/users: expire_at 0 is "never" and takes a pending term away; a PATCH of
// the quota alone leaves a started term as it is.
func TestAPIPatchUserTerm(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	patch := func(id int64, body string) int {
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("%s/v1/users/%d", base, id), strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = testClientIP + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	a, err := mgr.CreateUserWithTerm(t.Context(), "never-now", 0, 0, 2592000)
	if err != nil {
		t.Fatal(err)
	}
	if code := patch(a.ID, `{"expire_at":0}`); code != http.StatusOK {
		t.Fatalf("patch expire_at 0: %d", code)
	}
	if u, _ := st.GetUser(a.ID); u.HoldSeconds != 0 || u.ExpireAt != 0 {
		t.Errorf("expire_at 0: hold %d expire %d — want never, no pending term", u.HoldSeconds, u.ExpireAt)
	}

	b, err := mgr.CreateUserWithTerm(t.Context(), "started", 0, 0, 2592000)
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Unix()
	if _, err := st.RecordConnections([]store.ConnectionHit{{UserID: b.ID, IP: "198.51.100.51", SeenAt: seen, Hits: 1}}); err != nil {
		t.Fatal(err)
	}
	if code := patch(b.ID, `{"data_limit":5368709120}`); code != http.StatusOK {
		t.Fatalf("patch quota: %d", code)
	}
	if u, _ := st.GetUser(b.ID); u.ExpireAt != seen+2592000 || u.HoldSeconds != 0 || u.DataLimit != 5368709120 {
		t.Errorf("quota patch: expire %d hold %d quota %d — want the started term kept", u.ExpireAt, u.HoldSeconds, u.DataLimit)
	}
}
