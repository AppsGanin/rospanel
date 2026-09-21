package core

import (
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
)

// ingestNodeAbuse matches a node's reported destinations against the blocklists,
// dropping rows for user ids that do not exist or that the node is not believed about.
//
// The id check keeps a node from buffering matches against fabricated users (the
// EXISTS guard at write time would drop them anyway, but not before they cost buffer
// space). A node that predates the IP-only switch may still report hostnames; those
// simply never match and cost nothing beyond the row itself, since the per-sync abuse
// budget is only spent on rows that actually matched.
func (m *Manager) ingestNodeAbuse(nodeID int64, rows []nodeapi.SiteSample, believed func(userID int64) bool) {
	if m.abuse == nil {
		return
	}
	if len(rows) > maxNodeSiteRows {
		// An agent from before the cap was shared, on a node with thousands of users:
		// every one of its syncs is over. Said once an hour per node, not every 20s.
		if m.siteNotice.should(fmt.Sprintf("node-sites:%d", nodeID), time.Now()) {
			logWarn("node sync: site rows over the cap, the rest dropped (update the node)",
				"node", nodeID, "got", len(rows), "cap", maxNodeSiteRows)
		}
		rows = rows[:maxNodeSiteRows]
	}
	known, err := m.knownUserIDs()
	if err != nil {
		logErr("node sync: cannot validate site user ids", "err", err)
		return
	}
	abuseBudget := abuseNodeMax // cap this sync's contribution to the shared buffer
	for _, s := range rows {
		if !believed(s.UserID) {
			continue
		}
		if _, ok := known[s.UserID]; !ok {
			continue
		}
		// Attributed to the reporting node: an abuse complaint names one server's IP,
		// so which node emitted the traffic is the first thing the operator needs.
		// Bounded so a hostile node cannot fill the whole match buffer (the feeds are
		// public) and starve the master's own locally-observed matches.
		if abuseBudget > 0 && m.RecordNodeAbuse(nodeID, s.UserID, s.Host, s.Count) {
			abuseBudget--
		}
	}
}

// knownUserIDs returns the set of existing user ids, cached briefly.
//
// ingestNodeSites ran store.UserIDs() (a full id scan on the single write
// connection) on every sync that carried sites — a read a node triggers at will,
// and one a node opening parallel syncs could use to hammer the connection. The set
// changes rarely, so a short TTL turns "once per sync" into "at most once per TTL"
// while still picking up new users promptly. A newly created user's node-reported
// sites are dropped for at most the TTL, which an advisory view can absorb.
func (m *Manager) knownUserIDs() (map[int64]struct{}, error) {
	m.userIDCacheMu.Lock()
	defer m.userIDCacheMu.Unlock()
	if m.userIDCache != nil && time.Since(m.userIDCacheAt) < userIDCacheTTL {
		return m.userIDCache, nil
	}
	ids, err := m.store.UserIDs()
	if err != nil {
		return nil, err
	}
	m.userIDCache = ids
	m.userIDCacheAt = time.Now()
	return ids, nil
}

// IngestNodeSync records a node's reported status, ingests its traffic deltas
// idempotently, and answers with the report's acknowledgement — or, for a node that is
// switched off, that it is revoked. Whether the node's state must change is asked
// separately (NodeStatePush). It does NOT block for the long-poll — the handler owns
// the hold; this is the pure state transition.
func (m *Manager) IngestNodeSync(n *model.Node, req nodeapi.SyncRequest) (*nodeapi.SyncResponse, error) {
	// A disabled (or soft-deleted-but-unpurged) node's token still authenticates so we
	// can tell it to stop — but it is untrusted (being disabled is often WHY), so we
	// must NOT apply its reported traffic/devices/status. Revoke before any ingest.
	if !n.Enabled {
		return &nodeapi.SyncResponse{Revoked: true, AckReport: req.ReportID}, nil
	}
	now := time.Now()
	// Before anything else: did the Xray we asked this node to bounce actually come
	// back? The answer is in the report it just sent.
	m.ConfirmNodeXrayRestart(n.ID, req.XrayStartedAt)
	// Answers to any port probes an operator is waiting on before saving a custom
	// inbound on this node. Delivered early: the waiter has a short deadline, and
	// nothing below this line can change the answer.
	m.RecordNodeProbeResults(n.ID, req.ProbeResults)
	m.RecordNodeConfigCheck(n.ID, req.ConfigCheck)
	if len(req.Logs) > 0 {
		m.storeNodeLogs(n.ID, req.Logs)
	}
	m.nodeGeoMu.Lock()
	if len(req.GeoFiles) > 0 {
		m.nodeGeoFiles[n.ID] = req.GeoFiles
	}
	if req.Host != nil {
		m.nodeHostStats[n.ID] = *req.Host
	}
	// The tunnel's own state. Recorded for every sync from an agent that reports it,
	// so the health view and the alert both read one place.
	if req.AWGRunning || req.AWGError != "" {
		m.nodeAWG[n.ID] = nodeAWGState{Running: req.AWGRunning, Err: req.AWGError, Reported: true}
	}
	// Always refreshed (a healthy node reports 0), so the "limping" badge clears the
	// moment the transport recovers rather than sticking on a stale count.
	m.nodeSyncFails[n.ID] = req.SyncFails
	if m.nodeHas == nil {
		m.nodeHas = map[int64]NodeHas{}
	}
	m.nodeHas[n.ID] = NodeHas{DeltaRev: req.DeltaRev, StateTag: req.StateTag}
	m.nodeGeoMu.Unlock()
	// The node's own TLS state, for the fleet-wide "TLS certificate" alert. Recorded
	// here, raised by the node sweep — see manager_nodes_notify.go.
	m.NoteNodeCertError(n.ID, req.CertError)
	_ = m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen:       now.Unix(),
		NodeVersion:    req.NodeVersion,
		XrayVersion:    req.XrayVersion,
		XrayRunning:    req.XrayRunning,
		CertSHA256:     req.CertSHA256,
		CertSelfSigned: req.CertSelfSigned,
		CertIssuer:     req.CertIssuer,
		CertExpiresAt:  req.CertExpiresAt,
		ConfigHash:     req.ConfigHash,
	})

	// Everything below names users, and a node is believed only about its own (see
	// manager_node_served.go). When that cannot be told, nothing is taken: the traffic
	// is not acknowledged, so the node sends it again, and the samples are dropped.
	served, err := m.nodeServedFor(n)
	if err != nil {
		logErr("node sync: cannot tell which users this node serves, its report is not taken",
			"node", n.ID, "err", err)
	}
	foreign := 0
	believed := func(userID int64) bool {
		if served == nil {
			return false
		}
		if served.allows(userID, now.Unix()) {
			return true
		}
		foreign++
		return false
	}

	// Idempotent traffic ingest: atomically claim the report id. A report at-or-below
	// the stored watermark is a retry of an already-counted batch (lost response); the
	// conditional claim also stops two concurrent syncs from both counting the same
	// batch. The agent persists its report id, so a restart no longer regresses it.
	ack := req.ReportID
	if req.ReportID > 0 && served == nil {
		ack = 0
	} else if req.ReportID > 0 {
		// One commit for the node's whole batch, watermark included. Written per user
		// this was three fsyncs each on the panel's single connection, every 20s, per
		// node — the last write path whose cost still scaled with the user count.
		today := now.In(m.loc()).Format("2006-01-02")
		// The node's quota coefficient: real bytes go to the per-node stats, scaled
		// bytes to the user's allowance (see store.TrafficDelta / model.Node).
		coef := model.NodeCoefficientOr(n.TrafficCoefficient)
		deltas := make([]store.TrafficDelta, 0, len(req.Traffic))
		for _, d := range req.Traffic {
			up, down := nonNeg(d.Up), nonNeg(d.Down)
			if (up == 0 && down == 0) || !believed(d.UserID) {
				continue
			}
			// No Baseline: the node already subtracted on its side, and last_up/
			// last_down belong to the master's own Xray counters.
			deltas = append(deltas, store.TrafficDelta{
				UserID: d.UserID, NodeID: n.ID, Day: today,
				AddUp: up, AddDown: down,
				QuotaUp: scaleQuota(up, coef), QuotaDown: scaleQuota(down, coef),
				SeenAt: now.Unix(),
			})
		}
		claimed, err := m.store.ApplyNodeReport(n.ID, req.ReportID, deltas)
		switch {
		case err != nil:
			// Nothing was committed — watermark included — so do NOT ack: the node keeps
			// the batch and resends it, and that resend can still be counted.
			logErr("node sync: traffic ingest failed",
				"node", n.ID, "users", len(deltas), "err", err)
			ack = 0
		case claimed && len(deltas) > 0:
			m.enforceTrafficSoon()
		}
		// claimed==false with err==nil ⇒ already-counted duplicate ⇒ ack it (a no-op).
	}

	// Device counting across the fleet: feed each reported (email, ip) through the
	// same path as the master's access log. RecordAccess resolves the user, throttles,
	// upserts the connection, and triggers a user-sync if a new device pushed someone
	// over their cap — so the device limit counts unique IPs on every server, not just
	// the master. Not gated on ReportID: connection samples are idempotent (upsert by
	// user+ip) and independent of the traffic batch.
	for _, c := range req.Conns {
		id, ok := userIDFromEmail(c.Email)
		if !ok || !believed(id) {
			continue
		}
		// Recorded under the tag as the panel writes it: "u007" is user 7 too, and a
		// spelling of its own would be a throttle key of its own.
		m.RecordAccessOn(n.ID, model.UserEmail(id), c.IP, "")
	}

	// Destinations arrive pre-aggregated with a count, so they bypass RecordAccess
	// (which counts one connection per call) and are folded straight into the rolling
	// view. Not gated on ReportID either: a duplicated sync would double-count a
	// sampled top-N that ages out in hours, which is not worth an ack protocol.
	if len(req.Sites) > 0 && served != nil {
		m.ingestNodeAbuse(n.ID, req.Sites, believed)
	}
	if foreign > 0 && m.siteNotice.should(fmt.Sprintf("node-foreign:%d", n.ID), now) {
		// Not a lag: a user who left the node's config is believed for an hour after.
		// A node naming users it was never given is broken or no longer the operator's.
		logWarn("node sync: report names users this node does not serve, those rows dropped",
			"node", n.ID, "rows", foreign)
	}

	// The state the node should have is the caller's to add (NodeStatePush): it is held
	// until the response is encoded, which only the caller can know.
	return &nodeapi.SyncResponse{AckReport: ack}, nil
}
