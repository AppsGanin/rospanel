package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The external-API node surface mirrors the panel's Nodes page: same core.Manager
// methods, same envelope. Create/regen return the one-time install command exactly
// like the panel, so an integration can provision nodes end to end.

type (
	apiCreateNodeReq struct {
		Name string `json:"name"`
		Host string `json:"host"`
	}
	// apiPatchNodeReq mirrors the panel edit. Pointer fields distinguish "inherit
	// global" (nil) from an explicit value.
	apiPatchNodeReq struct {
		Name               *string              `json:"name,omitempty"`
		Host               *string              `json:"host,omitempty"`
		DecoyTemplate      *string              `json:"decoy_template,omitempty"`
		VLESS              *bool                `json:"vless_enabled,omitempty"`
		Hysteria           *bool                `json:"hysteria_enabled,omitempty"`
		Reality            *bool                `json:"reality_enabled,omitempty"`
		Routing            *model.RoutingConfig `json:"routing,omitempty"`
		XrayDNS            *string              `json:"xray_dns,omitempty"`
		WarpEnabled        *bool                `json:"warp_enabled,omitempty"`
		OperaEnabled       *bool                `json:"opera_enabled,omitempty"`
		OperaCountry       *string              `json:"opera_country,omitempty"`
		TrafficCoefficient *float64             `json:"traffic_coefficient,omitempty"`
		// Placement in subscriptions (see model.Placement): country (ISO-2), a manual
		// weight, capacity in users and whether to hide the node when full.
		Country      *string `json:"country,omitempty"`
		SortWeight   *int    `json:"sort_weight,omitempty"`
		Capacity     *int    `json:"capacity,omitempty"`
		HideWhenFull *bool   `json:"hide_when_full,omitempty"`
		// Traffic cap: bytes per period, the period, and whether reaching it drops the
		// server out of subscriptions. 0 clears the cap (and with it the other two).
		TrafficLimit  *int64  `json:"traffic_limit,omitempty"`
		TrafficPeriod *string `json:"traffic_period,omitempty"`
		HideWhenOver  *bool   `json:"hide_when_over,omitempty"`
		// The day of the month a monthly cap starts over (1–31; a shorter month uses
		// its last day).
		TrafficResetDay *int `json:"traffic_reset_day,omitempty"`
	}
	// apiPlacementReq is a partial edit of one server's placement: where it sits in
	// subscriptions and its traffic cap. A nil field keeps the current value.
	apiPlacementReq struct {
		Country         *string `json:"country,omitempty"`           // ISO-2, "" = detect from the host
		SortWeight      *int    `json:"sort_weight,omitempty"`       // higher sorts first, -1000..1000
		Capacity        *int    `json:"capacity,omitempty"`          // users the server is meant to carry, 0 = none stated
		HideWhenFull    *bool   `json:"hide_when_full,omitempty"`    // drop out of subscriptions while at capacity
		TrafficLimit    *int64  `json:"traffic_limit,omitempty"`     // bytes per period, 0 = no cap
		TrafficPeriod   *string `json:"traffic_period,omitempty"`    // month | day
		TrafficResetDay *int    `json:"traffic_reset_day,omitempty"` // 1-31: the day a monthly cap starts over
		HideWhenOver    *bool   `json:"hide_when_over,omitempty"`    // drop out of subscriptions once the cap is reached
	}
	apiSetNodeEnabledReq struct {
		Enabled bool `json:"enabled"`
	}
)

// apiSetServerProxy configures one server's system proxy — {id} 0 is the panel's own
// machine, anything else a node. Its own endpoint rather than a field on PATCH
// /v1/nodes/{id} for one reason: the master has no row in `nodes` to patch, and this
// listener is exactly as much the master's as a node's.
func (rt *Router) apiSetServerProxy(w http.ResponseWriter, r *http.Request, id int64) {
	var req model.SystemProxy
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetSystemProxy(id, req); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	// Answer with the stored state (ports defaulted, password as it now stands), so a
	// caller that sent {"socks_enabled":true} learns which port it got.
	views, err := rt.mgr.NodeViews()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	for _, v := range views {
		if v.ID == id {
			writeAPIData(w, http.StatusOK, v.Proxy)
			return
		}
	}
	writeAPIErr(w, http.StatusNotFound, "not_found", "no such server")
}

// applyTo overlays the fields the request carries onto a placement.
func (r apiPlacementReq) applyTo(p *model.Placement) {
	if r.Country != nil {
		p.Country = *r.Country
	}
	if r.SortWeight != nil {
		p.Weight = *r.SortWeight
	}
	if r.Capacity != nil {
		p.Capacity = *r.Capacity
	}
	if r.HideWhenFull != nil {
		p.HideWhenFull = *r.HideWhenFull
	}
	if r.TrafficLimit != nil {
		p.TrafficLimit = *r.TrafficLimit
	}
	if r.TrafficPeriod != nil {
		p.TrafficPeriod = *r.TrafficPeriod
	}
	if r.TrafficResetDay != nil {
		p.TrafficResetDay = *r.TrafficResetDay
	}
	if r.HideWhenOver != nil {
		p.HideWhenOver = *r.HideWhenOver
	}
}

// apiSetServerPlacement edits one server's placement — {id} 0 is the panel's own
// machine, which PATCH /v1/nodes/{id} cannot reach: the master has no node row, and
// its placement lives in settings. Answers with the placement as stored, so a caller
// learns what a value was normalised to (a country upper-cased, a reset day of 1
// stored as the default, a cleared cap taking its period with it).
func (rt *Router) apiSetServerPlacement(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiPlacementReq
	if !apiDecode(w, r, &req) {
		return
	}
	if id == model.LocalNodeID {
		set, err := rt.mgr.Settings()
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		p := set.MasterPlacement
		req.applyTo(&p)
		if err := rt.mgr.SetMasterPlacement(p); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	} else {
		node, err := rt.mgr.GetNode(id)
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		if node == nil {
			writeAPIErr(w, http.StatusNotFound, "not_found", "no such server")
			return
		}
		edit := store.NodeEdit{
			Name: node.Name, Host: node.Host, DecoyTemplate: node.DecoyTemplate,
			VLESS: node.VLESSEnabled, Hysteria: node.HysteriaEnabled, Reality: node.RealityEnabled,
			Routing: node.Routing, XrayDNS: node.XrayDNS,
			WarpEnabled: node.WarpEnabled, OperaEnabled: node.OperaEnabled, OperaCountry: node.OperaCountry,
			TrafficCoefficient: node.TrafficCoefficient, Placement: node.Placement,
		}
		req.applyTo(&edit.Placement)
		if err := edit.Placement.Validate(); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		edit.Placement = edit.Placement.Normalized()
		if err := rt.mgr.UpdateNode(id, edit); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	views, err := rt.mgr.NodeViews()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	for _, v := range views {
		if v.ID == id {
			writeAPIData(w, http.StatusOK, v.Placement)
			return
		}
	}
	writeAPIErr(w, http.StatusNotFound, "not_found", "no such server")
}

func (rt *Router) apiListNodes(w http.ResponseWriter, _ *http.Request) {
	views, err := rt.mgr.NodeViews()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, views)
}

// apiGetNode answers with the SAME shape the list does.
//
// It used to return the raw nodes row, which was a different object under the same
// documented name: no online/is_local/proxy/reality_public_key, and a client generated
// from the spec that read a list row then re-fetched it by id got something else back.
// Sharing NodeViews also makes /v1/nodes/0 work — the master is not a nodes row, so
// looking it up by id answered "no such node" while the list happily included it.
func (rt *Router) apiGetNode(w http.ResponseWriter, _ *http.Request, id int64) {
	views, err := rt.mgr.NodeViews()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	for _, v := range views {
		if v.ID == id {
			writeAPIData(w, http.StatusOK, v)
			return
		}
	}
	writeAPIErr(w, http.StatusNotFound, "not_found", "no such node")
}

func (rt *Router) apiCreateNode(w http.ResponseWriter, r *http.Request) {
	var req apiCreateNodeReq
	if !apiDecode(w, r, &req) {
		return
	}
	req.Name, req.Host = strings.TrimSpace(req.Name), strings.TrimSpace(req.Host)
	if req.Name == "" || req.Host == "" {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "name and host are required")
		return
	}
	node, err := rt.mgr.CreateNode(req.Name, req.Host)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	set, _ := rt.mgr.Store().GetSettings()
	nodePath := ""
	if set != nil {
		nodePath = set.NodeAPIPath
	}
	writeAPIData(w, http.StatusCreated, map[string]any{
		"id":              node.ID,
		"join_token":      node.RawJoinToken,
		"install_command": rt.nodeInstallCommand(r, nodePath, node.RawJoinToken),
	})
}

func (rt *Router) apiPatchNode(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiPatchNodeReq
	if !apiDecode(w, r, &req) {
		return
	}
	node, err := rt.mgr.GetNode(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if node == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such node")
		return
	}
	// Patch semantics: an omitted field keeps the node's current value; an omitted
	// override pointer keeps the node's current override state.
	edit := store.NodeEdit{
		Name:               node.Name,
		Host:               node.Host,
		DecoyTemplate:      node.DecoyTemplate,
		VLESS:              node.VLESSEnabled,
		Hysteria:           node.HysteriaEnabled,
		Reality:            node.RealityEnabled,
		Routing:            node.Routing,
		XrayDNS:            node.XrayDNS,
		WarpEnabled:        node.WarpEnabled,
		OperaEnabled:       node.OperaEnabled,
		OperaCountry:       node.OperaCountry,
		TrafficCoefficient: node.TrafficCoefficient,
		Placement:          node.Placement,
	}
	if req.Name != nil {
		edit.Name = strings.TrimSpace(*req.Name)
	}
	if req.Host != nil {
		edit.Host = strings.TrimSpace(*req.Host)
	}
	if req.DecoyTemplate != nil {
		edit.DecoyTemplate = *req.DecoyTemplate
	}
	if req.VLESS != nil {
		edit.VLESS = req.VLESS
	}
	if req.Hysteria != nil {
		edit.Hysteria = req.Hysteria
	}
	if req.Reality != nil {
		edit.Reality = req.Reality
	}
	if req.Routing != nil {
		edit.Routing = req.Routing
	}
	if req.XrayDNS != nil {
		edit.XrayDNS = req.XrayDNS
	}
	if req.WarpEnabled != nil {
		edit.WarpEnabled = *req.WarpEnabled
	}
	if req.OperaEnabled != nil {
		edit.OperaEnabled = *req.OperaEnabled
	}
	if req.OperaCountry != nil {
		edit.OperaCountry = strings.TrimSpace(*req.OperaCountry)
	}
	if req.TrafficCoefficient != nil {
		edit.TrafficCoefficient = *req.TrafficCoefficient
	}
	apiPlacementReq{
		Country: req.Country, SortWeight: req.SortWeight, Capacity: req.Capacity,
		HideWhenFull: req.HideWhenFull, TrafficLimit: req.TrafficLimit, TrafficPeriod: req.TrafficPeriod,
		TrafficResetDay: req.TrafficResetDay, HideWhenOver: req.HideWhenOver,
	}.applyTo(&edit.Placement)
	if err := edit.Placement.Validate(); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	edit.Placement = edit.Placement.Normalized()
	if edit.Name == "" || edit.Host == "" {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "name and host must not be empty")
		return
	}
	if err := rt.mgr.UpdateNode(id, edit); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"ok": true})
}

func (rt *Router) apiDeleteNode(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteNode(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"ok": true})
}

func (rt *Router) apiSetNodeEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiSetNodeEnabledReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetNodeEnabled(id, req.Enabled); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"ok": true})
}

func (rt *Router) apiUpdateNode(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.RequestNodeUpdate(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"ok": true})
}

func (rt *Router) apiUpdateAllNodes(w http.ResponseWriter, _ *http.Request) {
	n, err := rt.mgr.RequestAllNodesUpdate()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"nodes": n})
}

func (rt *Router) apiRegenNodeJoin(w http.ResponseWriter, r *http.Request, id int64) {
	if node, err := rt.mgr.GetNode(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	} else if node == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such node")
		return
	}
	token, err := rt.mgr.RegenJoinToken(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	set, _ := rt.mgr.Store().GetSettings()
	nodePath := ""
	if set != nil {
		nodePath = set.NodeAPIPath
	}
	writeAPIData(w, http.StatusOK, map[string]any{
		"join_token":      token,
		"install_command": rt.nodeInstallCommand(r, nodePath, token),
	})
}
