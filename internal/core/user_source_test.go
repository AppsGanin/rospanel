package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

func TestNormalizeSource(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"VK_Ads-2026": "vk_ads-2026",
		" tg post#1 ": "tgpost1",
		"реклама":     "",
		"":            "",
	} {
		if got := NormalizeSource(in); got != want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", in, got, want)
		}
	}
}

// The first tag a chat brings is the one its account gets at registration, and the
// funnel groups users by it.
func TestSourceTrackedToFunnel(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{store: st}
	m.TrackSource(100, "VK_ads")
	m.TrackSource(100, "later") // the first tag stays
	a, _ := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	m.AttachReferrer(context.Background(), a.ID, 100)
	b, _ := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	m.AttachReferrer(context.Background(), b.ID, 200) // a chat with no tag
	if got := m.UserSource(a.ID); got != "vk_ads" {
		t.Fatalf("source = %q, want vk_ads", got)
	}
	if got := m.UserSource(b.ID); got != "" {
		t.Fatalf("untagged user got %q", got)
	}
	if err := m.SetUserSource(context.Background(), b.ID, "Партнёр partner"); err != nil {
		t.Fatal(err)
	}
	if got := m.UserSource(b.ID); got != "partner" {
		t.Fatalf("set source = %q", got)
	}
	rows, err := m.FunnelBySource(0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Source] = r.Joined
	}
	if got["vk_ads"] != 1 || got["partner"] != 1 || len(got) != 2 {
		t.Fatalf("by source = %v", got)
	}
	if err := m.SetUserSource(context.Background(), 9999, "x"); err == nil {
		t.Fatal("an unknown user took a source")
	}
}
