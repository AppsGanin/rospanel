package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// nodeSyncHoldSec is how long a no-change sync request is held before returning
// Changed=false, so a connected node makes roughly one request per this interval
// in steady state (carrying its traffic report). Comfortably inside the server's
// idle timeout, and short enough that a node reflects a panel restart quickly.
const nodeSyncHoldSec = 45

// handleNodeAPI dispatches the node sync surface, mounted under the random
// node-API segment. Only two routes exist; anything else falls through to the
// decoy (the segment itself is the obscurity layer, same as apiPath).
func (rt *Router) handleNodeAPI(w http.ResponseWriter, r *http.Request, rest string) {
	leaf, afterLeaf := firstSegment(rest) // rest "/v1/join" → leaf "v1", afterLeaf "/join"
	if leaf != nodeapi.PathPrefix || r.Method != http.MethodPost {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	action, _ := firstSegment(afterLeaf) // "/join" → "join"
	switch action {
	case "join":
		rt.handleNodeJoin(w, r)
	case "sync":
		rt.handleNodeSync(w, r)
	default:
		rt.decoy.ServeHTTP(w, r)
	}
}

// handleNodeJoin exchanges a one-time join token for a permanent bearer token. An
// unknown/expired token gets a decoy response (404-equivalent) so a prober can't
// tell a wrong token from a wrong path.
func (rt *Router) handleNodeJoin(w http.ResponseWriter, r *http.Request) {
	var req nodeapi.JoinRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	node, token, err := rt.mgr.Store().ConsumeJoinToken(req.JoinToken)
	if err != nil || node == nil {
		// Unknown/expired token — or a transient store error (a JSON 500 here would
		// fingerprint the endpoint to a prober who already knows the segment). Either
		// way, look like an ordinary site; a legitimate node just retries.
		rt.decoy.ServeHTTP(w, r)
		return
	}
	rt.mu.RLock()
	nodePath := rt.nodePath
	rt.mu.RUnlock()
	writeJSON(w, http.StatusOK, nodeapi.JoinResponse{
		NodeID:   node.ID,
		Token:    token,
		PanelURL: panelPublicURL(r),
		HoldSec:  nodeSyncHoldSec,
		NodeAPI:  nodePath,
	})
}

// handleNodeSync is the long-poll: authenticate the node by bearer token, ingest
// its report, then either return a config change immediately or hold the request
// until the node is woken (config changed) or the hold elapses.
func (rt *Router) handleNodeSync(w http.ResponseWriter, r *http.Request) {
	token := apiKeyFromRequest(r)
	node, err := rt.mgr.Store().LookupNodeByToken(token)
	if err != nil || node == nil {
		// No valid token (or a transient store error) → look like an ordinary site,
		// so an unauthenticated prober can't distinguish this from unknown hosting.
		rt.decoy.ServeHTTP(w, r)
		return
	}

	var req nodeapi.SyncRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}

	// Capture the wake channel BEFORE computing desired state, so a config change
	// that lands between the hash check and the park below still fires the select
	// (the change closes this exact channel) — no lost wakeup.
	wake := rt.mgr.NodeWakeChan(node.ID)

	resp, err := rt.mgr.IngestNodeSync(node, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	// A change (or a revocation) is delivered right away.
	if resp.Changed || resp.Revoked {
		rt.writeNodeSync(w, r, node.ID, resp)
		return
	}
	// Otherwise hold the request until the node is woken or the hold elapses, then
	// return no-change so the agent loops. The timer is stopped explicitly so an
	// early wake (the common case — any user change wakes every node) doesn't leave
	// a 45s timer alive.
	timer := time.NewTimer(nodeSyncHoldSec * time.Second)
	defer timer.Stop()
	select {
	case <-wake:
	case <-timer.C:
	case <-r.Context().Done():
		return // client hung up
	}
	// Recompute after waking: the desired state may now differ.
	fresh, err := rt.mgr.GetNode(node.ID)
	if err != nil {
		rt.writeNodeSync(w, r, node.ID, resp) // transient store error; let it re-sync
		return
	}
	if fresh == nil {
		// The node was deleted while its poll was held. Tell it to stop serving —
		// otherwise it keeps running the last config with credentials we've revoked.
		writeJSON(w, http.StatusOK, &nodeapi.SyncResponse{AckReport: resp.AckReport, Revoked: true})
		return
	}
	out := &nodeapi.SyncResponse{AckReport: resp.AckReport}
	if !fresh.Enabled {
		out.Revoked = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	state, err := rt.mgr.NodeDesiredState(fresh)
	if err == nil && state.Hash != req.ConfigHash {
		out.Changed = true
		out.State = state
	}
	rt.writeNodeSync(w, r, node.ID, out)
}

// writeNodeSync stamps the per-request extras (a pending self-update flag, and a
// panel-address broadcast if the node reached us at a stale host) onto the response
// and writes it. A revoked response carries no extras (the node is going away).
func (rt *Router) writeNodeSync(w http.ResponseWriter, r *http.Request, nodeID int64, resp *nodeapi.SyncResponse) {
	if !resp.Revoked {
		if rt.mgr.TakeNodeUpdate(nodeID) {
			resp.Update = true
		}
		if rt.mgr.TakeNodeGeoRefresh(nodeID) {
			resp.RefreshGeo = true
		}
		if rt.mgr.WantNodeLogs(nodeID) {
			resp.WantLogs = true
		}
		if canonical := rt.canonicalPanelURL(r); canonical != "" {
			resp.PanelURL = canonical
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// canonicalPanelURL returns the panel's configured public URL when the node
// reached us at a different host than the one configured — i.e. the panel's
// address changed and this node is still using the old one. This auto-heals a
// panel move while both the old and new addresses still resolve; `rospanel node
// set-panel` is the manual fallback when they don't.
//
// It only ever broadcasts a bare-domain host on the standard :443. Broadcasting an
// IP (the panel cert may lack an IP SAN → the node's verifying sync client would
// then fail every sync), an IPv6 literal (bracketing/URL-encoding hazards), a
// non-standard port, or localhost would risk switching a node to an address it
// can't reach — a one-way brick recoverable only by hand. In all those cases it
// returns "" and the operator uses `node set-panel`.
func (rt *Router) canonicalPanelURL(r *http.Request) string {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil || !isBroadcastableHost(set.Host) {
		return ""
	}
	reqHost := r.Host
	if h, _, e := net.SplitHostPort(reqHost); e == nil {
		reqHost = h
	}
	if strings.EqualFold(reqHost, set.Host) {
		return "" // already on the canonical host
	}
	return "https://" + set.Host
}

// isBroadcastableHost reports whether host is safe to auto-broadcast to nodes: a
// real domain name (not an IP, not localhost/loopback, no port).
func isBroadcastableHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	if strings.ContainsAny(host, ":/") { // port, path, or IPv6 literal
		return false
	}
	if net.ParseIP(host) != nil { // an IPv4 address, not a domain
		return false
	}
	// Must look like a dotted domain (has a TLD label).
	return strings.Contains(host, ".")
}

// panelPublicURL reconstructs the panel's public base URL (scheme://host) from the
// request, so a joining node learns where to reach the panel. The panel sits
// behind Xray's TLS, so requests arrive as https to the public host.
func panelPublicURL(r *http.Request) string {
	host := r.Host
	scheme := "https"
	// A dev panel reached directly on loopback has no TLS in front of it.
	if strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost") {
		scheme = "http"
	}
	return scheme + "://" + host
}
