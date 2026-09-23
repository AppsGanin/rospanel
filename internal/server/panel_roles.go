package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Admin roles — what each account may see and do. Owner-only like the roster (see
// panelMux), and every mutation re-asks the owner for their password for the same
// reason: widening a colleague's role is as good as minting a new admin, and a
// stolen cookie must not be enough for either.

// roleReq is the body of a role save. Perms are permission keys from the catalog;
// unknown ones are dropped and manage brings its view (model.NormalizePerms).
type roleReq struct {
	Name            string   `json:"name"`
	Perms           []string `json:"perms"`
	CurrentPassword string   `json:"current_password"`
}

// listRoles returns the roles, the permission catalog the editor draws its rows from
// and what each permission implies, so the SPA never carries its own copy of either
// to drift from this one.
func (rt *Router) listRoles(w http.ResponseWriter, _ *http.Request) {
	roles, err := rt.mgr.ListAdminRoles()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles":   roles,
		"catalog": model.PermCatalog,
		// What each permission brings with it, so the editor ticks it too rather than
		// showing a role narrower than the server will store.
		"implies": model.PermImplies,
	})
}

func (rt *Router) createRole(w http.ResponseWriter, r *http.Request) {
	var req roleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	role, err := rt.mgr.CreateAdminRole(req.Name, req.Perms)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, role.Name)
	auditDetails(r, map[string]any{"perms": strings.Join(role.Perms, ",")})
	writeJSON(w, http.StatusCreated, role)
}

func (rt *Router) updateRole(w http.ResponseWriter, r *http.Request) {
	var req roleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	key := r.PathValue("key")
	before, _ := rt.mgr.Store().GetAdminRole(key) // for the audit row; a bad key fails below
	role, err := rt.mgr.UpdateAdminRole(key, req.Name, req.Perms)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, roleLabel(role))
	auditDetails(r, map[string]any{
		"from": strings.Join(before.Perms, ","),
		"to":   strings.Join(role.Perms, ","),
	})
	writeJSON(w, http.StatusOK, role)
}

// deleteRole removes a role nobody holds. The password rides in the body, as on
// every DELETE of the roster (see deleteAdmin for why not a header).
func (rt *Router) deleteRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	key := r.PathValue("key")
	before, _ := rt.mgr.Store().GetAdminRole(key)
	if err := rt.mgr.DeleteAdminRole(key); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, roleLabel(before))
	writeOK(w)
}

// roleForAudit names a role key in the trail the way the panel shows it: the owner
// and an unrenamed preset by their key (the journal translates those), anything else
// by the name the owner gave it — a generated key means nothing to a reader, and a
// name recorded now survives the role's deletion.
func (rt *Router) roleForAudit(key string) string {
	if r, err := rt.mgr.Store().GetAdminRole(key); err == nil && r.Name != "" {
		return r.Name
	}
	return key
}

// roleLabel names a role in the audit trail: its name, or its key for a preset
// nobody renamed (the journal shows it verbatim, and "admin" still says which).
func roleLabel(r model.AdminRole) string {
	if r.Name != "" {
		return r.Name
	}
	return r.Key
}
