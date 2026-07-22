package abuse

import (
	"fmt"
	"sync"
	"testing"
)

// TestMatchIgnoresHostnames pins the IP-only contract: a hostname is never matched,
// no matter what it looks like, because there are no domain lists to test it
// against. This is the guard against quietly re-growing domain matching.
func TestMatchIgnoresHostnames(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"203.0.113.0/24"})

	for _, h := range []string{
		"evil.example",
		"203.0.113.4.evil.example", // looks IP-ish, is not
		"cdn.bbc.co.uk",
		"localhost",
		"",
	} {
		if cat, ok := m.Match(h); ok {
			t.Errorf("Match(%q) matched %q — hostnames must never match", h, cat)
		}
	}
}

func TestMatchAddresses(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"203.0.113.0/24", "198.51.100.7", "2001:db8::/32"})

	hit := []string{"203.0.113.0", "203.0.113.4", "203.0.113.255", "198.51.100.7", "2001:db8::1"}
	for _, a := range hit {
		if cat, ok := m.Match(a); !ok || cat != CatBadIP {
			t.Errorf("Match(%q) = %q,%v — want badip,true", a, cat, ok)
		}
	}

	miss := []string{"203.0.112.255", "203.0.114.0", "198.51.100.8", "2001:db9::1", "8.8.8.8"}
	for _, a := range miss {
		if cat, ok := m.Match(a); ok {
			t.Errorf("Match(%q) matched %q — outside every range", a, cat)
		}
	}
}

// TestMatchOrderCustomWins: the operator's own list decides when both match.
func TestMatchOrderCustomWins(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"203.0.113.0/24"})
	m.SetIP(CatCustom, []string{"203.0.113.5"})

	if cat, _ := m.Match("203.0.113.5"); cat != CatCustom {
		t.Fatalf("got %q, want custom to win", cat)
	}
	if cat, _ := m.Match("203.0.113.6"); cat != CatBadIP {
		t.Fatalf("got %q, want badip for an address only it covers", cat)
	}
}

func TestNilMatcherIsInert(t *testing.T) {
	var m *Matcher
	if _, ok := m.Match("203.0.113.5"); ok {
		t.Fatal("nil matcher matched")
	}
	if m.Counts() != nil {
		t.Fatal("nil matcher reported counts")
	}
	m.Clear(CatBadIP) // must not panic
}

func TestEmptyMatcher(t *testing.T) {
	m := New()
	if _, ok := m.Match("203.0.113.5"); ok {
		t.Fatal("empty matcher matched")
	}
}

// TestClearDisablesCategory: a cleared category stops matching, which is what a
// settings toggle relies on.
func TestClearDisablesCategory(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"203.0.113.0/24"})
	if _, ok := m.Match("203.0.113.5"); !ok {
		t.Fatal("baseline match failed")
	}
	m.Clear(CatBadIP)
	if cat, ok := m.Match("203.0.113.5"); ok {
		t.Fatalf("cleared category still matched as %q", cat)
	}
	if n := m.Counts()[CatBadIP]; n != 0 {
		t.Fatalf("cleared category still counts %d entries", n)
	}
}

// TestSetIPReplaces: a reload replaces rather than merges.
func TestSetIPReplaces(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"203.0.113.0/24"})
	m.SetIP(CatBadIP, []string{"198.51.100.0/24"})
	if _, ok := m.Match("203.0.113.5"); ok {
		t.Fatal("stale range survived a reload")
	}
	if _, ok := m.Match("198.51.100.5"); !ok {
		t.Fatal("reloaded range missing")
	}
}

func TestConcurrentMatchAndReload(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"203.0.113.0/24"})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				m.Match(fmt.Sprintf("203.0.113.%d", i%256))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 20 {
			m.SetIP(CatBadIP, []string{fmt.Sprintf("203.0.%d.0/24", i), "203.0.113.0/24"})
		}
	}()
	wg.Wait()
}

// BenchmarkMatch sizes the hot path: Match runs for every access-log line that
// carries a destination.
func BenchmarkMatch(b *testing.B) {
	m := New()
	ranges := make([]string, 0, 50_000)
	for i := range 50_000 {
		ranges = append(ranges, fmt.Sprintf("10.%d.%d.0/24", i/256%256, i%256))
	}
	m.SetIP(CatBadIP, ranges)
	b.ResetTimer()
	for b.Loop() {
		m.Match("142.250.180.2")
	}
}
