package core

import (
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

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
	// TrafficPeriodUsed is what this server has carried in its cap period (the month
	// by default), and TrafficOver whether that has reached the cap. Reported even
	// with no cap set, so the operator can see the figure before choosing a number.
	TrafficPeriodUsed int64 `json:"traffic_period_used"`
	TrafficOver       bool  `json:"traffic_over"`

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
	lu := m.NodeTrafficUsage(model.LocalNodeID)
	local.TrafficPeriodUsed, local.TrafficOver = lu.Used, lu.Over
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
		nu := m.NodeTrafficUsage(n.ID)
		v.TrafficPeriodUsed, v.TrafficOver = nu.Used, nu.Over
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
