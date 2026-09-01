package store

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
)

func newAdminForSessions(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	hash, err := auth.HashPassword("a-password")
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateAdmin(name, hash, model.RoleAdmin, false)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return id
}

// A session carries where it was opened from, is listed by its owner with that
// trace, and can be ended by id — but only by its owner, and never by someone
// holding a different admin's cookie and a guessed integer.
func TestSessionMetaListAndRevoke(t *testing.T) {
	st := newStore(t)
	alice := newAdminForSessions(t, st, "alice")
	bob := newAdminForSessions(t, st, "bob")

	longUA := strings.Repeat("x", MaxSessionUserAgent+50)
	tokA1, err := st.CreateSessionFrom(alice, time.Hour, "203.0.113.5", "Mozilla/5.0 (Macintosh) Chrome/120")
	if err != nil {
		t.Fatal(err)
	}
	tokA2, err := st.CreateSessionFrom(alice, time.Hour, "198.51.100.9", longUA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSessionFrom(bob, time.Hour, "192.0.2.1", "curl/8"); err != nil {
		t.Fatal(err)
	}

	a1, ok := st.LookupSession(tokA1)
	if !ok || a1.SessionID == 0 || a1.LastSeenAt == 0 {
		t.Fatalf("lookup must return the session id and a last-seen stamp: %+v ok=%v", a1, ok)
	}
	a2, _ := st.LookupSession(tokA2)

	list, err := st.ListAdminSessions(alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("alice has 2 sessions, listed %d (bob's must not appear)", len(list))
	}
	byID := map[int64]AdminSession{}
	for _, s := range list {
		byID[s.ID] = s
		if s.ExpiresAt == 0 || s.CreatedAt == 0 {
			t.Errorf("session %d missing timestamps: %+v", s.ID, s)
		}
	}
	if s := byID[a1.SessionID]; s.IP != "203.0.113.5" || !strings.Contains(s.UserAgent, "Chrome") {
		t.Errorf("session 1 lost its origin: %+v", s)
	}
	if s := byID[a2.SessionID]; len(s.UserAgent) != MaxSessionUserAgent {
		t.Errorf("user agent should be capped at %d, stored %d", MaxSessionUserAgent, len(s.UserAgent))
	}

	// A touch moves the stamp and the address — the list must show where the
	// session is being used from NOW.
	if err := st.TouchSession(a1.SessionID, time.Now().Unix()+5, "198.51.100.77"); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListAdminSessions(alice)
	if list[0].ID != a1.SessionID || list[0].IP != "198.51.100.77" {
		t.Errorf("touched session should lead the list with its new address: %+v", list[0])
	}

	// Bob cannot end Alice's session by id.
	if gone, err := st.DeleteAdminSessionByID(bob, a1.SessionID); err != nil || gone {
		t.Errorf("bob revoked alice's session: gone=%v err=%v", gone, err)
	}
	if _, ok := st.LookupSession(tokA1); !ok {
		t.Fatal("alice's session should have survived bob's attempt")
	}
	// Alice can, and the cookie stops working at once.
	if gone, err := st.DeleteAdminSessionByID(alice, a1.SessionID); err != nil || !gone {
		t.Errorf("alice could not revoke her own session: gone=%v err=%v", gone, err)
	}
	if _, ok := st.LookupSession(tokA1); ok {
		t.Error("revoked session still resolves")
	}

	// "Everywhere else" keeps exactly the one named.
	tokA3, _ := st.CreateSessionFrom(alice, time.Hour, "", "")
	n, err := st.DeleteOtherAdminSessions(alice, a2.SessionID)
	if err != nil || n != 1 {
		t.Errorf("revoke others: n=%d err=%v", n, err)
	}
	if _, ok := st.LookupSession(tokA3); ok {
		t.Error("the other session survived")
	}
	if _, ok := st.LookupSession(tokA2); !ok {
		t.Error("the kept session was revoked")
	}
	// And none of this touched Bob.
	if bl, _ := st.ListAdminSessions(bob); len(bl) != 1 {
		t.Errorf("bob's sessions were disturbed: %d", len(bl))
	}
}

// Sessions that predate the rebuild (migration 0058) arrive with no address or
// client; they must still list, and still be revocable.
func TestSessionWithoutMetaStillListsAndRevokes(t *testing.T) {
	st := newStore(t)
	id := newAdminForSessions(t, st, "old")
	tok, err := st.CreateSession(id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.ListAdminSessions(id)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v (%d)", err, len(list))
	}
	if list[0].IP != "" || list[0].UserAgent != "" {
		t.Errorf("a bare session should carry empty meta, got %+v", list[0])
	}
	if gone, _ := st.DeleteAdminSessionByID(id, list[0].ID); !gone {
		t.Error("could not revoke")
	}
	if _, ok := st.LookupSession(tok); ok {
		t.Error("still valid after revoke")
	}
}
