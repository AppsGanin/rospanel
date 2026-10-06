package core

import (
	"context"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A Telegram sign-up through the API is the panel's own bot's: the trial, the
// Telegram linked, the source and referrer kept; the same account on asking again;
// no second trial once the account is gone; and an unlinked account restored.
func TestSignupByTelegram(t *testing.T) {
	t.Parallel()
	m, trial := signupFixture(t, model.RegOpen, "")
	ctx := context.Background()
	referrer, _ := m.store.CreateUser("ref", "uuid-ref", "pw", "tok-ref", 0, 0, 0)
	code, _ := m.RefCode(referrer.ID)

	res, err := m.Signup(ctx, SignupRequest{TelegramID: 555001, Name: "Ann", Source: "bot_ads", Ref: "r_" + code})
	if err != nil || res.Status != SignupCreated || res.User == nil {
		t.Fatalf("signup: %+v %v", res, err)
	}
	u, _ := m.store.GetUser(res.User.ID)
	if u.TgChatID != 555001 || u.PlanID != trial.ID || !u.TrialUsed || u.Name != "Ann" {
		t.Fatalf("account: chat %d plan %d trial %v name %q", u.TgChatID, u.PlanID, u.TrialUsed, u.Name)
	}
	if got := m.store.UserSource(u.ID); got != "bot_ads" {
		t.Errorf("source = %q", got)
	}
	if w, _ := m.store.GetWallet(u.ID); w.ReferrerID != referrer.ID {
		t.Errorf("referrer = %d, want %d", w.ReferrerID, referrer.ID)
	}
	again, err := m.Signup(ctx, SignupRequest{TelegramID: 555001})
	if err != nil || again.Status != SignupExisting || again.User.ID != u.ID {
		t.Fatalf("again: %+v %v", again, err)
	}
	if _, err := m.Signup(ctx, SignupRequest{TelegramID: 555001, ExternalID: "a@example.com"}); errCode(err) != "err.signupOneID" {
		t.Fatalf("both ids: %v", err)
	}

	// Unlinked, then signing up again: that account back.
	if err := m.UnlinkUserTelegram(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	back, err := m.Signup(ctx, SignupRequest{TelegramID: 555001})
	if err != nil || back.Status != SignupExisting || back.User.ID != u.ID {
		t.Fatalf("restore: %+v %v", back, err)
	}

	// Deleted: an account again, but not a second trial. (A minute later: one
	// sign-up a minute per Telegram.)
	if err := m.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	m.webSignups.mu.Lock()
	m.webSignups.key = nil
	m.webSignups.mu.Unlock()
	second, err := m.Signup(ctx, SignupRequest{TelegramID: 555001})
	if err != nil || second.Status != SignupCreated {
		t.Fatalf("after delete: %+v %v", second, err)
	}
	if u2, _ := m.store.GetUser(second.User.ID); u2.PlanID == trial.ID {
		t.Error("a second trial for the same Telegram")
	}
}

// Moderation files a request for the Telegram, which approval turns into the linked
// account; closed registration refuses.
func TestSignupByTelegramModerated(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegModeration, "")
	ctx := context.Background()
	res, err := m.Signup(ctx, SignupRequest{TelegramID: 777001, Name: "Bob"})
	if err != nil || res.Status != SignupPending || res.RequestID == 0 {
		t.Fatalf("moderated: %+v %v", res, err)
	}
	if again, _ := m.Signup(ctx, SignupRequest{TelegramID: 777001}); again.Status != SignupPending || again.RequestID != res.RequestID {
		t.Fatalf("asking again: %+v", again)
	}
	if err := m.ApproveRegistrationRequest(ctx, res.RequestID); err != nil {
		t.Fatal(err)
	}
	u, err := m.store.GetUserByTelegramChatID(777001)
	if err != nil || u.Name != "Bob" {
		t.Fatalf("approved: %+v %v", u, err)
	}

	closed, _ := signupFixture(t, model.RegOff, "")
	if _, err := closed.Signup(ctx, SignupRequest{TelegramID: 777002}); errCode(err) != "err.signupClosed" {
		t.Fatalf("closed: %v", err)
	}
}
