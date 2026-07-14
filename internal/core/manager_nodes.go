package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// nodeSettings materializes a node's effective settings: the global settings row
// with the node's own identity (address, TLS, REALITY) and protocol overrides
// applied. Everything else — ports, hop range, fingerprints, sub delivery —
// inherits from global, so xray.Generate, the link builders and tlsmgr all work
// for a remote node without changes.
//
// Egress routing (proxy lanes, WARP, Opera) is stripped: v1 nodes egress direct.
// A remote node has no access to the panel's proxy pool or WARP registration, and
// pushing panel-only lanes would just produce a config it can't honor.
func nodeSettings(set *model.Settings, n *model.Node) *model.Settings {
	ns := *set // shallow copy; we only overwrite value fields below
	ns.Host = n.Host
	ns.SNI = n.Host
	ns.RealityPrivateKey = n.RealityPrivateKey
	ns.RealityPublicKey = n.RealityPublicKey
	ns.RealityShortID = n.RealityShortID
	ns.RealityServiceName = n.RealityServiceName

	// Per-node protocol overrides (nil ⇒ inherit global).
	ns.VLESSEnabled = model.NodeProtoEnabled(n.VLESSEnabled, set.VLESSEnabled)
	ns.TrojanEnabled = model.NodeProtoEnabled(n.TrojanEnabled, set.TrojanEnabled)
	ns.HysteriaEnabled = model.NodeProtoEnabled(n.HysteriaEnabled, set.HysteriaEnabled)
	ns.RealityEnabled = model.NodeProtoEnabled(n.RealityEnabled, set.RealityEnabled)

	// TLS hints for this node's share links come from what the node reported about
	// its live cert — the panel can't read the remote node's disk.
	ns.TLSInsecure = n.CertSelfSigned
	ns.TLSPinSHA256 = ""
	if n.CertSelfSigned {
		ns.TLSPinSHA256 = n.CertSHA256
	}

	// Routing: the node's own override, or the panel's routing inherited. Either way
	// the egress-lane parts are stripped — a node has no proxy pool / WARP / Opera,
	// so those rules degrade to direct. WARP/Opera outbounds are force-disabled.
	routing := set.Routing
	if n.Routing != nil {
		routing = *n.Routing
	}
	ns.Routing = routing.WithoutEgressLanes()
	ns.WarpEnabled = false
	ns.OperaEnabled = false

	// DNS: the node's own override, or the panel's inherited.
	if n.XrayDNS != nil {
		ns.XrayDNS = *n.XrayDNS
	}
	return &ns
}

// NodeDesiredState builds the full desired state for a node: its Xray config
// (generated panel-side from nodeSettings + the working user set), the host-level
// meta the agent needs, and a hash over both so the sync handler can skip no-ops.
func (m *Manager) NodeDesiredState(n *model.Node) (*nodeapi.NodeState, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	users, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		return nil, err
	}
	ns := nodeSettings(set, n)
	// Cert paths are sentinels the agent rewrites to its own absolute paths (the
	// panel doesn't know the node's data dir); keeping them symbolic makes the hash
	// independent of where the node stores its certs.
	ns.CertPath = nodeapi.CertPathSentinel
	ns.KeyPath = nodeapi.KeyPathSentinel
	// The node's own fallback points at its local decoy/panel loopback, same as the
	// panel's own layout.
	cfg, err := xray.Generate(ns, users, m.opts, nil)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	connGuardPorts := []int{ns.VLESSPort}
	if ns.RealityEnabled {
		connGuardPorts = append(connGuardPorts, ns.RealityPort)
	}
	meta := nodeapi.NodeMeta{
		Host:              n.Host,
		SNI:               n.Host,
		ACMEEmail:         set.ACMEEmail,
		ACMEProvider:      set.ACMEProvider,
		ZeroSSLEABKID:     set.ZeroSSLEABKID,
		ZeroSSLEABHMAC:    set.ZeroSSLEABHMAC,
		HysteriaEnabled:   ns.HysteriaEnabled,
		HysteriaPort:      set.HysteriaPort,
		HopStart:          set.HopStart,
		HopEnd:            set.HopEnd,
		ConnGuardPorts:    connGuardPorts,
		LoopbackDest:      m.opts.PanelDest,
		DecoyTemplate:     n.DecoyTemplate,
		XrayPinnedVersion: xray.PinnedVersion,
	}
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(append(raw, metaRaw...))
	return &nodeapi.NodeState{
		Hash:       hex.EncodeToString(h[:]),
		XrayConfig: raw,
		Meta:       meta,
	}, nil
}

// --- node wake registry -------------------------------------------------------
//
// Each connected node's sync handler parks on a wake channel; a config change
// (user add/remove, node edit) closes it so the held poll returns immediately and
// re-pushes the fresh desired state. Panels with no connected nodes pay nothing.

type nodeRegistry struct {
	mu    sync.Mutex
	waits map[int64]chan struct{}
}

func newNodeRegistry() *nodeRegistry { return &nodeRegistry{waits: map[int64]chan struct{}{}} }

// wakeChan returns the current wake channel for a node, creating it on first use.
func (r *nodeRegistry) wakeChan(nodeID int64) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.waits[nodeID]
	if !ok {
		ch = make(chan struct{})
		r.waits[nodeID] = ch
	}
	return ch
}

// wakeOne closes and replaces one node's wake channel (any parked poll returns and
// re-parks on the fresh channel). It only acts on an existing entry: a poll always
// registers its channel via wakeChan before computing desired state, so there is
// nothing to wake until then — and not creating entries here keeps the map from
// accumulating channels for nodes that never poll.
func (r *nodeRegistry) wakeOne(nodeID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waits[nodeID]; ok {
		close(ch)
		r.waits[nodeID] = make(chan struct{})
	}
}

// dropWaiter wakes and removes a node's entry (used on delete, so a tombstoned
// node's channel isn't retained forever).
func (r *nodeRegistry) dropWaiter(nodeID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waits[nodeID]; ok {
		close(ch)
		delete(r.waits, nodeID)
	}
}

// wakeAll wakes every parked node — used after a user-set change that fans out to
// all nodes.
func (r *nodeRegistry) wakeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, ch := range r.waits {
		close(ch)
		r.waits[id] = make(chan struct{})
	}
}

// NodeWakeChan exposes a node's wake channel to the sync handler.
func (m *Manager) NodeWakeChan(nodeID int64) <-chan struct{} { return m.nodes.wakeChan(nodeID) }

// notifyNodes wakes all connected nodes so they re-pull desired state. Called
// after every reconcile/user-sync and after node edits.
func (m *Manager) notifyNodes() { m.nodes.wakeAll() }

// NodeView is one row for the Nodes UI: the node's identity and status plus its
// effective (override-resolved) protocol toggles and today's traffic. The local
// server appears as node 0 (IsLocal) so the UI lists every server uniformly.
type NodeView struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	Enabled         bool   `json:"enabled"`
	IsLocal         bool   `json:"is_local"`
	Online          bool   `json:"online"`
	Joined          bool   `json:"joined"`
	LastSeen        int64  `json:"last_seen"`
	NodeVersion     string `json:"node_version"`
	XrayVersion     string `json:"xray_version"`
	XrayRunning     bool   `json:"xray_running"`
	VersionSkew     bool   `json:"version_skew"` // running Xray differs from the pinned release
	VLESSEnabled    bool   `json:"vless_enabled"`
	TrojanEnabled   bool   `json:"trojan_enabled"`
	HysteriaEnabled bool   `json:"hysteria_enabled"`
	RealityEnabled  bool   `json:"reality_enabled"`
	DecoyTemplate   string `json:"decoy_template"`
	TrafficUp       int64  `json:"traffic_up"`   // today, this node
	TrafficDown     int64  `json:"traffic_down"` // today, this node
	// Overrides expose which protocol toggles are node-specific (non-nil) vs
	// inherited, so the UI can show an "inherited" state.
	Overrides NodeProtoOverrides `json:"overrides"`
	// Routing / XrayDNS carry the node's own override (nil ⇒ inherits the panel's),
	// so the per-node routing+DNS editor can prefill and show inherit vs custom.
	Routing   *model.RoutingConfig `json:"routing"`
	XrayDNS   *string              `json:"xray_dns"`
	JoinToken string               `json:"join_token,omitempty"` // only right after create/regen
	// MasterLabel is the master server's config-label name (local node only), so the
	// UI can edit it. Empty for remote nodes (they use their own Name).
	MasterLabel string `json:"master_label,omitempty"`
}

// NodeProtoOverrides marks which protocol toggles a node overrides (true) vs
// inherits from global (false).
type NodeProtoOverrides struct {
	VLESS    bool `json:"vless"`
	Trojan   bool `json:"trojan"`
	Hysteria bool `json:"hysteria"`
	Reality  bool `json:"reality"`
}

// NodeViews returns the local server (node 0) followed by every remote node, each
// with resolved protocols and today's traffic, for the Nodes UI.
func (m *Manager) NodeViews() ([]NodeView, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil, err
	}
	today := time.Now().In(m.loc()).Format("2006-01-02")
	traffic, _ := m.store.NodeTrafficTotals(today, today)
	now := time.Now().Unix()

	views := make([]NodeView, 0, len(nodes)+1)
	// Node 0: the panel's own server, identity from settings.
	local := NodeView{
		ID:              model.LocalNodeID,
		Name:            "Этот сервер",
		Host:            set.Host,
		Enabled:         true,
		IsLocal:         true,
		Online:          m.sup.Running(),
		Joined:          true,
		XrayRunning:     m.sup.Running(),
		XrayVersion:     m.sup.Version(),
		VLESSEnabled:    set.VLESSEnabled,
		TrojanEnabled:   set.TrojanEnabled,
		HysteriaEnabled: set.HysteriaEnabled,
		RealityEnabled:  set.RealityEnabled,
		DecoyTemplate:   set.DecoyTemplate,
		MasterLabel:     set.MasterLabel,
	}
	if t, ok := traffic[model.LocalNodeID]; ok {
		local.TrafficUp, local.TrafficDown = t[0], t[1]
	}
	views = append(views, local)

	for i := range nodes {
		n := &nodes[i]
		v := NodeView{
			ID:              n.ID,
			Name:            n.Name,
			Host:            n.Host,
			Enabled:         n.Enabled,
			Online:          n.Online(now),
			Joined:          n.Joined(),
			LastSeen:        n.LastSeen,
			NodeVersion:     n.NodeVersion,
			XrayVersion:     n.XrayVersion,
			XrayRunning:     n.XrayRunning,
			VersionSkew:     n.XrayVersion != "" && !xray.VersionMatchesPinned(n.XrayVersion),
			VLESSEnabled:    model.NodeProtoEnabled(n.VLESSEnabled, set.VLESSEnabled),
			TrojanEnabled:   model.NodeProtoEnabled(n.TrojanEnabled, set.TrojanEnabled),
			HysteriaEnabled: model.NodeProtoEnabled(n.HysteriaEnabled, set.HysteriaEnabled),
			RealityEnabled:  model.NodeProtoEnabled(n.RealityEnabled, set.RealityEnabled),
			DecoyTemplate:   n.DecoyTemplate,
			Overrides: NodeProtoOverrides{
				VLESS:    n.VLESSEnabled != nil,
				Trojan:   n.TrojanEnabled != nil,
				Hysteria: n.HysteriaEnabled != nil,
				Reality:  n.RealityEnabled != nil,
			},
			Routing: n.Routing,
			XrayDNS: n.XrayDNS,
		}
		if t, ok := traffic[n.ID]; ok {
			v.TrafficUp, v.TrafficDown = t[0], t[1]
		}
		views = append(views, v)
	}
	return views, nil
}

// NodeLinkSettings returns per-node settings clones for share-link/subscription
// generation: one for each enabled node that has connected at least once (so links
// point at a live server with a known cert), each carrying its NodeLabel and TLS
// hints. The local server is NOT included — the caller prepends it (with its own
// TLS hints applied by the server layer). Returns nil when there are no such nodes,
// so a single-server install produces byte-identical output.
func (m *Manager) NodeLinkSettings() ([]*model.Settings, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil, err
	}
	var out []*model.Settings
	seen := map[string]int{}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || n.LastSeen == 0 {
			continue // disabled, or never installed → don't hand clients a dead link
		}
		ns := nodeSettings(set, n)
		// Uniqueness is enforced on create/edit, but defend the subscription anyway:
		// a duplicate label would collide Clash proxy names / sing-box tags and make a
		// client reject the whole config. Disambiguate any collision with the node id.
		label := n.Name
		if seen[label] > 0 {
			label = fmt.Sprintf("%s #%d", n.Name, n.ID)
		}
		seen[n.Name]++
		ns.NodeLabel = label
		out = append(out, ns)
	}
	return out, nil
}

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
		return nil, &ValidationError{Msg: "нода с таким названием уже есть — имя должно быть уникальным"}
	}
	if err := m.EnsureNodeAPIPath(); err != nil {
		return nil, err
	}
	decoyTemplate, err := m.randomDecoy()
	if err != nil {
		return nil, err
	}
	return m.store.CreateNode(name, host, decoyTemplate)
}

// UpdateNode edits a node and wakes it so config/link changes apply promptly.
func (m *Manager) UpdateNode(id int64, e store.NodeEdit) error {
	if taken, err := m.store.NodeNameTaken(e.Name, id); err != nil {
		return err
	} else if taken {
		return &ValidationError{Msg: "нода с таким названием уже есть — имя должно быть уникальным"}
	}
	if e.Routing != nil {
		if err := e.Routing.ValidateLanes(); err != nil {
			return &ValidationError{Msg: err.Error()}
		}
	}
	if err := m.store.UpdateNode(id, e); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// SetNodeEnabled toggles a node and wakes it (a disabled node is told to stop).
func (m *Manager) SetNodeEnabled(id int64, enabled bool) error {
	if err := m.store.SetNodeEnabled(id, enabled); err != nil {
		return err
	}
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
	m.nodes.dropWaiter(id)
	return nil
}

// RegenJoinToken issues a fresh install token for an existing node.
func (m *Manager) RegenJoinToken(id int64) (string, error) { return m.store.RegenJoinToken(id) }

// SetMasterLabel sets the panel server's display name used in config labels.
func (m *Manager) SetMasterLabel(label string) error {
	return m.store.SetMasterLabel(strings.TrimSpace(label))
}

// RequestNodeUpdate flags a node to self-update on its next sync, and wakes it so
// it happens promptly. Returns an error if the node doesn't exist.
func (m *Manager) RequestNodeUpdate(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return &ValidationError{Msg: "нода не найдена"}
	}
	m.nodeUpdateMu.Lock()
	m.nodeUpdateWanted[id] = true
	m.nodeUpdateMu.Unlock()
	m.nodes.wakeOne(id)
	return nil
}

// RequestAllNodesUpdate flags every enabled, connected node to self-update.
func (m *Manager) RequestAllNodesUpdate() (int, error) {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return 0, err
	}
	n := 0
	m.nodeUpdateMu.Lock()
	for i := range nodes {
		if nodes[i].Enabled && nodes[i].LastSeen > 0 {
			m.nodeUpdateWanted[nodes[i].ID] = true
			n++
		}
	}
	m.nodeUpdateMu.Unlock()
	m.notifyNodes()
	return n, nil
}

// nodeLogsWantWindow is how long after an operator opens a node's logs the panel
// keeps asking that node to include its log tail (so viewing keeps refreshing, then
// stops on its own when the operator navigates away).
const nodeLogsWantWindow = 30 * time.Second

// RequestNodeLogs marks that an operator is viewing a node's logs and wakes it, so
// the node includes its log tail on the next sync. Returns the currently-stored
// tail (may be from a previous fetch) for an immediate render.
func (m *Manager) RequestNodeLogs(id int64) ([]string, int64) {
	m.nodeLogsMu.Lock()
	m.nodeLogsWanted[id] = time.Now().Unix()
	e := m.nodeLogs[id]
	m.nodeLogsMu.Unlock()
	m.nodes.wakeOne(id) // return the held poll promptly so the tail comes back fast
	return e.lines, e.at
}

// WantNodeLogs reports (and is used by the sync handler to set WantLogs) whether an
// operator is currently viewing this node's logs.
func (m *Manager) WantNodeLogs(id int64) bool {
	m.nodeLogsMu.Lock()
	defer m.nodeLogsMu.Unlock()
	last, ok := m.nodeLogsWanted[id]
	return ok && time.Now().Unix()-last < int64(nodeLogsWantWindow/time.Second)
}

// storeNodeLogs records a node's reported log tail.
func (m *Manager) storeNodeLogs(id int64, lines []string) {
	if len(lines) == 0 {
		return
	}
	m.nodeLogsMu.Lock()
	m.nodeLogs[id] = nodeLogEntry{lines: lines, at: time.Now().Unix()}
	m.nodeLogsMu.Unlock()
}

// TakeNodeUpdate consumes (and clears) a node's pending self-update flag.
func (m *Manager) TakeNodeUpdate(id int64) bool {
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	if m.nodeUpdateWanted[id] {
		delete(m.nodeUpdateWanted, id)
		return true
	}
	return false
}

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

// randomDecoy picks a bundled decoy template at random so nodes don't all share
// the panel's masquerade fingerprint. Falls back to "" (agent default) on error.
func (m *Manager) randomDecoy() (string, error) {
	list, err := decoy.Available()
	if err != nil || len(list) == 0 {
		return "", err
	}
	// Cheap, non-crypto pick: which masquerade a node wears isn't a secret.
	return list[time.Now().UnixNano()%int64(len(list))], nil
}

// --- sync ingest --------------------------------------------------------------

// IngestNodeSync records a node's reported status, ingests its traffic deltas
// idempotently, and computes the response (whether the node's applied hash still
// matches desired state). It does NOT block for the long-poll — the handler owns
// the hold; this is the pure state transition.
func (m *Manager) IngestNodeSync(n *model.Node, req nodeapi.SyncRequest) (*nodeapi.SyncResponse, error) {
	now := time.Now()
	if len(req.Logs) > 0 {
		m.storeNodeLogs(n.ID, req.Logs)
	}
	_ = m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen:       now.Unix(),
		NodeVersion:    req.NodeVersion,
		XrayVersion:    req.XrayVersion,
		XrayRunning:    req.XrayRunning,
		CertSHA256:     req.CertSHA256,
		CertSelfSigned: req.CertSelfSigned,
		ConfigHash:     req.ConfigHash,
	})

	// Idempotent traffic ingest: atomically claim the report id. A report at-or-
	// below the stored watermark is a retry of an already-counted batch (lost
	// response) or a restarted agent replaying a reset counter; the conditional
	// claim also stops two concurrent syncs from both counting the same batch.
	if req.ReportID > 0 {
		if claimed, _ := m.store.ClaimNodeReport(n.ID, req.ReportID); claimed && len(req.Traffic) > 0 {
			today := now.In(m.loc()).Format("2006-01-02")
			snapshot, _ := m.store.ListUsers()
			for _, d := range req.Traffic {
				up, down := nonNeg(d.Up), nonNeg(d.Down)
				if up == 0 && down == 0 {
					continue
				}
				_ = m.store.AddUsedTraffic(d.UserID, up, down)
				_ = m.store.AddDailyTrafficNode(d.UserID, n.ID, today, up, down)
				_ = m.store.TouchLastSeen(d.UserID, now.Unix())
			}
			_ = m.enforceAfterTraffic(snapshot)
		}
	}

	resp := &nodeapi.SyncResponse{AckReport: req.ReportID}
	if !n.Enabled {
		resp.Revoked = true
		return resp, nil
	}
	state, err := m.NodeDesiredState(n)
	if err != nil {
		return nil, err
	}
	if state.Hash != req.ConfigHash {
		resp.Changed = true
		resp.State = state
	}
	return resp, nil
}

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
