package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/tlsmgr"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
	"github.com/AppsGanin/rospanel/internal/warp"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// nodeSettings materializes a node's effective settings: the global settings row
// with the node's own identity (address, TLS, REALITY) and protocol overrides
// applied. Everything else — ports, hop range, fingerprints, sub delivery —
// inherits from global, so xray.Generate, the link builders and tlsmgr all work
// for a remote node without changes.
//
// Egress (proxy lanes, WARP, Opera) is the node's OWN and independent of the master:
// each server has its own proxy pool, its own WARP registration and its own Opera
// helper. All egress is off by default, so a node with no config egresses direct.
func nodeSettings(set *model.Settings, n *model.Node) *model.Settings {
	ns := *set // shallow copy; we only overwrite value fields below
	ns.ServerID = n.ID
	ns.ServerPlacement = n.Placement
	ns.Host = n.Host
	ns.SNI = n.Host
	ns.RealityPrivateKey = n.RealityPrivateKey
	ns.RealityPublicKey = n.RealityPublicKey
	ns.RealityShortID = n.RealityShortID
	ns.RealityPath = n.RealityPath
	// AmneziaWG: the node's own identity, never the master's; the port, name and
	// DNS ride in the connections blob below (off ⇒ zero port ⇒ no config).
	ns.AWGEnabled = derefBool(n.AWGEnabled)
	ns.AWGPrivateKey = n.AWGPrivateKey
	ns.AWGPublicKey = n.AWGPublicKey
	ns.AWGParams = n.AWGParams
	ns.AWGPort, ns.AWGName, ns.AWGDNS = 0, "", ""
	// REALITY donor: the node's own if set, otherwise inherit the panel's (a node
	// needs some donor for REALITY to work).
	if n.RealityDest != "" {
		ns.RealityDest = n.RealityDest
	}

	// A node's protocols are its OWN — no inheritance from the master. Unset ⇒ off.
	ns.VLESSEnabled = derefBool(n.VLESSEnabled)
	ns.HysteriaEnabled = derefBool(n.HysteriaEnabled)
	ns.RealityEnabled = derefBool(n.RealityEnabled)

	// TLS hints for this node's share links come from what the node reported about
	// its live cert — the panel can't read the remote node's disk.
	ns.TLSInsecure = n.CertSelfSigned
	ns.TLSPinSHA256 = ""
	if n.CertSelfSigned {
		ns.TLSPinSHA256 = n.CertSHA256
	}

	// Routing + egress are the node's OWN (each server is independent — a node does
	// not borrow the master's lanes/WARP/Opera, which point at the master's backends).
	// Nil routing ⇒ empty (direct). All egress is off by default, so a node with no
	// config produces the same "direct" output as before.
	if n.Routing != nil {
		ns.Routing = *n.Routing
	} else {
		ns.Routing = model.RoutingConfig{}
	}
	ns.WarpEnabled = n.WarpEnabled
	ns.WarpPrivateKey = n.WarpPrivateKey
	ns.WarpPublicKey = n.WarpPublicKey
	ns.WarpEndpoint = n.WarpEndpoint
	ns.WarpAddressV4 = n.WarpAddressV4
	ns.WarpAddressV6 = n.WarpAddressV6
	ns.WarpReserved = n.WarpReserved
	ns.OperaEnabled = n.OperaEnabled
	ns.OperaCountry = n.OperaCountry

	// System proxies are the node's OWN, never the master's: inheriting would open a
	// listener on every node the moment the master enabled one, and would write the
	// master's proxy password onto every node's disk.
	ns.ProxySocksEnabled = n.Proxy.SocksEnabled
	ns.ProxySocksPort = n.Proxy.SocksPort
	ns.ProxyHTTPEnabled = n.Proxy.HTTPEnabled
	ns.ProxyHTTPPort = n.Proxy.HTTPPort
	ns.ProxyAccounts = n.Proxy.Accounts

	// DNS: the node's OWN (no inheritance). Unset ⇒ Xray's default resolver.
	if n.XrayDNS != nil {
		ns.XrayDNS = *n.XrayDNS
	} else {
		ns.XrayDNS = ""
	}

	// Connection transport: the node's own if configured, otherwise inherit the
	// master's (ns already carries the master's values from the shallow copy).
	if c := n.Connections; c != nil {
		ns.HysteriaPort = c.HysteriaPort
		ns.HopStart = c.HopStart
		ns.HopEnd = c.HopEnd
		ns.HopInterval = c.HopInterval
		ns.RealityPort = c.RealityPort
		ns.RealityMaxTimeDiff = c.RealityMaxTimeDiff
		ns.TLSFragment = c.TLSFragment
		ns.TLSMin13 = c.TLSMin13
		ns.BlockQUIC = c.BlockQUIC
		ns.VLESSFp = c.VLESSFp
		ns.RealityFp = c.RealityFp
		ns.VLESSName = c.VLESSName
		ns.RealityName = c.RealityName
		ns.HysteriaName = c.HysteriaName
		ns.AWGPort = c.AWGPort
		ns.AWGName = c.AWGName
		ns.AWGDNS = c.AWGDNS
	}
	return &ns
}

// derefBool resolves an optional per-node bool to its value, treating unset as false
// (a node's toggles are its own — nothing is inherited from the master).
func derefBool(b *bool) bool { return b != nil && *b }

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
	// panel's own layout. Egress lanes resolve against the node's OWN proxy pool.
	opts, err := m.genOptsFor(n.ID)
	if err != nil {
		return nil, err
	}
	cfg, err := xray.Generate(ns, users, opts, m.getNodeProxies(n.ID))
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
	// Custom inbounds get the same per-IP flood guard as the built-in lanes — they are
	// public listeners on the same box, and leaving them out would make "add a custom
	// inbound" quietly the way to bypass the guard. Only the TCP ones: the guard's
	// rules count connections, which UDP/QUIC has none of.
	for _, in := range opts.Custom {
		if in.Protocol != model.InbHysteria {
			connGuardPorts = append(connGuardPorts, in.Port)
		}
	}
	// ACME: the node's own provider/email/EAB when set, otherwise the panel's.
	acmeEmail := set.ACMEEmail
	if n.ACMEEmail != "" {
		acmeEmail = n.ACMEEmail
	}
	acmeProvider, eabKID, eabHMAC := set.ACMEProvider, set.ZeroSSLEABKID, set.ZeroSSLEABHMAC
	if n.ACMEProvider != "" {
		acmeProvider, eabKID, eabHMAC = n.ACMEProvider, n.ZeroSSLEABKID, n.ZeroSSLEABHMAC
	}
	meta := nodeapi.NodeMeta{
		Host:              n.Host,
		SNI:               n.Host,
		ACMEEmail:         acmeEmail,
		ACMEProvider:      acmeProvider,
		ZeroSSLEABKID:     eabKID,
		ZeroSSLEABHMAC:    eabHMAC,
		HysteriaEnabled:   ns.HysteriaEnabled,
		HysteriaPort:      ns.HysteriaPort,
		HopStart:          ns.HopStart,
		HopEnd:            ns.HopEnd,
		HopRanges:         nodeHopMeta(ns, opts.Custom),
		ConnGuardPorts:    connGuardPorts,
		LoopbackDest:      m.opts.PanelDest,
		DecoyTemplate:     n.DecoyTemplate,
		GeoRefreshHours:   n.GeoRefreshHours, // the node's OWN geo cadence
		XrayPinnedVersion: xray.PinnedVersion,
		SpeedLimits:       m.SpeedLimits(),
	}
	if access, err := m.store.AccessMap(); err == nil {
		meta.AWG = m.nodeAWGState(n, ns, users, access)
	}
	// What the source policy has refused, for this node's own firewall. Read here
	// rather than pushed on each block so a node that was offline catches up on its
	// next sync, and so the hash covers it (a lifted block reaches the node too).
	if blocked, err := m.store.BlockedIPList(); err == nil && len(blocked) > 0 {
		meta.BlockedIPs = blocked
		meta.BlockTTLHours = int(policyTTL(set.ConnPolicy) / time.Hour)
	}
	if ns.OperaEnabled {
		meta.OperaEnabled = true
		meta.OperaCountry = ns.OperaCountryOr()
		meta.OperaPort = ns.OperaPortOr()
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

// NodeXrayConfig returns one server's Xray config for the read-only viewer: the
// master's live on-disk config.json for node 0, and for a remote node the config
// the panel generates and pushes (the same bytes the node applies).
//
// The node's copy differs in one respect: the cert/key paths are still the panel's
// sentinels here, because only the agent knows its own data dir — it substitutes
// them before handing the config to Xray. The viewer says so.
func (m *Manager) NodeXrayConfig(id int64) ([]byte, error) {
	if id == model.LocalNodeID {
		return m.XrayConfig()
	}
	n, err := m.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	state, err := m.NodeDesiredState(n)
	if err != nil {
		return nil, err
	}
	return state.XrayConfig, nil
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
	if r == nil {
		return // no registry (tests) ⇒ no parked poll to wake
	}
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
// all nodes. Nil-safe: a Manager assembled without a registry (tests) still has to
// survive the paths that now reach here, and "no registry" means "no node to wake".
func (r *nodeRegistry) wakeAll() {
	if r == nil {
		return
	}
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
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Enabled  bool   `json:"enabled"`
	IsLocal  bool   `json:"is_local"`
	Online   bool   `json:"online"`
	Joined   bool   `json:"joined"`
	LastSeen int64  `json:"last_seen"`
	// CreatedAt is when the node was registered. Carried because GET /v1/nodes/{id}
	// used to answer the raw nodes row, which published it — switching that route to
	// this view would otherwise have quietly dropped a documented field. 0 for the
	// local server, which was never registered.
	CreatedAt   int64  `json:"created_at"`
	NodeVersion string `json:"node_version"`
	XrayVersion string `json:"xray_version"`
	XrayRunning bool   `json:"xray_running"`
	VersionSkew bool   `json:"version_skew"` // running Xray differs from the pinned release
	// SyncFails is the node's last-reported count of sync failures in the past hour.
	// Nonzero means its long-poll to the panel is limping (transport degraded) even
	// though last_seen keeps advancing and the node still looks online. 0 for the local
	// server (it has no sync of its own).
	SyncFails int `json:"sync_fails"`
	// XrayRestart is the state of an operator-requested Xray bounce: "pending" while
	// the node has yet to prove it happened, then "done" or "timeout" briefly, then
	// "". Always "" for the master, whose restart is synchronous — nothing to wait for.
	XrayRestart     string `json:"xray_restart,omitempty"`
	VLESSEnabled    bool   `json:"vless_enabled"`
	HysteriaEnabled bool   `json:"hysteria_enabled"`
	RealityEnabled  bool   `json:"reality_enabled"`
	DecoyTemplate   string `json:"decoy_template"`
	// CertSelfSigned is what the node last reported about its live TLS cert: true ⇒
	// still on the self-signed fallback (ACME not obtained yet), false ⇒ a CA cert is
	// in place. Lets the node's Domain tab show the cert status like the master's.
	CertSelfSigned bool   `json:"cert_self_signed"`
	CertIssuer     string `json:"cert_issuer"`     // ≈ ACME provider (empty for the local node)
	CertExpiresAt  int64  `json:"cert_expires_at"` // unix; 0 ⇒ unknown
	// GeoRefreshHours is this server's own geo auto-refresh cadence (hours; 0 ⇒ never).
	GeoRefreshHours int   `json:"geo_refresh_hours"`
	TrafficUp       int64 `json:"traffic_up"`   // today, this node
	TrafficDown     int64 `json:"traffic_down"` // today, this node

	// The machine this server runs on, as it last reported (the master fills these
	// from its own sampler). HasHostStats is false when nothing has been reported
	// yet — a node that never checked in, or an agent older than the fields — and the
	// rest must then be read as unknown rather than as an idle machine.
	HasHostStats bool    `json:"has_host_stats"`
	CPUPercent   float64 `json:"cpu_percent"`
	MemUsed      int64   `json:"mem_used"`
	MemTotal     int64   `json:"mem_total"`
	DiskUsed     int64   `json:"disk_used"`
	DiskTotal    int64   `json:"disk_total"`
	HostUptime   int64   `json:"host_uptime"`
	NetUp        int64   `json:"net_up"`
	NetDown      int64   `json:"net_down"`
	// Routing / XrayDNS carry the node's own config (nil ⇒ the node runs with an EMPTY
	// routing/DNS, never the master's — see nodeSettings and model.Node), so the
	// per-node routing+DNS editor can prefill and show set vs unset. For
	// the local server (node 0) these carry the master's own routing/DNS so the same
	// editor edits the master.
	Routing *model.RoutingConfig `json:"routing"`
	XrayDNS *string              `json:"xray_dns"`
	// Proxy is this server's system proxy (SOCKS/HTTP listeners for non-VPN traffic).
	// Carries the account so the page can show a ready-to-paste address — it is an
	// operator screen, and the password is the point of the feature.
	Proxy model.SystemProxy `json:"proxy"`
	// Egress backends (node's own, independent of the master; all off by default).
	// WARP is native to Xray once registered; Opera runs a helper on the node.
	WarpEnabled    bool   `json:"warp_enabled"`
	WarpRegistered bool   `json:"warp_registered"`
	OperaEnabled   bool   `json:"opera_enabled"`
	OperaCountry   string `json:"opera_country"`
	// TrafficCoefficient scales quota consumption on this server (1.0 = neutral). The
	// master's row (node 0) always reports 1.0 — it has no coefficient of its own.
	TrafficCoefficient float64 `json:"traffic_coefficient"`
	// REALITY identity (per-server). RealityDest is this server's own donor ("" on a
	// node ⇒ inherits the panel's); the public key/shortId/service are shown so the
	// operator can see them and regenerate. The private key is never exposed.
	RealityDest      string `json:"reality_dest"`
	RealityPublicKey string `json:"reality_public_key"`
	RealityShortID   string `json:"reality_short_id"`
	RealityPath      string `json:"reality_path"`
	JoinToken        string `json:"join_token,omitempty"` // only right after create/regen
	// MasterLabel is the master server's config-label name (local node only), so the
	// UI can edit it. Empty for remote nodes (they use their own Name).
	MasterLabel string `json:"master_label,omitempty"`
	// Placement (country, weight, capacity) and the live online-user count the
	// subscription orders servers by; see model.Placement and sub.Order.
	model.Placement
	OnlineUsers int `json:"online_users"`
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
	traffic, _ := m.store.NodeTrafficTotals(0, today, today)
	now := time.Now().Unix()
	online := m.OnlineByServer()

	views := make([]NodeView, 0, len(nodes)+1)
	// Node 0: the panel's own server, identity from settings.
	local := NodeView{
		ID:      model.LocalNodeID,
		Name:    model.LocalNodeName,
		Host:    set.Host,
		Enabled: true,
		IsLocal: true,
		Online:  m.sup.Running(),
		Joined:  true,
		// Serving so the master's own row doesn't flash amber during a deliberate
		// restart, matching how a node reports itself (see Supervisor.Serving).
		XrayRunning:     m.sup.Serving(),
		XrayVersion:     m.sup.Version(),
		VLESSEnabled:    set.VLESSEnabled,
		HysteriaEnabled: set.HysteriaEnabled,
		RealityEnabled:  set.RealityEnabled,
		DecoyTemplate:   set.DecoyTemplate,
		MasterLabel:     set.MasterLabel,
		Placement:       set.MasterPlacement,
		OnlineUsers:     online[model.LocalNodeID],
		// The master's own routing/DNS/egress, so the relocated per-server editor edits
		// the master through the same controls as a node.
		Routing:        &set.Routing,
		XrayDNS:        &set.XrayDNS,
		WarpEnabled:    set.WarpEnabled,
		WarpRegistered: set.WarpRegistered(),
		OperaEnabled:   set.OperaEnabled,
		OperaCountry:   set.OperaCountryOr(),
		// The master counts traffic at face value — no coefficient of its own.
		TrafficCoefficient: 1.0,
		// The master's own REALITY identity.
		RealityDest:      set.RealityDest,
		RealityPublicKey: set.RealityPublicKey,
		RealityShortID:   set.RealityShortID,
		RealityPath:      set.RealityPath,
		GeoRefreshHours:  set.GeoRefreshHours,
		Proxy: model.SystemProxy{
			SocksEnabled: set.ProxySocksEnabled, SocksPort: set.ProxySocksPort,
			HTTPEnabled: set.ProxyHTTPEnabled, HTTPPort: set.ProxyHTTPPort,
			Accounts: set.ProxyAccounts,
		},
	}
	if t, ok := traffic[model.LocalNodeID]; ok {
		local.TrafficUp, local.TrafficDown = t[0], t[1]
	}
	// The master samples its own machine directly rather than reporting to itself.
	if m.sys != nil {
		st := m.sys.Read()
		local.HasHostStats = true
		local.CPUPercent, local.NetUp, local.NetDown = st.CPUPercent, st.NetUp, st.NetDown
		local.MemUsed, local.MemTotal = st.MemUsed, st.MemTotal
		local.DiskUsed, local.DiskTotal = st.DiskUsed, st.DiskTotal
		local.HostUptime = st.HostUptime
	}
	views = append(views, local)

	for i := range nodes {
		n := &nodes[i]
		v := NodeView{
			ID:                 n.ID,
			Name:               n.Name,
			Host:               n.Host,
			Enabled:            n.Enabled,
			Online:             n.Online(now),
			Joined:             n.Joined(),
			LastSeen:           n.LastSeen,
			CreatedAt:          n.CreatedAt,
			NodeVersion:        n.NodeVersion,
			XrayVersion:        n.XrayVersion,
			XrayRunning:        n.XrayRunning,
			VersionSkew:        n.XrayVersion != "" && !xray.VersionMatchesPinned(n.XrayVersion),
			XrayRestart:        m.NodeRestartState(n.ID),
			VLESSEnabled:       derefBool(n.VLESSEnabled),
			HysteriaEnabled:    derefBool(n.HysteriaEnabled),
			RealityEnabled:     derefBool(n.RealityEnabled),
			DecoyTemplate:      n.DecoyTemplate,
			CertSelfSigned:     n.CertSelfSigned,
			CertIssuer:         n.CertIssuer,
			CertExpiresAt:      n.CertExpiresAt,
			GeoRefreshHours:    n.GeoRefreshHours,
			Routing:            n.Routing,
			XrayDNS:            n.XrayDNS,
			WarpEnabled:        n.WarpEnabled,
			WarpRegistered:     n.WarpRegistered(),
			OperaEnabled:       n.OperaEnabled,
			OperaCountry:       n.OperaCountry,
			TrafficCoefficient: model.NodeCoefficientOr(n.TrafficCoefficient),
			Placement:          n.Placement,
			OnlineUsers:        online[n.ID],
			// The node's own REALITY identity (dest "" ⇒ inherits the panel's donor).
			RealityDest:      n.RealityDest,
			RealityPublicKey: n.RealityPublicKey,
			RealityShortID:   n.RealityShortID,
			RealityPath:      n.RealityPath,
			Proxy:            n.Proxy,
		}
		if t, ok := traffic[n.ID]; ok {
			v.TrafficUp, v.TrafficDown = t[0], t[1]
		}
		v.SyncFails = m.NodeSyncFails(n.ID)
		// What the node last said about its own machine. Absent until it checks in.
		if h, ok := m.NodeHostStats(n.ID); ok {
			v.HasHostStats = true
			v.CPUPercent, v.NetUp, v.NetDown = h.CPUPercent, h.NetUp, h.NetDown
			v.MemUsed, v.MemTotal = h.MemUsed, h.MemTotal
			v.DiskUsed, v.DiskTotal = h.DiskUsed, h.DiskTotal
			v.HostUptime = h.HostUptime
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
	now := time.Now().Unix()
	seen := map[string]int{}
	// The master occupies its label first, so a node whose name collides with the
	// master's config label gets disambiguated rather than silently overwriting the
	// master's Clash proxy name / sing-box tag (a client would drop one server).
	if set.MasterLabel != "" {
		seen[set.MasterLabel]++
	}
	for i := range nodes {
		n := &nodes[i]
		// Offline, if the operator asked for that (settings → subscriptions). Checked
		// before the "never installed" case below, which is a different thing: a node
		// that has never connected has no cert to pin, so it is skipped either way.
		if set.SubHideOffline && !n.Online(now) {
			continue
		}
		if !n.Enabled || n.LastSeen == 0 {
			// Disabled, or never installed. Deliberately NOT "currently offline": a node
			// bounces on every deploy and cert renewal, and yanking its links on a
			// two-minute blip would strand every client whose next refresh is hours
			// away, for a server that is already back. A client meeting a dead endpoint
			// fails over on its own; a client missing the entry cannot.
			continue
		}
		// A self-signed node that hasn't reported its cert fingerprint yet can't be
		// pinned, so its VLESS/Trojan/Hysteria links would fail silently in a modern
		// client (no allowInsecure). Skip it until it reports a fingerprint (or gets a
		// CA cert) — better no link than a broken one.
		if n.CertSelfSigned && n.CertSHA256 == "" {
			continue
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
	go m.RefreshNodeProxies()
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

// RequestNodeUpdate flags a node to self-update on its next sync, and wakes it so
// it happens promptly. Returns an error if the node doesn't exist.
func (m *Manager) RequestNodeUpdate(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	m.nodeUpdateMu.Lock()
	err = m.store.SetNodeCommand(id, nodeCmdUpdate, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		// Report it: the whole point of putting the command on disk is that the
		// operator is told whether it was actually recorded.
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// What a node's restart request currently reads as, for the UI.
const (
	RestartPending = "pending" // asked, waiting for the node to prove it happened
	RestartDone    = "done"    // the node reported a genuinely restarted Xray
	RestartTimeout = "timeout" // waited long enough and never got proof
)

const (
	// nodeRestartWait is how long an unconfirmed request stays pending. It covers the
	// whole round trip — deliver on the node's poll (held up to 45s), bounce Xray,
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

// takeCmd is the shared handover: deliver once, then keep the request until the node
// returns. Reports whether the command should ride this response.
//
// Backed by the store rather than a map so a panel restart does not drop what an
// operator asked for — and the restart that used to drop it is most often the panel's
// own self-update, i.e. exactly when the fleet is asked to update too.
func (m *Manager) takeCmd(id int64, kind string) bool {
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	c, err := m.store.NodeCommand(id, kind)
	if err != nil || c == nil {
		if err != nil {
			logErr("node command: read failed", "node", id, "kind", kind, "err", err)
		}
		return false
	}
	if time.Since(time.Unix(c.At, 0)) >= nodeCmdTTL {
		// Aged out. A node that was offline must not act on an order the operator gave
		// up on minutes ago.
		_ = m.store.DeleteNodeCommand(id, kind)
		return false
	}
	if c.Sent {
		// The node is back after being told — the response landed. Done.
		_ = m.store.DeleteNodeCommand(id, kind)
		return false
	}
	if err := m.store.MarkNodeCommandSent(id, kind); err != nil {
		logErr("node command: marking sent failed", "node", id, "kind", kind, "err", err)
		return false // don't send what we cannot record, or it is delivered forever
	}
	return true
}

// nodeRestartReq is one operator-requested Xray restart, tracked from the click
// until the node proves it happened — and then a little longer, so the answer is
// seen.
type nodeRestartReq struct {
	at   time.Time // when the operator asked (drives the timeout)
	sent bool      // handed to the node in a sync response
	// priorStart is the node's reported Xray start time captured at the moment the
	// command was handed over. Confirmation is "this value changed", never a compare
	// against the panel's own clock: the two machines' clocks disagree freely (this
	// pair runs an hour apart), and a node whose clock trails the panel's would never
	// look restarted at all.
	priorStart int64
	// outcome is RestartDone/RestartTimeout once resolved, "" while still waiting;
	// outcomeAt starts the window it stays on screen for.
	outcome   string
	outcomeAt time.Time
}

// waiting reports whether this request is still owed an answer — not yet resolved,
// and not yet out of time.
func (r *nodeRestartReq) waiting(now time.Time) bool {
	return r != nil && r.outcome == "" && now.Sub(r.at) < nodeRestartWait
}

// RequestNodeXrayRestart flags a node to bounce its Xray on the next sync and wakes
// it so it happens promptly. The panel can't reach into a node's process — this is
// the node-side twin of the master's own Xray restart button.
//
// The request stays on record after it is sent, until the node's next sync proves
// Xray actually bounced (see ConfirmNodeXrayRestart). That is the whole point: the
// panel cannot restart a node, only ask, and an answer that always reads "done"
// the instant it is asked is not an answer.
func (m *Manager) RequestNodeXrayRestart(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	m.nodeUpdateMu.Lock()
	m.nodeRestart[id] = &nodeRestartReq{at: time.Now()}
	m.nodeUpdateMu.Unlock()
	m.nodes.wakeOne(id)
	return nil
}

// TakeNodeXrayRestart reports whether this node should be told to bounce Xray now,
// and marks the command as sent. reportedStart is the Xray start time the node just
// reported, remembered as the "before" the confirmation compares against.
//
// It returns true exactly once per request: the node must not restart again on
// every poll while the panel is still waiting to hear that the first one landed.
func (m *Manager) TakeNodeXrayRestart(id int64, reportedStart int64) bool {
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	r := m.nodeRestart[id]
	if !r.waiting(time.Now()) || r.sent {
		// Absent, already answered, already sent, or out of time. The last case is
		// deliberate: a node that was offline must not bounce Xray minutes after the
		// operator gave up on the request.
		return false
	}
	r.sent = true
	r.priorStart = reportedStart
	return true
}

// ConfirmNodeXrayRestart clears a pending restart once the node reports an Xray that
// started at a different time than the one running when the command went out — the
// node's own proof that the process really came back.
//
// A node that reports no start time at all (an agent older than this field) can
// never confirm; its request simply times out, which reads as "we asked, we can't
// tell" rather than a false "done".
func (m *Manager) ConfirmNodeXrayRestart(id int64, reportedStart int64) {
	if reportedStart == 0 {
		return
	}
	now := time.Now()
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	r := m.nodeRestart[id]
	if r.waiting(now) && r.sent && reportedStart != r.priorStart {
		r.outcome, r.outcomeAt = RestartDone, now
	}
}

// NodeRestartState is what this node's restart request currently reads as:
// RestartPending while waiting, then RestartDone or RestartTimeout for a short
// window, then "" (nothing to say). Records past their window are dropped here, so
// the state can never be left stuck on a stale answer.
//
// The timeout is decided lazily, on read, rather than by a timer: nothing else in
// the panel needs to know, and a request nobody is looking at costs nothing to let
// sit until someone asks.
func (m *Manager) NodeRestartState(id int64) string {
	now := time.Now()
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	r := m.nodeRestart[id]
	switch {
	case r == nil:
		return ""
	case r.outcome != "":
		if now.Sub(r.outcomeAt) < nodeRestartShow {
			return r.outcome
		}
		delete(m.nodeRestart, id)
		return ""
	case r.waiting(now):
		return RestartPending
	default:
		// Out of time with no proof. Say so — and keep saying it for the display
		// window, so "we asked and never heard back" is an answer the operator
		// actually sees rather than the badge just disappearing.
		r.outcome, r.outcomeAt = RestartTimeout, now
		return RestartTimeout
	}
}

// RequestAllNodesUpdate flags every enabled, connected node to self-update.
func (m *Manager) RequestAllNodesUpdate() (int, error) {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, len(nodes))
	for i := range nodes {
		if nodes[i].Enabled && nodes[i].LastSeen > 0 {
			ids = append(ids, nodes[i].ID)
		}
	}
	// One transaction, so the lock is held for a single round trip rather than one per
	// node: every node's sync and the panel's Nodes page queue behind it otherwise. The
	// count is what actually landed — the operator's only receipt — so a failed write
	// reports zero rather than a number nobody honoured.
	m.nodeUpdateMu.Lock()
	n, err := m.store.SetNodeCommands(ids, nodeCmdUpdate, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		return 0, err
	}
	m.notifyNodes()
	return n, nil
}

// nodeLogsWantWindow is how long after an operator opens a node's logs the panel
// keeps asking that node to include its log tail (so viewing keeps refreshing, then
// stops on its own when the operator navigates away).
const nodeLogsWantWindow = 30 * time.Second

// nodeLogsWait is how long RequestNodeLogs waits for a woken node to deliver a
// fresh tail before answering with whatever it has. Long enough for a round trip to
// a node on another continent, short enough that a caller never wonders whether the
// request hung.
const nodeLogsWait = 3 * time.Second

// RequestNodeLogs returns a server's recent log tail.
//
// For the panel's own machine that is simply the in-memory ring every log line goes
// through. For a node it is a request: the panel marks that someone is watching,
// wakes the node's held poll, and the tail arrives on the sync that follows.
//
// It waits briefly for that to happen rather than answering empty and expecting the
// caller to ask again. The panel's own log viewer polls, so it never noticed; every
// other caller — an integration, an assistant asking once "why did this node restart"
// — got `{"lines":[],"at":0}` and no hint that asking twice was the protocol.
func (m *Manager) RequestNodeLogs(id int64) ([]string, int64) {
	if id == model.LocalNodeID {
		// The master never syncs with itself; its logs are right here.
		return logbuf.Default.Tail(), time.Now().Unix()
	}

	m.nodeLogsMu.Lock()
	m.nodeLogsWanted[id] = time.Now().Unix()
	e := m.nodeLogs[id]
	m.nodeLogsMu.Unlock()
	m.nodes.wakeOne(id) // return the held poll promptly so the tail comes back fast

	deadline := time.Now().Add(nodeLogsWait)
	for {
		m.nodeLogsMu.Lock()
		fresh := m.nodeLogs[id]
		m.nodeLogsMu.Unlock()
		// Newer than what we started with ⇒ the woken node answered.
		if fresh.at > e.at {
			return fresh.lines, fresh.at
		}
		if time.Now().After(deadline) {
			// Whatever was already stored: a node that is offline or slow still gets
			// its last known tail rendered, which beats an empty box.
			return e.lines, e.at
		}
		time.Sleep(100 * time.Millisecond)
	}
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

// TakeNodeUpdate hands over a node's pending self-update, if it has one. The lock lives
// in takeCmd — taking it here too self-deadlocks, since Go mutexes do not re-enter.
func (m *Manager) TakeNodeUpdate(id int64) bool {
	return m.takeCmd(id, nodeCmdUpdate)
}

// RequestNodeGeoRefresh flags a node to re-download its geo databases on its next
// sync and wakes it so it happens promptly.
func (m *Manager) RequestNodeGeoRefresh(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	m.nodeUpdateMu.Lock()
	err = m.store.SetNodeCommand(id, nodeCmdGeo, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		// Report it: the whole point of putting the command on disk is that the
		// operator is told whether it was actually recorded.
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// NodeTLSStatus reports a node's effective TLS/ACME status for its Domain tab: its
// address, its own (or inherited) ACME provider/email, and its cert metadata (built
// from what the node last reported). Mirrors the master's TLSStatus.
func (m *Manager) NodeTLSStatus(id int64) (*TLSStatus, error) {
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
	provider := n.ACMEProvider
	if provider == "" {
		provider = set.ACMEProvider
	}
	if provider == "" {
		provider = model.ACMEProviderLE
	}
	email := n.ACMEEmail
	if email == "" {
		email = set.ACMEEmail
	}
	var cert *tlsutil.CertInfo
	if n.CertExpiresAt > 0 || n.CertIssuer != "" {
		exp := time.Unix(n.CertExpiresAt, 0)
		cert = &tlsutil.CertInfo{
			Issuer:   n.CertIssuer, // "" when self-signed → the panel shows it as temporary
			NotAfter: exp,
			DaysLeft: int(time.Until(exp).Hours() / 24),
		}
	}
	return &TLSStatus{
		Mode:         model.TLSModeACME,
		Domain:       n.Host,
		SNI:          n.Host,
		ACMEEmail:    email,
		ACMEProvider: provider,
		Cert:         cert,
	}, nil
}

// SetNodeACME sets a node's own domain (ACME target), e-mail and CA provider, then
// wakes the node so its agent re-issues the cert. The panel can't issue a remote
// node's cert — the node does that — so this only persists the config and (for
// ZeroSSL) fetches the EAB the node's agent needs.
func (m *Manager) SetNodeACME(id int64, target, email, provider string) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	target = NormalizeACMEHost(target)
	email = strings.TrimSpace(email)
	if target == "" {
		return invalidCode("err.hostRequired", "укажите домен или IP-адрес")
	}
	if provider != model.ACMEProviderZeroSSL {
		provider = model.ACMEProviderLE
	}
	if !validACMETarget(target, provider) {
		if provider == model.ACMEProviderZeroSSL {
			return invalidCode("err.zerosslDomainsOnly", "ZeroSSL поддерживает только домены (не IP): {{value}} — это не похоже на домен", map[string]any{"value": target})
		}
		return invalidCode("err.notDomainOrIP", "{{value}} — это не похоже на домен или IP-адрес", map[string]any{"value": target})
	}
	if email != "" && !validEmail(email) {
		return invalidCode("err.notEmail", "{{value}} — это не похоже на e-mail адрес", map[string]any{"value": email})
	}
	if provider == model.ACMEProviderZeroSSL && email == "" {
		return invalidCode("err.zerosslNeedsEmail", "ZeroSSL требует e-mail адрес")
	}
	// ZeroSSL: reuse the node's stored EAB, else fetch a fresh one for its e-mail.
	eabKID, eabHMAC := "", ""
	if provider == model.ACMEProviderZeroSSL {
		if n.ZeroSSLEABKID != "" {
			eabKID, eabHMAC = n.ZeroSSLEABKID, n.ZeroSSLEABHMAC
		} else {
			kid, hmac, err := tlsmgr.FetchZeroSSLEAB(email)
			if err != nil {
				return fmt.Errorf("fetching the ZeroSSL EAB: %w", err)
			}
			eabKID, eabHMAC = kid, hmac
		}
	}
	if err := m.store.SetNodeACME(id, target, email, provider, eabKID, eabHMAC); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// NodeGeoFiles returns a node's last-reported geo database status (nil if it hasn't
// reported yet).
func (m *Manager) NodeGeoFiles(id int64) []nodeapi.GeoFile {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	return m.nodeGeoFiles[id]
}

// NodeHostStats returns a node's last-reported machine state (ok=false when the
// node hasn't reported one — an agent older than this feature never will).
func (m *Manager) NodeHostStats(id int64) (nodeapi.HostStats, bool) {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	h, ok := m.nodeHostStats[id]
	return h, ok
}

// NodeSyncFails returns a node's last-reported sync-failure count for the past hour
// (0 if it hasn't reported one).
func (m *Manager) NodeSyncFails(id int64) int {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	return m.nodeSyncFails[id]
}

// SetNodeGeoRefresh sets a node's own geo auto-refresh cadence (hours; 0 ⇒ never) and
// wakes it so the new cadence reaches its agent (via NodeMeta) promptly.
func (m *Manager) SetNodeGeoRefresh(id int64, hours int) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	if err := m.store.SetNodeGeoRefresh(id, hours); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// TakeNodeGeoRefresh consumes (and clears) a node's pending geo-refresh flag.
func (m *Manager) TakeNodeGeoRefresh(id int64) bool {
	return m.takeCmd(id, nodeCmdGeo)
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

// maxNodeSiteRows bounds how many destination rows one sync may contribute. The
// agent budgets its own payload well below this; the cap is here because the panel
// must not depend on a node behaving, and because applying an unbounded batch was
// measured at ~23ms of CPU on the same lock the master's access-log tap needs.
const maxNodeSiteRows = 4096

// userIDCacheTTL bounds how stale the node-site user-id validation set may be. Short
// enough that a new user's node sites start counting within seconds, long enough
// that a node cannot force a fresh id scan on every sync.
const userIDCacheTTL = 15 * time.Second

// ingestNodeAbuse matches a node's reported destinations against the blocklists,
// dropping rows for user ids that do not exist.
//
// The id check keeps a node from buffering matches against fabricated users (the
// EXISTS guard at write time would drop them anyway, but not before they cost buffer
// space). A node that predates the IP-only switch may still report hostnames; those
// simply never match and cost nothing beyond the row itself, since the per-sync abuse
// budget is only spent on rows that actually matched.
func (m *Manager) ingestNodeAbuse(nodeID int64, rows []nodeapi.SiteSample) {
	if m.abuse == nil {
		return
	}
	if len(rows) > maxNodeSiteRows {
		logErr("node sync: site rows truncated", "got", len(rows), "cap", maxNodeSiteRows)
		rows = rows[:maxNodeSiteRows]
	}
	known, err := m.knownUserIDs()
	if err != nil {
		logErr("node sync: cannot validate site user ids", "err", err)
		return
	}
	abuseBudget := abuseNodeMax // cap this sync's contribution to the shared buffer
	for _, s := range rows {
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
// idempotently, and computes the response (whether the node's applied hash still
// matches desired state). It does NOT block for the long-poll — the handler owns
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
	// Always refreshed (a healthy node reports 0), so the "limping" badge clears the
	// moment the transport recovers rather than sticking on a stale count.
	m.nodeSyncFails[n.ID] = req.SyncFails
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

	// Idempotent traffic ingest: atomically claim the report id. A report at-or-below
	// the stored watermark is a retry of an already-counted batch (lost response); the
	// conditional claim also stops two concurrent syncs from both counting the same
	// batch. The agent persists its report id, so a restart no longer regresses it.
	ack := req.ReportID
	if req.ReportID > 0 {
		// One commit for the node's whole batch, watermark included. Written per user
		// this was three fsyncs each on the panel's single connection, every 45s, per
		// node — the last write path whose cost still scaled with the user count.
		today := now.In(m.loc()).Format("2006-01-02")
		// The node's quota coefficient: real bytes go to the per-node stats, scaled
		// bytes to the user's allowance (see store.TrafficDelta / model.Node).
		coef := model.NodeCoefficientOr(n.TrafficCoefficient)
		deltas := make([]store.TrafficDelta, 0, len(req.Traffic))
		for _, d := range req.Traffic {
			up, down := nonNeg(d.Up), nonNeg(d.Down)
			if up == 0 && down == 0 {
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
		// Read before the write: enforceAfterTraffic wants the pre-ingest snapshot to
		// spot who just crossed a limit.
		var snapshot []model.User
		if len(deltas) > 0 {
			snapshot, _ = m.store.ListUsers()
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
			_ = m.enforceAfterTraffic(snapshot)
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
		m.RecordAccessOn(n.ID, c.Email, c.IP, "")
	}

	// Destinations arrive pre-aggregated with a count, so they bypass RecordAccess
	// (which counts one connection per call) and are folded straight into the rolling
	// view. Not gated on ReportID either: a duplicated sync would double-count a
	// sampled top-N that ages out in hours, which is not worth an ack protocol.
	if len(req.Sites) > 0 {
		m.ingestNodeAbuse(n.ID, req.Sites)
	}

	resp := &nodeapi.SyncResponse{AckReport: ack}
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

// validateDNSList refuses a DNS setting the generated Xray config could not parse. nil
// (inherit / leave alone) and an empty string are both fine.
func validateDNSList(dns *string) error {
	if dns == nil {
		return nil
	}
	for _, e := range strings.FieldsFunc(*dns, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' '
	}) {
		if !validDNSServer(e) {
			return invalidCode("err.badDNS", "неверный DNS-адрес: {{detail}}", map[string]any{"detail": e})
		}
	}
	return nil
}
