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
	// A key holds its own permissions, ticked from the catalog like a role's. The
	// caller may give what they hold themselves (grantable), and full access only
	// when they hold everything (core.CreateAPIKey refuses the rest).
	mine := callerPerms(r)
	grantable := []string{}
	for _, p := range model.AllPerms() {
		if mine.Has(p) {
			grantable = append(grantable, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     set.APIPath != "",
		"api_path":    set.APIPath,
		"base_url":    apiBaseURL(r, set.APIPath),
		"keys":        keys,
		"catalog":     model.PermCatalog,
		"implies":     model.PermImplies,
		"grantable":   grantable,
		"full_access": mine.Covers(model.OwnerPermSet()),
	})
}

// apiKeyPermsReq is what a key may do: everything, or the permissions ticked.
type apiKeyPermsReq struct {
	FullAccess bool     `json:"full_access"`
	Perms      []string `json:"perms"`
}

// createAPIKey mints a new named key and returns its raw value exactly once.
// Enabling the API surface is a separate action (POST /api/settings/api-path):
// keys can be created while the API is off and simply start working once it's
// turned on.
func (rt *Router) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// Never broader than the caller's own (see core.CreateAPIKey).
		apiKeyPermsReq
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "err.keyNameRequired", "укажите название ключа")
		return
	}
	key, err := rt.mgr.CreateAPIKey(req.Name, req.FullAccess, req.Perms, callerPerms(r))
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// Which key, and with what reach: a full-access key and a read-only one are
	// different events in the trail. The key itself is never recorded.
	auditTarget(r, key.Name)
	auditDetails(r, keyPermsForAudit(key.FullAccess, key.Grants))
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

// setAPIKeyPerms changes what an active key may do — one the caller could have
// issued, to what they could issue now.
func (rt *Router) setAPIKeyPerms(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiKeyPermsReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetAPIKeyPerms(id, req.FullAccess, req.Perms, callerPerms(r)); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, rt.apiKeyName(id))
	auditDetails(r, keyPermsForAudit(req.FullAccess, model.NormalizePerms(req.Perms)))
	writeOK(w)
}

// apiKeyName names a key in the trail.
func (rt *Router) apiKeyName(id int64) string {
	if keys, err := rt.mgr.Store().ListAPIKeys(); err == nil {
		for _, k := range keys {
			if k.ID == id {
				return k.Name
			}
		}
	}
	return ""
}

// revokeAPIKey permanently disables one key by id — one the caller could have issued.
func (rt *Router) revokeAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	name := rt.apiKeyName(id)
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

// keyPermsForAudit is what a key may do, as the trail records it: a full-access
// key and a read-only one are different events.
func keyPermsForAudit(full bool, perms []string) map[string]any {
	if full {
		return map[string]any{"perms": "full"}
	}
	return map[string]any{"perms": strings.Join(perms, ",")}
}
