package core

import (
	"context"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TestModerationRegistrationFlow covers the moderation path end to end: a signup is
// a pending request (no user created), approval creates the user and links the chat,
// and rejection drops the request.
func TestModerationRegistrationFlow(t *testing.T) {
	m := bulkTestManager(t)
	ctx := context.Background()

	var moderated []int64
	m.SetAdminModerationNotifier(func(reqID int64, name, plan string) { moderated = append(moderated, reqID) })
	var userMsgs []string
	m.SetUserNotifier(func(chatID int64, html string) { userMsgs = append(userMsgs, html) })

	// Request: no user is created, the admin is prompted, the chat is "pending".
	ok, err := m.RequestRegistration(ctx, 555, "Петя")
	if err != nil || !ok {
		t.Fatalf("request: ok=%v err=%v", ok, err)
	}
	if users, _ := m.store.ListUsers(); len(users) != 0 {
		t.Fatalf("moderation must not create a user, got %d", len(users))
	}
	if !m.RegistrationPending(555) {
		t.Fatal("chat should be pending after a request")
	}
	if len(moderated) != 1 {
		t.Fatalf("moderation notifier called %d times, want 1", len(moderated))
	}

	// A second request from the same chat is a no-op (still one pending).
	if ok, _ := m.RequestRegistration(ctx, 555, "Петя"); ok {
		t.Fatal("duplicate request must return ok=false")
	}

	// Approve → the user now exists, is active, linked to the chat; request gone.
	if err := m.ApproveRegistrationRequest(ctx, moderated[0]); err != nil {
		t.Fatalf("approve: %v", err)
	}
	users, _ := m.store.ListUsers()
	if len(users) != 1 || !users[0].Enabled || users[0].TgChatID != 555 {
		t.Fatalf("approved user wrong: %+v", users)
	}
	if m.RegistrationPending(555) {
		t.Fatal("request should be gone after approval")
	}
	if len(userMsgs) != 1 {
		t.Fatalf("applicant notified %d times on approve, want 1", len(userMsgs))
	}

	// A fresh request can be rejected → dropped, no user, applicant told.
	ok, err = m.RequestRegistration(ctx, 777, "Вова")
	if err != nil || !ok {
		t.Fatalf("request 2: ok=%v err=%v", ok, err)
	}
	req, _ := m.store.GetRegistrationRequestByChat(777)
	if err := m.RejectRegistrationRequest(ctx, req.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if m.RegistrationPending(777) {
		t.Fatal("rejected request must be gone")
	}
	if users, _ := m.store.ListUsers(); len(users) != 1 {
		t.Fatalf("reject must not create a user, total users %d", len(users))
	}
}

// TestRegModeHelpers checks the mode fallback and derived predicates.
func TestRegModeHelpers(t *testing.T) {
	cases := []struct {
		mode          string
		legacyEnabled bool
		wantMode      string
		open          bool
		activates     bool
	}{
		{model.RegOpen, false, model.RegOpen, true, true},
		{model.RegModeration, false, model.RegModeration, true, false},
		{model.RegInvite, false, model.RegInvite, true, true},
		{model.RegOff, true, model.RegOff, false, false},
		{"", true, model.RegOpen, true, true},   // legacy on ⇒ open
		{"", false, model.RegOff, false, false}, // legacy off ⇒ closed
	}
	for _, tc := range cases {
		s := &model.Settings{TGUserRegMode: tc.mode, TGUserRegEnabled: tc.legacyEnabled}
		if s.RegMode() != tc.wantMode || s.RegistrationOpen() != tc.open || s.RegistrationActivates() != tc.activates {
			t.Errorf("mode=%q legacy=%v → %q/%v/%v, want %q/%v/%v",
				tc.mode, tc.legacyEnabled, s.RegMode(), s.RegistrationOpen(), s.RegistrationActivates(),
				tc.wantMode, tc.open, tc.activates)
		}
	}
}
