package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

func TestParseBlacklist(t *testing.T) {
	t.Parallel()
	got, _ := parseBlacklist([]byte("# header\n6923193113 #Причина: скан\n79156181 # sharing\n\nnot-an-id # x\n206090793\n-5 # bad\n"))
	want := map[int64]string{6923193113: "Причина: скан", 79156181: "sharing", 206090793: ""}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
	for id, r := range want {
		if got[id] != r {
			t.Errorf("%d: reason %q, want %q", id, got[id], r)
		}
	}
}

// A listed account is refused a signup only while the operator has the list on; the
// card shows the mark either way. Replacing the list drops the accounts it no longer
// holds.
func TestBlacklistEnforcement(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "bl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{store: st}
	if err := st.ReplaceBlacklist(map[int64]string{42: "reseller"}, 100); err != nil {
		t.Fatal(err)
	}
	if m.RegistrationBlacklisted(42) {
		t.Fatal("refused while the list is off")
	}
	if r, ok := m.Blacklisted(42); !ok || r != "reseller" {
		t.Fatalf("Blacklisted(42) = %q, %v", r, ok)
	}
	// Enabling with a list already stored fetches nothing.
	if err := m.SaveBlacklist(context.Background(), true, ""); err != nil {
		t.Fatal(err)
	}
	if !m.RegistrationBlacklisted(42) || m.RegistrationBlacklisted(43) {
		t.Fatal("enforcement does not follow the list")
	}
	if err := st.ReplaceBlacklist(map[int64]string{43: ""}, 200); err != nil {
		t.Fatal(err)
	}
	if m.RegistrationBlacklisted(42) || !m.RegistrationBlacklisted(43) {
		t.Fatal("replacing the list kept the old accounts")
	}
	info, _ := m.BlacklistInfo()
	if info.Count != 1 || info.SyncedAt != 200 || !info.Enabled {
		t.Fatalf("info = %+v", info)
	}
	if err := m.SaveBlacklist(context.Background(), true, "http://example.com/x"); err == nil {
		t.Fatal("a plain-http list address was accepted")
	}
}
