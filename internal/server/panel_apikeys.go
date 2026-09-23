package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/auth"
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
	if keys == nil {
		keys = []model.APIKey{}
	}
	// The roles a new key may be given: the ones the caller's own permissions cover
	// (core.CreateAPIKey refuses the rest), and full access only for a caller who
	// holds everything. Listed here rather than read from /api/roles, which is the
	// owner's: whoever manages keys has to be able to pick a role for one.
	all, err := rt.mgr.ListAdminRoles()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	mine := callerPerms(r)
	// Every role is listed, so a key's role reads by name in the list; grantable says
	// which of them the caller may pick for a new one.
	type keyRole struct {
		Key       string `json:"key"`
		Name      string `json:"name"`
		Preset    bool   `json:"preset"`
		Grantable bool   `json:"grantable"`
	}
	roles := make([]keyRole, 0, len(all))
	for _, role := range all {
		roles = append(roles, keyRole{
			Key: role.Key, Name: role.Name, Preset: role.Preset,
			Grantable: mine.Covers(model.NewPermSet(role.Perms)),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     set.APIPath != "",
		"api_path":    set.APIPath,
		"base_url":    apiBaseURL(r, set.APIPath),
		"keys":        keys,
		"roles":       roles,
		"full_access": mine.Covers(model.OwnerPermSet()),
	})
}

// createAPIKey mints a new named key and returns its raw value exactly once.
// Enabling the API surface is a separate action (POST /api/settings/api-path):
// keys can be created while the API is off and simply start working once it's
// turned on.
func (rt *Router) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// Role the key acts with; empty = full access. Never broader than the caller's
		// own (see core.CreateAPIKey).
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "err.keyNameRequired", "укажите название ключа")
		return
	}
	key, err := rt.mgr.CreateAPIKey(req.Name, strings.TrimSpace(req.Role), callerPerms(r))
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// Which key, and with what reach: a full-access key and a read-only one are
	// different events in the trail. The key itself is never recorded.
	auditTarget(r, key.Name)
	auditDetails(r, map[string]any{"role": rt.keyRoleForAudit(key.Role)})
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

// revokeAPIKey permanently disables one key by id — one the caller could have issued.
func (rt *Router) revokeAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	name := ""
	if keys, err := rt.mgr.Store().ListAPIKeys(); err == nil {
		for _, k := range keys {
			if k.ID == id {
				name = k.Name
			}
		}
	}
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

// keyRoleForAudit names a key's role in the trail: "full" for none.
func (rt *Router) keyRoleForAudit(role string) string {
	if role == "" {
		return "full"
	}
	return rt.roleForAudit(role)
}
