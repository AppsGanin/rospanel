package server

import (
	"fmt"
	"testing"
)

func TestProbeGuardFlagsScanner(t *testing.T) {
	g := newProbeGuard()

	// Below the threshold: nothing fires, and the running distinct count climbs.
	for i := range probeThreshold - 1 {
		crossed, n := g.observe("1.2.3.4", fmt.Sprintf("/guess-%d", i))
		if crossed {
			t.Fatalf("crossed at %d distinct, want only at %d", i+1, probeThreshold)
		}
		if n != i+1 {
			t.Fatalf("distinct = %d after %d paths, want %d", n, i+1, i+1)
		}
	}

	// The threshold-th distinct path crosses, exactly once.
	crossed, n := g.observe("1.2.3.4", "/guess-final")
	if !crossed || n != probeThreshold {
		t.Fatalf("crossing = (%v, %d), want (true, %d)", crossed, n, probeThreshold)
	}

	// Further scanning from the same IP does not re-fire in the same window.
	if again, _ := g.observe("1.2.3.4", "/guess-more"); again {
		t.Error("crossed twice in one window; want a single record per window")
	}
}

func TestProbeGuardDistinctOnly(t *testing.T) {
	g := newProbeGuard()
	// The same missing path a thousand times is a dead bookmark, not a scan.
	for range probeThreshold * 100 {
		if crossed, _ := g.observe("9.9.9.9", "/dead-link"); crossed {
			t.Fatal("one repeated path crossed the threshold; must count distinct paths")
		}
	}
}

func TestProbeGuardIgnoresBenignAndRoot(t *testing.T) {
	g := newProbeGuard()
	// Browser/crawler auto-requests and the site root must never accrue toward a scan,
	// even far past the threshold.
	benign := []string{"/", "/favicon.ico", "/robots.txt", "/apple-touch-icon.png",
		"/.well-known/security.txt", "/sitemap.xml"}
	for range probeThreshold * 3 {
		for _, p := range benign {
			if crossed, n := g.observe("2.2.2.2", p); crossed || n != 0 {
				t.Fatalf("benign path %q counted (crossed=%v n=%d)", p, crossed, n)
			}
		}
	}
}

func TestProbeGuardPerIP(t *testing.T) {
	g := newProbeGuard()
	// One distinct path from many IPs must not aggregate into a false scan.
	for i := range probeThreshold + 5 {
		if crossed, _ := g.observe(fmt.Sprintf("10.0.0.%d", i), "/admin"); crossed {
			t.Fatal("distinct IPs sharing one path crossed; the guard must be per-IP")
		}
	}
}
