package server

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/decoy"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// The configuration half of the /v1 surface: the settings singleton, per-server
// routing/DNS, config snapshots, the geo databases and the two stats breakdowns the
// panel drew but never published.
//
// Everything here mirrors a panel screen an operator already has, and nothing here is
// new behaviour — each handler calls the same manager method the panel does. The point
// is that an integration can now BUILD a server, not just operate the users on one.
//
// Two things are deliberately NOT here, and should stay out:
//
//   - the admin roster and API keys. A key that can mint keys and admins turns one
//     leaked credential into permanent, self-renewing access; the panel (with its
//     session, its step-up password and its TOTP) is the right place for that.
//   - the panel's own secret path. It is withheld from every /v1 response on purpose
//     (see TestAPIBackupInfoHidesTheSecretPath) — it is the obscurity layer in front of
//     the panel, not a setting to hand out.

// apiSettingsView is the readable settings singleton. It carries the operational knobs
// and deliberately no credentials: bot tokens, provider configs and the REALITY/WARP
// private keys live behind their own panel screens and are encrypted at rest.
type apiSettingsView struct {
	XrayDNS            string   `json:"xray_dns"`
	DecoyTemplate      string   `json:"decoy_template"`
	DecoyTemplates     []string `json:"decoy_templates"`
	MaintenanceMode    bool     `json:"maintenance_mode"`
	ProbeDetect        bool     `json:"probe_detect"`
	ProbeBlock         bool     `json:"probe_block"`
	WatchdogEnabled    bool     `json:"watchdog_enabled"`
	WatchdogRestarts   int      `json:"watchdog_restarts"`
	UserAutoDeleteDays int      `json:"user_autodelete_days"`
	HWIDEnabled        bool     `json:"hwid_enabled"`
	HWIDRequire        bool     `json:"hwid_require"`
	HWIDFallbackLimit  int      `json:"hwid_fallback_limit"`
	HWIDTTLDays        int      `json:"hwid_ttl_days"`
	DeviceCountMode    string   `json:"device_count_mode"`
	LocalBackupCron    string   `json:"local_backup_cron"`
	LocalBackupKeep    int      `json:"local_backup_keep"`
	SubPath            string   `json:"sub_path"`
	WarpEnabled        bool     `json:"warp_enabled"`
	WarpRegistered     bool     `json:"warp_registered"`
}

// apiSettingsReq is a PARTIAL update: every field is a pointer, and only the ones
// present in the body are applied. A caller that reads the settings, changes one value
// and posts the whole object back would otherwise re-apply everything — including
// values another operator changed in between.
type apiSettingsReq struct {
	XrayDNS            *string `json:"xray_dns"`
	DecoyTemplate      *string `json:"decoy_template"`
	MaintenanceMode    *bool   `json:"maintenance_mode"`
	ProbeDetect        *bool   `json:"probe_detect"`
	ProbeBlock         *bool   `json:"probe_block"`
	WatchdogEnabled    *bool   `json:"watchdog_enabled"`
	UserAutoDeleteDays *int    `json:"user_autodelete_days"`
	HWIDEnabled        *bool   `json:"hwid_enabled"`
	HWIDRequire        *bool   `json:"hwid_require"`
	HWIDFallbackLimit  *int    `json:"hwid_fallback_limit"`
	HWIDTTLDays        *int    `json:"hwid_ttl_days"`
	DeviceCountMode    *string `json:"device_count_mode"`
	LocalBackupCron    *string `json:"local_backup_cron"`
	LocalBackupKeep    *int    `json:"local_backup_keep"`
}

func (rt *Router) apiSettingsPayload() (*apiSettingsView, error) {
	set, err := rt.mgr.Settings()
	if err != nil {
		return nil, err
	}
	templates, _ := decoy.Available()
	wd := rt.mgr.Watchdog()
	return &apiSettingsView{
		XrayDNS:            set.XrayDNS,
		DecoyTemplate:      set.DecoyTemplate,
		DecoyTemplates:     templates,
		MaintenanceMode:    set.MaintenanceMode,
		ProbeDetect:        set.ProbeDetect,
		ProbeBlock:         set.ProbeBlock,
		WatchdogEnabled:    wd.Enabled,
		WatchdogRestarts:   wd.Restarts,
		UserAutoDeleteDays: set.UserAutoDeleteDays,
		HWIDEnabled:        set.HWIDEnabled,
		HWIDRequire:        set.HWIDRequire,
		HWIDFallbackLimit:  set.HWIDFallbackLimit,
		DeviceCountMode:    set.DeviceCountModeOr(),
		HWIDTTLDays:        set.HWIDTTLDays,
		LocalBackupCron:    set.LocalBackupCron,
		LocalBackupKeep:    set.LocalBackupKeep,
		SubPath:            set.SubPathOr(),
		WarpEnabled:        set.WarpEnabled,
		WarpRegistered:     set.WarpRegistered(),
	}, nil
}

func (rt *Router) apiGetSettings(w http.ResponseWriter, _ *http.Request) {
	view, err := rt.apiSettingsPayload()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, view)
}

// apiPatchSettings applies the fields the body carries and answers with the settings as
// they now stand, so a caller learns what a value was normalised to.
func (rt *Router) apiPatchSettings(w http.ResponseWriter, r *http.Request) {
	var req apiSettingsReq
	if !apiDecode(w, r, &req) {
		return
	}
	if req.HWIDFallbackLimit != nil && *req.HWIDFallbackLimit < 0 ||
		req.HWIDTTLDays != nil && *req.HWIDTTLDays < 0 ||
		req.UserAutoDeleteDays != nil && *req.UserAutoDeleteDays < 0 ||
		req.LocalBackupKeep != nil && *req.LocalBackupKeep < 0 {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "values cannot be negative")
		return
	}
	// The decoy is validated BEFORE anything is written, the way the panel does it: the
	// template name is a slug that has to exist, and storing one that doesn't would
	// leave the panel unable to build its masquerade on the next boot.
	var newDecoy http.Handler
	if req.DecoyTemplate != nil {
		h, err := decoy.New(*req.DecoyTemplate, decoy.LoadStamp(rt.dataDir))
		if err != nil {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "unknown decoy template")
			return
		}
		newDecoy = h
	}
	// Each apply is the manager method the panel screen calls, so validation, audit
	// rows and the Xray reconcile that some of them trigger all behave identically.
	//
	// Three of these ALSO have a live side the manager does not own — the decoy
	// handler, the probe-detection flag and the maintenance switch are fields on this
	// Router, read per request. Writing only the database would store the new value and
	// keep serving the old behaviour until the next restart, which is the kind of
	// "it didn't work" an operator cannot debug.
	apply := []struct {
		set bool
		fn  func() error
	}{
		{req.XrayDNS != nil, func() error { return rt.mgr.SetXrayDNS(*req.XrayDNS) }},
		{req.DecoyTemplate != nil, func() error {
			if err := rt.mgr.SetDecoyTemplate(*req.DecoyTemplate); err != nil {
				return err
			}
			rt.setDecoy(newDecoy)
			return nil
		}},
		{req.MaintenanceMode != nil, func() error {
			if err := rt.mgr.SetMaintenanceMode(*req.MaintenanceMode); err != nil {
				return err
			}
			rt.setMaintenance(*req.MaintenanceMode)
			return nil
		}},
		{req.ProbeDetect != nil, func() error {
			if err := rt.mgr.SetProbeDetect(*req.ProbeDetect); err != nil {
				return err
			}
			rt.setProbeDetect(*req.ProbeDetect)
			return nil
		}},
		{req.ProbeBlock != nil, func() error { return rt.mgr.SetProbeBlock(*req.ProbeBlock) }},
		{req.WatchdogEnabled != nil, func() error { return rt.mgr.SetWatchdog(*req.WatchdogEnabled) }},
		{req.UserAutoDeleteDays != nil, func() error { return rt.mgr.SetUserAutoDelete(*req.UserAutoDeleteDays) }},
	}
	for _, a := range apply {
		if !a.set {
			continue
		}
		if err := a.fn(); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.DeviceCountMode != nil {
		if err := rt.mgr.SetDeviceCountMode(*req.DeviceCountMode); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if err := rt.apiApplyHWID(req); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if err := rt.apiApplyLocalBackup(req); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	view, err := rt.apiSettingsPayload()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, view)
}

// apiApplyHWID writes the device-binding group. The four fields are stored together, so
// a partial update has to read the current row and overlay only what was sent —
// otherwise setting hwid_enabled alone would silently reset the limit and the TTL.
func (rt *Router) apiApplyHWID(req apiSettingsReq) error {
	if req.HWIDEnabled == nil && req.HWIDRequire == nil &&
		req.HWIDFallbackLimit == nil && req.HWIDTTLDays == nil {
		return nil
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		return err
	}
	if req.HWIDEnabled != nil {
		set.HWIDEnabled = *req.HWIDEnabled
	}
	if req.HWIDRequire != nil {
		set.HWIDRequire = *req.HWIDRequire
	}
	if req.HWIDFallbackLimit != nil {
		set.HWIDFallbackLimit = *req.HWIDFallbackLimit
	}
	if req.HWIDTTLDays != nil {
		set.HWIDTTLDays = *req.HWIDTTLDays
	}
	return rt.mgr.Store().SetHWIDSettings(set)
}

// apiApplyLocalBackup writes the scheduled-backup pair, same overlay reasoning as HWID:
// the cron and the retention count are saved by one call.
func (rt *Router) apiApplyLocalBackup(req apiSettingsReq) error {
	if req.LocalBackupCron == nil && req.LocalBackupKeep == nil {
		return nil
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		return err
	}
	cron, keep := set.LocalBackupCron, set.LocalBackupKeep
	if req.LocalBackupCron != nil {
		cron = *req.LocalBackupCron
	}
	if req.LocalBackupKeep != nil {
		keep = *req.LocalBackupKeep
	}
	return rt.mgr.SaveLocalBackup(cron, keep)
}

// apiServerRouting is one server's routing config plus the egress backends that sit
// beside it. Server 0 is the master, matching /v1/servers/{id}/inbounds.
type apiServerRouting struct {
	Routing      model.RoutingConfig `json:"routing"`
	XrayDNS      string              `json:"xray_dns"`
	WarpEnabled  bool                `json:"warp_enabled"`
	OperaEnabled bool                `json:"opera_enabled"`
	OperaCountry string              `json:"opera_country"`
}

// apiServerRoutingReq is the write shape. Every field is optional and an absent one is
// LEFT ALONE — only `routing` itself replaces wholesale, because merging rule lists has
// no meaning a caller could predict.
//
// The scalars are pointers for a reason that is not cosmetic. With plain values, a body
// carrying only `routing` also sent warp_enabled=false, and turning WARP off makes the
// generator alias the warp outbound to `direct` — so traffic the operator had
// deliberately routed through the tunnel started leaving from the server's own IP, on a
// 200 OK, with an audit row that says nothing more than "server routing".
type apiServerRoutingReq struct {
	Routing      *model.RoutingConfig `json:"routing"`
	XrayDNS      *string              `json:"xray_dns"`
	WarpEnabled  *bool                `json:"warp_enabled"`
	OperaEnabled *bool                `json:"opera_enabled"`
	OperaCountry *string              `json:"opera_country"`
}

// serverRouting reads one server's routing view. Shared by the GET and the PATCH-style
// write, so "what a caller reads" and "what an omitted field keeps" are the same value
// by construction rather than by two functions agreeing.
func (rt *Router) serverRouting(id int64) (apiServerRouting, error) {
	views, err := rt.mgr.NodeViews()
	if err != nil {
		// Deliberately NOT folded into "no such server": this runs again after a
		// successful write to build the response, and answering 404 there tells the
		// caller their change was rejected when it was applied.
		return apiServerRouting{}, err
	}
	for _, v := range views {
		if v.ID != id {
			continue
		}
		out := apiServerRouting{
			WarpEnabled:  v.WarpEnabled,
			OperaEnabled: v.OperaEnabled,
			// The master's view already carries the effective country; a node's carries
			// the raw column, which is empty until someone picks one. Reporting the
			// effective value for both means one documented shape has one meaning.
			OperaCountry: model.OperaCountryOr(v.OperaCountry),
		}
		if v.Routing != nil {
			out.Routing = *v.Routing
		}
		if v.XrayDNS != nil {
			out.XrayDNS = *v.XrayDNS
		}
		return out, nil
	}
	return apiServerRouting{}, errNoSuchServer
}

// errNoSuchServer separates "this id does not exist" from "the lookup failed".
var errNoSuchServer = errors.New("no such server")

func (rt *Router) apiGetServerRouting(w http.ResponseWriter, _ *http.Request, id int64) {
	out, err := rt.serverRouting(id)
	if errors.Is(err, errNoSuchServer) {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such server")
		return
	}
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, out)
}

func (rt *Router) apiSetServerRouting(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiServerRoutingReq
	if !apiDecode(w, r, &req) {
		return
	}
	// Read the server as it stands, so an absent field keeps the value it already had.
	cur, err := rt.serverRouting(id)
	if errors.Is(err, errNoSuchServer) {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such server")
		return
	}
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	routing := cur.Routing
	if req.Routing != nil {
		routing = *req.Routing
	}
	dns := cur.XrayDNS
	if req.XrayDNS != nil {
		dns = *req.XrayDNS
	}
	warp, opera, country := cur.WarpEnabled, cur.OperaEnabled, cur.OperaCountry
	if req.WarpEnabled != nil {
		warp = *req.WarpEnabled
	}
	if req.OperaEnabled != nil {
		opera = *req.OperaEnabled
	}
	if req.OperaCountry != nil {
		country = *req.OperaCountry
	}

	if id == model.LocalNodeID {
		// The master keeps its routing in the settings singleton and its DNS is the
		// global one — the same two calls the panel's routing screen makes. DNS is
		// applied FIRST so a rejected value (see Manager.SetXrayDNS) fails before any
		// routing is committed, rather than leaving half the request applied.
		// Only when the body carried it: re-validating the stored value on every call
		// would let one bad value (restored from an old snapshot, say) lock every later
		// routing edit out of this endpoint.
		if req.XrayDNS != nil {
			if err := rt.mgr.SetXrayDNS(dns); err != nil {
				writeAPIManagerErr(w, err)
				return
			}
		}
		if err := rt.mgr.ApplyRouting(routing, warp, opera, country); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		rt.apiGetServerRouting(w, r, id)
		return
	}

	node, err := rt.mgr.GetNode(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if node == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such server")
		return
	}
	// Everything not on this screen is carried over from the stored node, so a routing
	// change cannot silently reset the name, host, protocols or traffic coefficient.
	//
	// DNS rides the SAME edit rather than a second SetNodeDNS call: two writes meant the
	// node could be woken by the first one and pull the new routing with the old DNS,
	// and a failure in between left the routing live with nothing in the audit trail.
	edit := storeNodeEditFrom(node)
	edit.Routing = &routing
	edit.XrayDNS = &dns
	edit.WarpEnabled = warp
	edit.OperaEnabled = opera
	edit.OperaCountry = country
	if err := rt.mgr.UpdateNode(id, edit); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiGetServerRouting(w, r, id)
}

// apiConfigSnapshots lists the master's config save-points.
func (rt *Router) apiConfigSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := rt.mgr.ConfigSnapshots()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIPage(w, r, snaps)
}

// apiSnapshotReq is the body of a create-snapshot call: a label the operator will
// recognise later, or nothing (the snapshot is then listed as an unlabelled manual one).
// Declared here rather than in the spec file so the documented shape IS the decoded one.
type apiSnapshotReq struct {
	Label string `json:"label,omitempty"`
}

func (rt *Router) apiCreateConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	var req apiSnapshotReq
	if !apiDecode(w, r, &req) {
		return
	}
	id, err := rt.mgr.SnapshotServerConfig(req.Label)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	// Answer with the save-point this call made, found by id. Returning "the newest one"
	// instead would hand the caller someone else's snapshot whenever a concurrent create
	// — or the auto-snapshot a rollback takes — landed in between, and the id is what a
	// later rollback or delete is aimed at.
	snaps, err := rt.mgr.ConfigSnapshots()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	for _, sn := range snaps {
		if sn.ID == id {
			writeAPIData(w, http.StatusCreated, sn)
			return
		}
	}
	writeAPIErr(w, http.StatusInternalServerError, "internal", "snapshot was not stored")
}

// apiRollbackConfigSnapshot restores the whole server config from a snapshot. Xray is
// regenerated and restarted fleet-wide, because nodes inherit the master's fields.
func (rt *Router) apiRollbackConfigSnapshot(w http.ResponseWriter, _ *http.Request, id int64) {
	defer rt.syncDecoyFromSettings() // a snapshot can restore a different masquerade
	if err := rt.mgr.RollbackServerConfig(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) apiDeleteConfigSnapshot(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteConfigSnapshot(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	// 200 with the envelope, like every other /v1 delete — a caller that unwraps `data`
	// must not have to special-case this one route.
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// geoStatsTTL is how long a connection breakdown is reused. Both aggregate the WHOLE
// connections table over its retention window and then do a per-row geo lookup, against
// the single SQLite connection every other request queues behind — and the API limiter
// allows 600 calls a minute. The public status page memoizes for exactly this reason;
// these are the same shape of work behind a key instead of behind nothing.
const geoStatsTTL = 30 * time.Second

type geoStatsCache[T any] struct {
	mu   sync.Mutex
	rows []T
	at   time.Time
}

// get returns the cached rows, refreshing them when the window has passed.
func (c *geoStatsCache[T]) get(load func() ([]T, error)) ([]T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows != nil && time.Since(c.at) < geoStatsTTL {
		return c.rows, nil
	}
	rows, err := load()
	if err != nil {
		return nil, err
	}
	c.rows, c.at = rows, time.Now()
	return rows, nil
}

// apiStatsCountries is the connection breakdown by country the map draws.
func (rt *Router) apiStatsCountries(w http.ResponseWriter, r *http.Request) {
	rows, err := rt.countryStats.get(rt.mgr.ConnectionCountries)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIPage(w, r, rows)
}

// apiStatsASNs is the same breakdown by network operator.
func (rt *Router) apiStatsASNs(w http.ResponseWriter, r *http.Request) {
	rows, err := rt.asnStats.get(rt.mgr.ConnectionASNs)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIPage(w, r, rows)
}

// apiXrayRestart asks a server to restart its Xray. Every live connection on it drops,
// which is why it is its own explicit call rather than a side effect of a config write.
// For a node the request rides the next sync, so a 200 means "queued", not "done".
func (rt *Router) apiXrayRestart(w http.ResponseWriter, _ *http.Request, id int64) {
	// The master runs its own Xray, so it restarts it directly; a node is asked over the
	// sync channel and obeys on its next poll. Same distinction the panel makes, and the
	// reason "node not found" is not the answer for server 0.
	var err error
	if id == model.LocalNodeID {
		err = rt.mgr.RestartXray()
	} else {
		err = rt.mgr.RequestNodeXrayRestart(id)
	}
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// storeNodeEditFrom snapshots a node into the edit struct UpdateNode takes, so a handler
// that changes ONE screen's worth of fields carries the rest over unchanged. Without it
// every partial edit has to remember all eleven fields, and the one it forgets is
// silently reset to a zero value.
func storeNodeEditFrom(n *model.Node) store.NodeEdit {
	return store.NodeEdit{
		Name:               n.Name,
		Host:               n.Host,
		DecoyTemplate:      n.DecoyTemplate,
		VLESS:              n.VLESSEnabled,
		Hysteria:           n.HysteriaEnabled,
		Reality:            n.RealityEnabled,
		Routing:            n.Routing,
		XrayDNS:            n.XrayDNS,
		WarpEnabled:        n.WarpEnabled,
		OperaEnabled:       n.OperaEnabled,
		OperaCountry:       n.OperaCountry,
		TrafficCoefficient: n.TrafficCoefficient,
	}
}

// syncDecoyFromSettings rebuilds the live decoy handler from the stored template.
//
// The masquerade is a handler held by the router, not a value the manager can reach, so
// any path that changes decoy_template WITHOUT going through the settings write — a
// config-snapshot rollback restores it along with everything else — has to re-swap it
// here, or the panel keeps serving the previous site until it restarts.
func (rt *Router) syncDecoyFromSettings() {
	set, err := rt.mgr.Settings()
	if err != nil {
		return
	}
	h, err := decoy.New(set.DecoyTemplate, decoy.LoadStamp(rt.dataDir))
	if err != nil {
		return // an unknown template: keep serving the one that works
	}
	rt.setDecoy(h)
}
