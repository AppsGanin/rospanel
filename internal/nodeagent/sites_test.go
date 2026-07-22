package nodeagent

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// ipN builds a distinct address for index n. The node only buffers addresses now
// (hostnames are filtered out), so the fixtures are IPs.
func ipN(n int) string {
	return fmt.Sprintf("10.%d.%d.%d", n/65536%256, n/256%256, n%256)
}

func sitesAgent() *Agent {
	return &Agent{
		conns: map[string]nodeapi.ConnSample{},
		sites: map[siteKey]int64{},
	}
}

func TestRecordConnCountsDestinations(t *testing.T) {
	a := sitesAgent()
	for range 3 {
		a.recordConn("u7", "1.1.1.1", "203.0.113.1")
	}
	a.recordConn("u7", "1.1.1.1", "203.0.113.2")

	got := a.takeSites(sitesBytesMax)
	if len(got) != 2 {
		t.Fatalf("got %d site samples, want 2: %v", len(got), got)
	}
	counts := map[string]int64{}
	for _, s := range got {
		if s.UserID != 7 {
			t.Fatalf("user id not resolved from email: %+v", s)
		}
		counts[s.Host] = s.Count
	}
	if counts["203.0.113.1"] != 3 || counts["203.0.113.2"] != 1 {
		t.Fatalf("counts = %v", counts)
	}

	// Conns stay deduped per (email, ip) regardless of how many hosts were seen —
	// that set must not inherit destination cardinality.
	if n := len(a.takeConns()); n != 1 {
		t.Fatalf("conns = %d, want 1", n)
	}
}

// TestRecordConnWithoutDestination: a line with no host still counts the device.
func TestRecordConnWithoutDestination(t *testing.T) {
	a := sitesAgent()
	a.recordConn("u7", "1.1.1.1", "")
	if len(a.takeConns()) != 1 {
		t.Fatal("device sighting lost")
	}
	if got := a.takeSites(sitesBytesMax); got != nil {
		t.Fatalf("empty host recorded: %v", got)
	}
}

func TestRecordConnIgnoresJunkEmail(t *testing.T) {
	a := sitesAgent()
	for _, email := range []string{"", "admin", "unotanumber", "u"} {
		a.recordConn(email, "1.1.1.1", "203.0.113.1")
	}
	if got := a.takeSites(sitesBytesMax); got != nil {
		t.Fatalf("junk email produced site samples: %v", got)
	}
}

// TestTakeSitesTruncatesPerUser pins the wire bound: the panel renders a top-N, so
// the long tail must not be shipped, and what does ship must be the busiest hosts.
func TestTakeSitesTruncatesPerUser(t *testing.T) {
	a := sitesAgent()
	// Hosts 0..(3*sitesPerUser) with ascending counts, so the busiest are the last.
	total := 3 * sitesPerUser
	for i := range total {
		for range i + 1 {
			a.recordConn("u1", "1.1.1.1", ipN(i))
		}
	}
	a.recordConn("u2", "1.1.1.1", "198.51.100.1")

	got := a.takeSites(sitesBytesMax)
	var u1, u2 int
	for _, s := range got {
		switch s.UserID {
		case 1:
			u1++
			// Busiest survive: the kept set is the top sitesPerUser counts, i.e. the
			// hosts with index >= total-sitesPerUser (count = index+1).
			if s.Count < int64(total-sitesPerUser+1) {
				t.Fatalf("kept a low-count host: %+v", s)
			}
		case 2:
			u2++
		}
	}
	if u1 != sitesPerUser {
		t.Fatalf("user 1 shipped %d hosts, want %d", u1, sitesPerUser)
	}
	// Truncation is per user: a quiet user is not crowded out by a noisy one.
	if u2 != 1 {
		t.Fatalf("user 2 shipped %d hosts, want 1", u2)
	}
}

// TestTakeSitesFitsSyncBody is the one that keeps this feature from breaking the
// channel it borrows. The panel caps a sync body at 1 MB and 400s anything larger,
// which the agent can only read as a generic failure — so an oversized sites
// payload would stop config pushes and stall traffic reporting.
//
// A per-user row cap does not achieve this: nothing bounds the number of users.
func TestTakeSitesFitsSyncBody(t *testing.T) {
	const bodyCap = 1 << 20 // internal/server/node_api.go

	// Full-length IPv6 — the widest a destination can now be, since only addresses
	// are buffered (a hostname could be 253 bytes; an address cannot).
	longIP := func(i int) string {
		return fmt.Sprintf("2001:0db8:85a3:0000:0000:8a2e:%04x:%04x", i/65536%65536, i%65536)
	}

	cases := []struct {
		name  string
		users int
		hosts int
		host  func(i int) string
	}{
		{"many users, v4", 4000, 40, func(i int) string { return ipN(i) }},
		{"many users, full-length v6", 4000, 40, longIP},
		{"one user, many hosts", 1, 20000, func(i int) string { return ipN(i) }},
		{"few users, full-length v6", 200, 100, longIP},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := sitesAgent()
			for u := range tc.users {
				for h := range tc.hosts {
					a.recordConn(fmt.Sprintf("u%d", u+1), "1.1.1.1", tc.host(h))
				}
			}
			rows := a.takeSites(sitesBytesMax)
			body, err := json.Marshal(nodeapi.SyncRequest{Sites: rows})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Leave room for the traffic deltas, conns and logs that share the body.
			if len(body) > bodyCap/2 {
				t.Fatalf("sites payload is %d bytes of a %d cap (%d rows)",
					len(body), bodyCap, len(rows))
			}
		})
	}
}

// TestTakeSitesZeroBudgetClearsBuffer: when the rest of the body left no room,
// sites must still drain rather than accumulate across syncs.
func TestTakeSitesZeroBudgetClearsBuffer(t *testing.T) {
	a := sitesAgent()
	for i := range 100 {
		a.recordConn("u1", "1.1.1.1", ipN(i))
	}
	if got := a.takeSites(0); got != nil {
		t.Fatalf("zero budget shipped %d rows", len(got))
	}
	if len(a.sites) != 0 {
		t.Fatalf("zero budget left %d buffered — it must clear", len(a.sites))
	}
}

// TestTakeSitesSharesBudgetAcrossUsers: when the budget binds, every user should
// keep their busiest host rather than the first users seen spending it all.
func TestTakeSitesSharesBudgetAcrossUsers(t *testing.T) {
	a := sitesAgent()
	const users = 3000
	for u := range users {
		for h := range 10 {
			a.recordConn(fmt.Sprintf("u%d", u+1), "1.1.1.1", ipN(h))
		}
	}
	seen := map[int64]bool{}
	for _, r := range a.takeSites(sitesBytesMax) {
		seen[r.UserID] = true
	}
	// Not every user need survive a hard budget stop, but the vast majority must.
	if len(seen) < users*9/10 {
		t.Fatalf("only %d of %d users represented — budget not shared", len(seen), users)
	}
}

// TestTakeSitesCapacityNotOverAllocated: sizing the result by users×cap reserved
// 33 MB to hold 1 MB on a node that may be a small box.
func TestTakeSitesCapacityNotOverAllocated(t *testing.T) {
	a := sitesAgent()
	for u := range 500 {
		a.recordConn(fmt.Sprintf("u%d", u+1), "1.1.1.1", "198.51.100.1")
	}
	rows := a.takeSites(sitesBytesMax)
	if c := cap(rows); c > 4*len(rows)+16 {
		t.Fatalf("capacity %d for %d rows", c, len(rows))
	}
}

// TestTakeSitesClears: the buffer must drain, or every sync would resend history.
func TestTakeSitesClears(t *testing.T) {
	a := sitesAgent()
	a.recordConn("u1", "1.1.1.1", "203.0.113.1")
	if len(a.takeSites(sitesBytesMax)) != 1 {
		t.Fatal("nothing taken")
	}
	if got := a.takeSites(sitesBytesMax); got != nil {
		t.Fatalf("second take returned %v, want nil", got)
	}
}

// TestRecordConnBoundsBuffer: admitted keys keep counting past the bound, so an
// overflow degrades to a partial host set rather than frozen counts.
func TestRecordConnBoundsBuffer(t *testing.T) {
	a := sitesAgent()
	for i := range sitesMax + 500 {
		a.recordConn("u1", "1.1.1.1", ipN(i))
	}
	if n := len(a.sites); n > sitesMax {
		t.Fatalf("buffer grew to %d, bound is %d", n, sitesMax)
	}
	before := a.sites[siteKey{userID: 1, host: ipN(0)}]
	a.recordConn("u1", "1.1.1.1", ipN(0))
	if a.sites[siteKey{userID: 1, host: ipN(0)}] != before+1 {
		t.Fatal("an already-counted host stopped counting at the bound")
	}
}

// TestRecordConnIgnoresHostnames: the node ships only addresses, because the panel
// matches destinations against IP-reputation lists and drops anything else — a
// hostname here would be node memory and sync payload spent on a discarded row.
func TestRecordConnIgnoresHostnames(t *testing.T) {
	a := sitesAgent()
	for _, h := range []string{"example.com", "cdn.evil.example", "localhost"} {
		a.recordConn("u1", "1.1.1.1", h)
	}
	if got := a.takeSites(sitesBytesMax); got != nil {
		t.Fatalf("hostnames were buffered: %v", got)
	}
	// The device sighting itself is unaffected.
	if n := len(a.takeConns()); n != 1 {
		t.Fatalf("conns = %d, want 1", n)
	}
}

// TestRecordConnConcurrent runs under -race: the access-log tap and the sync loop
// touch these buffers from different goroutines.
func TestRecordConnConcurrent(t *testing.T) {
	a := sitesAgent()
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 300 {
				a.recordConn(fmt.Sprintf("u%d", g), "1.1.1.1", ipN(i%20))
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			a.takeSites(sitesBytesMax)
			a.takeConns()
		}
	}()
	wg.Wait()
}
