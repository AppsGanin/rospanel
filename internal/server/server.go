// Package server implements the public-facing masquerade router and the hidden
// admin panel mounted under the secret path.
//
// Request routing (first match wins):
//   - /<secret>/...  → admin panel (login + authed API + SPA)
//   - everything else → decoy site (identical 404 for unknown paths)
//
// The only observable difference in the whole surface is gated behind the
// ~128-bit secret segment, compared in constant time.
package server

import (
	"bytes"
	"crypto/subtle"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/msTimofeev/rospanel/internal/core"
	"github.com/msTimofeev/rospanel/internal/decoy"
	webui "github.com/msTimofeev/rospanel/web"
)

const (
	sessionCookie = "rcsid"
	sessionTTLSec = 7 * 24 * 60 * 60 // 7 days
)

// Router is the top-level HTTP handler. The secret path, SPA shell and decoy can
// be swapped at runtime (from the settings page) without restarting.
type Router struct {
	mgr      *core.Manager
	dataDir  string
	panel      http.Handler
	assets     http.Handler
	indexRaw   []byte // index.html before <base href> injection
	limiter    *loginLimiter
	subLimiter *ipRateLimiter // per-IP throttle for the public subscription endpoint
	streams    *streamGate    // caps concurrent SSE streams

	mu       sync.RWMutex
	secret   string
	subPath  string       // public subscription URL prefix (/<subPath>/<token>)
	spaIndex []byte       // index.html with <base href> injected for the secret
	decoy    http.Handler // current decoy template handler
}

// New builds the masquerade router for the given secret path and decoy template.
func New(mgr *core.Manager, secret, decoyTemplate, dataDir string) (http.Handler, error) {
	d, err := decoy.New(decoyTemplate)
	if err != nil {
		return nil, err
	}
	spa, err := webui.FS()
	if err != nil {
		return nil, err
	}
	indexRaw, err := fs.ReadFile(spa, "index.html")
	if err != nil {
		return nil, err
	}

	subPath := "sub"
	if set, err := mgr.Store().GetSettings(); err == nil && set.SubPath != "" {
		subPath = set.SubPath
	}

	rt := &Router{
		mgr:        mgr,
		dataDir:    dataDir,
		assets:     http.FileServer(http.FS(spa)),
		indexRaw:   indexRaw,
		limiter:    newLoginLimiter(),
		subLimiter: newIPRateLimiter(120, time.Minute),
		streams:    newStreamGate(),
		secret:     secret,
		subPath:    subPath,
		spaIndex:   injectBase(indexRaw, "/"+secret+"/"),
		decoy:      d,
	}
	// The panel mux is wrapped with the CSRF guard (state-changing requests must
	// carry the SPA's custom header + same-origin) and security headers (CSP,
	// nosniff, frame/clickjacking, referrer). The decoy and subscription surfaces
	// are deliberately left bare so they still look like an ordinary site.
	rt.panel = securityHeaders(csrfGuard(rt.panelMux()))
	return rt, nil
}

// setSubPath swaps the live public subscription path prefix.
func (rt *Router) setSubPath(p string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.subPath = p
}

// setSecret swaps the panel's secret path (and the SPA's injected <base href>).
func (rt *Router) setSecret(secret string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.secret = secret
	rt.spaIndex = injectBase(rt.indexRaw, "/"+secret+"/")
}

// setDecoy swaps the live decoy template handler.
func (rt *Router) setDecoy(h http.Handler) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.decoy = h
}

func (rt *Router) currentSecret() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.secret
}

// injectBase inserts a <base href> so the SPA's relative asset and API URLs
// resolve under the per-install secret path regardless of the current route.
func injectBase(html []byte, base string) []byte {
	return bytes.Replace(html, []byte("<head>"), []byte("<head><base href=\""+base+"\">"), 1)
}

// index serves the SPA shell (with injected base) for any non-asset panel path.
func (rt *Router) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	rt.mu.RLock()
	idx := rt.spaIndex
	rt.mu.RUnlock()
	_, _ = w.Write(idx)
}

// ServeHTTP routes by the first path segment: the secret unlocks the panel,
// anything else falls through to the decoy.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	seg, rest := firstSegment(r.URL.Path)

	rt.mu.RLock()
	secret, decoy, subPath := rt.secret, rt.decoy, rt.subPath
	rt.mu.RUnlock()

	// Public subscription surface (invalid tokens fall through to the decoy). The
	// path is just an obscurity prefix — the token is the real secret — so a plain
	// compare is fine.
	if subPath != "" && seg == subPath {
		// Light per-IP throttle: the token is the real secret (256-bit, unguessable),
		// so this isn't about enumeration — it just stops a leaked token from being
		// pulled in a tight loop (and the per-request routing-template fetch with it).
		if !rt.subLimiter.allow(clientIP(r)) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		handleSub(rt, w, r, rest)
		return
	}

	// Hidden panel, gated by the constant-time secret compare.
	if secret != "" && subtle.ConstantTimeCompare([]byte(seg), []byte(secret)) == 1 {
		r.URL.Path = rest
		rt.panel.ServeHTTP(w, r)
		return
	}

	decoy.ServeHTTP(w, r)
}

// cacheControl wraps h with a Cache-Control header. Content-hashed SPA assets get
// an immutable, year-long TTL (a new build changes the filename); stable files
// (favicons, logo) get a shorter one.
func cacheControl(h http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		h.ServeHTTP(w, r)
	})
}

// panelCSP restricts the admin SPA to its own bundled, same-origin assets. The
// built SPA has no inline <script> and loads nothing cross-origin, so script-src
// 'self' holds; 'unsafe-inline' is needed only for style attributes (React/recharts
// inline styles). base-uri 'self' keeps the injected <base href> working while
// blocking a base hijack; frame-ancestors 'none' blocks clickjacking.
const panelCSP = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; " +
	"object-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'"

// securityHeaders adds the standard hardening headers to every panel response. It
// wraps only the panel mux — the decoy and subscription surfaces are left bare so
// they keep looking like an ordinary site (a strict CSP would also break decoy
// templates that use inline scripts/styles/external assets).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", panelCSP)
		next.ServeHTTP(w, r)
	})
}

// csrfGuard blocks cross-site state-changing requests to the panel. Every mutating
// panel call goes through the SPA's fetch wrapper, which sets X-RosPanel-CSRF; a
// cross-origin page cannot set a custom header without a CORS preflight the panel
// never grants, so requiring it stops form/img/script-driven CSRF. The Origin check
// is defense-in-depth. Safe methods (GET/HEAD/OPTIONS) pass through untouched so
// EventSource streams and asset loads keep working without the header.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-RosPanel-CSRF") == "" {
			writeErr(w, http.StatusForbidden, "запрос отклонён (CSRF)")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			writeErr(w, http.StatusForbidden, "запрос отклонён (origin)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the Origin header's host matches the request Host,
// comparing host without port (the panel is reached on :443, which browsers omit
// from Origin but may appear in Host).
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return hostOnly(u.Host) == hostOnly(host)
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// firstSegment splits "/abc/def" into ("abc", "/def"). "/abc" → ("abc", "/").
func firstSegment(p string) (seg, rest string) {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i:]
	}
	return p, "/"
}
