package server

import (
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

// loginLimiter throttles login attempts to blunt brute-forcing. It tracks failures
// both per client IP (the primary gate — a single source is locked out after
// maxFails) and per account name (a secondary, higher ceiling that slows a
// distributed attack spread across many IPs against one username). Both windows
// auto-expire, so any lockout is temporary; a correct password clears them
// immediately. Expired records are swept and the IP map is hard-capped, so a spray
// from many addresses can't grow it without bound (memory-exhaustion DoS).
type loginLimiter struct {
	mu       sync.Mutex
	ips      map[string]*attemptRec
	accounts map[string]*attemptRec
	maxFails int           // per-IP failures before lockout
	maxAcct  int           // per-account failures before lockout (distributed spray)
	maxKeys  int           // hard cap on tracked IP keys (memory bound)
	window   time.Duration // lockout / counter lifetime
	swept    time.Time     // last expired-record sweep
}

type attemptRec struct {
	count int
	until time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		ips:      make(map[string]*attemptRec),
		accounts: make(map[string]*attemptRec),
		maxFails: 10,
		maxAcct:  20,
		maxKeys:  4096,
		window:   15 * time.Minute,
	}
}

// newAPIKeyGuard locks out an IP that keeps presenting invalid API keys. The
// apiLimiter in front of /v1 is a flood guard — it caps request *rate* but never
// looks at whether a request authenticated, so on its own it still hands an
// attacker its full budget of guesses every minute, forever. This counts failures
// instead, so a source that can't produce a valid key stops getting attempts.
//
// It reuses loginLimiter with the account dimension switched off: an invalid key
// names no account, so there is nothing to spray *at* the way a username can be
// sprayed. Every call therefore passes account "", and the accounts map stays empty.
func newAPIKeyGuard() *loginLimiter {
	return &loginLimiter{
		ips:      make(map[string]*attemptRec),
		accounts: make(map[string]*attemptRec),
		maxFails: 10,
		maxAcct:  1 << 30, // unused (account is always ""); kept absurd so it can never gate
		maxKeys:  4096,
		window:   15 * time.Minute,
	}
}

// blocked reports whether this IP or this account is currently locked out.
func (l *loginLimiter) blocked(ip, account string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked()
	return recBlocked(l.ips[ip], l.maxFails) || recBlocked(l.accounts[account], l.maxAcct)
}

func recBlocked(r *attemptRec, max int) bool {
	return r != nil && time.Now().Before(r.until) && r.count >= max
}

// fail records a failed attempt against both the IP and the account.
func (l *loginLimiter) fail(ip, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked()
	bumpAttempt(l.ips, ip, l.window)
	if account != "" {
		bumpAttempt(l.accounts, account, l.window)
	}
}

// success clears the counters for a winning IP + account pair.
func (l *loginLimiter) success(ip, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.ips, ip)
	if account != "" {
		delete(l.accounts, account)
	}
}

func bumpAttempt(m map[string]*attemptRec, key string, window time.Duration) {
	r := m[key]
	if r == nil || time.Now().After(r.until) {
		r = &attemptRec{}
		m[key] = r
	}
	r.count++
	r.until = time.Now().Add(window)
}

// sweepLocked drops expired records (at most once a minute) and, when the IP map
// reaches its cap, sheds entries so a flood of unique live keys can't grow it
// without bound. Caller holds l.mu.
//
// Shedding runs down to a low-water mark rather than to the cap itself: trimming to
// exactly maxKeys would leave the map saturated, so the very next failed login
// would trigger another shed — turning a spray into a full sort per request (a CPU
// DoS). Cutting to 3/4 buys ~maxKeys/4 inserts before the next one, amortising it.
func (l *loginLimiter) sweepLocked() {
	now := time.Now()
	if now.Sub(l.swept) < time.Minute && len(l.ips) < l.maxKeys {
		return
	}
	l.swept = now
	for k, r := range l.ips {
		if now.After(r.until) {
			delete(l.ips, k)
		}
	}
	for k, r := range l.accounts {
		if now.After(r.until) {
			delete(l.accounts, k)
		}
	}
	if len(l.ips) < l.maxKeys {
		return
	}
	lowWater := l.maxKeys * 3 / 4

	// At the cap even after dropping expired entries → an active flood of live keys.
	// Shed the entries that are NOT currently locked out first: wiping the map
	// wholesale (as this used to) would also clear the lockouts, so an attacker could
	// spray from thousands of throwaway IPs purely to force a reset and hand their
	// own locked-out IP a fresh attempt budget.
	for k, r := range l.ips {
		if len(l.ips) <= lowWater {
			break
		}
		if !recBlocked(r, l.maxFails) {
			delete(l.ips, k)
		}
	}
	// Pathological case: the locked-out records ALONE still fill the map (an attacker
	// burned maxFails attempts from thousands of throwaway IPs just to bloat it).
	// Memory must stay bounded, so evict the lockouts closest to expiring — they have
	// the least protection left to give. A lockout just issued is the last to go.
	if len(l.ips) > lowWater {
		type entry struct {
			key   string
			until time.Time
		}
		all := make([]entry, 0, len(l.ips))
		for k, r := range l.ips {
			all = append(all, entry{k, r.until})
		}
		slices.SortFunc(all, func(a, b entry) int { return a.until.Compare(b.until) })
		for _, e := range all[:len(all)-lowWater] {
			delete(l.ips, e.key)
		}
	}
}

// ipRateLimiter is a fixed-window per-IP request limiter used for the public
// (unauthenticated) subscription endpoint. It bounds how fast one IP can pull,
// blunting a tight-loop fetch of a leaked subscription token, and sweeps expired
// windows so the map stays bounded.
type ipRateLimiter struct {
	mu      sync.Mutex
	hits    map[string]*windowRec
	limit   int
	window  time.Duration
	maxKeys int
	swept   time.Time
}

type windowRec struct {
	count int
	reset time.Time
}

func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		hits:    make(map[string]*windowRec),
		limit:   limit,
		window:  window,
		maxKeys: 8192,
	}
}

// allow reports whether ip may make another request in the current window.
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.swept) > l.window || len(l.hits) > l.maxKeys {
		l.swept = now
		for k, r := range l.hits {
			if now.After(r.reset) {
				delete(l.hits, k)
			}
		}
		if len(l.hits) > l.maxKeys {
			l.hits = make(map[string]*windowRec)
		}
	}
	r := l.hits[ip]
	if r == nil || now.After(r.reset) {
		l.hits[ip] = &windowRec{count: 1, reset: now.Add(l.window)}
		return true
	}
	if r.count >= l.limit {
		return false
	}
	r.count++
	return true
}

// streamGate caps concurrent Server-Sent Events streams, both globally and per
// client IP, so an authenticated client (or a stolen session) can't open an
// unbounded number of long-lived streams — each of which holds a goroutine and, for
// the dashboard stream, drives a periodic xray fork.
type streamGate struct {
	mu       sync.Mutex
	perIP    map[string]int
	total    int
	maxTotal int
	maxPerIP int
}

func newStreamGate() *streamGate {
	return &streamGate{perIP: make(map[string]int), maxTotal: 128, maxPerIP: 8}
}

// acquire reserves a stream slot for ip, or returns false if a cap is hit.
func (g *streamGate) acquire(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.total >= g.maxTotal || g.perIP[ip] >= g.maxPerIP {
		return false
	}
	g.total++
	g.perIP[ip]++
	return true
}

// release returns a previously acquired slot.
func (g *streamGate) release(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.perIP[ip] > 0 {
		g.perIP[ip]--
		if g.perIP[ip] == 0 {
			delete(g.perIP, ip)
		}
	}
	if g.total > 0 {
		g.total--
	}
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
