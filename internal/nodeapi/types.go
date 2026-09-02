// Package nodeapi defines the wire contract between the panel and a node agent.
// Both the panel (internal/server) and the agent (internal/nodeagent) import it,
// so the JSON shapes can never drift between the two sides.
//
// Transport is node → panel: the agent holds an authenticated HTTPS long-poll to
// the panel's public domain. The panel pushes desired state on the response; the
// node reports traffic/health on the request. See the handlers in
// internal/server/node_api.go and the loop in internal/nodeagent.
package nodeapi

import (
	"encoding/json"

	"github.com/AppsGanin/rospanel/internal/awg"
)

// PathPrefix is the fixed sub-path under the panel's random node-API segment, so
// the full URL is /<node_api_path>/<PathPrefix>/{join,sync}.
const PathPrefix = "v1"

// Cert-path sentinels. The panel generates a node's Xray config with these literal
// placeholders where the TLS cert/key file paths go, because it doesn't know the
// node's data directory. The agent substitutes them with its own absolute paths
// before applying. They are part of the hashed desired state, so the hash is
// stable regardless of where the node stores its certs.
const (
	CertPathSentinel = "__ROSPANEL_NODE_CERT__"
	KeyPathSentinel  = "__ROSPANEL_NODE_KEY__"
)

// JoinRequest is sent once, with the one-time join token, to exchange it for a
// permanent bearer token.
type JoinRequest struct {
	JoinToken   string `json:"join_token"`
	NodeVersion string `json:"node_version"`
}

// JoinResponse carries the permanent credential and where to reach the panel. The
// agent persists all of it to node.json.
type JoinResponse struct {
	NodeID   int64  `json:"node_id"`
	Token    string `json:"token"`
	PanelURL string `json:"panel_url"`
	HoldSec  int    `json:"hold_sec"` // how long the panel will hold a no-change sync
	NodeAPI  string `json:"node_api"` // node-API path segment (in case the URL is bare)
}

// SyncRequest is the body of every long-poll. The node states what it currently
// has applied (config_hash) and reports its health + accumulated traffic deltas.
type SyncRequest struct {
	ConfigHash  string `json:"config_hash"`
	NodeVersion string `json:"node_version"`
	XrayVersion string `json:"xray_version"`
	XrayRunning bool   `json:"xray_running"`
	// Revoked ⇒ this node already knows the panel switched it off and has stopped
	// serving. It changes how the panel answers: a node that has yet to hear the bad
	// news is told at once, but one that already knows has its request HELD like any
	// other, so switching it back on reaches it immediately instead of on its next
	// slow poll. Absent from an older agent, which simply keeps the old behaviour.
	Revoked bool `json:"revoked,omitempty"`

	// XrayStartedAt is when the currently-running Xray started, in the NODE's clock
	// (0 ⇒ down, or an agent too old to report it). The panel never compares it to
	// its own clock — only to the previous value from this same node — which is what
	// lets it confirm an operator-requested restart actually happened without the two
	// machines having to agree on the time.
	XrayStartedAt int64 `json:"xray_started_at,omitempty"`

	// Live cert fingerprint, so the panel can emit correct pinning in this node's
	// share links without ever seeing the node's disk. Empty sha ⇒ no cert yet.
	CertSHA256     string `json:"cert_sha256"`
	CertSelfSigned bool   `json:"cert_self_signed"`
	// Cert details for the node's Domain tab: the issuer (≈ ACME provider) and expiry.
	CertIssuer    string `json:"cert_issuer,omitempty"`
	CertExpiresAt int64  `json:"cert_expires_at,omitempty"` // unix; 0 ⇒ unknown/no cert
	// CertError is the node's last TLS/ACME failure, empty once a cert is in place.
	// Reported because the panel raises the "TLS certificate" admin alert for the whole
	// fleet — the node has no bot to tell anyone itself. Absent from an older agent,
	// which simply never triggers that alert.
	CertError string `json:"cert_error,omitempty"`

	// Traffic deltas accumulated since the last acked report. ReportID is monotonic
	// per node and persisted by the agent, so a lost response is retried without
	// double-counting (the panel dedupes against its stored watermark).
	ReportID int64          `json:"report_id"`
	Traffic  []TrafficDelta `json:"traffic,omitempty"`

	// Conns are distinct (user-email, source-IP) samples seen in this node's Xray
	// access log since the last sync. The panel feeds them through the same device-
	// counting pipeline as the master (RecordAccess → AddConnection), so a user's
	// device cap counts unique IPs across the WHOLE fleet, not just the master.
	Conns []ConnSample `json:"conns,omitempty"`

	// Sites are this node's busiest destination addresses per user since the last
	// sync, which the panel matches against its IP blocklists.
	//
	// Aggregated on the node and truncated there, rather than shipped raw the way
	// Conns are: destinations are high-cardinality (a browsing user produces dozens
	// of distinct hosts a minute), so a raw per-connection feed would put the same
	// unbounded growth on the wire that keeps it out of the database. Lossy by
	// construction — the tail below the truncation never leaves the node, which is
	// the right trade for a view that only ever shows a top-N.
	Sites []SiteSample `json:"sites,omitempty"`

	// Logs is the node's recent log tail (agent + Xray), sent only when the panel
	// asked for it via SyncResponse.WantLogs — so a viewing operator sees fresh logs
	// without every sync carrying the payload.
	Logs []string `json:"logs,omitempty"`

	// GeoFiles is the on-disk status of the node's geo databases (name/size/mtime),
	// so its Domain/Geo tab can show them like the master's. Small — sent every sync.
	GeoFiles []GeoFile `json:"geo_files,omitempty"`

	// Host is the node's own machine state for its diagnostics page. The panel can't
	// stat a remote node's disk or read its nftables, so the node reports the same
	// handful of facts the panel shows for itself. Optional: an older agent omits it
	// and the report says so rather than inventing numbers.
	Host *HostStats `json:"host,omitempty"`

	// ConfigCheck answers a SyncResponse.CheckConfig request: did `xray run -test`
	// accept the candidate config? The panel asks before SAVING an inbound, so the
	// operator is told in the editor rather than by a crashed Xray and a rollback.
	// An agent too old to know the request omits this, which the panel reads as
	// "couldn't check".
	ConfigCheck *ConfigCheckResult `json:"config_check,omitempty"`

	// ProbeResults answers the port probes the panel asked for in the previous
	// response (SyncResponse.ProbePorts). The panel is validating a custom inbound
	// before saving it and cannot bind a socket on this machine itself. An agent too
	// old to know the request simply omits this, which the panel reads as "couldn't
	// check" rather than as a failure.
	ProbeResults []PortProbeResult `json:"probe_results,omitempty"`

	// SyncFails is how many sync attempts failed in the last hour, as the node counts
	// them. The panel never sees these directly — a failed long-poll still lands the
	// request (updating last_seen) and only the RESPONSE is lost — so a node can be
	// retrying constantly while looking "online". Reporting the count lets the panel
	// flag a node that is limping (transport degraded) before it decays into a hard
	// "not responding" outage. An older agent omits it (reads as 0 = healthy).
	SyncFails int `json:"sync_fails,omitempty"`
}

// ConfigCheckResult is the node's verdict on a candidate config. ID echoes the
// request so a stale answer can't satisfy a newer question; Err carries Xray's own
// complaint, which is the only thing precise enough to act on.
type ConfigCheckResult struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// HopRange is one UDP port-hopping funnel: Start..End redirected onto Target.
type HopRange struct {
	Start  int `json:"start"`
	End    int `json:"end"`
	Target int `json:"target"`
}

// ConfigCheckRequest is a candidate config to validate but not apply. ID is echoed
// back so the panel can match the answer to the request that is still being waited on.
type ConfigCheckRequest struct {
	ID     string          `json:"id"`
	Config json.RawMessage `json:"config"`
}

// PortProbe asks the node whether a port is free — "try to bind it, answer yes/no".
// Network is "tcp" or "udp": a Hysteria2 inbound binds UDP, and testing the wrong
// one would pass while the real bind fails.
type PortProbe struct {
	Network string `json:"network"`
	Port    int    `json:"port"`
}

// PortProbeResult is the node's answer. Free is what the panel acts on; Err carries
// the bind error for the operator when it isn't.
type PortProbeResult struct {
	Network string `json:"network"`
	Port    int    `json:"port"`
	Free    bool   `json:"free"`
	Err     string `json:"err,omitempty"`
}

// HostStats is a node's machine state as of its last sync.
type HostStats struct {
	// CPUPercent is the node's current CPU utilisation, and NetUp/NetDown its live
	// interface throughput in bytes per second. Added after the first version of this
	// struct, so an older agent simply reports zero — the panel shows those fields as
	// unknown rather than as an idle machine.
	CPUPercent float64 `json:"cpu_percent,omitempty"`
	NetUp      int64   `json:"net_up,omitempty"`
	NetDown    int64   `json:"net_down,omitempty"`

	DiskUsed   int64 `json:"disk_used"`
	DiskTotal  int64 `json:"disk_total"`
	MemUsed    int64 `json:"mem_used"`
	MemTotal   int64 `json:"mem_total"`
	HostUptime int64 `json:"host_uptime"` // seconds
	// ConnGuard is whether the per-IP flood guard's nftables rules are actually in
	// force on the node (it degrades to a no-op without nft/root, silently).
	ConnGuard bool `json:"connguard"`
	BBR       bool `json:"bbr"`
}

// GeoFile mirrors geo.FileInfo for reporting a node's geo database status.
type GeoFile struct {
	Name       string `json:"name"`
	Present    bool   `json:"present"`
	Size       int64  `json:"size"`
	ModifiedAt int64  `json:"modified_at"`
}

// TrafficDelta is one user's up/down bytes on this node since the last ack.
type TrafficDelta struct {
	UserID int64 `json:"user_id"`
	Up     int64 `json:"up"`
	Down   int64 `json:"down"`
}

// ConnSample is one (user-email, source-IP) pair the node observed. Email is the
// Xray "uN" tag; the panel resolves it to a user id. Deduped per node per sync.
//
// Carries no destination on purpose: this set is deduped per (email, ip) and stays
// small because a user has few source IPs. Adding the host would key it per
// destination instead and multiply it without bound — see SiteSample, which is
// pre-aggregated for exactly that reason.
type ConnSample struct {
	Email string `json:"e"`
	IP    string `json:"ip"`
}

// SiteSample is one (user, destination address) pair with how many connections the
// node saw to it since the last sync. UserID is already resolved from the Xray
// "uN" tag node-side, matching TrafficDelta.
//
// Host only ever carries an IP: the panel matches destinations against
// IP-reputation lists and ignores anything else, so the node filters hostnames out
// rather than spending payload on rows the panel would drop.
type SiteSample struct {
	UserID int64  `json:"u"`
	Host   string `json:"h"`
	Count  int64  `json:"c"`
}

// SyncResponse is returned immediately when the desired state differs from what
// the node has (Changed=true), otherwise held up to HoldSec and returned with
// Changed=false so the node loops again.
type SyncResponse struct {
	Changed   bool       `json:"changed"`
	AckReport int64      `json:"ack_report"` // highest ReportID the panel has ingested
	State     *NodeState `json:"state,omitempty"`

	// Revoked ⇒ the node was deleted or disabled: stop serving, keep polling slowly
	// so it recovers if re-enabled. Distinct from an unreachable panel (which the
	// agent treats as "keep serving last-known config").
	Revoked bool `json:"revoked,omitempty"`

	// PanelURL, when set, tells the agent the panel moved — persist and switch to it.
	PanelURL string `json:"panel_url,omitempty"`

	// RefreshGeo ⇒ the operator asked this node to re-download its geo databases now
	// (and reload Xray to pick them up).
	RefreshGeo bool `json:"refresh_geo,omitempty"`

	// Update ⇒ the operator asked this node to self-update to the latest release.
	// The agent downloads + verifies the new binary and restarts itself.
	Update bool `json:"update,omitempty"`

	// RestartXray ⇒ the operator asked this node to bounce its Xray process (the
	// config is unchanged; every live connection on that node drops and reconnects).
	RestartXray bool `json:"restart_xray,omitempty"`

	// WantLogs ⇒ an operator is viewing this node's logs; include the log tail in the
	// next sync request.
	WantLogs bool `json:"want_logs,omitempty"`

	// CheckConfig asks the node to run `xray run -test` over this candidate config and
	// report the verdict, WITHOUT applying it. Set while an operator is saving an
	// inbound: only the node's own Xray can say whether its advanced settings parse,
	// and finding out by crashing it is the failure this avoids.
	CheckConfig *ConfigCheckRequest `json:"check_config,omitempty"`

	// ProbePorts asks the node to bind-test these ports and report the outcome in its
	// next sync (SyncRequest.ProbeResults). Set while an operator is adding a custom
	// inbound to this node: the panel validates the port before writing anything, and
	// only the node itself can tell whether the port is free there.
	ProbePorts []PortProbe `json:"probe_ports,omitempty"`
}

// NodeState is the full desired state for a node. XrayConfig is generated panel-
// side by xray.Generate against the node's settings, so the node never needs the
// DB or the business rules; its local `xray -test` + rollback guard against a
// config its (possibly older) Xray can't parse. Hash is over XrayConfig + Meta.
type NodeState struct {
	Hash       string          `json:"hash"`
	XrayConfig json.RawMessage `json:"xray_config"`
	Meta       NodeMeta        `json:"meta"`
}

// NodeMeta is the host-level configuration the agent needs that isn't part of the
// Xray config itself: what to get a cert for, the port-hopping range, the decoy.
type NodeMeta struct {
	Host           string `json:"host"`
	SNI            string `json:"sni"`
	ACMEEmail      string `json:"acme_email"`
	ACMEProvider   string `json:"acme_provider"`
	ZeroSSLEABKID  string `json:"zerossl_eab_kid,omitempty"`
	ZeroSSLEABHMAC string `json:"zerossl_eab_hmac,omitempty"`

	HysteriaEnabled bool `json:"hysteria_enabled"`
	HysteriaPort    int  `json:"hysteria_port"`
	HopStart        int  `json:"hop_start"`
	HopEnd          int  `json:"hop_end"`

	// HopRanges is every UDP funnel this node should install — the built-in Hysteria2
	// lane's range plus one per custom Hysteria2 inbound that asks for hopping. The
	// agent recreates its nftables table wholesale from this list, so it must be the
	// complete set. An older agent ignores it and keeps using the three fields above,
	// which still describe the built-in lane exactly.
	HopRanges []HopRange `json:"hop_ranges,omitempty"`

	// ConnGuardPorts are the public TCP ports the per-IP connection guard should
	// protect (VLESS, and REALITY when enabled).
	ConnGuardPorts []int `json:"connguard_ports,omitempty"`

	// LoopbackDest is where the node's Xray fallback forwards non-VPN traffic — the
	// agent runs its decoy server there (matches the panel's own layout).
	LoopbackDest string `json:"loopback_dest"`

	DecoyTemplate string `json:"decoy_template"`

	// Opera egress: when enabled, the agent runs the opera-proxy helper locally on
	// OperaPort in the given country. The generated Xray config's "opera" outbound
	// already points at 127.0.0.1:OperaPort, so the agent only has to keep the helper
	// alive. WARP needs no helper — it's a native WireGuard outbound in the config.
	OperaEnabled bool   `json:"opera_enabled,omitempty"`
	OperaCountry string `json:"opera_country,omitempty"`
	OperaPort    int    `json:"opera_port,omitempty"`

	// GeoRefreshHours is how often the node should auto-refresh its geo databases
	// (hours; 0 ⇒ never). Pushed from the panel so the fleet shares one cadence.
	GeoRefreshHours int `json:"geo_refresh_hours,omitempty"`

	// XrayPinnedVersion is the release the panel expects; the UI flags a node whose
	// running Xray differs so version skew is visible.
	XrayPinnedVersion string `json:"xray_pinned_version,omitempty"`

	// SpeedLimits caps how fast individual users may move traffic on this node, in
	// kbit/s, keyed by the Xray email tag ("u12"). Only capped users appear.
	//
	// The node shapes its OWN traffic from its OWN view of who is connected from
	// where: the addresses a user reaches this node from are not the ones they reach
	// the master from, and shipping the master's view would cap the wrong addresses.
	// An older agent ignores the field and simply doesn't shape.
	SpeedLimits map[string]int `json:"speed_limits,omitempty"`

	// BlockedIPs are the addresses the source policy refused, fleet-wide (see
	// core.ConnPolicy). The node drops them at its own firewall, so a client refused
	// on one server is refused on all of them. Empty ⇒ nothing is blocked and any
	// table the node installed comes down.
	BlockedIPs []string `json:"blocked_ips,omitempty"`
	// BlockTTLHours is how long those blocks last, so a node cut off from the panel
	// expires them on the operator's schedule rather than the blocker's default.
	BlockTTLHours int `json:"block_ttl_hours,omitempty"`

	// AWG is the node's AmneziaWG tunnel as the panel wants it — its identity, the
	// obfuscation parameters and every peer allowed on it. nil ⇒ the lane is off
	// on this node and any running tunnel comes down. See internal/awg.
	AWG *AWGState `json:"awg,omitempty"`
}

// AWGState is one server's tunnel: what the node feeds to awg.Device.Apply.
type AWGState struct {
	Port       int        `json:"port"`
	PrivateKey string     `json:"private_key"`
	Params     awg.Params `json:"params"`
	Peers      []AWGPeer  `json:"peers"`
}

// AWGPeer is one user on the tunnel: their public key, their tunnel address and
// their Xray tag, which the node reports counters and sightings under.
type AWGPeer struct {
	PublicKey string `json:"pk"`
	Addr      string `json:"ip"`
	Email     string `json:"e"`
}
