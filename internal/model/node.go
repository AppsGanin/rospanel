package model

// LocalNodeID is the virtual node the panel's own Xray runs as. It has no row in
// `nodes` — its identity is the settings singleton — but it carries an ID so that
// traffic history, link generation and the UI can treat every server uniformly.
const LocalNodeID int64 = 0

// NodeOnlineWindow is how long after its last sync a node still counts as online.
// Generous next to the node's own poll cadence (a held poll returns at least every
// 45s), so one slow round trip doesn't flap the badge.
const NodeOnlineWindow int64 = 120

// Node is a remote VPN server managed by this panel. It runs the same rospanel
// binary in node mode: it holds an outbound long-poll to the panel, applies the
// Xray config the panel generates for it, and reports traffic back.
//
// A node inherits every setting from the global settings row except the fields
// below — its own address, TLS/REALITY identity, protocol overrides, and its OWN
// routing/DNS/egress (proxy lanes, WARP, Opera), independent of the master. See
// core.nodeSettings, which materializes exactly that.
type Node struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Enabled bool   `json:"enabled"`

	// Per-node REALITY identity. RealityPrivateKey is encrypted at rest and never
	// serialized to any client.
	RealityPrivateKey  string `json:"-"`
	RealityPublicKey   string `json:"-"`
	RealityShortID     string `json:"-"`
	RealityServiceName string `json:"-"`

	// Protocol overrides: nil ⇒ inherit the global toggle. See NodeProtoEnabled.
	VLESSEnabled    *bool `json:"vless_enabled"`
	TrojanEnabled   *bool `json:"trojan_enabled"`
	HysteriaEnabled *bool `json:"hysteria_enabled"`
	RealityEnabled  *bool `json:"reality_enabled"`

	DecoyTemplate string `json:"decoy_template"`

	// Routing is the node's own routing override: nil ⇒ inherit the panel's routing.
	// A node's egress lanes (proxy pools) live in Routing.Lanes and resolve against
	// the node's OWN proxy pool; WARP/Opera below are the node's own too.
	Routing *RoutingConfig `json:"routing,omitempty"`

	// XrayDNS is the node's own upstream DNS override: nil ⇒ inherit the panel's DNS.
	XrayDNS *string `json:"xray_dns,omitempty"`

	// Per-node egress backends (independent of the master; all off by default).
	// WARP is a per-node Cloudflare registration (WireGuard); Opera runs a local
	// helper on the node. Proxy lanes live in Routing.Lanes.
	WarpEnabled    bool   `json:"warp_enabled"`
	WarpPrivateKey string `json:"-"` // encrypted at rest
	WarpPublicKey  string `json:"-"`
	WarpEndpoint   string `json:"-"`
	WarpAddressV4  string `json:"-"`
	WarpAddressV6  string `json:"-"`
	WarpReserved   string `json:"-"`
	OperaEnabled   bool   `json:"opera_enabled"`
	OperaCountry   string `json:"opera_country"`

	// Reported by the node on each sync.
	LastSeen       int64  `json:"last_seen"`
	NodeVersion    string `json:"node_version"`
	XrayVersion    string `json:"xray_version"`
	XrayRunning    bool   `json:"xray_running"`
	CertSHA256     string `json:"-"`
	CertSelfSigned bool   `json:"-"`
	ConfigHash     string `json:"-"`
	LastReportID   int64  `json:"-"`

	CreatedAt int64 `json:"created_at"`
	// DeletedAt is the tombstone timestamp: non-zero ⇒ the node was deleted and is
	// kept only so its next sync can be answered Revoked before the row is purged.
	DeletedAt int64 `json:"-"`

	// JoinExpiresAt is when the pending one-time join token lapses (0 ⇒ the node has
	// already joined, or its token expired and was cleared).
	JoinExpiresAt int64 `json:"join_expires_at"`

	// RawJoinToken is populated ONLY by CreateNode/RegenJoinToken, and shown to the
	// operator exactly once (it is the credential in the install command). It is
	// never stored in clear and never read back.
	RawJoinToken string `json:"join_token,omitempty"`
}

// Joined reports whether the node has exchanged its join token for a permanent
// one — i.e. whether the install command has actually been run on a server.
func (n *Node) Joined() bool { return n.ConfigHash != "" || n.LastSeen > 0 }

// Online reports whether the node has synced within NodeOnlineWindow of now.
func (n *Node) Online(now int64) bool {
	return n.LastSeen > 0 && now-n.LastSeen < NodeOnlineWindow
}

// WarpRegistered reports whether the node has a WARP account provisioned.
func (n *Node) WarpRegistered() bool { return n.WarpPrivateKey != "" }

// NodeStatusUpdate is what a node reports on each sync, persisted by
// Store.UpdateNodeStatus.
type NodeStatusUpdate struct {
	LastSeen       int64
	NodeVersion    string
	XrayVersion    string
	XrayRunning    bool
	CertSHA256     string
	CertSelfSigned bool
	ConfigHash     string
}

// NodeProtoEnabled resolves a per-node protocol override against the global
// toggle: an unset override (nil) inherits, so a node added today automatically
// picks up whatever protocols the panel serves.
func NodeProtoEnabled(override *bool, global bool) bool {
	if override == nil {
		return global
	}
	return *override
}
