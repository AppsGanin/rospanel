package model

// LocalNodeID is the virtual node the panel's own Xray runs as. It has no row in
// `nodes` — its identity is the settings singleton — but it carries an ID so that
// traffic history, link generation and the UI can treat every server uniformly.
const LocalNodeID int64 = 0

// LocalNodeName is what the panel's own server is called on screen. Shared rather
// than written out at each call site: the Nodes tab and the traffic breakdown both
// name node 0, and two spellings of the same server read as two servers.
//
// It sits next to operator-chosen node names in every list that shows it, so it is
// a name and not a dictionary key — and "Master" is the word in both languages.
const LocalNodeName = "Master"

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
// Node traffic-coefficient bounds. Below 0.1 a real transfer could round to zero
// quota; above 10 is past any real differential-pricing need.
const (
	MinNodeCoefficient = 0.1
	MaxNodeCoefficient = 10.0
)

// NodeCoefficientOr returns a usable coefficient: 1.0 for an unset/old row (stored 0)
// or a negative value, clamped into [Min,Max] otherwise. Used at read time so a bad
// stored number can never zero out or explode a user's quota accounting.
func NodeCoefficientOr(c float64) float64 {
	if c <= 0 {
		return 1.0
	}
	return min(max(c, MinNodeCoefficient), MaxNodeCoefficient)
}

type Node struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Enabled bool   `json:"enabled"`

	// TrafficCoefficient scales how much of a user's quota is spent per real byte on
	// this node: 2.0 makes an expensive location drain the allowance twice as fast,
	// 0.5 makes a promo one drain it half as fast. It bends only the quota, never the
	// per-node statistics, which stay the true byte count. Default 1.0; a zero read
	// from an older row is normalized to 1.0 (see NodeCoefficientOr).
	TrafficCoefficient float64 `json:"traffic_coefficient"`
	// Placement: where this node sits in a subscription (see placement.go).
	Placement

	// Per-node REALITY identity. RealityPrivateKey is encrypted at rest and never
	// serialized to any client. RealityDest is the node's own masquerade donor SNI
	// (empty ⇒ inherit the panel's donor).
	RealityPrivateKey string `json:"-"`
	RealityPublicKey  string `json:"-"`
	RealityShortID    string `json:"-"`
	RealityPath       string `json:"-"`
	RealityDest       string `json:"-"`

	// Per-node AmneziaWG identity (its own keypair and obfuscation parameters, see
	// internal/awg); the port and display name ride in Connections.
	AWGPrivateKey string    `json:"-"`
	AWGPublicKey  string    `json:"-"`
	AWGParams     AWGParams `json:"-"`

	// Per-node protocols (the node's OWN — no inheritance from the master). A stored
	// nil is treated as off; every write sets an explicit value.
	VLESSEnabled    *bool `json:"vless_enabled"`
	HysteriaEnabled *bool `json:"hysteria_enabled"`
	RealityEnabled  *bool `json:"reality_enabled"`
	AWGEnabled      *bool `json:"awg_enabled"`

	DecoyTemplate string `json:"decoy_template"`

	// Routing is the node's own routing config. nil ⇒ the node gets an EMPTY routing
	// config, NOT the master's (see manager.nodeSettings) — a node must never silently
	// borrow the master's lanes, because those resolve against the master's proxy pool
	// and would send this node's traffic somewhere its operator never configured.
	// A node's egress lanes (proxy pools) live in Routing.Lanes and resolve against
	// the node's OWN proxy pool; WARP/Opera below are the node's own too.
	Routing *RoutingConfig `json:"routing,omitempty"`

	// XrayDNS is the node's own upstream DNS. nil ⇒ empty (Xray's default resolver),
	// not the master's — same reasoning as Routing above.
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

	// Proxy is this node's own system proxy (SOCKS/HTTP forward listeners for things
	// that are not VPN clients). Never inherited from the master: inheriting would
	// open a listener on every node the moment the master enabled one, and would put
	// the master's password on every node's disk. Pass is encrypted at rest.
	Proxy SystemProxy `json:"proxy"`

	// Connections is the node's own transport override (nil ⇒ inherit the master's).
	Connections *NodeConnections `json:"-"`

	// Per-node ACME (empty ⇒ inherit the panel's). ZeroSSLEABHMAC is encrypted at rest.
	ACMEEmail      string `json:"-"`
	ACMEProvider   string `json:"-"`
	ZeroSSLEABKID  string `json:"-"`
	ZeroSSLEABHMAC string `json:"-"`

	// Reported by the node on each sync.
	LastSeen       int64  `json:"last_seen"`
	NodeVersion    string `json:"node_version"`
	XrayVersion    string `json:"xray_version"`
	XrayRunning    bool   `json:"xray_running"`
	CertSHA256     string `json:"-"`
	CertSelfSigned bool   `json:"-"`
	CertIssuer     string `json:"-"` // ≈ ACME provider that signed the node's cert
	CertExpiresAt  int64  `json:"-"` // unix; 0 ⇒ unknown

	// GeoRefreshHours is the node's OWN geo auto-refresh cadence (hours; 0 ⇒ never —
	// its own, not inherited from the master).
	GeoRefreshHours int    `json:"-"`
	ConfigHash      string `json:"-"`
	LastReportID    int64  `json:"-"`

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

// NodeConnections is a node's own connection transport, overriding the master's when
// present (nil ⇒ inherit the master's). Protocol on/off and the REALITY donor/keys
// are separate per-node fields; this holds the ports, port-hopping, WS path, REALITY
// port + anti-replay, uTLS fingerprints, connection display names, and anti-DPI.
type NodeConnections struct {
	HysteriaPort       int    `json:"hysteria_port"`
	HopStart           int    `json:"hop_start"`
	HopEnd             int    `json:"hop_end"`
	HopInterval        string `json:"hop_interval"`
	RealityPort        int    `json:"reality_port"`
	RealityMaxTimeDiff int    `json:"reality_max_time_diff"` // >0 ⇒ anti-replay on
	TLSFragment        bool   `json:"tls_fragment"`
	TLSMin13           bool   `json:"tls_min13"`
	BlockQUIC          bool   `json:"block_quic"`
	VLESSFp            string `json:"vless_fp"`
	RealityFp          string `json:"reality_fp"`
	VLESSName          string `json:"vless_name"`
	RealityName        string `json:"reality_name"`
	HysteriaName       string `json:"hysteria_name"`
	AWGPort            int    `json:"awg_port"`
	AWGName            string `json:"awg_name"`
	AWGDNS             string `json:"awg_dns"`
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
	CertIssuer     string
	CertExpiresAt  int64
	ConfigHash     string
}
