package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/updater"
)

// nodeInstallCommand builds the one-line command an operator runs on a fresh
// server to join it as a node. The join token lives in the URL fragment so it
// never lands in an HTTP access log if the path is mistyped; the CLI parses it out.
//
// When the panel itself isn't on a CA-valid cert (e.g. served over a bare IP with a
// self-signed cert), the node can't verify the panel's TLS on join/sync, so the
// command gets --insecure automatically — otherwise the join fails with an x509
// error and the node never connects. A panel on a real domain omits it.
func (rt *Router) nodeInstallCommand(r *http.Request, nodePath, joinToken string) string {
	joinURL := panelPublicURL(r) + "/" + nodePath + "/" + nodeapi.PathPrefix + "/join#" + joinToken
	cmd := "curl -Ls https://raw.githubusercontent.com/" + updater.Repo +
		"/main/install.sh | sudo bash -s -- --join '" + joinURL + "'"
	if !rt.mgr.HasValidCert() {
		cmd += " --insecure"
	}
	return cmd
}

// listNodes returns the local server (node 0) plus every remote node, with status
// and today's traffic, for the Nodes UI.
func (rt *Router) listNodes(w http.ResponseWriter, _ *http.Request) {
	views, err := rt.mgr.NodeViews()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if views == nil {
		views = []core.NodeView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": views})
}

// nodeCreateReq is the add-node dialog payload.
type nodeCreateReq struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

// createNode registers a node and returns its view plus the one-time install
// command (join token shown exactly once).
func (rt *Router) createNode(w http.ResponseWriter, r *http.Request) {
	var req nodeCreateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "укажите название ноды")
		return
	}
	if req.Host == "" {
		writeErr(w, http.StatusBadRequest, "укажите домен или IP ноды")
		return
	}
	node, err := rt.mgr.CreateNode(req.Name, req.Host)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, _ := rt.mgr.Store().GetSettings()
	nodePath := ""
	if set != nil {
		nodePath = set.NodeAPIPath
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":              node.ID,
		"install_command": rt.nodeInstallCommand(r, nodePath, node.RawJoinToken),
	})
}

// nodePatchReq edits a node's name/host, protocol overrides and decoy. Protocol
// pointers use nil ⇒ "inherit global". Routing and DNS overrides are NOT edited
// here — they get a dedicated editor later; a protocol/decoy toggle must never
// silently wipe a node's routing override, so this handler preserves them.
type nodePatchReq struct {
	Name          string `json:"name"`
	Host          string `json:"host"`
	DecoyTemplate string `json:"decoy_template"`
	VLESS         *bool  `json:"vless_enabled"`
	Trojan        *bool  `json:"trojan_enabled"`
	Hysteria      *bool  `json:"hysteria_enabled"`
	Reality       *bool  `json:"reality_enabled"`
}

// updateNode applies an edit and wakes the node so the change propagates.
func (rt *Router) updateNode(w http.ResponseWriter, r *http.Request, id int64) {
	var req nodePatchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	node, err := rt.mgr.GetNode(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if node == nil {
		writeErr(w, http.StatusNotFound, "нода не найдена")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	if req.Name == "" || req.Host == "" {
		writeErr(w, http.StatusBadRequest, "название и домен обязательны")
		return
	}
	edit := store.NodeEdit{
		Name:          req.Name,
		Host:          req.Host,
		DecoyTemplate: req.DecoyTemplate,
		VLESS:         req.VLESS,
		Trojan:        req.Trojan,
		Hysteria:      req.Hysteria,
		Reality:       req.Reality,
		// Preserve the node's existing routing/DNS/egress config — this endpoint doesn't
		// edit them, and sending zero values would clear them.
		Routing:      node.Routing,
		XrayDNS:      node.XrayDNS,
		WarpEnabled:  node.WarpEnabled,
		OperaEnabled: node.OperaEnabled,
		OperaCountry: node.OperaCountry,
	}
	if err := rt.mgr.UpdateNode(id, edit); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// nodeRoutingReq sets a node's routing + DNS + egress overrides. A null routing/DNS
// field means "inherit the panel's"; egress (WARP/Opera) is the node's own and off
// by default. The node card's full routing editor is the only place these are set,
// so it always sends every field — no risk of a protocol toggle wiping them. Mirrors
// the master's ApplyRouting(cfg, warp, opera, country) shape.
type nodeRoutingReq struct {
	Routing      *model.RoutingConfig `json:"routing"`  // null ⇒ inherit global routing
	XrayDNS      *string              `json:"xray_dns"` // null ⇒ inherit global DNS
	WarpEnabled  bool                 `json:"warp_enabled"`
	OperaEnabled bool                 `json:"opera_enabled"`
	OperaCountry string               `json:"opera_country"`
}

// setNodeRouting saves a node's per-node routing + DNS + egress override.
func (rt *Router) setNodeRouting(w http.ResponseWriter, r *http.Request, id int64) {
	var req nodeRoutingReq
	if !decodeJSON(w, r, &req) {
		return
	}
	node, err := rt.mgr.GetNode(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if node == nil {
		writeErr(w, http.StatusNotFound, "нода не найдена")
		return
	}
	edit := store.NodeEdit{
		Name:          node.Name,
		Host:          node.Host,
		DecoyTemplate: node.DecoyTemplate,
		VLESS:         node.VLESSEnabled,
		Trojan:        node.TrojanEnabled,
		Hysteria:      node.HysteriaEnabled,
		Reality:       node.RealityEnabled,
		Routing:       req.Routing, // may be nil ⇒ inherit
		XrayDNS:       req.XrayDNS,
		WarpEnabled:   req.WarpEnabled,
		OperaEnabled:  req.OperaEnabled,
		OperaCountry:  req.OperaCountry,
	}
	if err := rt.mgr.UpdateNode(id, edit); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setMasterName sets the panel server's display name shown in config labels.
func (rt *Router) setMasterName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetMasterLabel(req.Name); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setMasterReality sets the master's REALITY donor and optionally regenerates keys.
func (rt *Router) setMasterReality(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dest  string `json:"dest"`
		Regen bool   `json:"regen"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetMasterReality(req.Dest, req.Regen); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// nodeConnections returns a node's effective connection status for its editor.
func (rt *Router) nodeConnections(w http.ResponseWriter, _ *http.Request, id int64) {
	c, err := rt.mgr.NodeConnectionsInfo(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// applyNodeConnections applies a full connections update to a node (its own transport,
// protocols, REALITY donor/keys).
func (rt *Router) applyNodeConnections(w http.ResponseWriter, r *http.Request, id int64) {
	var req core.ConnectionsUpdate
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.ApplyNodeConnections(id, req); err != nil {
		writeManagerErr(w, err)
		return
	}
	c, err := rt.mgr.NodeConnectionsInfo(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// nodeGeoRefresh asks a node to re-download its geo databases now.
func (rt *Router) nodeGeoRefresh(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.RequestNodeGeoRefresh(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setNodeReality sets a node's own REALITY donor and optionally regenerates its keys.
func (rt *Router) setNodeReality(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Dest  string `json:"dest"`
		Regen bool   `json:"regen"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetNodeReality(id, req.Dest, req.Regen); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setMasterProtocols toggles the panel's own protocols on/off from the master server
// card (the connection details stay in the global Подключения settings).
func (rt *Router) setMasterProtocols(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VLESS    bool `json:"vless_enabled"`
		Trojan   bool `json:"trojan_enabled"`
		Hysteria bool `json:"hysteria_enabled"`
		Reality  bool `json:"reality_enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetMasterProtocols(req.VLESS, req.Trojan, req.Hysteria, req.Reality); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// nodeLogs returns a node's recent log tail. Requesting it also asks the node to
// send fresh logs on its next sync, so a UI polling this endpoint keeps refreshing.
func (rt *Router) nodeLogs(w http.ResponseWriter, _ *http.Request, id int64) {
	lines, at := rt.mgr.RequestNodeLogs(id)
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "at": at})
}

// setNodeEnabled toggles whether a node serves traffic and appears in links.
func (rt *Router) setNodeEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetNodeEnabled(id, req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// deleteNode removes a node; its held poll learns it's revoked and stops serving.
func (rt *Router) deleteNode(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteNode(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// updateNodeVersion flags one node to self-update to the latest release.
func (rt *Router) updateNodeVersion(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.RequestNodeUpdate(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// updateAllNodes flags every connected node to self-update.
func (rt *Router) updateAllNodes(w http.ResponseWriter, _ *http.Request) {
	n, err := rt.mgr.RequestAllNodesUpdate()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": n})
}

// regenNodeJoin issues a fresh install command for an existing node (e.g. to
// re-install it), invalidating the node's current permanent token.
func (rt *Router) regenNodeJoin(w http.ResponseWriter, r *http.Request, id int64) {
	token, err := rt.mgr.RegenJoinToken(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, _ := rt.mgr.Store().GetSettings()
	nodePath := ""
	if set != nil {
		nodePath = set.NodeAPIPath
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"install_command": rt.nodeInstallCommand(r, nodePath, token),
	})
}
