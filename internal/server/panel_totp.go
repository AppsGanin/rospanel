package server

import (
	"net/http"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The admin's own second factor. Every route here acts on the CALLER — there is no id
// in any path — so nobody can start, confirm or remove somebody else's 2FA through the
// panel, not even the owner. The way to help a colleague who lost their phone is
// `rospanel totp reset <login>` on the server, which needs the machine rather than a
// session.

// totpStatus tells the account screen whether a second factor is on. Whether a
// half-finished setup is lying around is deliberately not reported: pressing
// "turn on" always mints a fresh secret, so a leftover one changes nothing the
// operator could act on.
func (rt *Router) totpStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	t, err := rt.mgr.Store().AdminTOTPByID(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": t.Enabled()})
}

// totpStart mints a secret and returns it once, with the otpauth URI the account
// screen renders as a QR. Behind the password: a session left open on an unlocked
// laptop must not be enough to bind a second factor to a stranger's phone.
//
// The secret is stored as PENDING. Until a live code proves the app holds it, the
// password alone still signs this admin in — otherwise a QR that never made it into
// an authenticator would lock the operator out of their own panel.
func (rt *Router) totpStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	admin, err := rt.mgr.Store().GetAdmin(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if admin.TOTPEnabled {
		writeErrCode(w, http.StatusBadRequest, "err.totpAlreadyOn", "двухфакторная аутентификация уже включена")
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	if err := rt.mgr.Store().SetAdminTOTPPending(id, secret); err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	issuer := branding.Name(set.PanelName)
	writeJSON(w, http.StatusOK, map[string]any{
		"secret": secret,
		"uri":    auth.TOTPURI(issuer, admin.Username, secret),
	})
}

// totpEnable turns the pending secret into the live one, and only against a code the
// authenticator produced right now — which is the only proof that the operator can
// still generate codes tomorrow.
func (rt *Router) totpEnable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	t, err := rt.mgr.Store().AdminTOTPByID(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if t.Enabled() {
		writeErrCode(w, http.StatusBadRequest, "err.totpAlreadyOn", "двухфакторная аутентификация уже включена")
		return
	}
	if t.Pending == "" {
		writeErrCode(w, http.StatusBadRequest, "err.totpNoSetup", "сначала начните подключение")
		return
	}
	// Guessing is guessing, even on a route that needs a session: six digits fall to a
	// few hundred thousand tries, and a hit here binds a second factor whose secret the
	// guesser doesn't hold — a lockout that costs the operator a trip to the server.
	// The login's counters are the right ones to share: it is the same account.
	a, _ := sessionAdminFrom(r.Context())
	ip := clientIP(r)
	if rt.limiter.blocked(ip, a.Username) {
		writeErrCode(w, http.StatusTooManyRequests, "err.tooManyAttempts", "слишком много попыток, повторите позже")
		return
	}
	step, valid := auth.VerifyTOTP(t.Pending, req.Code, time.Now(), 0)
	if !valid {
		rt.limiter.fail(ip, a.Username)
		writeErrCode(w, http.StatusBadRequest, "err.totpInvalid", "неверный код")
		return
	}
	// The step travels with the secret so the code just used cannot also sign in.
	if err := rt.mgr.Store().EnableAdminTOTP(id, t.Pending, step); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.auditTOTP(r, model.AuditTOTPEnabled)
	writeOK(w)
}

// totpDisable removes the second factor. Password-gated for the same reason as
// starting one: the point of 2FA is that a stolen session is not enough on its own.
func (rt *Router) totpDisable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	if err := rt.mgr.Store().DisableAdminTOTP(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.auditTOTP(r, model.AuditTOTPDisabled)
	writeOK(w)
}

// auditTOTP records a second-factor change against the admin who made it. Worth a row
// of its own: "who removed 2FA on this account, and when" is exactly the question
// asked after an account is misused.
func (rt *Router) auditTOTP(r *http.Request, action string) {
	a, _ := sessionAdminFrom(r.Context())
	name := a.Username
	rt.mgr.AddAdminAudit(model.AdminAudit{
		Action: action, Target: name,
		ActorKind: model.ActorAdmin, ActorName: name, IP: clientIP(r),
	})
}
