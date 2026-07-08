package store

import (
	"path/filepath"
	"testing"
)

// TestUnlinkReattach verifies the anti-trial-farming flow: unlinking a chat
// remembers it in tg_prev_chat_id so the same chat can restore that exact account
// (keeping its consumed trial) instead of registering a fresh trial user.
func TestUnlinkReattach(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "reattach.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u, err := st.CreateUser("alice", "uuid-a", "pw", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const chat = int64(555)
	if err := st.SetUserTelegramChat(u.ID, chat); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Mark the trial as consumed to prove restore preserves it.
	if err := st.SetUserPlan(u.ID, 0, true); err != nil {
		t.Fatalf("set plan: %v", err)
	}

	// Before unlink there's nothing to restore.
	if _, err := st.GetDetachedUserByPrevChat(chat); err == nil {
		t.Fatal("expected no detached user before unlink")
	}

	if err := st.ClearUserTelegramChat(u.ID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	// The chat is no longer actively linked...
	if _, err := st.GetUserByTelegramChatID(chat); err == nil {
		t.Fatal("chat should be unlinked")
	}
	// ...but is restorable.
	got, err := st.GetDetachedUserByPrevChat(chat)
	if err != nil {
		t.Fatalf("detached lookup: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("restored wrong user: got %d want %d", got.ID, u.ID)
	}
	if !got.TrialUsed {
		t.Fatal("restored account lost its consumed-trial flag")
	}

	// Reattaching consumes the restore slot: a second restore must find nothing.
	if err := st.SetUserTelegramChat(got.ID, chat); err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if _, err := st.GetDetachedUserByPrevChat(chat); err == nil {
		t.Fatal("restore slot should be consumed after reattach")
	}
	back, err := st.GetUserByTelegramChatID(chat)
	if err != nil {
		t.Fatalf("relink lookup: %v", err)
	}
	if back.ID != u.ID {
		t.Fatalf("reattached wrong user: got %d want %d", back.ID, u.ID)
	}
}

// TestProtocolNamesRoundTrip covers the 0016 migration + name persistence and the
// ProtoLabel fallback used to render node labels.
func TestProtocolNamesRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "names.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if set.VLESSName != "" || set.HysteriaName != "" {
		t.Fatalf("unexpected default names: %+v", set)
	}

	if err := st.SetProtocolNames("Основной", "", "Резерв", ""); err != nil {
		t.Fatalf("set names: %v", err)
	}
	set, err = st.GetSettings()
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if set.VLESSName != "Основной" || set.TrojanName != "Резерв" {
		t.Fatalf("names not persisted: %+v", set)
	}
	if got := set.ProtoLabel("VLESS-TCP-TLS"); got != "Основной" {
		t.Fatalf("custom label: got %q", got)
	}
	// Empty custom name falls back to the protocol constant.
	if got := set.ProtoLabel("HYSTERIA-UDP"); got != "HYSTERIA-UDP" {
		t.Fatalf("fallback label: got %q", got)
	}
}
