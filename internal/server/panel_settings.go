package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/abuse"
	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/model"
)

func (rt *Router) setupPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.adminID(r)
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
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
		"secret_path":          set.PanelSecretPath,
		"decoy_template":       set.DecoyTemplate,
		"decoy_templates":      templates,
		"sub_path":             set.SubPathOr(),
		"sub_base64":           set.SubBase64,
		"sub_name_in_title":    set.SubNameInTitle,
		"sub_title":            set.SubTitle,
		"sub_routing":          set.SubRouting,
		"sub_routing_happ":     set.SubRoutingHapp,
		"sub_routing_incy":     set.SubRoutingIncy,
		"sub_routing_mihomo":   set.SubRoutingMihomo,
		"sub_update_interval":  set.SubUpdateInterval,
		"sub_announce":         set.SubAnnounce,
		"sub_show_configs":     set.SubShowConfigs,
		"sub_dpi":              set.SubDPI,
		"sub_order_mode":       set.SubOrderMode,
		"sub_hide_offline":     set.SubHideOffline,
		"maintenance_mode":     set.MaintenanceMode,
		"probe_detect":         set.ProbeDetect,
		"probe_block":          set.ProbeBlock,
		"watchdog":             rt.mgr.Watchdog(),
		"hwid":                 hwidSettingsView(set),
		"user_autodelete_days": set.UserAutoDeleteDays,
		"xray_dns":             set.XrayDNS,
		"warp_enabled":         set.WarpEnabled,
		"warp_registered":      set.WarpRegistered(),
		"local_backup_cron":    set.LocalBackupCron,
		"local_backup_keep":    set.LocalBackupKeep,
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

// setServerProxy configures one server's system proxy listeners — {id} 0 is the
// panel's own machine, anything else a node.
func (rt *Router) setServerProxy(w http.ResponseWriter, r *http.Request, id int64) {
	var req model.SystemProxy
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetSystemProxy(id, req); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// geoCategories returns the geosite + geoip category codes and the iplist group
// names for routing presets.
func (rt *Router) geoCategories(w http.ResponseWriter, _ *http.Request) {
	geosite, geoip, err := rt.mgr.GeoCategories()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// The iplist groups are an independent, optional source — if those databases
	// are missing the geosite/geoip presets must still load, just without groups.
	var iplist []string
	if g, err := rt.mgr.GeoGroups(); err == nil {
		iplist = g.GroupNames()
	}
	writeJSON(w, http.StatusOK, map[string]any{"geosite": geosite, "geoip": geoip, "iplist": iplist})
}

// geoStatus reports both database sets' presence + last-download time and each
// set's own auto-refresh cadence. They are reported separately because they are
// separate concerns with separate tabs and separate schedules.
func (rt *Router) geoStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"files":                rt.mgr.GeoStatus(),
		"iplist_files":         rt.mgr.IPListStatus(),
		"refresh_hours":        rt.mgr.GeoRefreshHours(),
		"iplist_refresh_hours": rt.mgr.IPListRefreshHours(),
	})
}

// updateIPLists re-downloads the iplist databases and reloads Xray, so changed
// groups take effect without waiting for the auto-refresh tick.
func (rt *Router) updateIPLists(w http.ResponseWriter, _ *http.Request) {
	info, err := rt.mgr.RefreshIPLists()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"iplist_files":         info,
		"iplist_refresh_hours": rt.mgr.IPListRefreshHours(),
	})
}

// updateGeo re-downloads the geo databases to the latest version and reloads Xray.
func (rt *Router) updateGeo(w http.ResponseWriter, _ *http.Request) {
	info, err := rt.mgr.RefreshGeo()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":         info,
		"refresh_hours": rt.mgr.GeoRefreshHours(),
	})
}

// setGeoCadence persists how often the geo databases auto-refresh (hours; 0 = never).
func (rt *Router) setGeoCadence(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshHours int `json:"refresh_hours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetGeoRefresh(req.RefreshHours); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setIPListCadence persists the iplist auto-refresh cadence — its own schedule,
// independent of the geo one.
func (rt *Router) setIPListCadence(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshHours int `json:"refresh_hours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetIPListRefresh(req.RefreshHours); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// getRouting returns the structured routing config plus WARP availability so the
// panel knows whether to offer the "via WARP" category.
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
		// Where anything ON this box can dial to leave through each lane, empty when
		// that lane is off. Published so an operator can point the Telegram proxy — or
		// anything else — at an egress the panel already runs, without having to know
		// its loopback port.
		"warp_proxy_url":  set.WarpProxyURL(),
		"opera_proxy_url": set.OperaProxyURL(),
		"proxy_count":     rt.mgr.ProxyCount(),  // total across all lanes
		"proxy_counts":    rt.mgr.ProxyCounts(), // per lane ID
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
	// Validation lives in the manager now, so this surface and /v1 refuse the same
	// values; the error carries the offending entry back as a translated key.
	if err := rt.mgr.SetXrayDNS(req.DNS); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// saveMaintenance toggles the public-surface maintenance page and applies it live.
func (rt *Router) saveMaintenance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetMaintenanceMode(req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setMaintenance(req.Enabled) // swap the live routing immediately
	writeOK(w)
}

// saveProbeDetect toggles secret-path probe detection.
func (rt *Router) saveProbeDetect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetProbeDetect(req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setProbeDetect(req.Enabled) // swap the live flag immediately
	writeOK(w)
}

// saveWatchdog toggles the wedged-process auto-recovery.
func (rt *Router) saveWatchdog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetWatchdog(req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// saveProbeBlock toggles firewall auto-blocking of flagged scanner IPs.
func (rt *Router) saveProbeBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetProbeBlock(req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// listProbes returns the IPs caught scanning for the hidden panel path.
func (rt *Router) listProbes(w http.ResponseWriter, _ *http.Request) {
	probes, err := rt.mgr.Probes(200)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if probes == nil {
		probes = []model.ProbeHit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"probes": probes,
		// How far back the list can reach: a row survives this long after the address
		// was last seen. The panel says so next to the list rather than hardcoding a
		// number that would drift from model.ProbeRetentionDays.
		"retention_days": model.ProbeRetentionDays,
	})
}

// getSubRules returns the subscription response rules for the editor.
func (rt *Router) getSubRules(w http.ResponseWriter, _ *http.Request) {
	rules, err := rt.mgr.SubRules()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if rules == nil {
		rules = []model.SubRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// saveSubRules replaces the whole rule list (the editor sends the full set).
func (rt *Router) saveSubRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []model.SubRule `json:"rules"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SaveSubRules(req.Rules); err != nil {
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
		Announce       string `json:"sub_announce"`
		ShowConfigs    bool   `json:"sub_show_configs"`
		OrderMode      string `json:"sub_order_mode"`
		HideOffline    bool   `json:"sub_hide_offline"`
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
		SubAnnounce:       req.Announce,
		SubShowConfigs:    req.ShowConfigs,
		SubOrderMode:      strings.TrimSpace(req.OrderMode),
		SubHideOffline:    req.HideOffline,
	})
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setSubPath(path) // swap the live /<path>/ route immediately
	writeOK(w)
}

// setUserAutoDelete configures the grace period after which an expired user is
// deleted (0 = never).
func (rt *Router) setUserAutoDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Days int `json:"user_autodelete_days"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserAutoDelete(req.Days); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"days": req.Days})
	writeOK(w)
}

// getAbuseSettings returns the blocklist config plus each category's live status
// (loaded entry count, whether a cached feed is present, when it was updated).
func (rt *Router) getAbuseSettings(w http.ResponseWriter, _ *http.Request) {
	enabled, cats, custom, alertMin := rt.mgr.AbuseConfig()
	if cats == nil {
		cats = map[string]bool{}
	}
	status := rt.mgr.AbuseStatus()
	if status == nil {
		status = []abuse.FileInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    enabled,
		"categories": cats,
		"custom":     custom,
		"alert_min":  alertMin,
		"measures":   rt.mgr.AbuseMeasures(),
		"status":     status,
	})
}

// saveAbuseSettings persists the blocklist config and reconfigures the live matcher.
func (rt *Router) saveAbuseSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled    bool                 `json:"enabled"`
		Categories map[string]bool      `json:"categories"`
		Custom     string               `json:"custom"`
		AlertMin   int                  `json:"alert_min"`
		Measures   *model.AbuseMeasures `json:"measures"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// The ladder is validated first: a bad rung must not half-save the rest.
	if req.Measures != nil {
		if err := req.Measures.Validate(); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	if err := rt.mgr.SetAbuseConfig(req.Enabled, req.Categories, req.Custom, req.AlertMin); err != nil {
		writeManagerErr(w, err)
		return
	}
	if req.Measures != nil {
		if err := rt.mgr.SetAbuseMeasures(*req.Measures); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	auditDetails(r, map[string]any{"enabled": req.Enabled, "alert_min": req.AlertMin, "measures": req.Measures})
	writeOK(w)
}

// refreshAbuse forces an immediate re-download of the enabled feeds.
func (rt *Router) refreshAbuse(w http.ResponseWriter, _ *http.Request) {
	rt.mgr.RefreshAbuse()
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
	h, err := decoy.New(req.Template, decoy.LoadStamp(rt.dataDir)) // validates the slug exists
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.unknownTemplate", "неизвестный шаблон")
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
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
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
