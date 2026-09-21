package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/warp"
	"github.com/AppsGanin/rospanel/internal/xray"
)

const nodeInputsTTL = 10 * time.Second

// --- node CRUD (thin wrappers that wake the node registry) --------------------

// ListNodes returns all configured nodes.
func (m *Manager) ListNodes() ([]model.Node, error) { return m.store.ListNodes() }

// GetNode returns one node, or (nil, nil) if absent.
func (m *Manager) GetNode(id int64) (*model.Node, error) { return m.store.GetNode(id) }

// CreateNode registers a node with a random decoy and a one-time join token,
// ensuring the node-API surface exists. The returned node carries RawJoinToken.
// The name must be unique (it becomes a subscription proxy name/tag).
func (m *Manager) CreateNode(name, host string) (*model.Node, error) {
	if taken, err := m.store.NodeNameTaken(name, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, invalidCode("err.nodeNameTaken", "нода с таким названием уже есть — имя должно быть уникальным")
	}
	if err := m.EnsureNodeAPIPath(); err != nil {
		return nil, err
	}
	n, err := m.store.CreateNode(name, host, m.randomDecoy())
	if errors.Is(err, store.ErrNodeNameTaken) {
		return nil, invalidCode("err.nodeNameTaken", "нода с таким названием уже есть — имя должно быть уникальным")
	}
	return n, err
}

// UpdateNode edits a node and wakes it so config/link changes apply promptly.
func (m *Manager) UpdateNode(id int64, e store.NodeEdit) error {
	if taken, err := m.store.NodeNameTaken(e.Name, id); err != nil {
		return err
	} else if taken {
		return invalidCode("err.nodeNameTaken", "нода с таким названием уже есть — имя должно быть уникальным")
	}
	// The node's DNS goes into the config the panel GENERATES for it, and the node's
	// own Xray refuses a config it cannot parse — leaving that node frozen on its last
	// good config while the panel reports it online and answers 200. Same check the
	// master's DNS gets, for the same reason.
	if err := validateDNSList(e.XrayDNS); err != nil {
		return err
	}
	// A region the helper doesn't know is silently replaced by the default on the
	// master; storing it raw on a node made the two disagree and handed opera-proxy a
	// country it would reject.
	e.OperaCountry = model.OperaCountryOr(e.OperaCountry)
	if e.Routing != nil {
		if err := e.Routing.ValidateLanes(); err != nil {
			return fromFieldErr(err)
		}
	}
	// WARP is a per-node Cloudflare registration: provision one BEFORE persisting the
	// edit the first time WARP is enabled on this node, so a failed registration
	// leaves nothing half-applied (mirrors the master's ApplyRouting).
	if e.WarpEnabled {
		if err := m.ensureNodeWarp(id); err != nil {
			return err
		}
	}
	if err := m.store.UpdateNode(id, e); err != nil {
		if errors.Is(err, store.ErrNodeNameTaken) {
			return invalidCode("err.nodeNameTaken", "нода с таким названием уже есть — имя должно быть уникальным")
		}
		return err
	}
	// Re-resolve this node's own lane proxies now (mirrors the master's
	// setProxies-on-save) so a lane edit applies on the node's next pull.
	if n, err := m.store.GetNode(id); err == nil && n != nil {
		m.resolveNodeProxies(n)
	}
	m.nodes.wakeOne(id)
	return nil
}

// SetNodeDNS saves a node's own DNS override (nil ⇒ inherit the panel's) without
// touching routing/egress, and wakes the node so it pulls the new config. The DNS tab
// saves through here, independent of the routing tab.
func (m *Manager) SetNodeDNS(id int64, dns *string) error {
	if err := validateDNSList(dns); err != nil {
		return err
	}
	if err := m.store.SetNodeDNS(id, dns); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// ensureNodeWarp provisions a Cloudflare WARP account for a node the first time WARP
// is enabled on it. Each node needs its OWN registration — a shared WireGuard
// identity across servers is unsafe — so this never reuses the master's account.
// No-op if the node is already registered.
func (m *Manager) ensureNodeWarp(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil || n.WarpRegistered() {
		return nil
	}
	logInfo("warp: registering Cloudflare WARP account for node", "node", id)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	acc, err := warp.Register(ctx)
	if err != nil {
		logErr("warp: node registration failed", "node", id, "err", err)
		return invalidCode("err.nodeWarpFailed", "регистрация WARP для ноды не удалась: {{detail}}", map[string]any{"detail": err.Error()})
	}
	return m.store.SaveNodeWarp(id, acc.PrivateKey, acc.PeerPublicKey, acc.Endpoint,
		acc.AddressV4, acc.AddressV6, joinInts(acc.Reserved))
}

// SetNodeReality sets a node's own REALITY donor (empty ⇒ inherit the panel's) and,
// when regen is set, regenerates the node's REALITY keypair. Wakes the node so the
// new identity (and its share links) propagate.
func (m *Manager) SetNodeReality(id int64, dest string, regen bool) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	dest = strings.TrimSpace(dest)
	if dest != "" {
		norm, err := validateRealityDests(dest)
		if err != nil {
			return err
		}
		dest = norm
	}
	if err := m.store.SetNodeRealityDest(id, dest); err != nil {
		return err
	}
	if regen {
		priv, pub, err := auth.GenerateRealityKeys()
		if err != nil {
			return err
		}
		shortID, err := auth.RandomShortIDs()
		if err != nil {
			return err
		}
		svc, err := auth.RandomRealityPath()
		if err != nil {
			return err
		}
		if err := m.store.SaveNodeReality(id, priv, pub, shortID, svc); err != nil {
			return err
		}
	}
	m.nodes.wakeOne(id)
	return nil
}

// SetMasterReality sets the panel's own REALITY donor and optionally regenerates its
// keys, then reloads Xray. The donor is live-probed when it changes while REALITY is
// on (mirrors ApplyConnections).
func (m *Manager) SetMasterReality(dest string, regen bool) error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	norm, err := validateRealityDests(dest)
	if err != nil {
		return err
	}
	if set.RealityEnabled && norm != set.RealityDest {
		for _, d := range strings.Split(norm, ",") {
			if err := validateRealityDestLive(d); err != nil {
				return err
			}
		}
	}
	if err := m.store.SetRealityPorts(set.RealityPort, norm); err != nil {
		return err
	}
	if regen {
		if err := m.regenRealityKeys(); err != nil {
			return err
		}
	}
	m.TriggerReconcile()
	return nil
}

// NodeConnectionsInfo reports a node's effective connection status (its own transport
// where set, else the master's), for the per-node connections editor.
func (m *Manager) NodeConnectionsInfo(id int64) (*ConnectionsStatus, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	n, err := m.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	return buildConnectionsStatus(nodeSettings(set, n)), nil
}

// ApplyNodeConnections applies a full connections update to a node: its protocols,
// REALITY donor/keys, and transport (ports, hop, WS, anti-replay, fingerprints,
// names, anti-DPI) — all the node's OWN. Validation is syntactic; the node's local
// `xray -test` is the backstop, and port-free / donor-live checks need the node's own
// host, which the panel can't reach.
func (m *Manager) ApplyNodeConnections(id int64, u ConnectionsUpdate) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}

	fpOf := func(key string) string {
		if v := u.Fingerprints[key]; v != "" {
			return v
		}
		return "firefox"
	}
	vlessFp, realityFp := fpOf("vless"), fpOf("reality")
	for _, fp := range []string{vlessFp, realityFp} {
		if !model.ValidFingerprint(fp) {
			return invalidCode("err.unknownFingerprint", "неизвестный fingerprint {{value}}", map[string]any{"value": fp})
		}
	}
	connNames, err := validateConnNames(u.Names, m.inboundNames(id))
	if err != nil {
		return err
	}
	if u.HysteriaPort < 1 || u.HysteriaPort > 65535 {
		return invalidCode("err.portRange", "порт вне диапазона 1–65535")
	}
	if u.HopStart < 1 || u.HopEnd > 65535 || u.HopStart > u.HopEnd {
		return invalidCode("err.badHopRange", "неверный диапазон хопа")
	}
	interval := strings.TrimSpace(u.HopInterval)
	if interval == "" {
		interval = "5-10"
	}
	if !hopIntervalRe.MatchString(interval) {
		return invalidCode("err.badInterval", "неверный интервал (нужно «N-M», напр. 5-10)")
	}
	obfs, err := resolveObfs(u.HysteriaObfs, u.RegenObfs)
	if err != nil {
		return err
	}
	if u.RealityPort < 1 || u.RealityPort > 65535 {
		return invalidCode("err.realityPortRange", "порт REALITY вне диапазона 1–65535")
	}
	realityDest := strings.TrimSpace(u.RealityDest)
	if realityDest != "" {
		norm, derr := validateRealityDests(realityDest)
		if derr != nil {
			return derr
		}
		realityDest = norm
	}
	maxTimeDiff := 0
	if u.RealityAntiReplay {
		maxTimeDiff = realityAntiReplayWindowMs
	}

	// Protocols (the node's own explicit on/off).
	awgPort, awgDNS, err := validateAWGUpdate(u.AWGPort, u.AWGDNS)
	if err != nil {
		return err
	}
	if u.Protocols["awg"] && awgPort == 0 {
		if n.Connections != nil && n.Connections.AWGPort != 0 {
			awgPort = n.Connections.AWGPort
		} else {
			awgPort = pickAWGPort()
		}
	}
	if err := m.store.SetNodeProtocols(id,
		u.Protocols["vless"], u.Protocols["hysteria2"], u.Protocols["reality"]); err != nil {
		return err
	}
	if err := m.store.SetNodeAWGEnabled(id, u.Protocols["awg"]); err != nil {
		return err
	}
	if u.Protocols["awg"] || u.RegenAWGKeys {
		if err := m.ensureNodeAWGIdentity(n, u.RegenAWGKeys); err != nil {
			return err
		}
	}
	// REALITY donor + optional key regeneration.
	if err := m.store.SetNodeRealityDest(id, realityDest); err != nil {
		return err
	}
	if u.RegenRealityKeys {
		priv, pub, kerr := auth.GenerateRealityKeys()
		if kerr != nil {
			return kerr
		}
		shortID, kerr := auth.RandomShortIDs()
		if kerr != nil {
			return kerr
		}
		svc, kerr := auth.RandomRealityPath()
		if kerr != nil {
			return kerr
		}
		if err := m.store.SaveNodeReality(id, priv, pub, shortID, svc); err != nil {
			return err
		}
	}
	// Transport blob.
	blob := &model.NodeConnections{
		HysteriaPort:       u.HysteriaPort,
		HopStart:           u.HopStart,
		HopEnd:             u.HopEnd,
		HopInterval:        interval,
		HysteriaObfs:       obfs,
		RealityPort:        u.RealityPort,
		RealityMaxTimeDiff: maxTimeDiff,
		TLSFragment:        u.TLSFragment,
		TLSMin13:           u.TLSMin13,
		BlockQUIC:          u.BlockQUIC,
		VLESSFp:            vlessFp,
		RealityFp:          realityFp,
		VLESSName:          connNames["vless"],
		RealityName:        connNames["reality"],
		HysteriaName:       connNames["hysteria2"],
		AWGPort:            awgPort,
		AWGName:            connNames["awg"],
		AWGDNS:             awgDNS,
	}
	if err := m.store.SetNodeConnections(id, blob); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// SetNodeEnabled toggles a node and wakes it (a disabled node is told to stop).
func (m *Manager) SetNodeEnabled(id int64, enabled bool) error {
	if err := m.store.SetNodeEnabled(id, enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalidCode("err.nodeNotFound", "нода не найдена")
		}
		return err
	}
	// Resolve (on enable) or drop (on disable) this node's lane proxies in the
	// background: a node enabled after boot was skipped by seedNodeProxies, so without
	// this its lanes would egress direct until the next cadence tick (or forever when
	// auto-refresh is "never"). RefreshNodeProxies also wakes the node on any change.
	m.runAsync(m.RefreshNodeProxies)
	m.nodes.wakeOne(id)
	return nil
}

// DeleteNode removes a node and wakes any held poll so it learns it's revoked.
//
// A node that is CONNECTED when deleted is almost always parked in its held poll,
// so wakeOne makes it return, find its row gone, and be told revoked (see
// handleNodeSync). A node that is OFFLINE at delete time and reconnects later
// gets only the decoy (its token row is gone), which the agent reads as "panel
// unreachable" and keeps serving the last config. Closing that residual window
// needs a tombstone (keep the token briefly, answer revoked) — deferred to the
// node-agent PR, where it first becomes reachable. Until then, disabling a node
// (which keeps the token and answers revoked) is the reliable "stop now" control.
func (m *Manager) DeleteNode(id int64) error {
	if err := m.store.DeleteNode(id); err != nil {
		return err
	}
	// A node row is tombstoned rather than removed, but its custom inbounds are not
	// carried by that row — they are their own table keyed by server id. Left behind
	// they would be orphans the fleet-wide readers still hand out, and a re-created
	// node reusing the id would silently inherit them.
	inbounds, _ := m.store.Inbounds(id) // for grant cleanup below, before they're gone
	if err := m.store.DeleteServerInbounds(id); err != nil {
		logErr("inbounds: cleanup after node delete failed", "node", id, "err", err)
	}
	// Sweep group grants that referenced this node's built-in lanes and its inbounds,
	// so a group doesn't keep tokens for a server that no longer exists.
	if err := m.store.DeleteServerGrants(id); err != nil {
		logErr("groups: builtin grant cleanup after node delete failed", "node", id, "err", err)
	}
	for _, in := range inbounds {
		if err := m.store.DeleteInboundGrants(in.ID); err != nil {
			logErr("groups: inbound grant cleanup after node delete failed", "inbound", in.ID, "err", err)
		}
	}
	m.nodes.dropWaiter(id)
	m.served.forget(id)
	return nil
}

// RegenJoinToken issues a fresh install token for an existing node.
func (m *Manager) RegenJoinToken(id int64) (string, error) { return m.store.RegenJoinToken(id) }

// IssueJoinToken issues a fresh install token WITHOUT revoking the node's current
// permanent token — for SSH re-provisioning, so a failed install can't down a live node.
func (m *Manager) IssueJoinToken(id int64) (string, error) { return m.store.IssueJoinToken(id) }

// SetMasterLabel sets the panel server's display name used in config labels.
func (m *Manager) SetMasterLabel(label string) error {
	return m.store.SetMasterLabel(strings.TrimSpace(label))
}

// What a node's restart request currently reads as, for the UI.
const (
	RestartPending = "pending" // asked, waiting for the node to prove it happened
	RestartDone    = "done"    // the node reported a genuinely restarted Xray
	RestartTimeout = "timeout" // waited long enough and never got proof
)

const (
	// nodeRestartWait is how long an unconfirmed request stays pending. It covers the
	// whole round trip — deliver on the node's poll (held up to 27s), bounce Xray,
	// report the new start time on the next sync — with room to spare.
	//
	// Giving up matters as much as waiting: a node that is offline, or whose Xray
	// refused to come back, must not leave the button saying "queued" forever.
	nodeRestartWait = 2 * time.Minute
	// nodeRestartShow is how long the OUTCOME stays visible after the request
	// resolves. Without it the feature is invisible: confirmation normally lands
	// about a second after the click, so the pending badge appears and vanishes
	// between two refreshes and the operator — having seen nothing change — clicks
	// again. Long enough to be read, short enough not to linger as stale news.
	nodeRestartShow = 5 * time.Second
)

// nodeCmdTTL bounds how long a one-shot node command (self-update, geo refresh) stays
// pending. Past it the request is dropped rather than delivered: an operator who asked a
// node to update half an hour ago, gave up and walked away should not have it update on
// its own the moment it comes back.
const nodeCmdTTL = 15 * time.Minute

// The kinds of one-shot command a node can be carrying. Stored as-is in node_commands.
const (
	nodeCmdUpdate = "update"
	nodeCmdGeo    = "geo"
)

// nodeLogsWantWindow is how long after an operator opens a node's logs the panel
// keeps asking that node to include its log tail (so viewing keeps refreshing, then
// stops on its own when the operator navigates away).
const nodeLogsWantWindow = 30 * time.Second

// nodeLogsWait is how long RequestNodeLogs waits for a woken node to deliver a
// fresh tail before answering with whatever it has. Long enough for a round trip to
// a node on another continent, short enough that a caller never wonders whether the
// request hung.
const nodeLogsWait = 3 * time.Second

// nodeTombstoneGrace is how long a deleted node's row is kept so it can still be
// told Revoked on a late reconnect before the row is purged.
const nodeTombstoneGrace = 7 * 24 * time.Hour

// PurgeDeletedNodes reclaims tombstoned node rows past the grace window.
func (m *Manager) PurgeDeletedNodes() {
	cutoff := time.Now().Add(-nodeTombstoneGrace).Unix()
	if n, err := m.store.PurgeDeletedNodes(cutoff); err != nil {
		logWarn("purge deleted nodes", "err", err)
	} else if n > 0 {
		logInfo("purged tombstoned nodes", "count", n)
	}
}

// randomDecoy picks a decoy template for a new node so nodes don't all share the
// panel's masquerade fingerprint.
//
// Drawn from the busy-site pool rather than every bundled template: a node carries
// nothing BUT tunnelled traffic, so landing it on a placeholder or a "temporarily
// unavailable" page states outright that a box moving gigabytes is a site with
// nothing on it. The operator can still set any template afterwards.
func (m *Manager) randomDecoy() string {
	return decoy.RandomTemplate()
}

// --- sync ingest --------------------------------------------------------------

// maxNodeSiteRows bounds how many destination rows one sync may contribute: the panel
// must not depend on a node behaving, and applying an unbounded batch was measured at
// ~23ms of CPU on the same lock the master's access-log tap needs. Current agents stay
// within it themselves (nodeapi.MaxSiteRows); an older one sent up to ~6,500 rows from
// a busy node and lost the rest here.
const maxNodeSiteRows = nodeapi.MaxSiteRows

// userIDCacheTTL bounds how stale the node-site user-id validation set may be. Short
// enough that a new user's node sites start counting within seconds, long enough
// that a node cannot force a fresh id scan on every sync.
const userIDCacheTTL = 15 * time.Second

// EnsureNodeAPIPath generates the node-API URL segment the first time a node is
// created, then swaps it live into the router via the registered callback. It is
// serialized so two nodes created concurrently can't each mint a different path
// (which would leave the router and the DB disagreeing on the segment).
func (m *Manager) EnsureNodeAPIPath() error {
	m.nodeEnsureMu.Lock()
	defer m.nodeEnsureMu.Unlock()
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	if set.NodeAPIPath != "" {
		return nil
	}
	path, err := randomPathSegment()
	if err != nil {
		return err
	}
	if err := m.store.SetNodeAPIPath(path); err != nil {
		return err
	}
	m.onNodeAPIPathChange(path)
	return nil
}

// onNodeAPIPathChange is set by the server so a freshly-generated node-API segment
// takes effect without a restart. nil-safe for tests/CLI that never serve.
func (m *Manager) onNodeAPIPathChange(path string) {
	m.nodePathMu.Lock()
	cb := m.nodePathCB
	m.nodePathMu.Unlock()
	if cb != nil {
		cb(path)
	}
}

// SetNodeAPIPathCallback registers the live-swap hook (called by the router).
func (m *Manager) SetNodeAPIPathCallback(cb func(string)) {
	m.nodePathMu.Lock()
	m.nodePathCB = cb
	m.nodePathMu.Unlock()
}

// randomPathSegment mints an unguessable URL segment for the node-API mount,
// reusing the same generator as the panel secret path.
func randomPathSegment() (string, error) {
	return auth.RandomSecretPath()
}

// validateDNSList refuses a DNS setting the generated Xray config could not parse. nil
// (inherit / leave alone) and an empty string are both fine.
func validateDNSList(dns *string) error {
	if dns == nil {
		return nil
	}
	for _, e := range strings.FieldsFunc(*dns, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' '
	}) {
		switch xray.CheckDNSServer(e) {
		case xray.DNSOK:
		case xray.DNSScheme:
			return invalidCode("err.dnsScheme",
				"DNS {{detail}}: у Xray нет клиента для этой схемы — подойдут https://, h2c://, tcp://, их варианты +local и quic+local://",
				map[string]any{"detail": e})
		case xray.DNSPort:
			return invalidCode("err.dnsPort",
				"DNS {{detail}}: порт указывается только в URL, например tcp://{{detail}}",
				map[string]any{"detail": e})
		default:
			return invalidCode("err.badDNS", "неверный DNS-адрес: {{detail}}", map[string]any{"detail": e})
		}
	}
	return nil
}
