package store

import (
	"errors"
	"testing"
)

// Website requests live beside chat ones: several have no chat without colliding,
// each id holds one request, and a request carries exactly one of the two.
func TestWebRegistrationRequests(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	if _, err := st.CreateRegistrationRequest(555, "chat", 1); err != nil {
		t.Fatalf("chat request: %v", err)
	}
	a, err := st.CreateWebRegistrationRequest("a@example.com", "A", "vk", 7, 2)
	if err != nil {
		t.Fatalf("web request a: %v", err)
	}
	if _, err := st.CreateWebRegistrationRequest("b@example.com", "B", "", 0, 3); err != nil {
		t.Fatalf("web request b: %v", err)
	}
	if _, err := st.CreateWebRegistrationRequest("a@example.com", "A again", "", 0, 4); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("second request for one id: %v", err)
	}
	if a.ChatID != 0 || a.ExternalID != "a@example.com" || a.Source != "vk" || a.ReferrerID != 7 {
		t.Fatalf("web request = %+v", a)
	}
	all, err := st.ListRegistrationRequests()
	if err != nil || len(all) != 3 || all[0].ChatID != 555 || all[0].ExternalID != "" {
		t.Fatalf("queue = %+v, %v", all, err)
	}
	if r, _ := st.GetRegistrationRequestByChat(0); r != nil {
		t.Fatalf("chat 0 found a website request: %+v", r)
	}
	if _, err := st.db.Exec(`INSERT INTO registration_requests (chat_id, external_id, name, created_at) VALUES (1, 'x', 'both', 1)`); err == nil {
		t.Fatal("a request with both a chat and an external id was stored")
	}
	if _, err := st.db.Exec(`INSERT INTO registration_requests (name, created_at) VALUES ('neither', 1)`); err == nil {
		t.Fatal("a request with neither was stored")
	}
}

func TestUserExternalID(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	a, _ := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	b, _ := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if err := st.SetUserExternalID(a.ID, "x@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalID(b.ID, "x@example.com"); !errors.Is(err, ErrExternalIDTaken) {
		t.Fatalf("a second account with one id: %v", err)
	}
	if id, err := st.UserIDByExternalID("x@example.com"); err != nil || id != a.ID {
		t.Fatalf("lookup = %d, %v", id, err)
	}
	if id, err := st.UserIDByExternalID(""); err != nil || id != 0 {
		t.Fatalf("empty id matched %d, %v", id, err)
	}
	if err := st.DeleteUser(a.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := st.UserIDByExternalID("x@example.com"); id != 0 {
		t.Fatalf("a deleted account still holds its id: %d", id)
	}
}
