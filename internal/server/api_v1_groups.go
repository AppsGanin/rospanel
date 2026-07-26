package server

import "net/http"

// User groups over the external API. Same shapes as the panel (model.Group in,
// groupReq for writes), so an integration and the panel describe a group identically.

func (rt *Router) apiListGroups(w http.ResponseWriter, _ *http.Request) {
	groups, err := rt.mgr.Groups()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, groups)
}

func (rt *Router) apiCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupReq
	if !apiDecode(w, r, &req) {
		return
	}
	g, err := rt.mgr.CreateGroup(req.Name, req.Grants)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusCreated, g)
}

func (rt *Router) apiUpdateGroup(w http.ResponseWriter, r *http.Request, id int64) {
	var req groupReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.UpdateGroup(id, req.Name, req.Grants); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) apiDeleteGroup(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteGroup(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiSetGroupMembers replaces the users in a group.
func (rt *Router) apiSetGroupMembers(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetGroupMembers(id, req.UserIDs); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiSetUserGroups replaces a user's group membership.
func (rt *Router) apiSetUserGroups(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		GroupIDs []int64 `json:"group_ids"`
	}
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserGroups(id, req.GroupIDs); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}
