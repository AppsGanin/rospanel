package server

import (
	"net/http"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// The admin roster. Every route here is owner-only (see panelMux), and every
// mutation additionally re-asks the owner for their own password: a session cookie
// alone must not be enough to mint a second admin, which would be a quiet way to
// turn a stolen cookie into permanent access.

// listAdmins returns the roster plus the caller's own id, so the SPA can tell which
// row is "you" and grey out the actions that don't apply to yourself.
func (rt *Router) listAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := rt.mgr.ListAdmins()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	me, _ := rt.adminID(r)
	if a, ok := sessionAdminFrom(r.Context()); ok && a.Role == model.RoleAdmin {
		var filtered []model.Admin
		for _, adm := range admins {
			if adm.ID == me || adm.Role == model.RoleOperator {
				filtered = append(filtered, adm)
			}
		}
		admins = filtered
	}
	if admins == nil {
		admins = []model.Admin{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"admins": admins,
		"me":     me,
	})
}

// createAdmin adds an account with a password the owner chose. The password is shown
// to the owner once, to hand over; the account cannot do anything until it replaces
// it (model gate: must_change_password).
func (rt *Router) createAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		Role            string `json:"role"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	admin, err := rt.mgr.CreateAdmin(req.Username, req.Password, req.Role)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// Name the new account in the audit row. The password is never recorded.
	auditTarget(r, admin.Username)
	auditDetails(r, map[string]any{"role": admin.Role})
	writeJSON(w, http.StatusCreated, admin)
}

// setAdminRole moves an account between roles.
func (rt *Router) setAdminRole(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Role            string `json:"role"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	me, _ := rt.adminID(r)
	target, _ := rt.mgr.Store().GetAdmin(id) // for the audit row; a bad id fails below anyway
	if err := rt.mgr.SetAdminRole(me, id, req.Role); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, target.Username)
	auditDetails(r, map[string]any{"from": target.Role, "to": req.Role})
	writeOK(w)
}

// resetAdminPassword assigns a new password to a locked-out colleague and kicks
// every session they had.
func (rt *Router) resetAdminPassword(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Password        string `json:"password"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	me, _ := rt.adminID(r)
	target, _ := rt.mgr.Store().GetAdmin(id)
	if err := rt.mgr.ResetAdminPassword(me, id, req.Password); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, target.Username)
	writeOK(w)
}

// deleteAdmin removes an account. The password comes in a header rather than a body:
// DELETE bodies are the kind of thing proxies and clients feel free to drop.
func (rt *Router) deleteAdmin(w http.ResponseWriter, r *http.Request, id int64) {
	if !rt.verifyAdminPassword(w, r, r.Header.Get("X-Current-Password")) {
		return
	}
	me, _ := rt.adminID(r)
	// Read the login before the row is gone — afterwards the audit trail would only
	// be able to say that "some id" was deleted.
	target, _ := rt.mgr.Store().GetAdmin(id)
	if err := rt.mgr.DeleteAdmin(me, id); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, target.Username)
	auditDetails(r, map[string]any{"role": target.Role})
	writeOK(w)
}

func (rt *Router) currentSessionHash(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		hash, err := rt.mgr.Store().TokenHash(c.Value)
		if err == nil {
			return hash
		}
	}
	return ""
}

// mySessions returns the active sessions of the currently signed-in user.
func (rt *Router) mySessions(w http.ResponseWriter, r *http.Request) {
	me, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	sessions, err := rt.mgr.ListAdminSessions(me, me)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	currentHash := rt.currentSessionHash(r)
	for i := range sessions {
		if sessions[i].TokenHash == currentHash {
			sessions[i].IsCurrent = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// deleteMySession revokes a specific session of the currently signed-in user.
func (rt *Router) deleteMySession(w http.ResponseWriter, r *http.Request) {
	me, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeErrCode(w, http.StatusBadRequest, "err.badRequestBody", "не указан идентификатор сессии")
		return
	}
	if a, ok := sessionAdminFrom(r.Context()); ok {
		auditTarget(r, a.Username)
	}
	if err := rt.mgr.DeleteAdminSession(me, me, hash); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// deleteAllMyOtherSessions revokes all sessions of the current user except the current one.
func (rt *Router) deleteAllMyOtherSessions(w http.ResponseWriter, r *http.Request) {
	me, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = c.Value
	}
	if a, ok := sessionAdminFrom(r.Context()); ok {
		auditTarget(r, a.Username)
	}
	if err := rt.mgr.DeleteAllAdminSessions(me, me, keep); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// getAdminSessions returns the active sessions for a target admin/operator.
func (rt *Router) getAdminSessions(w http.ResponseWriter, r *http.Request, id int64) {
	me, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	sessions, err := rt.mgr.ListAdminSessions(me, id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	currentHash := rt.currentSessionHash(r)
	for i := range sessions {
		if sessions[i].TokenHash == currentHash {
			sessions[i].IsCurrent = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// deleteAdminSession revokes a specific session for a target admin/operator.
func (rt *Router) deleteAdminSession(w http.ResponseWriter, r *http.Request, id int64) {
	me, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeErrCode(w, http.StatusBadRequest, "err.badRequestBody", "не указан идентификатор сессии")
		return
	}
	target, _ := rt.mgr.Store().GetAdmin(id)
	if target.Username != "" {
		auditTarget(r, target.Username)
	}
	if err := rt.mgr.DeleteAdminSession(me, id, hash); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// deleteAllAdminSessions revokes all sessions for a target admin/operator.
func (rt *Router) deleteAllAdminSessions(w http.ResponseWriter, r *http.Request, id int64) {
	me, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	target, _ := rt.mgr.Store().GetAdmin(id)
	if target.Username != "" {
		auditTarget(r, target.Username)
	}
	keep := ""
	if id == me && r.URL.Query().Get("all") != "true" {
		if c, err := r.Cookie(sessionCookie); err == nil {
			keep = c.Value
		}
	}
	if err := rt.mgr.DeleteAllAdminSessions(me, id, keep); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
