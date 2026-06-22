package server

import (
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/version"
)

// validDNSServer reports whether s is an acceptable Xray DNS server: "localhost",
// a scheme URL (https://, tcp://, quic://…), a bare IP, or IP:port.
func validDNSServer(s string) bool {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return false
	case s == "localhost":
		return true
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		return err == nil && u.Host != ""
	case net.ParseIP(s) != nil:
		return true
	default:
		host, _, err := net.SplitHostPort(s)
		return err == nil && net.ParseIP(host) != nil
	}
}

// userView is a user plus its derived share links (one credential set, three
// protocols).
type userView struct {
	model.User
	SystemEmail string `json:"system_email"` // Xray client id "u<id>" (logs/stats/links)
	SubURL      string `json:"sub_url"`
	VLESS       string `json:"vless"`
	Trojan      string `json:"trojan"`
	Hysteria2   string `json:"hysteria2"`
	Reality     string `json:"reality"`
}

func makeUserView(u model.User, set *model.Settings) userView {
	v := userView{
		User:        u,
		SystemEmail: model.UserEmail(u.ID),
		SubURL:      sub.URL(set, u.SubToken),
	}
	// A protocol switched off in the Connections panel drops out of the user's
	// links (empty string ⇒ the UI hides it).
	if set.VLESSEnabled {
		v.VLESS = link.VLESS(u, set)
	}
	if set.TrojanEnabled {
		v.Trojan = link.Trojan(u, set)
	}
	if set.HysteriaEnabled {
		v.Hysteria2 = link.Hysteria2(u, set)
	}
	if set.RealityEnabled {
		v.Reality = link.Reality(u, set)
	}
	return v
}

// applyTLSHints fills the per-request TLS fields used by link/sub generation. When
// the active cert isn't CA-trusted (a self-signed fallback), it flags TLSInsecure
// and attaches the cert pin so Xray links can pin it (pinnedPeerCertSha256); a
// trusted CA cert leaves verification on.
func (rt *Router) applyTLSHints(set *model.Settings) {
	if rt.mgr.HasValidCert() {
		return
	}
	set.TLSInsecure = true
	set.TLSPinSHA256 = rt.mgr.CertPinSHA256()
}

func (rt *Router) panelMux() http.Handler {
	mux := http.NewServeMux()
	// authed registers a route behind the session check — so a new sensitive route
	// can't silently be added without auth.
	authed := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, rt.requireAuth(h))
	}
	// authedID is authed for routes carrying an {id} segment: it parses (and
	// validates) the id once, so the handler receives it directly instead of
	// repeating the pathID/ok dance.
	authedID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authed(pattern, func(w http.ResponseWriter, r *http.Request) {
			if id, ok := pathID(w, r); ok {
				h(w, r, id)
			}
		})
	}
	mux.HandleFunc("POST /api/login", rt.login)
	mux.HandleFunc("POST /api/logout", rt.logout)
	authed("GET /api/me", rt.me)
	authed("GET /api/update", rt.checkUpdate)
	authed("POST /api/update", rt.applyUpdate)
	authed("POST /api/setup/password", rt.setupPassword)
	authed("POST /api/setup/timezone", rt.setupTimezone)
	authed("POST /api/setup/finish", rt.setupFinish)
	authed("POST /api/account/credentials", rt.updateCredentials)
	authed("GET /api/settings", rt.getSettings)
	authed("POST /api/settings/secret", rt.regenSecret)
	authed("POST /api/settings/decoy", rt.setDecoyTemplate)
	authed("POST /api/settings/subscription", rt.saveSubSettings)
	authed("POST /api/settings/dns", rt.setXrayDNS)
	authed("POST /api/settings/proxy-mode", rt.setProxyMode)
	authed("GET /api/geo/categories", rt.geoCategories)
	authed("GET /api/geo", rt.geoStatus)
	authed("POST /api/geo/update", rt.updateGeo)
	authed("GET /api/routing", rt.getRouting)
	authed("POST /api/routing", rt.saveRouting)
	authed("GET /api/system/stream", rt.systemStream)
	authed("GET /api/xray/config", rt.xrayConfig)
	authed("GET /api/xray/status", rt.xrayStatus)
	authed("GET /api/xray/logs/stream", rt.xrayLogs)
	authed("GET /api/logs/stream", rt.appLogs)
	authed("GET /api/backup", rt.downloadBackup)
	authed("GET /api/backup/info", rt.backupInfo)
	authed("POST /api/backup/inspect", rt.inspectBackup)
	authed("POST /api/restore", rt.uploadRestore)
	authed("POST /api/reset", rt.factoryReset)
	authed("GET /api/connections", rt.connections)
	authed("POST /api/connections", rt.applyConnections)
	authed("GET /api/users", rt.listUsers)
	authed("POST /api/users", rt.createUser)
	authedID("DELETE /api/users/{id}", rt.deleteUser)
	authedID("POST /api/users/{id}/reset", rt.resetUserTraffic)
	authedID("POST /api/users/{id}/limits", rt.setUserLimits)
	authedID("POST /api/users/{id}/enabled", rt.setUserEnabled)
	authedID("POST /api/users/{id}/name", rt.renameUser)
	authedID("GET /api/users/{id}/connections", rt.userConnections)
	authedID("POST /api/users/{id}/reset-period", rt.setResetPeriod)
	authed("GET /api/stats/series", rt.statsSeries)
	authed("GET /api/stats/users", rt.statsByUser)
	authed("POST /api/stats/reset", rt.statsReset)
	authed("GET /api/tls", rt.tlsStatus)
	authed("POST /api/tls", rt.setACME)
	authed("GET /api/telegram", rt.getTelegram)
	authed("POST /api/telegram", rt.saveTelegram)
	authed("POST /api/telegram/link", rt.genTelegramLink)
	authed("GET /api/telegram/link/status", rt.telegramLinkStatus)
	authed("POST /api/telegram/link/cancel", rt.cancelTelegramLink)
	authed("POST /api/telegram/unlink", rt.unlinkTelegram)
	authed("POST /api/telegram/test-backup", rt.testTelegramBackup)
	// Content-hashed build assets (JS/CSS/fonts) never change for a given URL → cache forever.
	mux.Handle("GET /assets/", cacheControl(rt.assets, "public, max-age=31536000, immutable"))
	favicon := cacheControl(rt.assets, "public, max-age=604800") // stable name → 1 week
	mux.Handle("GET /favicon.svg", favicon)
	mux.Handle("GET /favicon.ico", favicon)
	mux.Handle("GET /favicon-96x96.png", favicon)
	mux.HandleFunc("GET /", rt.index) // SPA shell (client-side rendered)
	return mux
}

// cookiePath scopes the session cookie to the secret path so it never leaks on
// decoy requests.
func (rt *Router) cookiePath() string { return "/" + rt.currentSecret() + "/" }

func (rt *Router) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	ip := clientIP(r)
	username := strings.TrimSpace(req.Username)
	if rt.limiter.blocked(ip, username) {
		log.Printf("[WARN] login: rate-limited %s", ip)
		writeErr(w, http.StatusTooManyRequests, "слишком много попыток, повторите позже")
		return
	}

	id, hash, err := rt.mgr.Store().GetAdminAuth(username)
	if err != nil {
		// Unknown user: equalize timing against the real verify path.
		auth.DummyVerify()
		rt.limiter.fail(ip, username)
		log.Printf("[WARN] login: failed (unknown user) from %s", ip)
		writeErr(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}
	if !auth.VerifyPassword(hash, req.Password) {
		rt.limiter.fail(ip, username)
		log.Printf("[WARN] login: failed (bad password) for %q from %s", username, ip)
		writeErr(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}
	rt.limiter.success(ip, username)

	token, err := rt.mgr.Store().CreateSession(id, sessionTTLSec*time.Second)
	if err != nil {
		log.Printf("[ERROR] login: session creation failed: %v", err)
		writeErr(w, http.StatusInternalServerError, "не удалось создать сессию")
		return
	}
	log.Printf("[INFO] login: admin %q authenticated from %s", req.Username, ip)
	rt.setSessionCookie(w, r, token, rt.cookiePath())
	writeOK(w)
}

// setSessionCookie writes the session cookie scoped to the given panel path.
// Secure is set unconditionally: the panel is only ever reached over Xray's
// TLS-terminated :443, even though r.TLS is nil here (the request arrives over the
// plaintext loopback fallback after Xray terminated TLS). Keying Secure off r.TLS
// would wrongly drop the flag and let the session ride an accidental plaintext path.
func (rt *Router) setSessionCookie(w http.ResponseWriter, r *http.Request, token, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sessionTTLSec,
	})
}

func (rt *Router) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = rt.mgr.Store().DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: rt.cookiePath(),
		HttpOnly: true, MaxAge: -1,
	})
	writeOK(w)
}

func (rt *Router) me(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	_, username, _ := rt.mgr.Store().LookupSession(c.Value)
	resp := map[string]any{
		"username":             username,
		"setup_done":           true,
		"timezone":             "",
		"version":              version.Version,
		"must_change_password": rt.mgr.MustChangePassword(),
	}
	if set, err := rt.mgr.Store().GetSettings(); err == nil {
		resp["setup_done"] = set.SetupDone
		resp["timezone"] = set.Timezone
	}
	writeJSON(w, http.StatusOK, resp)
}

// adminID returns the authenticated admin's id from the session cookie.
func (rt *Router) adminID(r *http.Request) (int64, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0, false
	}
	id, _, ok := rt.mgr.Store().LookupSession(c.Value)
	return id, ok
}

// mustChangeAllowed lists the only panel paths reachable while the admin still has
// the default password (must_change_password). They let the operator get OUT of
// that state — change the password (wizard / account settings) or restore a backup
// (which replaces the credentials wholesale) — and nothing else, so a panel whose
// secret path leaks before first setup can't be driven with admin/admin (no user
// management, settings, backup download, or factory reset). Paths are matched after
// the secret prefix is stripped, e.g. "/api/setup/password".
var mustChangeAllowed = map[string]bool{
	"/api/me":                  true,
	"/api/logout":              true,
	"/api/setup/password":      true,
	"/api/account/credentials": true,
	"/api/backup/info":         true,
	"/api/backup/inspect":      true,
	"/api/restore":             true,
}

// requireAuth rejects requests without a valid session. Because this only runs
// under the secret path, a 401 here never reveals the panel to outsiders. While the
// default password is still in place it also blocks everything but the
// password-change / restore endpoints (see mustChangeAllowed).
func (rt *Router) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "не авторизован")
			return
		}
		if _, _, ok := rt.mgr.Store().LookupSession(c.Value); !ok {
			writeErr(w, http.StatusUnauthorized, "не авторизован")
			return
		}
		if !mustChangeAllowed[r.URL.Path] && rt.mgr.MustChangePassword() {
			writeErr(w, http.StatusForbidden, "смените пароль администратора по умолчанию, прежде чем пользоваться панелью")
			return
		}
		next(w, r)
	}
}
