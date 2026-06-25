package server

import (
	"net/http"
	"time"

	"github.com/AppsGanin/rospanel/internal/branding"
)

// brandingInfo is the shape returned to the SPA: the resolved name/theme plus the
// built-in defaults (so the UI can show what "reset" reverts to).
func (rt *Router) brandingInfo() map[string]any {
	name := branding.DefaultName
	theme := branding.DefaultTheme()
	if set, err := rt.mgr.Settings(); err == nil {
		name = branding.Name(set.PanelName)
		theme = branding.ParseTheme(set.PanelTheme)
	}
	return map[string]any{
		"panel_name":      name,
		"theme":           theme,
		"has_custom_logo": branding.HasCustomLogo(rt.dataDir),
		"default_name":    branding.DefaultName,
		"default_theme":   branding.DefaultTheme(),
	}
}

// getBranding returns name/accent/logo state. Unauthenticated — the login screen
// (under the secret path) needs it before any session exists.
func (rt *Router) getBranding(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rt.brandingInfo())
}

// brandingLogo serves the panel logo (custom file or built-in default).
func (rt *Router) brandingLogo(w http.ResponseWriter, _ *http.Request) {
	b, err := branding.ReadLogo(rt.dataDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "не удалось прочитать логотип")
		return
	}
	w.Header().Set("Content-Type", branding.LogoContentType(b))
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}

// saveBranding persists the panel name and colour theme.
func (rt *Router) saveBranding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PanelName string         `json:"panel_name"`
		Theme     branding.Theme `json:"theme"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetPanelName(req.PanelName); err != nil {
		writeManagerErr(w, err)
		return
	}
	if err := rt.mgr.SetPanelTheme(req.Theme); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rt.brandingInfo())
}

// uploadBrandingLogo stores an uploaded PNG/JPEG as the custom logo.
func (rt *Router) uploadBrandingLogo(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, branding.MaxLogoBytes+4096)
	if err := r.ParseMultipartForm(branding.MaxLogoBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "не удалось разобрать загрузку")
		return
	}
	f, _, err := r.FormFile("logo")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "файл logo не найден")
		return
	}
	defer f.Close()
	if err := branding.SaveLogo(rt.dataDir, f); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rt.brandingInfo())
}

// deleteBrandingLogo removes the custom logo, reverting to the default.
func (rt *Router) deleteBrandingLogo(w http.ResponseWriter, _ *http.Request) {
	if err := branding.DeleteLogo(rt.dataDir); err != nil {
		writeErr(w, http.StatusInternalServerError, "не удалось удалить логотип")
		return
	}
	writeJSON(w, http.StatusOK, rt.brandingInfo())
}
