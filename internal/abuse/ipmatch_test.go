package abuse

import (
	"os"
	"strings"
	"testing"
)

func TestIPMatchCIDRAndBoundaries(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"10.0.0.0/24", "192.0.2.5", "2001:db8::/32"})

	hit := []string{
		"10.0.0.0",     // network address
		"10.0.0.1",     //
		"10.0.0.255",   // broadcast / last in /24
		"192.0.2.5",    // single host
		"2001:db8::1",  // inside v6 prefix
		"2001:db8:ffff:ffff::1",
	}
	for _, ip := range hit {
		if cat, ok := m.Match(ip); !ok || cat != CatBadIP {
			t.Errorf("Match(%q) = %q,%v — want badip,true", ip, cat, ok)
		}
	}

	miss := []string{
		"10.0.1.0",   // just past the /24
		"9.255.255.255",
		"192.0.2.4",  // adjacent to the single host
		"192.0.2.6",
		"2001:db9::1", // just past the v6 /32
		"8.8.8.8",
	}
	for _, ip := range miss {
		if cat, ok := m.Match(ip); ok {
			t.Errorf("Match(%q) matched %q — want no match", ip, cat)
		}
	}
}

// TestIPMatchMergesOverlaps: overlapping ranges must collapse so the binary search's
// "at most one covering range" assumption holds.
func TestIPMatchMergesOverlaps(t *testing.T) {
	m := New()
	m.SetIP(CatBadIP, []string{"1.0.0.0/24", "1.0.0.128/25", "1.0.0.200"})
	l := m.ipLists[CatBadIP]
	if len(l.ranges) != 1 {
		t.Fatalf("overlapping ranges not merged: %d ranges", len(l.ranges))
	}
	if _, ok := m.Match("1.0.0.250"); !ok {
		t.Fatal("address in the merged range should match")
	}
}

// TestIPMatchCategoriesCoexist: two IP categories in one matcher, each answering for
// its own ranges, with unlisted addresses missing both.
func TestIPMatchCategoriesCoexist(t *testing.T) {
	m := New()
	m.SetIP(CatCustom, []string{"198.51.100.0/24"})
	m.SetIP(CatBadIP, []string{"203.0.113.0/24"})

	if cat, _ := m.Match("198.51.100.9"); cat != CatCustom {
		t.Fatal("custom range broke")
	}
	if cat, _ := m.Match("203.0.113.9"); cat != CatBadIP {
		t.Fatal("feed range broke")
	}
	if _, ok := m.Match("8.8.8.8"); ok {
		t.Fatal("unlisted ip matched")
	}
}

func TestParseIPList(t *testing.T) {
	in := `# FireHOL-style header
; spamhaus-style comment

1.2.3.0/24
203.0.113.5
198.51.100.0/24 ; hijacked netblock
not-an-ip
10.0.0.0/8	# with tab comment
`
	got, err := ParseIPList(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"1.2.3.0/24", "203.0.113.5", "198.51.100.0/24", "10.0.0.0/8"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNilMatcherIPInert(t *testing.T) {
	var m *Matcher
	if _, ok := m.Match("1.2.3.4"); ok {
		t.Fatal("nil matcher matched an IP")
	}
}

// TestRealFireholFeed loads the actual FireHOL level1 netset if it was fetched to
// /tmp, sanity-checking scale and that a well-known clean IP is absent.
func TestRealFireholFeed(t *testing.T) {
	f, err := os.Open("/tmp/firehol_level1.netset")
	if err != nil {
		t.Skip("no cached firehol feed")
	}
	defer f.Close()
	entries, err := ParseIPList(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := New()
	m.SetIP(CatBadIP, entries)
	t.Logf("firehol level1: %d entries, %d merged ranges", len(entries), len(m.ipLists[CatBadIP].ranges))
	if len(entries) < 100 {
		t.Skipf("feed too small (%d) — likely a failed download", len(entries))
	}
	// Cloudflare/Google DNS must never be on a "safe to block" list.
	for _, clean := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if cat, ok := m.Match(clean); ok {
			t.Errorf("FALSE POSITIVE: %s matched %q on FireHOL level1", clean, cat)
		}
	}
}
