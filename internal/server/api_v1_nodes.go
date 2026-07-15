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
		Name          *string              `json:"name,omitempty"`
		Host          *string              `json:"host,omitempty"`
		DecoyTemplate *string              `json:"decoy_template,omitempty"`
		VLESS         *bool                `json:"vless_enabled,omitempty"`
		Trojan        *bool                `json:"trojan_enabled,omitempty"`
		Hysteria      *bool                `json:"hysteria_enabled,omitempty"`
		Reality       *bool                `json:"reality_enabled,omitempty"`
		Routing       *model.RoutingConfig `json:"routing,omitempty"`
		XrayDNS       *string              `json:"xray_dns,omitempty"`
		WarpEnabled   *bool                `json:"warp_enabled,omitempty"`
		OperaEnabled  *bool                `json:"opera_enabled,omitempty"`
		OperaCountry  *string              `json:"opera_country,omitempty"`
	}
	apiSetNodeEnabledReq struct {
		Enabled bool `json:"enabled"`
	}
)

func (rt *Router) apiListNodes(w http.ResponseWriter, _ *http.Request) {
	views, err := rt.mgr.NodeViews()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, views)
}

func (rt *Router) apiGetNode(w http.ResponseWriter, _ *http.Request, id int64) {
	node, err := rt.mgr.GetNode(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if node == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such node")
		return
	}
	writeAPIData(w, http.StatusOK, node)
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
		Name:          node.Name,
		Host:          node.Host,
		DecoyTemplate: node.DecoyTemplate,
		VLESS:         node.VLESSEnabled,
		Trojan:        node.TrojanEnabled,
		Hysteria:      node.HysteriaEnabled,
		Reality:       node.RealityEnabled,
		Routing:       node.Routing,
		XrayDNS:       node.XrayDNS,
		WarpEnabled:   node.WarpEnabled,
		OperaEnabled:  node.OperaEnabled,
		OperaCountry:  node.OperaCountry,
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
	if req.Trojan != nil {
		edit.Trojan = req.Trojan
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
