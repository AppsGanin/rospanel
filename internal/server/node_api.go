package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// The hold and its jitter belong to the protocol, not to this file: the agent reads
// the same numbers (see nodeapi) to tell a recycled poll from a dead panel.
const (
	nodeSyncHoldSec    = nodeapi.HoldSec
	nodeSyncHoldJitter = nodeapi.HoldJitter
)

// nodeSyncBodyMax caps a sync request body. It was 1 MB, and an agent's traffic batch
// had no bound of its own: a busy node went past the cap, the body was refused, the
// agent resent the same batch forever, and the node stopped reporting and stopped
// receiving config — including the update that would have fixed it. Current agents
// send traffic in chunks well under 1 MB; the room above that is for an older agent
// already holding an oversized batch, so it can get it through and be updated.
const nodeSyncBodyMax = 8 << 20

// nodeSyncHold returns one jittered hold duration. Independent per request, so
// even a single node's own successive polls don't line up into a period.
func nodeSyncHold() time.Duration {
	spread := 2*nodeSyncHoldJitter + 1
	return time.Duration(nodeSyncHoldSec-nodeSyncHoldJitter+rand.IntN(spread)) * time.Second
}

// handleNodeAPI dispatches the node sync surface, mounted under the random
// node-API segment. Only two routes exist; anything else falls through to the
// decoy (the segment itself is the obscurity layer, same as apiPath).
func (rt *Router) handleNodeAPI(w http.ResponseWriter, r *http.Request, rest string) {
	leaf, afterLeaf := firstSegment(rest) // rest "/v1/join" → leaf "v1", afterLeaf "/join"
	if leaf != nodeapi.PathPrefix || r.Method != http.MethodPost {
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	action, _ := firstSegment(afterLeaf) // "/join" → "join"
	switch action {
	case "join":
		rt.handleNodeJoin(w, r)
	case "sync":
		rt.handleNodeSync(w, r)
	default:
		rt.currentDecoy().ServeHTTP(w, r)
	}
}

// handleNodeJoin exchanges a one-time join token for a permanent bearer token. An
// unknown/expired token gets a decoy response (404-equivalent) so a prober can't
// tell a wrong token from a wrong path.
func (rt *Router) handleNodeJoin(w http.ResponseWriter, r *http.Request) {
	// Bound a slow-trickle body: /v1/join is unauthenticated (segment-gated only), so
	// without a read deadline a dribbled body pins a goroutine indefinitely. The server
	// has no global ReadTimeout (it would kill the SSE/long-poll streams), so set it here.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	var req nodeapi.JoinRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	node, token, err := rt.mgr.Store().ConsumeJoinToken(req.JoinToken)
	if err != nil || node == nil {
		// Unknown/expired token — or a transient store error (a JSON 500 here would
		// fingerprint the endpoint to a prober who already knows the segment). Either
		// way, look like an ordinary site; a legitimate node just retries.
		rt.currentDecoy().ServeHTTP(w, r)
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
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}

	// Bound the body read with a deadline, then clear it before the long-poll hold
	// (the hold does no reads, and a leftover deadline would disturb connection reuse).
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(30 * time.Second))
	var req nodeapi.SyncRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, nodeSyncBodyMax)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	_ = rc.SetReadDeadline(time.Time{})

	// Capture the wake channel BEFORE computing desired state, so a config change
	// that lands between the hash check and the park below still fires the select
	// (the change closes this exact channel) — no lost wakeup.
	wake := rt.mgr.NodeWakeChan(node.ID)

	resp, err := rt.mgr.IngestNodeSync(node, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	// A config change — or any disagreement about whether this node is switched on —
	// is answered on the spot. Only a node whose belief already matches ours has its
	// request held.
	//
	// Both directions of that disagreement matter, and missing either one costs a
	// node about a minute of downtime:
	//
	//   - it thinks it is on, we say off ⇒ tell it now, or it keeps serving users
	//     the panel has stopped counting on it for;
	//   - it thinks it is off, we say on ⇒ tell it now, or it sits suspended while
	//     the panel shows it as enabled and phones fail to connect.
	//
	// The wake alone cannot cover this: re-enabling a node that is between polls
	// (rather than parked on one) has nothing to wake, and that request — the one
	// carrying the stale belief — is exactly the one that must not be held.
	// A port probe or config check the operator is blocked on is answered on the spot
	// for the same reason. Only an UNSENT one counts: an agent too old to answer would
	// otherwise leave the request pending forever, making every poll return instantly
	// and turning the node into a hot loop against the panel for the whole timeout.
	//
	// A chunk of a traffic backlog is answered at once too, so the next chunk follows
	// straight away — but only one that was counted: a report the panel failed to
	// ingest is held, or a persistent database error would become a tight loop. So is
	// a chunk of connection samples; those are recorded in memory, so nothing to count.
	// Either flag with nothing in the request is held: the flag alone must not buy a
	// node a loop.
	backlog := (req.TrafficMore && len(req.Traffic) > 0 && resp.AckReport > 0) ||
		(req.ConnsMore && len(req.Conns) > 0)
	if resp.Changed || resp.Revoked != req.Revoked || rt.mgr.NodeHasFreshWork(node.ID) || backlog {
		rt.writeNodeSync(w, r, node.ID, req.XrayStartedAt, resp)
		return
	}
	// Otherwise hold the request until the node is woken or the hold elapses, then
	// return no-change so the agent loops. The timer is stopped explicitly so an
	// early wake (the common case — any user change wakes every node) doesn't leave
	// a timer alive for the rest of the hold.
	timer := time.NewTimer(nodeSyncHold())
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
		rt.writeNodeSync(w, r, node.ID, req.XrayStartedAt, resp) // transient store error; let it re-sync
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
	state, err := rt.mgr.NodeStateChange(fresh, req.ConfigHash)
	if err != nil {
		// Not silent: a desired state that cannot be built means this node stops
		// receiving config for as long as the failure lasts, and nothing else in the
		// panel would say so.
		slog.Error("node: cannot build desired state", "node", fresh.ID, "err", err)
	} else if state != nil {
		out.Changed = true
		out.State = state
		slog.Info("node: pushing new state", "node", fresh.ID,
			"hash", state.Hash[:12], "speed_limits", len(state.Meta.SpeedLimits))
	}
	rt.writeNodeSync(w, r, node.ID, req.XrayStartedAt, out)
}

// writeNodeSync stamps the per-request extras (a pending self-update flag, and a
// panel-address broadcast if the node reached us at a stale host) onto the response
// and writes it. A revoked response carries no extras (the node is going away).
//
// reportedXrayStart is the Xray start time from the request being answered: handing
// over a restart command records it, so the next sync can tell "it bounced" from
// "nothing happened" by that value changing.
func (rt *Router) writeNodeSync(w http.ResponseWriter, r *http.Request, nodeID, reportedXrayStart int64, resp *nodeapi.SyncResponse) {
	if !resp.Revoked {
		if rt.mgr.TakeNodeUpdate(nodeID) {
			resp.Update = true
		}
		if rt.mgr.TakeNodeGeoRefresh(nodeID) {
			resp.RefreshGeo = true
		}
		if rt.mgr.TakeNodeXrayRestart(nodeID, reportedXrayStart) {
			resp.RestartXray = true
		}
		if rt.mgr.WantNodeLogs(nodeID) {
			resp.WantLogs = true
		}
		// Port probes an operator is waiting on before saving a custom inbound here.
		// Not "taken" like the flags above: the pending entry lives until its answer
		// arrives or the waiter gives up, so a probe that misses one round trip is
		// simply asked again on the next.
		resp.ProbePorts = rt.mgr.NodeProbePorts(nodeID)
		// A candidate config an operator is waiting on a verdict for. Like the probes
		// it is not "taken": it stays pending until answered or abandoned, so missing
		// one round trip only means asking again on the next.
		resp.CheckConfig = rt.mgr.NodeConfigCheck(nodeID)
		if canonical := rt.canonicalPanelURL(r); canonical != "" {
			resp.PanelURL = canonical
		}
	}
	writeSyncResponse(w, r, resp)
}

// gzipWriters are reused across config pushes: a writer carries its compressor's
// tables, and a push at every working-set change would otherwise allocate them anew.
var gzipWriters = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return zw
}}

// writeSyncResponse writes a sync answer, compressed when it carries a config and the
// node accepts gzip — which every agent does: Go's HTTP client asks for it and
// unpacks it without being told.
//
// A pushed config is the whole Xray config, every user's credentials in it: 4 MB at
// 20,000 users, 10 MB at 50,000, sent to every node at every working-set change.
// Credentials are random and compress poorly, but the JSON around them does not —
// measured 3.0× at the fastest level, for ~50 ms of CPU per 10 MB on a fast core.
// Every other answer is a few hundred bytes and goes out as it is.
func writeSyncResponse(w http.ResponseWriter, r *http.Request, resp *nodeapi.SyncResponse) {
	if resp.State == nil || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.WriteHeader(http.StatusOK)
	zw := gzipWriters.Get().(*gzip.Writer)
	zw.Reset(w)
	// Errors go the way writeJSON's do: the status is out already, and a node that hung
	// up or got a truncated stream fails its decode and asks again.
	_ = json.NewEncoder(zw).Encode(resp)
	_ = zw.Close()
	zw.Reset(io.Discard) // drop the reference to this response
	gzipWriters.Put(zw)
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			if q, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
				if v, err := strconv.ParseFloat(q, 64); err == nil && v == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
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
