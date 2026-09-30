package store

import "testing"

// An account moved from A to C, and then pushed off C by another account, cannot be
// taken back by A: the Telegram its owner moved it away from.
func TestMovedAccountIsNotRestoredToTheChatItLeft(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	u, _ := s.CreateUser("u", "uuid-u", "pw", "tok-u", 0, 0, 0)
	x, _ := s.CreateUser("x", "uuid-x", "pw", "tok-x", 0, 0, 0)
	const A, C = 1001, 3003
	if err := s.SetUserTelegramChat(u.ID, A); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserTelegramChat(u.ID, C); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserTelegramChat(x.ID, C); err != nil {
		t.Fatal(err)
	}
	if d, err := s.GetDetachedUserByPrevChat(A); err == nil && d != nil {
		t.Fatalf("A can restore %q, the account moved away from it", d.Name)
	}
	// An operator's unlink still leaves the way back open, as before.
	if err := s.SetUserTelegramChat(u.ID, A); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearUserTelegramChat(u.ID); err != nil {
		t.Fatal(err)
	}
	if d, err := s.GetDetachedUserByPrevChat(A); err != nil || d == nil || d.ID != u.ID {
		t.Fatalf("an unlinked account is not restorable by its chat: %v %v", d, err)
	}
}

// A Telegram that had its trial keeps that record whatever its account goes through.
func TestTrialRecordOutlivesTheAccount(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	u, _ := s.CreateUser("u", "uuid-u", "pw", "tok-u", 0, 0, 0)
	const A, B, C = 1001, 2002, 3003
	if err := s.MarkChatTrial(A); err != nil {
		t.Fatal(err)
	}
	_ = s.SetUserTelegramChat(u.ID, A)
	_ = s.SetUserTelegramChat(u.ID, B)
	_ = s.SetUserTelegramChat(u.ID, C)
	_ = s.DeleteUser(u.ID)
	if !s.ChatHadTrial(A) {
		t.Fatal("A lost its trial record after A→B→C and a delete")
	}
	if s.ChatHadTrial(B) || s.ChatHadTrial(C) {
		t.Fatal("chats that never signed up are marked")
	}
}

// Changing Telegram from the page is the operator's to switch on: whoever holds the
// page's link could take the bot account. Binding is on.
func TestPageTelegramSwitchesDefaults(t *testing.T) {
	t.Parallel()
	set, err := newStore(t).GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !set.SubTGBind || set.SubTGRebind {
		t.Fatalf("bind=%v rebind=%v, want bind on and rebind off", set.SubTGBind, set.SubTGRebind)
	}
}
