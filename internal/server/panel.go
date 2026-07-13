package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/telegram"
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
	SystemEmail      string `json:"system_email"` // Xray client id "u<id>" (logs/stats/links)
	SubURL           string `json:"sub_url"`
	VLESS            string `json:"vless"`
	Trojan           string `json:"trojan"`
	Hysteria2        string `json:"hysteria2"`
	Reality          string `json:"reality"`
	TelegramLinked   bool   `json:"telegram_linked"`
	TelegramLink     string `json:"telegram_link"`      // public user bot URL
	TelegramDeepLink string `json:"telegram_deep_link"` // bind this (panel-created) account
}

// makeUserView builds the API view for a user. userBotUsername is the resolved
// @username of the public user bot ("" when disabled/unresolved) — passed in so
// the caller resolves it once per request instead of per user.
func makeUserView(u model.User, set *model.Settings, userBotUsername string) userView {
	v := userView{
		User:           u,
		SystemEmail:    model.UserEmail(u.ID),
		SubURL:         sub.URL(set, u.SubToken),
		TelegramLinked: u.TgChatID != 0,
	}
	// The bind deep link is no longer embedded here (it carried the permanent
	// sub-token). It's now minted on demand as a one-time code via
	// POST /api/users/{id}/telegram/link.
	if set.TGUserBotEnabled && userBotUsername != "" && u.TgChatID == 0 {
		v.TelegramLink = telegram.UserBotLink(userBotUsername)
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
	// Route tiers. Every helper below puts the route behind the session check — so a
	// new sensitive route can't silently be added without auth — and additionally
	// pins the minimum role that may call it.
	//
	// authed (admin and up) is the default on purpose: a route added later without a
	// second thought lands closed to operators rather than open to them. Opening one
	// up to operators is then a deliberate act — authedOp — visible in this list.
	//
	// Every one of them also routes the handler through rt.audited, which writes the
	// admin trail (see audit.go). It sits INSIDE the auth check, so the row already
	// knows who is acting; and it is applied here, once, rather than in each handler
	// — that is what makes "no mutating route ships unaudited" a property of the
	// router instead of a habit.
	register := func(tier, pattern string, h http.HandlerFunc) {
		rt.routes = append(rt.routes, pattern) // for the exhaustiveness test
		mux.HandleFunc(pattern, rt.requireRole(tier, rt.audited(pattern, h)))
	}
	authedAny := func(pattern string, h http.HandlerFunc) { // any signed-in admin
		rt.routes = append(rt.routes, pattern)
		mux.HandleFunc(pattern, rt.requireAuth(rt.audited(pattern, h)))
	}
	authedOp := func(pattern string, h http.HandlerFunc) { // operator and up
		register(model.RoleOperator, pattern, h)
	}
	authed := func(pattern string, h http.HandlerFunc) { // admin and up
		register(model.RoleAdmin, pattern, h)
	}
	authedOwner := func(pattern string, h http.HandlerFunc) { // owner only
		register(model.RoleOwner, pattern, h)
	}
	// withID adapts a handler for routes carrying an {id} segment: it parses (and
	// validates) the id once, so the handler receives it directly instead of
	// repeating the pathID/ok dance.
	withID := func(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if id, ok := pathID(w, r); ok {
				h(w, r, id)
			}
		}
	}
	authedID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authed(pattern, withID(h))
	}
	authedOpID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authedOp(pattern, withID(h))
	}
	authedOwnerID := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		authedOwner(pattern, withID(h))
	}
	mux.HandleFunc("POST /api/login", rt.login)
	mux.HandleFunc("POST /api/logout", rt.logout)
	// Branding reads are unauthenticated: the login screen (under the secret path)
	// renders the panel name/accent/logo before any session exists.
	mux.HandleFunc("GET /api/branding", rt.getBranding)
	mux.HandleFunc("GET /api/branding/logo", rt.brandingLogo)
	authed("POST /api/settings/branding", rt.saveBranding)
	authed("POST /api/settings/branding/logo", rt.uploadBrandingLogo)
	authed("DELETE /api/settings/branding/logo", rt.deleteBrandingLogo)
	// Your own account: every role reaches these, whatever their tier — including
	// while gated on a forced password change (see mustChangeAllowed), which is the
	// only way out of that state.
	authedAny("GET /api/me", rt.me)
	authedAny("POST /api/setup/password", rt.setupPassword)
	authedAny("POST /api/account/credentials", rt.updateCredentials)
	// The admin roster and its trail — owner only. Who signed in from where, who
	// created or removed whom, who changed what setting: same tier as the roster
	// itself.
	authedOwner("GET /api/admin-audit", rt.adminAudit)
	authedOwner("GET /api/admin-audit/catalog", rt.adminAuditCatalog)
	authedOwner("GET /api/admins", rt.listAdmins)
	authedOwner("POST /api/admins", rt.createAdmin)
	authedOwnerID("POST /api/admins/{id}/role", rt.setAdminRole)
	authedOwnerID("POST /api/admins/{id}/password", rt.resetAdminPassword)
	authedOwnerID("DELETE /api/admins/{id}", rt.deleteAdmin)
	authed("GET /api/update", rt.checkUpdate)
	authed("POST /api/update", rt.applyUpdate)
	authed("POST /api/setup/timezone", rt.setupTimezone)
	authed("POST /api/setup/finish", rt.setupFinish)
	authed("GET /api/settings", rt.getSettings)
	authed("POST /api/settings/secret", rt.regenSecret)
	authed("POST /api/settings/decoy", rt.setDecoyTemplate)
	authed("POST /api/settings/subscription", rt.saveSubSettings)
	authed("POST /api/settings/dns", rt.setXrayDNS)
	authed("POST /api/settings/proxy-mode", rt.setProxyMode)
	authed("POST /api/settings/local-backup", rt.setLocalBackup)
	authed("POST /api/settings/autodelete", rt.setUserAutoDelete)
	authed("GET /api/geo/categories", rt.geoCategories)
	authed("GET /api/geo", rt.geoStatus)
	authed("POST /api/geo/update", rt.updateGeo)
	authed("GET /api/routing", rt.getRouting)
	authed("POST /api/routing", rt.saveRouting)
	authedOp("GET /api/system/stream", rt.systemStream)
	authedOp("GET /api/health", rt.health)
	authedOp("POST /api/health/selftest", rt.selfTest)
	authed("GET /api/xray/config", rt.xrayConfig)
	authed("GET /api/xray/status", rt.xrayStatus)
	authed("POST /api/xray/restart", rt.xrayRestart)
	authed("GET /api/xray/logs/stream", rt.xrayLogs)
	authed("GET /api/logs/stream", rt.appLogs)
	authed("GET /api/backup", rt.downloadBackup)
	authed("GET /api/backup/info", rt.backupInfo)
	authed("POST /api/backup/inspect", rt.inspectBackup)
	authed("POST /api/restore", rt.uploadRestore)
	authed("POST /api/reset", rt.factoryReset)
	authed("GET /api/connections", rt.connections)
	authed("POST /api/connections", rt.applyConnections)
	// End users, the journal and stats are the operator's job — everything below is
	// open from RoleOperator up.
	authedOp("GET /api/users", rt.listUsers)
	authedOp("POST /api/users", rt.createUser)
	authedOp("POST /api/users/bulk", rt.bulkUsers)
	authedOpID("DELETE /api/users/{id}", rt.deleteUser)
	authedOpID("POST /api/users/{id}/reset", rt.resetUserTraffic)
	authedOpID("POST /api/users/{id}/limits", rt.setUserLimits)
	authedOpID("POST /api/users/{id}/enabled", rt.setUserEnabled)
	authedOpID("POST /api/users/{id}/name", rt.renameUser)
	authedOpID("GET /api/users/{id}/connections", rt.userConnections)
	authedOpID("POST /api/users/{id}/rotate-sub", rt.rotateSubToken)
	authedOpID("POST /api/users/{id}/telegram/unlink", rt.unlinkUserTelegram)
	authedOpID("POST /api/users/{id}/telegram/link", rt.genUserTelegramLink)
	authedOpID("POST /api/users/{id}/reset-period", rt.setResetPeriod)
	authedOpID("POST /api/users/{id}/plan", rt.setUserPlan)
	authedOpID("GET /api/users/{id}/events", rt.userEvents)
	authedOp("GET /api/events", rt.events)
	authedOp("GET /api/events/catalog", rt.eventCatalog)
	// Read-only: the user card lists the plans it can assign. The billing *settings*
	// (POST below) and the payment provider keys stay admin-only.
	authedOp("GET /api/billing", rt.getBilling)
	authed("POST /api/billing", rt.saveBilling)
	authed("POST /api/billing/plans", rt.saveTariffPlan)
	authedID("DELETE /api/billing/plans/{id}", rt.deleteTariffPlan)
	authedID("POST /api/billing/plans/{id}/migrate", rt.migratePlanUsers)
	authed("GET /api/billing/orders", rt.listPaymentOrders)
	authedID("POST /api/billing/orders/{id}/confirm", rt.confirmPaymentOrder)
	authedID("POST /api/billing/orders/{id}/cancel", rt.cancelPaymentOrder)
	authed("GET /api/payments", rt.getPayments)
	authed("POST /api/payments", rt.savePayments)
	authed("GET /api/payments/stats", rt.paymentStats)
	authedOp("GET /api/stats/series", rt.statsSeries)
	authedOp("GET /api/stats/users", rt.statsByUser)
	authed("POST /api/stats/reset", rt.statsReset)
	authed("GET /api/tls", rt.tlsStatus)
	authed("POST /api/tls", rt.setACME)
	authed("GET /api/apikeys", rt.listAPIKeys)
	authed("POST /api/apikeys", rt.createAPIKey)
	authedID("DELETE /api/apikeys/{id}", rt.revokeAPIKey)
	authed("POST /api/settings/api-path", rt.setAPIPathSettings)
	authed("GET /api/webhooks", rt.listWebhooks)
	authed("POST /api/webhooks", rt.createWebhook)
	authedID("POST /api/webhooks/{id}", rt.updateWebhook)
	authedID("DELETE /api/webhooks/{id}", rt.deleteWebhook)
	authedID("POST /api/webhooks/{id}/test", rt.testWebhook)
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
		slog.Warn("login: rate-limited", "ip", ip)
		writeErr(w, http.StatusTooManyRequests, "слишком много попыток, повторите позже")
		return
	}

	// Sign-ins are audited here rather than by the audit middleware: a failed one —
	// the row actually worth having — never reaches a success path, and neither
	// attempt has a session for the middleware to read an actor from. The attempted
	// login is recorded as the target; it is not a secret, and "someone tried to sign
	// in as owner from 1.2.3.4, twelve times" is the whole point of the row.
	auditLogin := func(action string) {
		rt.mgr.AddAdminAudit(model.AdminAudit{
			Action: action, Target: username,
			ActorKind: model.ActorAdmin, ActorName: username, IP: ip,
		})
	}

	id, hash, role, err := rt.mgr.Store().GetAdminAuth(username)
	if err != nil {
		// Unknown user: equalize timing against the real verify path.
		auth.DummyVerify()
		rt.limiter.fail(ip, username)
		slog.Warn("login: unknown user", "ip", ip)
		auditLogin(model.AuditLoginFailed)
		writeErr(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}
	if !auth.VerifyPassword(hash, req.Password) {
		rt.limiter.fail(ip, username)
		slog.Warn("login: bad password", "user", username, "ip", ip)
		auditLogin(model.AuditLoginFailed)
		writeErr(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}
	rt.limiter.success(ip, username)
	auditLogin(model.AuditLogin)

	token, err := rt.mgr.Store().CreateSession(id, sessionTTLSec*time.Second)
	if err != nil {
		slog.Error("login: session creation failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "не удалось создать сессию")
		return
	}
	// Best-effort: the roster shows it, nothing depends on it, and a failed write
	// must not cost an otherwise valid login.
	if err := rt.mgr.Store().TouchAdminLogin(id); err != nil {
		slog.Warn("login: could not record last-login", "user", username, "err", err)
	}
	slog.Info("login: authenticated", "user", req.Username, "role", role, "ip", ip)
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
		// Resolve who is leaving before the session is destroyed — afterwards there is
		// nothing left to attribute the row to.
		if a, ok := rt.mgr.Store().LookupSession(c.Value); ok {
			rt.mgr.AddAdminAudit(model.AdminAudit{
				Action:    model.AuditLogout,
				ActorKind: model.ActorAdmin,
				ActorName: a.Username,
				IP:        clientIP(r),
			})
		}
		_ = rt.mgr.Store().DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: rt.cookiePath(),
		HttpOnly: true, MaxAge: -1,
	})
	writeOK(w)
}

func (rt *Router) me(w http.ResponseWriter, r *http.Request) {
	// requireAuth already resolved the session; reading it back off the context keeps
	// this from being the one place that could disagree with what the gate saw.
	a, _ := sessionAdminFrom(r.Context())
	resp := map[string]any{
		"username":             a.Username,
		"role":                 a.Role,
		"setup_done":           true,
		"timezone":             "",
		"version":              version.Version,
		"must_change_password": a.MustChangePassword,
	}
	if set, err := rt.mgr.Store().GetSettings(); err == nil {
		resp["setup_done"] = set.SetupDone
		resp["timezone"] = set.Timezone
		resp["billing_enabled"] = set.BillingEnabled
	}
	writeJSON(w, http.StatusOK, resp)
}

// ctxKeyAdmin carries the resolved session admin down the request. requireAuth is
// the only writer, so a handler that reads it is looking at the same account the
// auth gate and the role check just approved.
type ctxKeyAdmin struct{}

func sessionAdminFrom(ctx context.Context) (store.SessionAdmin, bool) {
	a, ok := ctx.Value(ctxKeyAdmin{}).(store.SessionAdmin)
	return a, ok
}

// adminID returns the authenticated admin's id.
func (rt *Router) adminID(r *http.Request) (int64, bool) {
	a, ok := sessionAdminFrom(r.Context())
	return a.ID, ok
}

// verifyStepUp re-checks the admin password before a sensitive operation. It is
// skipped while the first-run wizard is still in progress (!SetupDone) — the
// session was only just issued and the operator is completing guided setup.
// On failure it writes the error response and returns false.
func (rt *Router) verifyStepUp(w http.ResponseWriter, r *http.Request, password string) bool {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		return false
	}
	if !set.SetupDone {
		return true
	}
	return rt.verifyAdminPassword(w, r, password)
}

// verifyAdminPassword checks the current admin password (step-up for sensitive
// ops). On failure it writes the error response and returns false.
//
// A missing/expired session is 401 (the SPA treats that as "session gone" and
// drops to the login screen). A WRONG step-up password, though, must NOT be 401:
// the session is still valid, only this one action is refused — so it returns 403
// and the SPA shows the error inline instead of logging the admin out.
func (rt *Router) verifyAdminPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	id, ok := rt.adminID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "не авторизован")
		return false
	}
	hash, err := rt.mgr.Store().GetAdminHash(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		return false
	}
	if !auth.VerifyPassword(hash, password) {
		writeErr(w, http.StatusForbidden, "неверный пароль")
		return false
	}
	return true
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
	// The first-run wizard reads TLS status on mount (before the password is
	// changed, so must_change is still set) to show the correct address step —
	// "already on domain <host>" vs "over IP". Without this it 403s, the wizard
	// silently falls back to the IP wording and claims a self-signed cert even when
	// a real domain cert is live. Read-only; the wizard's POST /api/tls (issue cert)
	// runs later, after the password step has already cleared must_change.
	"/api/tls": true,
}

// requireAuth rejects requests without a valid session. Because this only runs
// under the secret path, a 401 here never reveals the panel to outsiders. While the
// admin still carries a password someone else picked, it also blocks everything but
// the password-change / restore endpoints (see mustChangeAllowed).
//
// The gate is per-account: a colleague who has not yet replaced the temporary
// password the owner handed them is locked to the password screen, while everyone
// else keeps working.
func (rt *Router) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "не авторизован")
			return
		}
		a, ok := rt.mgr.Store().LookupSession(c.Value)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "не авторизован")
			return
		}
		if !mustChangeAllowed[r.URL.Path] && a.MustChangePassword {
			writeErr(w, http.StatusForbidden, "смените пароль, прежде чем пользоваться панелью")
			return
		}
		// Stamp the acting admin onto the context so the audit log can attribute every
		// mutation this request makes, without each handler re-reading the cookie, and
		// carry the resolved session for the role check and the handlers.
		ctx := actor.With(r.Context(), actor.Admin(a.Username))
		next(w, r.WithContext(context.WithValue(ctx, ctxKeyAdmin{}, a)))
	}
}

// requireRole is requireAuth plus a floor on the caller's role. Roles are a ladder
// (operator < admin < owner), so the check is a rank comparison — see model.RoleAtLeast.
//
// A caller below the tier gets 403, never 401: their session is perfectly valid, so
// the SPA must show "недостаточно прав" rather than bounce them to the login screen.
func (rt *Router) requireRole(tier string, next http.HandlerFunc) http.HandlerFunc {
	return rt.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		a, ok := sessionAdminFrom(r.Context())
		if !ok || !model.RoleAtLeast(a.Role, tier) {
			slog.Warn("panel: role check failed",
				"admin", a.Username, "role", a.Role, "need", tier, "path", r.URL.Path)
			writeErr(w, http.StatusForbidden, "недостаточно прав")
			return
		}
		next(w, r)
	})
}
