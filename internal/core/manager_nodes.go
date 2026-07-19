package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/decoy"
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
	ns.Host = n.Host
	ns.SNI = n.Host
	ns.RealityPrivateKey = n.RealityPrivateKey
	ns.RealityPublicKey = n.RealityPublicKey
	ns.RealityShortID = n.RealityShortID
	ns.RealityServiceName = n.RealityServiceName
	// REALITY donor: the node's own if set, otherwise inherit the panel's (a node
	// needs some donor for REALITY to work).
	if n.RealityDest != "" {
		ns.RealityDest = n.RealityDest
	}

	// A node's protocols are its OWN — no inheritance from the master. Unset ⇒ off.
	ns.VLESSEnabled = derefBool(n.VLESSEnabled)
	ns.TrojanEnabled = derefBool(n.TrojanEnabled)
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

	// Proxy mode is a master-ONLY local forward proxy: its inbound (and the master's
	// credentials) must never be generated into a node's config — that would open a
	// chainable proxy on the node's port and leak the master's proxy password onto
	// every node's disk. Nodes never run it.
	ns.ProxyModeEnabled = false
	ns.ProxyModeType = ""
	ns.ProxyModePort = 0
	ns.ProxyModeUser = ""
	ns.ProxyModePass = ""

	// DNS: the node's OWN (no inheritance). Unset ⇒ Xray's default resolver.
	if n.XrayDNS != nil {
		ns.XrayDNS = *n.XrayDNS
	} else {
		ns.XrayDNS = ""
	}

	// Connection transport: the node's own if configured, otherwise inherit the
	// master's (ns already carries the master's values from the shallow copy).
	if c := n.Connections; c != nil {
		ns.WSPath = c.WSPath
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
		ns.TrojanFp = c.TrojanFp
		ns.RealityFp = c.RealityFp
		ns.VLESSName = c.VLESSName
		ns.TrojanName = c.TrojanName
		ns.RealityName = c.RealityName
		ns.HysteriaName = c.HysteriaName
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
	cfg, err := xray.Generate(ns, users, m.genOpts(), m.getNodeProxies(n.ID))
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
		ConnGuardPorts:    connGuardPorts,
		LoopbackDest:      m.opts.PanelDest,
		DecoyTemplate:     n.DecoyTemplate,
		GeoRefreshHours:   n.GeoRefreshHours, // the node's OWN geo cadence
		XrayPinnedVersion: xray.PinnedVersion,
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
	// CertSelfSigned is what the node last reported about its live TLS cert: true ⇒
	// still on the self-signed fallback (ACME not obtained yet), false ⇒ a CA cert is
	// in place. Lets the node's Домен tab show the cert status like the master's.
	CertSelfSigned bool   `json:"cert_self_signed"`
	CertIssuer     string `json:"cert_issuer"`     // ≈ ACME provider (empty for the local node)
	CertExpiresAt  int64  `json:"cert_expires_at"` // unix; 0 ⇒ unknown
	// GeoRefreshHours is this server's own geo auto-refresh cadence (hours; 0 ⇒ never).
	GeoRefreshHours int   `json:"geo_refresh_hours"`
	TrafficUp       int64 `json:"traffic_up"`   // today, this node
	TrafficDown     int64 `json:"traffic_down"` // today, this node
	// Routing / XrayDNS carry the node's own override (nil ⇒ inherits the panel's),
	// so the per-node routing+DNS editor can prefill and show inherit vs custom. For
	// the local server (node 0) these carry the master's own routing/DNS so the same
	// editor edits the master.
	Routing *model.RoutingConfig `json:"routing"`
	XrayDNS *string              `json:"xray_dns"`
	// Egress backends (node's own, independent of the master; all off by default).
	// WARP is native to Xray once registered; Opera runs a helper on the node.
	WarpEnabled    bool   `json:"warp_enabled"`
	WarpRegistered bool   `json:"warp_registered"`
	OperaEnabled   bool   `json:"opera_enabled"`
	OperaCountry   string `json:"opera_country"`
	// REALITY identity (per-server). RealityDest is this server's own donor ("" on a
	// node ⇒ inherits the panel's); the public key/shortId/service are shown so the
	// operator can see them and regenerate. The private key is never exposed.
	RealityDest        string `json:"reality_dest"`
	RealityPublicKey   string `json:"reality_public_key"`
	RealityShortID     string `json:"reality_short_id"`
	RealityServiceName string `json:"reality_service_name"`
	JoinToken          string `json:"join_token,omitempty"` // only right after create/regen
	// MasterLabel is the master server's config-label name (local node only), so the
	// UI can edit it. Empty for remote nodes (they use their own Name).
	MasterLabel string `json:"master_label,omitempty"`
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
		// The master's own routing/DNS/egress, so the relocated per-server editor edits
		// the master through the same controls as a node.
		Routing:        &set.Routing,
		XrayDNS:        &set.XrayDNS,
		WarpEnabled:    set.WarpEnabled,
		WarpRegistered: set.WarpRegistered(),
		OperaEnabled:   set.OperaEnabled,
		OperaCountry:   set.OperaCountryOr(),
		// The master's own REALITY identity.
		RealityDest:        set.RealityDest,
		RealityPublicKey:   set.RealityPublicKey,
		RealityShortID:     set.RealityShortID,
		RealityServiceName: set.RealityServiceName,
		GeoRefreshHours:    set.GeoRefreshHours,
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
			VLESSEnabled:    derefBool(n.VLESSEnabled),
			TrojanEnabled:   derefBool(n.TrojanEnabled),
			HysteriaEnabled: derefBool(n.HysteriaEnabled),
			RealityEnabled:  derefBool(n.RealityEnabled),
			DecoyTemplate:   n.DecoyTemplate,
			CertSelfSigned:  n.CertSelfSigned,
			CertIssuer:      n.CertIssuer,
			CertExpiresAt:   n.CertExpiresAt,
			GeoRefreshHours: n.GeoRefreshHours,
			Routing:         n.Routing,
			XrayDNS:         n.XrayDNS,
			WarpEnabled:     n.WarpEnabled,
			WarpRegistered:  n.WarpRegistered(),
			OperaEnabled:    n.OperaEnabled,
			OperaCountry:    n.OperaCountry,
			// The node's own REALITY identity (dest "" ⇒ inherits the panel's donor).
			RealityDest:        n.RealityDest,
			RealityPublicKey:   n.RealityPublicKey,
			RealityShortID:     n.RealityShortID,
			RealityServiceName: n.RealityServiceName,
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
	// The master occupies its label first, so a node whose name collides with the
	// master's config label gets disambiguated rather than silently overwriting the
	// master's Clash proxy name / sing-box tag (a client would drop one server).
	if set.MasterLabel != "" {
		seen[set.MasterLabel]++
	}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || n.LastSeen == 0 {
			continue // disabled, or never installed → don't hand clients a dead link
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
		return nil, &ValidationError{Msg: "нода с таким названием уже есть — имя должно быть уникальным"}
	}
	if err := m.EnsureNodeAPIPath(); err != nil {
		return nil, err
	}
	decoyTemplate, err := m.randomDecoy()
	if err != nil {
		return nil, err
	}
	n, err := m.store.CreateNode(name, host, decoyTemplate)
	if errors.Is(err, store.ErrNodeNameTaken) {
		return nil, &ValidationError{Msg: "нода с таким названием уже есть — имя должно быть уникальным"}
	}
	return n, err
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
			return &ValidationError{Msg: "нода с таким названием уже есть — имя должно быть уникальным"}
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
		return &ValidationError{Msg: fmt.Sprintf("регистрация WARP для ноды не удалась: %v", err)}
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
		return &ValidationError{Msg: "нода не найдена"}
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
		svc, err := auth.RandomServiceName()
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
		return nil, &ValidationError{Msg: "нода не найдена"}
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
		return &ValidationError{Msg: "нода не найдена"}
	}

	fpOf := func(key string) string {
		if v := u.Fingerprints[key]; v != "" {
			return v
		}
		return "firefox"
	}
	vlessFp, trojanFp, realityFp := fpOf("vless"), fpOf("trojan"), fpOf("reality")
	for _, fp := range []string{vlessFp, trojanFp, realityFp} {
		if !model.ValidFingerprint(fp) {
			return invalid("неизвестный fingerprint %q", fp)
		}
	}
	connNames, err := validateConnNames(u.Names)
	if err != nil {
		return err
	}
	ws := "/" + strings.TrimLeft(strings.TrimSpace(u.WSPath), "/")
	if !wsPathRe.MatchString(ws) {
		return invalid("неверный путь WebSocket (начинается с «/», допустимы латиница, цифры, - _ . /)")
	}
	if u.HysteriaPort < 1 || u.HysteriaPort > 65535 {
		return invalid("порт вне диапазона 1–65535")
	}
	if u.HopStart < 1 || u.HopEnd > 65535 || u.HopStart > u.HopEnd {
		return invalid("неверный диапазон хопа")
	}
	interval := strings.TrimSpace(u.HopInterval)
	if interval == "" {
		interval = "5-10"
	}
	if !hopIntervalRe.MatchString(interval) {
		return invalid("неверный интервал (нужно «N-M», напр. 5-10)")
	}
	if u.RealityPort < 1 || u.RealityPort > 65535 {
		return invalid("порт REALITY вне диапазона 1–65535")
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
	if err := m.store.SetNodeProtocols(id,
		u.Protocols["vless"], u.Protocols["trojan"], u.Protocols["hysteria2"], u.Protocols["reality"]); err != nil {
		return err
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
		svc, kerr := auth.RandomServiceName()
		if kerr != nil {
			return kerr
		}
		if err := m.store.SaveNodeReality(id, priv, pub, shortID, svc); err != nil {
			return err
		}
	}
	// Transport blob.
	blob := &model.NodeConnections{
		WSPath:             ws,
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
		TrojanFp:           trojanFp,
		RealityFp:          realityFp,
		VLESSName:          connNames["vless"],
		TrojanName:         connNames["trojan"],
		RealityName:        connNames["reality"],
		HysteriaName:       connNames["hysteria2"],
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

// RequestNodeGeoRefresh flags a node to re-download its geo databases on its next
// sync and wakes it so it happens promptly.
func (m *Manager) RequestNodeGeoRefresh(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return &ValidationError{Msg: "нода не найдена"}
	}
	m.nodeUpdateMu.Lock()
	m.nodeGeoWanted[id] = true
	m.nodeUpdateMu.Unlock()
	m.nodes.wakeOne(id)
	return nil
}

// NodeTLSStatus reports a node's effective TLS/ACME status for its Домен tab: its
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
		return nil, &ValidationError{Msg: "нода не найдена"}
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
			Issuer:   n.CertIssuer, // "" when self-signed → UI shows "временный"
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
		return &ValidationError{Msg: "нода не найдена"}
	}
	target = NormalizeACMEHost(target)
	email = strings.TrimSpace(email)
	if target == "" {
		return invalid("укажите домен или IP-адрес")
	}
	if provider != model.ACMEProviderZeroSSL {
		provider = model.ACMEProviderLE
	}
	if !validACMETarget(target, provider) {
		if provider == model.ACMEProviderZeroSSL {
			return invalid("ZeroSSL поддерживает только домены (не IP): %q — это не похоже на домен", target)
		}
		return invalid("%q — это не похоже на домен или IP-адрес", target)
	}
	if email != "" && !validEmail(email) {
		return invalid("%q — это не похоже на e-mail адрес", email)
	}
	if provider == model.ACMEProviderZeroSSL && email == "" {
		return invalid("ZeroSSL требует e-mail адрес")
	}
	// ZeroSSL: reuse the node's stored EAB, else fetch a fresh one for its e-mail.
	eabKID, eabHMAC := "", ""
	if provider == model.ACMEProviderZeroSSL {
		if n.ZeroSSLEABKID != "" {
			eabKID, eabHMAC = n.ZeroSSLEABKID, n.ZeroSSLEABHMAC
		} else {
			kid, hmac, err := tlsmgr.FetchZeroSSLEAB(email)
			if err != nil {
				return fmt.Errorf("получение EAB от ZeroSSL: %w", err)
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

// SetNodeGeoRefresh sets a node's own geo auto-refresh cadence (hours; 0 ⇒ never) and
// wakes it so the new cadence reaches its agent (via NodeMeta) promptly.
func (m *Manager) SetNodeGeoRefresh(id int64, hours int) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return &ValidationError{Msg: "нода не найдена"}
	}
	if err := m.store.SetNodeGeoRefresh(id, hours); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// TakeNodeGeoRefresh consumes (and clears) a node's pending geo-refresh flag.
func (m *Manager) TakeNodeGeoRefresh(id int64) bool {
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	if m.nodeGeoWanted[id] {
		delete(m.nodeGeoWanted, id)
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
	// A disabled (or soft-deleted-but-unpurged) node's token still authenticates so we
	// can tell it to stop — but it is untrusted (being disabled is often WHY), so we
	// must NOT apply its reported traffic/devices/status. Revoke before any ingest.
	if !n.Enabled {
		return &nodeapi.SyncResponse{Revoked: true, AckReport: req.ReportID}, nil
	}
	now := time.Now()
	if len(req.Logs) > 0 {
		m.storeNodeLogs(n.ID, req.Logs)
	}
	if len(req.GeoFiles) > 0 {
		m.nodeGeoMu.Lock()
		m.nodeGeoFiles[n.ID] = req.GeoFiles
		m.nodeGeoMu.Unlock()
	}
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
				AddUp: up, AddDown: down, SeenAt: now.Unix(),
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
		m.RecordAccess(c.Email, c.IP)
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
