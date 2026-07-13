// Package nodeapi defines the wire contract between the panel and a node agent.
// Both the panel (internal/server) and the agent (internal/nodeagent) import it,
// so the JSON shapes can never drift between the two sides.
//
// Transport is node → panel: the agent holds an authenticated HTTPS long-poll to
// the panel's public domain. The panel pushes desired state on the response; the
// node reports traffic/health on the request. See the handlers in
// internal/server/node_api.go and the loop in internal/nodeagent.
package nodeapi

import "encoding/json"

// PathPrefix is the fixed sub-path under the panel's random node-API segment, so
// the full URL is /<node_api_path>/<PathPrefix>/{join,sync}.
const PathPrefix = "v1"

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

	// Live cert fingerprint, so the panel can emit correct pinning in this node's
	// share links without ever seeing the node's disk. Empty sha ⇒ no cert yet.
	CertSHA256     string `json:"cert_sha256"`
	CertSelfSigned bool   `json:"cert_self_signed"`

	// Traffic deltas accumulated since the last acked report. ReportID is monotonic
	// per node and persisted by the agent, so a lost response is retried without
	// double-counting (the panel dedupes against its stored watermark).
	ReportID int64          `json:"report_id"`
	Traffic  []TrafficDelta `json:"traffic,omitempty"`
}

// TrafficDelta is one user's up/down bytes on this node since the last ack.
type TrafficDelta struct {
	UserID int64 `json:"user_id"`
	Up     int64 `json:"up"`
	Down   int64 `json:"down"`
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

	DecoyTemplate string `json:"decoy_template"`

	// XrayPinnedVersion is the release the panel expects; the UI flags a node whose
	// running Xray differs so version skew is visible.
	XrayPinnedVersion string `json:"xray_pinned_version,omitempty"`
}
