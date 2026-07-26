package server

import (
	"net/http"
)

// Group endpoints. A group gates which connections its members may use; membership is
// assigned on the user (POST /api/users/{id}/groups).

// listGroups returns every group with its grants and member count.
func (rt *Router) listGroups(w http.ResponseWriter, _ *http.Request) {
	groups, err := rt.mgr.Groups()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// groupTargets returns the grantable connections (built-in lanes + custom inbounds)
// per server, so the group editor can render checkboxes and resolve tokens to names.
func (rt *Router) groupTargets(w http.ResponseWriter, _ *http.Request) {
	targets, err := rt.mgr.GroupTargets()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, targets)
}

// groupReq is the editable shape of a group.
type groupReq struct {
	Name   string   `json:"name"`
	Grants []string `json:"grants"`
}

// createGroup adds a group.
func (rt *Router) createGroup(w http.ResponseWriter, r *http.Request) {
	var req groupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	g, err := rt.mgr.CreateGroup(req.Name, req.Grants)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

// updateGroup edits a group (rename + grants).
func (rt *Router) updateGroup(w http.ResponseWriter, r *http.Request, id int64) {
	var req groupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.UpdateGroup(id, req.Name, req.Grants); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// deleteGroup removes a group; its members lose that grant.
func (rt *Router) deleteGroup(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteGroup(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setGroupMembers replaces the users in a group (the group-side membership editor).
func (rt *Router) setGroupMembers(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetGroupMembers(id, req.UserIDs); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setUserGroups replaces a user's group membership.
func (rt *Router) setUserGroups(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		GroupIDs []int64 `json:"group_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserGroups(id, req.GroupIDs); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
