package abuse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConfigureTogglesCategories: a disabled category must stop matching, and match
// again once re-enabled — the settings toggle is only real if the live matcher
// follows it.
func TestConfigureTogglesCategories(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Matcher().SetIP(CatBadIP, []string{"203.0.113.0/24"})

	// nil config == all on.
	if _, ok := s.Matcher().Match("203.0.113.5"); !ok {
		t.Fatal("baseline match failed")
	}

	// Disable the feed: it stops matching, the custom list still works.
	s.Configure(map[Category]bool{CatCustom: true}, "198.51.100.0/24")
	if cat, ok := s.Matcher().Match("203.0.113.5"); ok {
		t.Fatalf("disabled feed still matched as %q", cat)
	}
	if cat, ok := s.Matcher().Match("198.51.100.5"); !ok || cat != CatCustom {
		t.Fatalf("custom list should still match: %q,%v", cat, ok)
	}

	// Master off (nothing enabled): nothing matches.
	s.Configure(map[Category]bool{}, "198.51.100.0/24")
	if _, ok := s.Matcher().Match("198.51.100.5"); ok {
		t.Fatal("master-off still matched")
	}
}

// TestConfigureCustomList: the operator's list is parsed and takes priority, and
// clears when emptied.
func TestConfigureCustomList(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Configure(nil, "198.51.100.0/24\n2001:db8::1\n# comment\n\n")

	if cat, ok := s.Matcher().Match("198.51.100.7"); !ok || cat != CatCustom {
		t.Fatalf("custom CIDR: got %q,%v", cat, ok)
	}
	if cat, ok := s.Matcher().Match("2001:db8::1"); !ok || cat != CatCustom {
		t.Fatalf("custom v6: got %q,%v", cat, ok)
	}
	if _, ok := s.Matcher().Match("198.51.101.7"); ok {
		t.Fatal("address outside the custom CIDR matched")
	}

	s.Configure(nil, "")
	if _, ok := s.Matcher().Match("198.51.100.7"); ok {
		t.Fatal("custom entry survived an empty list")
	}
}

// TestConfigureDisablingCustom: custom off must stop matching even with content.
func TestConfigureDisablingCustom(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Configure(map[Category]bool{CatCustom: false}, "198.51.100.0/24")
	if _, ok := s.Matcher().Match("198.51.100.7"); ok {
		t.Fatal("custom matched while disabled")
	}
}

// TestParseCustom: addresses and CIDRs survive; comments, blanks and anything that
// is not an address are skipped rather than poisoning the list.
func TestParseCustom(t *testing.T) {
	got := ParseCustom("198.51.100.0/24\n# skip\n\nevil.example\n2001:db8::1\n203.0.113.7 trailing comment\nnot an address")
	want := []string{"198.51.100.0/24", "2001:db8::1", "203.0.113.7"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestRefreshIsSingleFlight: the operator's "refresh now" button spawns a goroutine
// per click and the route has no rate limit, so overlapping passes must be dropped.
// Two concurrent passes would fight over the temp files — sweepTempFiles removes
// every .dl-*, including the other pass's in-flight download.
func TestRefreshIsSingleFlight(t *testing.T) {
	s := NewStore(t.TempDir())
	// Point the feed at a server that blocks until we let it go, so the first pass is
	// still running while the second one arrives.
	release := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		_, _ = w.Write([]byte("203.0.113.0/24\n"))
	}))
	defer srv.Close()

	orig := Feeds
	Feeds = []Feed{{Category: CatBadIP, URLs: []string{srv.URL}}}
	defer func() { Feeds = orig }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.Refresh(context.Background(), true) }()

	// Wait until the first pass is actually inside the download.
	for range 100 {
		if hits.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A second pass while the first is in flight must return immediately, not fetch.
	done := make(chan struct{})
	go func() { s.Refresh(context.Background(), true); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("overlapping Refresh blocked instead of being dropped")
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("overlapping Refresh fetched too: %d requests, want 1", n)
	}

	close(release)
	wg.Wait()

	// And once the first finished, a later refresh is allowed again.
	s.Refresh(context.Background(), true)
	if n := hits.Load(); n != 2 {
		t.Fatalf("refresh after completion did not run: %d requests, want 2", n)
	}
}
