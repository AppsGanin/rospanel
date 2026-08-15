package server

import (
	"hash/fnv"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	// probeThreshold distinct missing paths from one IP within probeWindow marks it a
	// scanner. Set above the handful of stray misses a real visitor or crawler makes,
	// below what a directory brute-forcer fires in seconds.
	probeThreshold = 10
	probeWindow    = 10 * time.Minute
)

// benignMissPaths are paths browsers, crawlers and app stores auto-request that a
// static site often lacks. They are misses, but not scanning — excluded so a normal
// visitor never accrues toward the threshold.
var benignMissPaths = map[string]struct{}{
	"favicon.ico":                      {},
	"robots.txt":                       {},
	"sitemap.xml":                      {},
	"apple-touch-icon.png":             {},
	"apple-touch-icon-precomposed.png": {},
	"browserconfig.xml":                {},
	"ads.txt":                          {},
	"security.txt":                     {},
}

// probeGuard flags an IP that requests many DISTINCT paths the decoy doesn't have —
// the signature of scanning for the hidden panel. Counting distinct paths (not raw
// requests) is deliberate: reloading one dead link a thousand times is a broken
// bookmark, not a scan; guessing a thousand different paths is a scan. It never
// changes what the caller serves — the flag only feeds the operator-facing record —
// so the masquerade holds. Memory is bounded like the other per-IP guards here.
type probeGuard struct {
	mu        sync.Mutex
	ips       map[string]*probeRec
	threshold int
	window    time.Duration
	maxKeys   int
	swept     time.Time
}

type probeRec struct {
	seen    map[uint64]struct{} // distinct missed-path hashes this window
	until   time.Time           // window expiry
	flagged bool                // already crossed the threshold this window
}

func newProbeGuard() *probeGuard {
	return &probeGuard{
		ips:       make(map[string]*probeRec),
		threshold: probeThreshold,
		window:    probeWindow,
		maxKeys:   4096,
	}
}

// observe records that ip requested a missing path. It returns crossed=true exactly
// once per IP per window — the moment the distinct-miss count reaches the threshold —
// so the caller records the scan on the transition, not on every request. distinct is
// the count so far (what to store as the burst size).
func (g *probeGuard) observe(ip, urlPath string) (crossed bool, distinct int) {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" {
		return false, 0 // the site root is not a probe
	}
	if _, ok := benignMissPaths[name]; ok {
		return false, 0
	}
	if strings.HasPrefix(name, ".well-known/") {
		return false, 0
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked()

	now := time.Now()
	r := g.ips[ip]
	if r == nil || now.After(r.until) {
		r = &probeRec{seen: make(map[uint64]struct{})}
		g.ips[ip] = r
	}
	r.until = now.Add(g.window)
	if r.flagged {
		return false, len(r.seen) // already reported this window; stop growing the set
	}
	r.seen[hashPath(name)] = struct{}{}
	if len(r.seen) >= g.threshold {
		r.flagged = true
		return true, len(r.seen)
	}
	return false, len(r.seen)
}

func hashPath(name string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return h.Sum64()
}

// sweepLocked drops expired records (at most once a minute) and, if the map is still
// over its cap, clears it wholesale. Unlike the login limiter there is no lockout to
// preserve — a flag only records, it never blocks — so the cheap reset is fine.
func (g *probeGuard) sweepLocked() {
	now := time.Now()
	if now.Sub(g.swept) < time.Minute && len(g.ips) < g.maxKeys {
		return
	}
	g.swept = now
	for k, r := range g.ips {
		if now.After(r.until) {
			delete(g.ips, k)
		}
	}
	if len(g.ips) > g.maxKeys {
		g.ips = make(map[string]*probeRec)
	}
}
