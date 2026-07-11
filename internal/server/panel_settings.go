package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/model"
)

func (rt *Router) setupPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.adminID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "не авторизован")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.ChangeAdminPassword(id, req.Password); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setupTimezone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Timezone string `json:"timezone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetTimezone(req.Timezone); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) getSettings(w http.ResponseWriter, _ *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	templates, _ := decoy.Available()
	writeJSON(w, http.StatusOK, map[string]any{
		"secret_path":         set.PanelSecretPath,
		"ws_path":             set.WSPath,
		"decoy_template":      set.DecoyTemplate,
		"decoy_templates":     templates,
		"sub_path":            set.SubPathOr(),
		"sub_base64":          set.SubBase64,
		"sub_name_in_title":   set.SubNameInTitle,
		"sub_title":           set.SubTitle,
		"sub_routing":         set.SubRouting,
		"sub_routing_happ":    set.SubRoutingHapp,
		"sub_routing_incy":    set.SubRoutingIncy,
		"sub_routing_mihomo":  set.SubRoutingMihomo,
		"sub_update_interval": set.SubUpdateInterval,
		"xray_dns":            set.XrayDNS,
		"warp_enabled":        set.WarpEnabled,
		"warp_registered":     set.WarpRegistered(),
		"proxy_mode_enabled":  set.ProxyModeEnabled,
		"proxy_mode_type":     set.ProxyModeType,
		"proxy_mode_port":     set.ProxyModePort,
		"proxy_mode_user":     set.ProxyModeUser,
		"proxy_mode_pass":     set.ProxyModePass,
		"local_backup_cron":   set.LocalBackupCron,
		"local_backup_keep":   set.LocalBackupKeep,
	})
}

// setLocalBackup configures the scheduled local backup (cron in the operator
// timezone; empty disables it) and how many archives to retain.
func (rt *Router) setLocalBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cron string `json:"cron"`
		Keep int    `json:"keep"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SaveLocalBackup(req.Cron, req.Keep); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setProxyMode toggles/configures the socks/http forward-proxy inbound.
func (rt *Router) setProxyMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		Type    string `json:"type"`
		Port    int    `json:"port"`
		User    string `json:"user"`
		Pass    string `json:"pass"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetProxyMode(req.Enabled, req.Type, req.Port, req.User, req.Pass); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// geoCategories returns the geosite + geoip category codes for routing presets.
func (rt *Router) geoCategories(w http.ResponseWriter, _ *http.Request) {
	geosite, geoip, err := rt.mgr.GeoCategories()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"geosite": geosite, "geoip": geoip})
}

// geoStatus reports the geo databases' presence + last-download time.
func (rt *Router) geoStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rt.mgr.GeoStatus())
}

// updateGeo re-downloads the geo databases to the latest version and reloads Xray.
func (rt *Router) updateGeo(w http.ResponseWriter, _ *http.Request) {
	info, err := rt.mgr.RefreshGeo()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// getRouting returns the structured routing config plus WARP availability so the
// panel knows whether to offer the "через WARP" category.
func (rt *Router) getRouting(w http.ResponseWriter, _ *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":          set.Routing,
		"warp_enabled":    set.WarpEnabled,
		"warp_registered": set.WarpRegistered(),
		"opera_enabled":   set.OperaEnabled,
		"opera_country":   set.OperaCountryOr(),
		"opera_running":   rt.mgr.OperaRunning(),
		"opera_alive":     rt.mgr.OperaHealthy(),
		"proxy_count":     rt.mgr.ProxyCount(),
	})
}

// saveRouting persists the routing rules and the WARP on/off state in one request
// (registering WARP on first enable), then reconciles once.
func (rt *Router) saveRouting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		model.RoutingConfig
		WarpEnabled  bool   `json:"warp_enabled"`
		OperaEnabled bool   `json:"opera_enabled"`
		OperaCountry string `json:"opera_country"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.ApplyRouting(req.RoutingConfig, req.WarpEnabled, req.OperaEnabled, req.OperaCountry); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setXrayDNS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DNS string `json:"dns"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	for _, e := range strings.FieldsFunc(req.DNS, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' '
	}) {
		if !validDNSServer(e) {
			writeErr(w, http.StatusBadRequest, "неверный DNS-адрес: "+e)
			return
		}
	}
	if err := rt.mgr.SetXrayDNS(req.DNS); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) saveSubSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path           string `json:"sub_path"`
		Base64         bool   `json:"sub_base64"`
		NameInTitle    bool   `json:"sub_name_in_title"`
		Title          string `json:"sub_title"`
		Routing        bool   `json:"sub_routing"`
		RoutingHapp    string `json:"sub_routing_happ"`
		RoutingIncy    string `json:"sub_routing_incy"`
		RoutingMihomo  string `json:"sub_routing_mihomo"`
		UpdateInterval int    `json:"sub_update_interval"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UpdateInterval < 0 {
		req.UpdateInterval = 0 // 0 = never
	}
	path := strings.TrimSpace(req.Path)
	err := rt.mgr.SaveSubSettings(&model.Settings{
		SubPath:           path,
		SubBase64:         req.Base64,
		SubNameInTitle:    req.NameInTitle,
		SubTitle:          strings.TrimSpace(req.Title),
		SubRouting:        req.Routing,
		SubRoutingHapp:    strings.TrimSpace(req.RoutingHapp),
		SubRoutingIncy:    strings.TrimSpace(req.RoutingIncy),
		SubRoutingMihomo:  strings.TrimSpace(req.RoutingMihomo),
		SubUpdateInterval: req.UpdateInterval,
	})
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setSubPath(path) // swap the live /<path>/ route immediately
	writeOK(w)
}

func (rt *Router) regenSecret(w http.ResponseWriter, r *http.Request) {
	p, err := rt.mgr.RegenerateSecretPath()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setSecret(p) // swap the live route immediately
	// Keep the admin logged in across the path change: the current session cookie
	// was scoped to the old secret path and the browser won't send it to /<new>/.
	// Re-issue the same session token scoped to the new path.
	if c, err := r.Cookie(sessionCookie); err == nil {
		rt.setSessionCookie(w, r, c.Value, "/"+p+"/")
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret_path": p})
}

func (rt *Router) setDecoyTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template string `json:"template"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	h, err := decoy.New(req.Template) // validates the slug exists
	if err != nil {
		writeErr(w, http.StatusBadRequest, "неизвестный шаблон")
		return
	}
	if err := rt.mgr.SetDecoyTemplate(req.Template); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setDecoy(h) // swap the live decoy immediately
	writeOK(w)
}

func (rt *Router) updateCredentials(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.adminID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "не авторизован")
		return
	}
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Preserve the caller's own session across the change; every other session for
	// this admin is revoked inside UpdateAdminCredentials.
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = c.Value
	}
	if err := rt.mgr.UpdateAdminCredentials(id, req.CurrentPassword, req.Username, req.Password, keep); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setupFinish(w http.ResponseWriter, _ *http.Request) {
	if err := rt.mgr.FinishSetup(); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
