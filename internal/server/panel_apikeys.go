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
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  set.APIPath != "",
		"api_path": set.APIPath,
		"base_url": apiBaseURL(r, set.APIPath),
		"keys":     keys,
	})
}

// createAPIKey mints a new named key and returns its raw value exactly once.
// Enabling the API surface is a separate action (POST /api/settings/api-path):
// keys can be created while the API is off and simply start working once it's
// turned on.
func (rt *Router) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "укажите название ключа")
		return
	}
	key, err := rt.mgr.Store().CreateAPIKey(req.Name)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
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

// revokeAPIKey permanently disables one key by id.
func (rt *Router) revokeAPIKey(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.Store().RevokeAPIKey(id); err != nil {
		writeManagerErr(w, err)
		return
	}
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
			writeErr(w, http.StatusInternalServerError, "не удалось сгенерировать путь API")
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
