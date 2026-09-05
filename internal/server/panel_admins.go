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

// deleteAdmin removes an account, re-checking the owner's password first.
//
// The password rides in the request BODY, on a DELETE. It used to be a header, which
// cannot carry it: header values are ISO-8859-1, so a browser refuses outright to send
// a Cyrillic password and turns an accented one into bytes that can never match the
// stored hash — a correct password answered "wrong password" with nothing to explain
// it, and an owner whose password was not ASCII could never remove an account at all.
// Passwords have no charset restriction, so that was not a corner case. Go's mux and
// every browser's fetch both handle a DELETE body; see stepUpBody, which the two
// irreversible actions use for the same reason.
func (rt *Router) deleteAdmin(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
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
