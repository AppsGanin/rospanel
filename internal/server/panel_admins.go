package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
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
	if admins == nil {
		admins = []model.Admin{}
	}
	me, _ := rt.adminID(r)
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
