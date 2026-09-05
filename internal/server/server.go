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

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/model"
	webui "github.com/AppsGanin/rospanel/web"
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
	panel    http.Handler
	api      http.Handler // external REST API mux (key-authenticated), mounted under apiPath
	assets   http.Handler
	indexRaw []byte // index.html before <base href> injection
	limiter  *loginLimiter
	// stepUp throttles wrong second factors on the irreversible actions. Its OWN
	// counter, not the login one: sharing it meant a mistyped code in the delete dialog
	// locked the admin out of the login form for fifteen minutes while doing nothing at
	// all to the endpoint being guessed at — the collateral without the protection.
	stepUp *loginLimiter
	// statusCache memoizes the rendered public status page per language; see statusBody.
	statusMu    sync.Mutex
	statusCache map[string]statusPageCache

	// The two connection breakdowns, memoized for the same reason — see geoStatsTTL.
	countryStats geoStatsCache[model.CountryStat]
	asnStats     geoStatsCache[model.ASNStat]

	subLimiter *ipRateLimiter // per-IP throttle for the public subscription endpoint
	apiLimiter *ipRateLimiter // per-IP throttle for the external API surface
	apiKeys    *loginLimiter  // per-IP lockout after repeated invalid API keys
	probes     *probeGuard    // flags IPs scanning for the hidden panel path
	streams    *streamGate    // caps concurrent SSE streams
	status     *statusFeed    // one dashboard-payload timer shared by every viewer
	routes     []string       // panel route patterns, in registration order (audit exhaustiveness test)
	apiRoutes  []string       // /v1 route patterns (OpenAPI coverage test)

	mu        sync.RWMutex
	secret    string
	subPath   string // public subscription URL prefix (/<subPath>/<token>)
	paySecret string // random segment for the public payment-webhook path
	apiPath   string // external-API URL prefix (/<apiPath>/v1/...); "" = disabled
	nodePath  string // node sync API URL prefix (/<nodePath>/v1/{join,sync}); "" = no nodes
	// statusPath is the public status page's segment, "" while the page is off. Held
	// as a routing decision rather than read from settings per request: an unrouted
	// path costs nothing and the disabled page is then genuinely absent.
	statusPath  string
	maintenance bool         // public surfaces show the maintenance page
	maintDecoy  http.Handler // the 503 maintenance page (nil ⇒ fall back to decoy)
	probeDetect bool         // record IPs that scan for the hidden panel path
	spaIndex    []byte       // index.html with <base href> injected for the secret
	decoy       http.Handler // current decoy template handler
}

// New builds the masquerade router for the given secret path and decoy template.
func New(mgr *core.Manager, secret, decoyTemplate, dataDir string) (http.Handler, error) {
	d, err := decoy.New(decoyTemplate, decoy.LoadStamp(dataDir))
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
	var paySecret, apiPath, nodePath, statusPath string
	var maintenance, probeDetect bool
	if set, err := mgr.Store().GetSettings(); err == nil {
		if set.SubPath != "" {
			subPath = set.SubPath
		}
		paySecret = set.PaymentWebhookSecret
		apiPath = set.APIPath
		nodePath = set.NodeAPIPath
		statusPath = statusPathOf(set.StatusEnabled, set.StatusPathOr())
		maintenance = set.MaintenanceMode
		probeDetect = set.ProbeDetect
	}

	// The maintenance page is a dedicated 503-answering decoy, built once so toggling
	// maintenance is a flag flip rather than a handler rebuild. If its template can't
	// load, maintenance simply falls back to the normal decoy (never a hard failure on
	// a surface whose whole job is to look ordinary).
	maintDecoy, _ := decoy.New("maintenance", decoy.LoadStamp(dataDir))

	rt := &Router{
		mgr:         mgr,
		dataDir:     dataDir,
		assets:      http.FileServer(http.FS(spa)),
		indexRaw:    indexRaw,
		limiter:     newLoginLimiter(),
		stepUp:      newAPIKeyGuard(),
		subLimiter:  newIPRateLimiter(120, time.Minute),
		apiLimiter:  newIPRateLimiter(600, time.Minute),
		apiKeys:     newAPIKeyGuard(),
		probes:      newProbeGuard(),
		streams:     newStreamGate(),
		status:      newStatusFeed(mgr),
		secret:      secret,
		subPath:     subPath,
		paySecret:   paySecret,
		apiPath:     apiPath,
		nodePath:    nodePath,
		statusPath:  statusPath,
		spaIndex:    injectBase(indexRaw, "/"+secret+"/"),
		decoy:       d,
		maintenance: maintenance,
		maintDecoy:  maintDecoy,
		probeDetect: probeDetect,
	}
	// The router live-swaps the node-API segment in when the first node is created.
	mgr.SetNodeAPIPathCallback(rt.setNodePath)
	// The external API surface. The /v1 mux is wrapped with per-key
	// authentication (no session cookie, not subject to the CSRF/same-origin guard
	// that exists for the browser SPA); the OpenAPI spec + Swagger UI are served
	// key-free so a browser can load the docs.
	rt.api = rt.apiHandler()
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

// setPaySecret swaps the payment-webhook URL segment (generated on first save).
func (rt *Router) setPaySecret(s string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.paySecret = s
}

// setStatusPath swaps the live status-page segment ("" takes the page off the
// router entirely).
func (rt *Router) setStatusPath(s string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.statusPath = s
}

// setAPIPath swaps the live external-API URL segment ("" disables the surface).
func (rt *Router) setAPIPath(s string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.apiPath = s
}

// setNodePath swaps the live node sync API URL segment. Called when the first node
// is created (the segment is generated then).
func (rt *Router) setNodePath(s string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.nodePath = s
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
//
// Except an API path. The shell is the right answer for a client-side route the server
// has never heard of — that is how the SPA's own routing works — but a request under
// /api/ is asking for JSON, and answering it with a page means the caller parses
// "<!doctype" and reports "is not valid JSON": an error that names nothing and points
// nowhere. It happens whenever a browser tab outlives the endpoint it is calling, and
// it cost one operator an afternoon and an apology for a bug that was not theirs
// (issue #70). A 404 with a code says which of the two sides is out of date.
// fallback answers everything no route claimed. See index for why an API path may not
// be given the app shell, and the mux registration for why this takes every method
// rather than only GET.
func (rt *Router) fallback(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErrCode(w, http.StatusNotFound, "err.staleTab",
			"панель ответила страницей вместо данных — вкладка устарела, обновите её")
		return
	}
	// A client route is a page, and a page is fetched. Anything else aimed at one is
	// not something this app does, so it keeps the status net/http used to give it —
	// with a body the caller can read, and its own code: nothing was answered with a
	// page here, so the stale-tab wording would be describing something that did not
	// happen.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErrCode(w, http.StatusMethodNotAllowed, "err.methodNotSupported",
			"панель не принимает такой метод запроса по этому адресу")
		return
	}
	rt.index(w, r)
}

func (rt *Router) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	rt.mu.RLock()
	idx := rt.spaIndex
	rt.mu.RUnlock()
	_, _ = w.Write(idx)
}

// currentDecoy returns the live decoy handler under the read lock. The handler is
// swapped at runtime (the operator can change the masquerade template), and an interface
// value is two words — reading it unlocked while SetDecoy writes it is a real data race
// that can hand a request a torn handler. ServeHTTP snapshots it once for its own use;
// every other handler goes through here.
func (rt *Router) currentDecoy() http.Handler {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.decoy
}

// ServeHTTP routes by the first path segment: the secret unlocks the panel,
// anything else falls through to the decoy.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	seg, rest := firstSegment(r.URL.Path)

	rt.mu.RLock()
	secret, decoy, subPath, paySecret, apiPath, nodePath := rt.secret, rt.decoy, rt.subPath, rt.paySecret, rt.apiPath, rt.nodePath
	statusPath := rt.statusPath
	maintenance := rt.maintenance
	maintDecoy := rt.maintDecoy
	probeDetect := rt.probeDetect
	rt.mu.RUnlock()

	// In maintenance mode the public-facing surfaces show the unavailable page, but the
	// infrastructure ones do not: the panel (so the operator can switch it off), the
	// external API, node sync and payment webhooks all keep working, and so do the
	// tunnels themselves — a maintenance banner is not an outage. maintDecoy is what the
	// subscription/status/fallback branches below serve when this is set.
	publicDecoy := decoy
	if maintenance && maintDecoy != nil {
		publicDecoy = maintDecoy
	}

	// External REST API, mounted under its own stable, unguessable segment (kept
	// separate from the panel secret so rotating the secret never breaks
	// integrations). Every request is authenticated by a bearer API key; a
	// per-IP throttle blunts key-guessing and runaway clients. The segment itself
	// is the obscurity layer — once it matches, the API answers with real REST
	// status codes (401/403) so integrators can debug their credentials.
	if apiPath != "" && seg == apiPath {
		if !rt.apiLimiter.allow(clientIP(r)) {
			// A real REST status, unlike every other surface below: reaching here means
			// the caller already knows the segment, and an integrator throttled by us
			// needs to see why. The body is the API's own JSON shape, so it is at least
			// not net/http's bare text/plain default.
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		r.URL.Path = rest
		rt.api.ServeHTTP(w, r)
		return
	}

	// Node sync surface, mounted under its own random unguessable segment (kept
	// separate from the panel secret and the external API so rotating either never
	// orphans a joined node). Authentication is per-node bearer token inside the
	// handler; an unmatched sub-path or a bad/absent token falls through to the
	// decoy, so the surface is invisible to anyone without the segment + a token.
	if nodePath != "" && seg == nodePath {
		if !rt.apiLimiter.allow(clientIP(r)) {
			// Decoy, not a 429: this surface's whole contract is that a caller without
			// a valid token cannot tell it from unknown hosting, and a throttle reply
			// that no static site emits would break that for anyone who floods it. A
			// real node polls once per hold — it never approaches the limit.
			decoy.ServeHTTP(w, r)
			return
		}
		rt.handleNodeAPI(w, r, rest)
		return
	}

	// Public payment-webhook surface, mounted at /<paySecret>/<provider key> under a
	// random unguessable segment so providers can POST to a fixed URL without
	// revealing the hidden panel. The webhook itself verifies the payload (signature
	// / re-fetch); an unknown provider leaf falls through to the decoy.
	if paySecret != "" && subtle.ConstantTimeCompare([]byte(seg), []byte(paySecret)) == 1 {
		// Throttle per-IP: signature-less providers (e.g. YooKassa) re-fetch from the
		// provider on callback, so an amplification/flood is possible if the secret leaks.
		// Over the limit the answer is the decoy — see the subscription surface below
		// for why a plain 429 here is a giveaway. Providers retry failed callbacks.
		if !rt.subLimiter.allow(clientIP(r)) {
			decoy.ServeHTTP(w, r)
			return
		}
		leaf, _ := firstSegment(rest)
		handlePaymentWebhook(rt, w, r, leaf)
		return
	}

	// Public subscription surface (invalid tokens fall through to the decoy). The
	// path is just an obscurity prefix — the token is the real secret — so a plain
	// compare is fine.
	if subPath != "" && seg == subPath {
		if maintenance && maintDecoy != nil {
			publicDecoy.ServeHTTP(w, r)
			return
		}
		// Light per-IP throttle: the token is the real secret (256-bit, unguessable),
		// so this isn't about enumeration — it just stops a leaked token from being
		// pulled in a tight loop (and the per-request routing-template fetch with it).
		//
		// Over the limit the decoy answers, never a 429. This prefix defaults to the
		// literal "sub", so unlike every other segment above it is known in advance:
		// anyone can fire 120 requests at /sub/anything and, if a throttle reply comes
		// back, has confirmed a panel — no token, no domain, no guessing. Only the
		// bytes are throttled here; the decoy is cheap and the surface stays silent.
		if !rt.subLimiter.allow(clientIP(r)) {
			decoy.ServeHTTP(w, r)
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

	// Public status page. The only surface that answers a caller holding nothing at
	// all, which is why it exists only when an operator switched it on — and why it
	// shows liveness and nothing else (see internal/status).
	//
	// Checked LAST of the public surfaces on purpose. Its path is operator-typed, and
	// every other segment here is either a secret or something integrations depend on;
	// a status path that happened to equal one of them must lose, not shadow it. The
	// save handler already refuses the collisions it can see, but the other paths can
	// change afterwards and this ordering is what makes that harmless.
	if statusPath != "" && seg == statusPath {
		if maintenance && maintDecoy != nil {
			publicDecoy.ServeHTTP(w, r)
			return
		}
		if !rt.subLimiter.allow(clientIP(r)) {
			decoy.ServeHTTP(w, r)
			return
		}
		handleStatus(rt, w, r, rest)
		return
	}

	// Nothing above matched: this is the decoy's own territory. If the request is for
	// a path the decoy doesn't have, it may be a scan for the hidden panel — record
	// the IP when it has guessed enough distinct dead paths to be one. This never
	// changes the reply (the scanner still gets the same decoy), so the masquerade is
	// untouched; it only makes the scanning visible to the operator.
	if probeDetect {
		if m, ok := decoy.(misser); ok && m.Miss(r.URL.Path) {
			ip := clientIP(r)
			if crossed, n := rt.probes.observe(ip, r.URL.Path); crossed {
				go rt.mgr.RecordProbe(ip, n)
			}
		}
	}

	publicDecoy.ServeHTTP(w, r)
}

// misser is the read-only decoy capability the probe detector needs: whether a path
// resolves to no asset (a miss). Kept as a local interface so ServeHTTP can use it
// even though the `decoy` local variable shadows the decoy package name.
type misser interface {
	Miss(urlPath string) bool
}

// setProbeDetect flips secret-path probe detection on or off, live.
func (rt *Router) setProbeDetect(on bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.probeDetect = on
}

// setMaintenance flips the public-surface maintenance page on or off, live.
func (rt *Router) setMaintenance(on bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.maintenance = on
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
			writeErrCode(w, http.StatusForbidden, "err.csrfRejected", "запрос отклонён (CSRF)")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			writeErrCode(w, http.StatusForbidden, "err.originRejected", "запрос отклонён (origin)")
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
