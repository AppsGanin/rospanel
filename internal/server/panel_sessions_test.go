package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func sessionsOf(t *testing.T, h http.Handler, c *http.Cookie) []store.AdminSession {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/account/sessions", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sessions: %d %s", rec.Code, rec.Body.String())
	}
	var out []store.AdminSession
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// The account screen lists the caller's sessions and ends them one at a time or
// all at once — and the id it sends can never reach another admin's session.
func TestAccountSessionsListAndRevoke(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	alice := signIn(t, st, "alice", model.RoleOperator, false)
	aliceID, _ := st.LookupSession(alice.Value)
	// A second session of Alice's, from a phone, and one of Bob's.
	tok2, err := st.CreateSessionFrom(aliceID.ID, time.Hour, "198.51.100.7", "Mozilla/5.0 (iPhone) Safari/605")
	if err != nil {
		t.Fatal(err)
	}
	alice2 := &http.Cookie{Name: sessionCookie, Value: tok2}
	bob := signIn(t, st, "bob", model.RoleOperator, false)
	bobSess, _ := st.LookupSession(bob.Value)

	list := sessionsOf(t, h, alice)
	if len(list) != 2 {
		t.Fatalf("alice should see her 2 sessions, got %d", len(list))
	}
	var current, other *store.AdminSession
	for i := range list {
		if list[i].Current {
			current = &list[i]
		} else {
			other = &list[i]
		}
	}
	if current == nil || other == nil || current.ID != aliceID.SessionID {
		t.Fatalf("exactly one session must be marked current, and it must be this cookie's: %+v", list)
	}
	if other.IP != "198.51.100.7" || other.UserAgent == "" {
		t.Errorf("the phone session lost its origin: %+v", other)
	}

	// The current session cannot be ended from here.
	if code := call(h, http.MethodDelete, "/api/account/sessions/"+itoa64(current.ID), alice); code != http.StatusBadRequest {
		t.Errorf("revoking the current session: %d, want 400", code)
	}
	// Bob's cannot be ended by Alice, and the answer does not say it exists.
	if code := call(h, http.MethodDelete, "/api/account/sessions/"+itoa64(bobSess.SessionID), alice); code != http.StatusNotFound {
		t.Errorf("revoking bob's session as alice: %d, want 404", code)
	}
	if code := call(h, http.MethodGet, "/api/me", bob); code != http.StatusOK {
		t.Errorf("bob's session should be untouched, /api/me gave %d", code)
	}
	// Her own other one can.
	if code := call(h, http.MethodDelete, "/api/account/sessions/"+itoa64(other.ID), alice); code != http.StatusOK {
		t.Errorf("revoking own other session: %d", code)
	}
	if code := call(h, http.MethodGet, "/api/me", alice2); code != http.StatusUnauthorized {
		t.Errorf("revoked cookie still works: %d", code)
	}

	// "Everywhere else": two more sessions, one call, the current one survives.
	for range 2 {
		if _, err := st.CreateSessionFrom(aliceID.ID, time.Hour, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/account/sessions/revoke-others", nil)
	req.AddCookie(alice)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp struct {
		Revoked int `json:"revoked"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || resp.Revoked != 2 {
		t.Errorf("revoke-others: %d %s", rec.Code, rec.Body.String())
	}
	if got := sessionsOf(t, h, alice); len(got) != 1 || !got[0].Current {
		t.Errorf("after revoke-others only the current session should remain: %+v", got)
	}
	if code := call(h, http.MethodGet, "/api/me", bob); code != http.StatusOK {
		t.Errorf("revoke-others reached bob: %d", code)
	}

	// Both revocations left a row in the panel log.
	rows, err := st.ListAdminAudit(store.AdminAuditFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Action == model.AuditSessionRevoked && r.ActorName == "alice" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("want 2 %q rows by alice, got %d", model.AuditSessionRevoked, n)
	}
}

// A request from a session whose last-seen stamp is stale refreshes it, with the
// address the request came from; a fresh stamp is left alone (one write a minute,
// not one per request).
func TestSessionLastSeenIsStampedOncePerInterval(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	c := signIn(t, st, "alice", model.RoleOperator, false)
	a, _ := st.LookupSession(c.Value)

	// Fresh: a request must not move it.
	before := sessionsOf(t, h, c)[0]
	if before.IP != "" {
		t.Fatalf("a session made without an address should have none yet: %+v", before)
	}
	// Stale: push the stamp back past the interval and request again.
	stale := time.Now().Add(-2 * store.SessionTouchInterval).Unix()
	if err := st.TouchSession(a.SessionID, stale, ""); err != nil {
		t.Fatal(err)
	}
	after := sessionsOf(t, h, c)[0]
	if after.LastSeenAt <= stale {
		t.Errorf("stale stamp was not refreshed: %d <= %d", after.LastSeenAt, stale)
	}
	if after.IP == "" {
		t.Errorf("the refresh should record the request's address: %+v", after)
	}
}
