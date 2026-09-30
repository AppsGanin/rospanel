package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/mcp"
	"github.com/AppsGanin/rospanel/internal/model"
)

// apiBaseURL builds the external API's base URL for display in the panel, from
// the request's own host and the configured path segment. Empty path ⇒ "".
func apiBaseURL(r *http.Request, apiPath string) string {
	if apiPath == "" {
		return ""
	}
	return "https://" + r.Host + "/" + apiPath
}

// listAPIKeys returns the API surface state (enabled flag, path, base URL) plus
// the list of keys. Raw keys are never included here — only prefixes.
func (rt *Router) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	keys, err := rt.mgr.Store().ListAPIKeys()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	mine := callerPerms(r)
	// Each key with the methods it may call (a key still held to its permissions
	// shows the methods those open) and whether this caller may change or revoke it.
	type keyView struct {
		model.APIKey
		CanManage bool `json:"can_manage"`
	}
	views := make([]keyView, 0, len(keys))
	for _, k := range keys {
		k.Routes = keyRoutes(k)
		views = append(views, keyView{APIKey: k, CanManage: keyCoveredBy(k, mine)})
	}
	// Every method a key can be ticked for, grouped by the spec's sections;
	// grantable is whether this caller may give it — the methods they may call
	// themselves. Full access only for a caller who holds everything.
	type routeView struct {
		Route     string `json:"route"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		Tag       string `json:"tag"`
		Key       string `json:"key"` // the dictionary key naming it (the MCP tool name)
		Grantable bool   `json:"grantable"`
	}
	routes := []routeView{}
	for _, t := range tickableRoutes() {
		routes = append(routes, routeView{
			Route: t.pattern, Method: t.method, Path: t.path, Tag: t.tag,
			Key: mcp.ToolName(t.method, t.path), Grantable: apiMayCall(apiAccess{perms: mine}, t.pattern),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     set.APIPath != "",
		"api_path":    set.APIPath,
		"base_url":    apiBaseURL(r, set.APIPath),
		"keys":        views,
		"routes":      routes,
		"full_access": mine.Covers(model.OwnerPermSet()),
	})
}

// apiKeyAccessReq is what a key may do: everything, or the API methods ticked.
type apiKeyAccessReq struct {
	FullAccess bool     `json:"full_access"`
	Routes     []string `json:"routes"`
}

// grant turns the request into what core stores: for methods, the permissions they
// carry, checked against the caller.
func (req apiKeyAccessReq) grant(caller model.PermSet) (perms, routes []string, err error) {
	if req.FullAccess {
		return nil, nil, nil
	}
	perms, err = keyGrantForRoutes(req.Routes, caller)
	return perms, req.Routes, err
}

// createAPIKey mints a new named key and returns its raw value exactly once.
// Enabling the API surface is a separate action (POST /api/settings/api-path):
// keys can be created while the API is off and simply start working once it's
// turned on.
func (rt *Router) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// Never broader than the caller's own (see keyGrantForRoutes, core.CreateAPIKey).
		apiKeyAccessReq
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "err.keyNameRequired", "укажите название ключа")
		return
	}
	caller := callerPerms(r)
	perms, routes, err := req.grant(caller)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	key, err := rt.mgr.CreateAPIKey(req.Name, req.FullAccess, perms, routes, caller)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// Which key, and with what reach: a full-access key and a read-only one are
	// different events in the trail. The key itself is never recorded.
	auditTarget(r, key.Name)
	auditDetails(r, keyAccessForAudit(key.FullAccess, key.Routes))
	set, _ := rt.mgr.Store().GetSettings()
	base := ""
	if set != nil {
		base = apiBaseURL(r, set.APIPath)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":      key, // includes raw_key — shown once
		"base_url": base,
	})
}

// setAPIKeyAccess changes what an active key may do — one the caller could have
// issued, to what they could issue now.
func (rt *Router) setAPIKeyAccess(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiKeyAccessReq
	if !decodeJSON(w, r, &req) {
		return
	}
	caller := callerPerms(r)
	k, ok := rt.apiKey(id)
	if !ok || k.RevokedAt != 0 {
		writeErrCode(w, http.StatusBadRequest, "err.keyNotFound", "ключ не найден")
		return
	}
	if !keyCoveredBy(k, caller) {
		writeErrCode(w, http.StatusBadRequest, "err.keyOutranksYou", "у ключа больше прав, чем у вас, — менять или отзывать его может тот, у кого они есть")
		return
	}
	perms, routes, err := req.grant(caller)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if err := rt.mgr.SetAPIKeyPerms(id, req.FullAccess, perms, routes, caller); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, k.Name)
	auditDetails(r, keyAccessForAudit(req.FullAccess, routes))
	writeOK(w)
}

// apiKey finds one key, revoked or not.
func (rt *Router) apiKey(id int64) (model.APIKey, bool) {
	if keys, err := rt.mgr.Store().ListAPIKeys(); err == nil {
		for _, k := range keys {
			if k.ID == id {
				return k, true
			}
		}
	}
	return model.APIKey{}, false
}

// revokeAPIKey permanently disables one key by id — one the caller could have issued.
func (rt *Router) revokeAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	k, ok := rt.apiKey(id)
	if ok && !keyCoveredBy(k, callerPerms(r)) {
		writeErrCode(w, http.StatusBadRequest, "err.keyOutranksYou", "у ключа больше прав, чем у вас, — менять или отзывать его может тот, у кого они есть")
		return
	}
	name := k.Name
	if err := rt.mgr.RevokeAPIKey(id, callerPerms(r)); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, name)
	writeOK(w)
}

// setAPIPathSettings turns the API surface on/off and rotates its path segment.
// Disabling ({"enabled": false}) clears the path — every integration URL breaks
// until re-enabled, but existing keys are untouched and work again once a path is
// restored. Rotating ({"enabled": true, "rotate": true}) mints a fresh segment.
func (rt *Router) setAPIPathSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
		Rotate  bool `json:"rotate"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	newPath := set.APIPath
	switch {
	case !req.Enabled:
		newPath = ""
	case set.APIPath == "" || req.Rotate:
		p, err := auth.RandomSecretPath()
		if err != nil {
			writeErrCode(w, http.StatusInternalServerError, "err.apiPathGenFailed", "не удалось сгенерировать путь API")
			return
		}
		newPath = p
	}
	if newPath != set.APIPath {
		if err := rt.mgr.Store().SetAPIPath(newPath); err != nil {
			writeManagerErr(w, err)
			return
		}
		rt.setAPIPath(newPath)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  newPath != "",
		"api_path": newPath,
		"base_url": apiBaseURL(r, newPath),
	})
}

// keyAccessForAudit is what a key may do, as the trail records it: a full-access
// key and a read-only one are different events.
func keyAccessForAudit(full bool, routes []string) map[string]any {
	if full {
		return map[string]any{"routes": "full"}
	}
	return map[string]any{"routes": strings.Join(routes, ", ")}
}
