package store

import (
	"path/filepath"
	"testing"
)

// TestTelegramRoundTrip exercises the 0002 migration, the GetSettings scan of the
// new columns, and the telegram setters end to end on a fresh database.
func TestTelegramRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "tg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if set.TGBotEnabled || set.TGBackupCron != "" || set.TGChatIDs != "" {
		t.Fatalf("unexpected telegram defaults: %+v", set)
	}

	if err := st.SetTelegramBot(true, "123456:ABC", "0 3 * * *"); err != nil {
		t.Fatalf("SetTelegramBot: %v", err)
	}
	if err := st.SetTelegramChats("111,222"); err != nil {
		t.Fatalf("SetTelegramChats: %v", err)
	}
	if err := st.SetTelegramLinkCode("deadbeef"); err != nil {
		t.Fatalf("SetTelegramLinkCode: %v", err)
	}

	set, err = st.GetSettings()
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if !set.TGBotEnabled || set.TGBotToken != "123456:ABC" || set.TGBackupCron != "0 3 * * *" {
		t.Fatalf("bot fields not persisted: %+v", set)
	}
	if set.TGLinkCode != "deadbeef" {
		t.Fatalf("link code = %q, want deadbeef", set.TGLinkCode)
	}
	ids := set.TelegramChatIDs()
	if len(ids) != 2 || ids[0] != 111 || ids[1] != 222 {
		t.Fatalf("chat ids = %v, want [111 222]", ids)
	}
	if !set.TelegramAuthorized(222) || set.TelegramAuthorized(333) {
		t.Fatalf("authorization check wrong")
	}
}
