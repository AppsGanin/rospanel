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
	pal := branding.ParsePalette("")
	if set, err := rt.mgr.Settings(); err == nil {
		name = branding.Name(set.PanelName)
		pal = branding.ParsePalette(set.PanelTheme)
	}
	// default_theme_dark has no accent: the dark default is whatever the light
	// accent is, which the editor knows better than this reply (it may be unsaved).
	defDark := branding.DefaultDarkTheme("")
	return map[string]any{
		"panel_name":         name,
		"theme":              pal.Light,
		"theme_dark":         pal.Dark,
		"has_custom_logo":    branding.HasCustomLogo(rt.dataDir),
		"default_name":       branding.DefaultName,
		"default_theme":      branding.DefaultTheme(),
		"default_theme_dark": defDark,
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
		writeErrCode(w, http.StatusInternalServerError, "err.logoReadFailed", "не удалось прочитать логотип")
		return
	}
	w.Header().Set("Content-Type", branding.LogoContentType(b))
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}

// saveBranding persists the panel name and colour theme.
func (rt *Router) saveBranding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PanelName string          `json:"panel_name"`
		Theme     branding.Theme  `json:"theme"`
		ThemeDark *branding.Theme `json:"theme_dark"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// A caller that knows nothing of the dark theme (an old panel tab) keeps what
	// is stored rather than wiping it.
	if req.ThemeDark == nil {
		var d branding.Theme
		if set, err := rt.mgr.Settings(); err == nil {
			pal := branding.ParsePalette(set.PanelTheme)
			d = pal.Dark
			if d.Accent == pal.Light.Accent {
				d.Accent = "" // still following the light accent
			}
		}
		req.ThemeDark = &d
	}
	if err := rt.mgr.SetPanelName(req.PanelName); err != nil {
		writeManagerErr(w, err)
		return
	}
	if err := rt.mgr.SetPanelTheme(req.Theme, *req.ThemeDark); err != nil {
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
		writeErrCode(w, http.StatusBadRequest, "err.uploadParseFailed", "не удалось разобрать загрузку")
		return
	}
	f, _, err := r.FormFile("logo")
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.logoFileMissing", "файл logo не найден")
		return
	}
	defer f.Close()
	// writeManagerErr, not writeErr: SaveLogo raises a coded FieldError for a
	// rejected upload (too big, wrong format) and a plain error for a disk failure.
	// writeErr would flatten both to text and drop the code the panel translates.
	if err := branding.SaveLogo(rt.dataDir, f); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rt.brandingInfo())
}

// deleteBrandingLogo removes the custom logo, reverting to the default.
func (rt *Router) deleteBrandingLogo(w http.ResponseWriter, _ *http.Request) {
	if err := branding.DeleteLogo(rt.dataDir); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.logoDeleteFailed", "не удалось удалить логотип")
		return
	}
	writeJSON(w, http.StatusOK, rt.brandingInfo())
}
