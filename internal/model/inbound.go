package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/AppsGanin/rospanel/internal/datasec"
	"net"
	"regexp"
	"sort"
	"strings"
)

// Custom-inbound protocols. Deliberately a short list: these three cover every
// transport the panel can also emit a working client config for, and each reuses a
// credential the user already has (VLESS → UUID, Trojan/Hysteria2 → Password), so
// adding an inbound never needs a new per-user secret.
const (
	InbVLESS       = "vless"
	InbTrojan      = "trojan"
	InbHysteria    = "hysteria2"
	InbShadowsocks = "shadowsocks"
	// InbTurnVK is WireGuard behind a TURN relay (Xray's "wireguard" inbound).
	InbTurnVK = "turn-vk"
	// InbWireGuard is plain WireGuard: Xray's "wireguard" inbound on its own public UDP
	// port, with no relay in front of it. Users import a config file into any stock
	// WireGuard client.
	InbWireGuard = "wg"
	// inbTurnVKOld is what InbTurnVK was called until 5.1; still accepted on input.
	inbTurnVKOld = "wireguard"
)

// Shadowsocks-2022 AEAD methods, the only ones offered. The pre-2022 ciphers
// (aes-256-gcm, chacha20-ietf-poly1305) authenticate a whole inbound with ONE
// password, so they cannot tell one user from another — which would take away
// per-user stats, quotas, speed caps and access groups, the whole point of the
// panel. The 2022 methods carry a per-user key alongside the server key, so a
// single inbound serves every user and still attributes traffic. The cost is that
// only current clients (sing-box, mihomo, v2rayN, Shadowrocket, Streisand) speak
// them; older Shadowsocks apps do not.
const (
	SS2022AES128    = "2022-blake3-aes-128-gcm"
	SS2022AES256    = "2022-blake3-aes-256-gcm"
	SS2022ChaCha    = "2022-blake3-chacha20-poly1305"
	DefaultSSMethod = SS2022AES128
)

// SSMethods lists the offered Shadowsocks methods, in UI order (lightest first).
var SSMethods = []string{SS2022AES128, SS2022AES256, SS2022ChaCha}

// SSKeyLen is the byte length of both the server key and each per-user key for a
// method — they must match, and 2022-blake3 fixes it at the AEAD's key size.
// aes-128 wants 16 bytes; aes-256 and chacha20 want 32. An unknown method returns
// 0, which callers treat as "not a Shadowsocks method".
func SSKeyLen(method string) int {
	switch method {
	case SS2022AES128:
		return 16
	case SS2022AES256, SS2022ChaCha:
		return 32
	}
	return 0
}

// Custom-inbound transports. Hysteria2 has none of these — it is its own QUIC
// transport — and stores TransportHysteria so the stored value is never empty.
const (
	TrTCP         = "tcp"
	TrWS          = "ws"
	TrXHTTP       = "xhttp"
	TrGRPC        = "grpc"
	TrHTTPUpgrade = "httpupgrade"
	TrHysteria    = "hysteria"
	TrShadowsocks = "shadowsocks" // placeholder: SS-2022 is raw TCP with its own AEAD
	TrTURN        = "turn"        // placeholder: WireGuard relayed by a call service's TURN servers
	TrWireGuard   = "wireguard"   // placeholder: WireGuard on a UDP port of its own
)

// Custom-inbound security layers. "none" is only meaningful behind a TLS-terminating
// front (CDN, nginx); it is rejected on raw TCP, where it would be plaintext proxy
// traffic on a public port.
const (
	SecNone    = "none"
	SecTLS     = "tls"
	SecReality = "reality"
)

// XHTTP modes. "stream-one" is one HTTP request per connection (the closest thing
// to WebSocket); the rest multiplex. Empty ⇒ Xray's own default ("auto").
const (
	XHTTPAuto      = "auto"
	XHTTPPacketUp  = "packet-up"
	XHTTPStreamUp  = "stream-up"
	XHTTPStreamOne = "stream-one"
)

// MaxInboundsPerServer caps how many custom inbounds one server may define. Every
// inbound is a listening socket plus an entry in every generated client config, so
// the ceiling keeps a runaway config from melting the box or bloating subscriptions.
const MaxInboundsPerServer = 16

// Inbound is one operator-defined listening endpoint on one server. It sits beside
// the built-in lanes (VLESS-Vision on :443, VLESS-XHTTP-REALITY, Hysteria2) rather
// than replacing them: the built-ins stay the opinionated happy path, and these are
// the escape hatch for a specific client or a specific censor.
//
// An inbound belongs to exactly one server (ServerID, LocalNodeID for the master) —
// there is no global list with per-node toggles, because a port that is free on one
// box says nothing about the next one.
type Inbound struct {
	ID       int64       `json:"id"`
	ServerID int64       `json:"server_id"` // LocalNodeID (0) = the master
	Enabled  bool        `json:"enabled"`
	Sort     int         `json:"sort"`
	Name     string      `json:"name"` // node label shown in the client
	Protocol string      `json:"protocol"`
	Port     int         `json:"port"`
	Opts     InboundOpts `json:"opts"`

	CreatedAt int64 `json:"created_at"`
}

// InboundOpts is the transport/security-dependent half of an inbound. It is stored
// as one JSON blob rather than thirty nullable columns because which fields are even
// meaningful depends on the protocol × transport × security combination — see
// (*Inbound).Validate, which is the single place that decides.
type InboundOpts struct {
	Transport string `json:"transport"`
	Security  string `json:"security"`

	// SNI overrides the TLS server name (empty ⇒ the server's own host). Also the
	// value that goes into the share link's sni=.
	SNI string `json:"sni,omitempty"`
	// FP is the uTLS fingerprint advertised in the share link (fp=). Not applicable
	// to Hysteria2, which has no uTLS.
	FP string `json:"fp,omitempty"`

	// Path is the request path for ws / httpupgrade / xhttp. Host is the Host header
	// those transports send (empty ⇒ the SNI).
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`

	// Mode is the XHTTP mode (see XHTTP* constants); empty ⇒ Xray's default.
	Mode string `json:"mode,omitempty"`

	// ServiceName is the gRPC service name.
	ServiceName string `json:"service_name,omitempty"`

	// Flow is the VLESS flow ("" or xtls-rprx-vision). Vision is raw-TCP only.
	Flow string `json:"flow,omitempty"`

	// REALITY material. Unlike the built-in lane (whose keys live in settings / on
	// the node row), a custom inbound carries its own, so two REALITY inbounds on one
	// box are genuinely independent identities.
	RealityDest        string `json:"reality_dest,omitempty"` // donor SNI(s), comma-separated
	RealityPrivateKey  string `json:"reality_private_key,omitempty"`
	RealityPublicKey   string `json:"reality_public_key,omitempty"`
	RealityShortID     string `json:"reality_short_id,omitempty"`
	RealityMaxTimeDiff int    `json:"reality_max_time_diff,omitempty"`

	// Hysteria2 port-hopping: the client sprays HopStart–HopEnd and the host's
	// nftables funnels the range onto Port. HopInterval is "min-max" seconds.
	HopStart    int    `json:"hop_start,omitempty"`
	HopEnd      int    `json:"hop_end,omitempty"`
	HopInterval string `json:"hop_interval,omitempty"`

	// Obfs is the Hysteria2 Salamander pre-shared key (empty ⇒ no obfuscation).
	// Client and server must agree on it, so it rides in the share link and in every
	// generated profile. See ValidObfsPassword for the shape and Settings.HysteriaObfs
	// for the built-in lane's equivalent.
	Obfs string `json:"obfs,omitempty"`

	// VLESS Encryption (Xray's post-quantum handshake, `xray vlessenc`): EncMode is
	// what the operator picks — "" (off) or how the handshake looks on the wire
	// (EncNative, EncXorPub, EncRandom); Decryption (the server's, with its private
	// key) and Encryption (the clients', with the public one) are generated from it by
	// the panel — mlkem768x25519plus.<mode>.600s|0rtt.<X25519 key>. It is what lets a
	// VLESS inbound go without TLS (raw TCP, or behind a CDN that reads the HTTP), and
	// Xray refuses fallbacks with it, so the built-in :443 lane cannot have it.
	EncMode    string `json:"enc_mode,omitempty"`
	Decryption string `json:"decryption,omitempty"`
	Encryption string `json:"encryption,omitempty"`

	// CDNHost puts the inbound behind a CDN: clients connect to this domain (on CDNPort,
	// 443 when 0) over TLS, the CDN to the inbound's own port — plain (security none) or
	// TLS — and the inbound trusts the X-Forwarded-For it adds, so users are seen from
	// their own addresses. Only for the HTTP-shaped transports, never with REALITY.
	CDNHost string `json:"cdn_host,omitempty"`
	CDNPort int    `json:"cdn_port,omitempty"`

	// PinCert has clients check the server's certificate by its fingerprint rather than
	// by name, so SNI can be any name — one a censor lets through, say. TLS only, not
	// behind a CDN (whose certificate is its own).
	PinCert bool `json:"pin_cert,omitempty"`

	// Shadowsocks-2022. Method is the AEAD (see SS2022* constants). ShadowKey is the
	// inbound's SERVER key — the half shared by every user, generated by the panel
	// (see prepareInbound), base64 of SSKeyLen(Method) random bytes. The per-user key
	// is NOT stored: it is derived from the user's UUID on demand (UserShadowKey), so
	// it stays in lockstep with the credential the rest of the panel already tracks.
	Method    string `json:"method,omitempty"`
	ShadowKey string `json:"shadow_key,omitempty"`

	// WireGuard over a TURN relay (see inbound_wireguard.go). WGPrivateKey is the
	// inbound's own server key, generated by the panel and never shown; WGPublicKey is
	// what clients pin. WGLocalPort is where Xray's WireGuard inbound listens, on
	// loopback — the public Port belongs to the DTLS relay in front of it. TurnLink is
	// the call invite link users' TURN clients take their credentials from; empty
	// leaves each user to bring their own.
	WGPrivateKey string `json:"wg_private_key,omitempty"`
	WGPublicKey  string `json:"wg_public_key,omitempty"`
	WGLocalPort  int    `json:"wg_local_port,omitempty"`
	TurnLink     string `json:"turn_link,omitempty"`
	// TurnMaskKey is the Free Turn Proxy masking key the relay and every client share
	// (hex of 32 bytes), generated by the panel: without the mask the call service
	// shapes the relay to a couple of megabits.
	TurnMaskKey string `json:"turn_mask_key,omitempty"`

	// --- Advanced knobs -------------------------------------------------------
	//
	// These split by a property that decides everything about how they are handled:
	// whether the CLIENT has to know the same value.
	//
	// XHTTPExtra, HeaderType/Hosts/Paths, Authority and MultiMode must match on both
	// ends, so each is projected into the generated share links as well as the
	// inbound. Sockopt and TLSExtra are server-local — the client negotiates or
	// simply doesn't care — so a mistake there cannot desync anyone.
	//
	// Nothing here is exposed that the panel cannot mirror. Arbitrary ws/httpupgrade
	// request headers are the notable omission: the server accepts them but the share
	// link has nowhere to carry them, so offering them would mean handing out links
	// that don't work against the inbound the same form just created.

	// XHTTPExtra is the XHTTP transport's `extra` object, stored and emitted verbatim.
	//
	// It works as one blob rather than N fields because Xray defines `extra` as a
	// complete XHTTP config that the outer host/path/mode then override, and the
	// vless:// link carries the same object in its `extra=` parameter. So one stored
	// value projects to both sides with no field-by-field mapping to drift out of
	// sync. Validated against XHTTPExtraKeys — an unknown key is silently ignored by
	// Xray, which is the worst kind of wrong.
	XHTTPExtra json.RawMessage `json:"xhttp_extra,omitempty"`

	// HTTP masquerade for the raw-TCP transport: the connection carries a plausible
	// HTTP request/response framing instead of going straight to proxy bytes.
	// HeaderType is "" (none) or "http"; the hosts and paths are what the framing
	// claims. Mirrored into links as headerType/host/path.
	HeaderType  string   `json:"header_type,omitempty"`
	HeaderHosts []string `json:"header_hosts,omitempty"`
	HeaderPaths []string `json:"header_paths,omitempty"`

	// gRPC extras. Authority overrides the :authority pseudo-header; MultiMode
	// multiplexes several streams per connection (link: mode=multi).
	Authority string `json:"authority,omitempty"`
	MultiMode bool   `json:"multi_mode,omitempty"`

	// Sockopt is the inbound's socket options, server-only. Validated against
	// SockoptKeys.
	Sockopt json.RawMessage `json:"sockopt,omitempty"`

	// TLSExtra overlays extra tlsSettings keys (cipher suites, version ceiling, SNI
	// rejection …) onto the ones the panel derives. Server-only; validated against
	// TLSExtraKeys, which deliberately excludes the fields the panel owns — letting
	// an operator overwrite the certificate or the ALPN from here would break the
	// lane in a way the editor cannot see.
	TLSExtra json.RawMessage `json:"tls_extra,omitempty"`
}

// XHTTPExtraKeys is every key Xray's XHTTP parser reads, taken from
// infra/conf.SplitHTTPConfig in the pinned release. An unknown key is not an error
// in Xray — it is silently dropped — so the panel refuses it rather than letting an
// operator believe a misspelled setting is in force.
//
// host/path/mode are absent on purpose: the parser overwrites them from the outer
// settings, so accepting them here would only look like it worked.
var XHTTPExtraKeys = map[string]bool{
	"headers": true, "xPaddingBytes": true, "xPaddingObfsMode": true,
	"xPaddingKey": true, "xPaddingHeader": true, "xPaddingPlacement": true,
	"xPaddingMethod": true, "uplinkHTTPMethod": true,
	"sessionIDPlacement": true, "sessionIDKey": true, "sessionIDTable": true,
	"sessionIDLength": true, "seqPlacement": true, "seqKey": true,
	"uplinkDataPlacement": true, "uplinkDataKey": true, "uplinkChunkSize": true,
	"noGRPCHeader": true, "noSSEHeader": true,
	"scMaxEachPostBytes": true, "scMinPostsIntervalMs": true,
	"scMaxBufferedPosts": true, "scStreamUpServerSecs": true,
	"serverMaxHeaderBytes": true, "xmux": true,
}

// SockoptKeys is the socket-option set, from infra/conf.SocketConfig.
// acceptProxyProtocol and trustedXForwardedFor are excluded: they say to trust a
// forwarded client IP, and the panel — not the operator — decides which upstreams
// are trusted, since getting it wrong lets a client forge its own source address and
// defeat the per-user device limit.
var SockoptKeys = map[string]bool{
	"mark": true, "tcpFastOpen": true, "tproxy": true, "domainStrategy": true,
	"dialerProxy": true, "tcpKeepAliveInterval": true, "tcpKeepAliveIdle": true,
	"tcpCongestion": true, "tcpWindowClamp": true, "tcpMaxSeg": true,
	"penetrate": true, "tcpUserTimeout": true, "v6only": true, "interface": true,
	"tcpMptcp": true, "customSockopt": true, "addressPortStrategy": true,
	"happyEyeballs": true,
}

// TLSExtraKeys is the tlsSettings the operator may add on top of the derived ones.
//
// Excluded and why: certificates (the panel manages them), serverName and alpn (both
// are mirrored into the link, so an override here would desync clients), and
// allowInsecure (removed from current Xray, and asking a server to be lax about its
// own certificate is meaningless).
var TLSExtraKeys = map[string]bool{
	"minVersion": true, "maxVersion": true, "cipherSuites": true,
	"rejectUnknownSni": true, "curvePreferences": true,
	"enableSessionResumption": true, "disableSystemRoot": true,
	"verifyPeerCertByName": true, "echServerKeys": true, "echConfigList": true,
}

// validateJSONObject checks that a stored blob is a JSON object whose keys are all
// recognized. label names the field in the error.
func validateJSONObject(blob json.RawMessage, allowed map[string]bool, label string) error {
	if len(blob) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(blob, &fields); err != nil {
		return fieldErr("err.expectJSONObject", "{{field}}: ожидается JSON-объект ({{err}})",
			map[string]any{"field": label, "err": err})
	}
	var unknown []string
	for k := range fields {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fieldErr("err.unknownXrayKeys",
			"{{field}}: Xray не знает эти параметры и молча их проигнорирует — {{keys}}",
			map[string]any{"field": label, "keys": strings.Join(unknown, ", ")})
	}
	return nil
}

// LaneNameRe matches a connection's display name — a custom inbound's and a
// built-in lane's alike, which is why it lives here rather than in two packages:
// both become a sing-box tag and a Clash node name, and a charset that drifted
// between them would let one surface accept what the other rejects.
//
// It is an allowlist, and what it keeps out is the punctuation that would break the
// documents the name is embedded in: quotes and colons. Braces are IN, because a name
// may carry the variables in nametmpl.go ("{server} {left}") — they are expanded before
// the name reaches any document, and a brace that survives (an unknown variable, kept
// verbatim on purpose) is escaped by every surface that renders it. The middle dot is
// in for the same reason: it is the separator the panel's own "<server> · <lane>"
// prefix uses, so a name that places {server} itself has to be able to spell it. Emoji are none of
// those and are explicitly in — a flag is how an operator labels a location, and
// every format the name reaches handles it (Clash quotes with %q, sing-box goes
// through encoding/json, and a share link percent-escapes its fragment).
//
// Flags are the reason for the ranges below: one is a PAIR of regional indicator
// symbols, which are category So, and the rest of the emoji machinery — skin tones,
// ZWJ sequences, variation selectors — is spread across categories that \p{So}
// alone does not cover.
var LaneNameRe = regexp.MustCompile(`^[\p{L}\p{N} _.(){}·\-` +
	`\p{So}` + // emoji proper, and both halves of a flag
	`\x{1F3FB}-\x{1F3FF}` + // skin-tone modifiers (category Sk)
	`\x{200D}\x{FE0E}\x{FE0F}\x{20E3}` + // ZWJ, variation selectors, keycap
	`]+$`)

// inboundPathRe matches a ws/httpupgrade/xhttp request path.
var inboundPathRe = regexp.MustCompile(`^/[A-Za-z0-9_\-./]{0,64}$`)

// inboundServiceRe matches a gRPC service name.
var inboundServiceRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// inboundHopRe matches the port-hopping interval "min-max" (seconds).
var inboundHopRe = regexp.MustCompile(`^\d+-\d+$`)

// CanonicalProtocol is an inbound protocol as the panel stores it: lower case, and the
// name a protocol had before a rename mapped to the current one.
func CanonicalProtocol(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == inbTurnVKOld {
		return InbTurnVK
	}
	return p
}

// InboundProtocols lists the offered protocols, in UI order.
var InboundProtocols = []string{InbVLESS, InbTrojan, InbHysteria, InbShadowsocks, InbTurnVK, InbWireGuard}

// IsWireGuard reports whether a protocol is one of the two WireGuard inbounds — behind
// a TURN relay or on a port of its own. Both are Xray's "wireguard" inbound with the
// users' tunnel identities as peers; they differ in who holds the public port.
func IsWireGuard(protocol string) bool { return protocol == InbTurnVK || protocol == InbWireGuard }

// InboundTransports returns the transports valid for a protocol. Hysteria2 has
// exactly one (its own QUIC), so the UI shows no transport control for it, and
// Shadowsocks-2022 is raw TCP (carrying UDP inside it) with no transport choice
// either — TrShadowsocks stands in so the stored value is never empty.
func InboundTransports(protocol string) []string {
	switch protocol {
	case InbVLESS, InbTrojan:
		return []string{TrTCP, TrWS, TrXHTTP, TrGRPC, TrHTTPUpgrade}
	case InbHysteria:
		return []string{TrHysteria}
	case InbShadowsocks:
		return []string{TrShadowsocks}
	case InbTurnVK:
		return []string{TrTURN}
	case InbWireGuard:
		return []string{TrWireGuard}
	}
	return nil
}

// InboundSecurities returns the security layers valid for a protocol × transport.
//
// The rules encode two constraints. REALITY is a TCP-based TLS impersonation, so it
// is offered only where a client can actually speak it (raw TCP, gRPC, XHTTP) and
// never for Trojan, whose clients have no consistent REALITY support. "none" is
// offered only for the HTTP-shaped transports, where something else (a CDN, nginx)
// is plausibly terminating TLS in front — on raw TCP it would just be a plaintext
// proxy on a public port.
func InboundSecurities(protocol, transport string) []string {
	if protocol == InbHysteria {
		return []string{SecTLS}
	}
	if protocol == InbShadowsocks || IsWireGuard(protocol) {
		// Shadowsocks-2022 brings its own AEAD, and WireGuard its own Noise handshake
		// (wrapped in DTLS by the relay); there is no TLS/REALITY layer to pick, so
		// "none" is the only (and derived, not operator-chosen) value.
		return []string{SecNone}
	}
	switch transport {
	case TrTCP:
		if protocol == InbVLESS {
			// none only with VLESS Encryption (see Validate): it brings its own.
			return []string{SecTLS, SecReality, SecNone}
		}
		return []string{SecTLS}
	case TrGRPC, TrXHTTP:
		if protocol == InbVLESS {
			return []string{SecNone, SecTLS, SecReality}
		}
		return []string{SecNone, SecTLS}
	case TrWS, TrHTTPUpgrade:
		return []string{SecNone, SecTLS}
	}
	return nil
}

// contains reports whether list holds v.
func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// NeedsRealityKeys reports whether this inbound's REALITY material still has to be
// generated (security is REALITY but no keypair is stored yet).
func (in *Inbound) NeedsRealityKeys() bool {
	return in.Opts.Security == SecReality && in.Opts.RealityPrivateKey == ""
}

// NeedsShadowKey reports whether this inbound still needs its server key generated —
// a Shadowsocks inbound whose stored key is missing or the wrong length for its
// method (which happens when the operator switches methods, since aes-128 and
// aes-256 use different key sizes).
func (in *Inbound) NeedsShadowKey() bool {
	if in.Protocol != InbShadowsocks {
		return false
	}
	n := SSKeyLen(in.Opts.Method)
	if n == 0 {
		return false // unknown method; Validate rejects it before this matters
	}
	raw, err := base64.StdEncoding.DecodeString(in.Opts.ShadowKey)
	return err != nil || len(raw) != n
}

// UserShadowKey derives one user's Shadowsocks-2022 key from their UUID, as base64
// of the method's key length. Derived rather than stored so it tracks the UUID the
// rest of the panel already rotates and revokes: reset a user's UUID and their
// Shadowsocks key changes with it, with nothing else to update. The domain prefix
// keeps this hash from ever colliding with another use of the same UUID.
func UserShadowKey(uuid, method string) string {
	n := SSKeyLen(method)
	if n == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte("rospanel-ss2022|" + uuid))
	return base64.StdEncoding.EncodeToString(sum[:n])
}

// LockedShadowKey is the key of the placeholder user kept on a Shadowsocks inbound that
// no real user may use — the one that stops Xray collapsing an empty user list into a
// single-user server keyed by the server key.
//
// It is bound to THIS install's secret key, not to the server key. The old derivation
// hashed the server key under a private-looking domain string and claimed the result was
// "known to no client" — but SS2022 multi-user REQUIRES the server key inside every
// client credential (the link is base64(method:serverKey:userKey)), so every user who
// ever held a link for this inbound has the input, and the domain string is in public
// source. Anyone who was ever granted the lane could therefore compute this key and
// authenticate as the placeholder — with no account and no quota behind the traffic —
// in exactly the state the placeholder exists to close.
//
// Deriving from the install key keeps the property that actually mattered: stable across
// restarts, so the generated config does not churn and bounce Xray.
//
// Without an install key (encryption disabled) there is no secret to build on, so it
// falls back to the old derivation. That mode already keeps every secret in the clear on
// disk, so this is not the weakest link there.
// deriveProbe reports whether this install has a key to derive placeholder secrets from.
// Used by tests to tell the secure path from the documented fallback.
func deriveProbe() ([]byte, bool) { return datasec.Derive("probe") }

func LockedShadowKey(serverKey, method string) string {
	n := SSKeyLen(method)
	if n == 0 {
		return ""
	}
	if secret, ok := datasec.Derive("ss2022-locked|" + serverKey); ok {
		return base64.StdEncoding.EncodeToString(secret[:n])
	}
	sum := sha256.Sum256([]byte("rospanel-ss2022-locked|" + serverKey))
	return base64.StdEncoding.EncodeToString(sum[:n])
}

// UsesHopping reports whether this inbound asks for Hysteria2 port-hopping, i.e. a
// range above its base port that the host's nftables must funnel onto it.
func (in *Inbound) UsesHopping() bool {
	return in.Protocol == InbHysteria && in.Opts.HopEnd > in.Port
}

// Tag is the Xray inbound tag for this record, and the handle the live add/remove-
// user API addresses it by. Derived from the immutable row id so it survives every
// rename and reorder.
func (in *Inbound) Tag() string { return fmt.Sprintf("custom-%d", in.ID) }

// RealitySNI is the primary donor (the one that goes into share links).
func (o InboundOpts) RealitySNI() string {
	first, _, _ := strings.Cut(o.RealityDest, ",")
	return strings.TrimSpace(first)
}

// RealityServerNames is every accepted donor SNI.
func (o InboundOpts) RealityServerNames() []string {
	var out []string
	for _, d := range strings.Split(o.RealityDest, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// RealityShortIDs splits the stored comma-separated shortId list.
func (o InboundOpts) RealityShortIDs() []string {
	var out []string
	for _, s := range strings.Split(o.RealityShortID, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// FPOr returns the link fingerprint, defaulting to firefox.
func (o InboundOpts) FPOr() string { return fpOr(o.FP) }

// Salamander obfuscation key limits. The floor is well above Xray's own 4-byte
// minimum: the key is the only thing standing between a probe and a recognisable
// QUIC handshake, and four characters is guessable. The charset is deliberately
// URL- and JSON-safe — the key is carried in a share-link query parameter and inside
// the link's double-encoded finalmask blob, so a quote or an ampersand there would
// corrupt the link rather than fail loudly.
const (
	ObfsMinLen = 8
	ObfsMaxLen = 64
)

var obfsRe = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// ValidObfsPassword reports whether s is usable as a Salamander pre-shared key.
func ValidObfsPassword(s string) bool {
	return len(s) >= ObfsMinLen && len(s) <= ObfsMaxLen && obfsRe.MatchString(s)
}

// HopIntervalOr returns the port-hopping interval, defaulting to "5-10".
func (o InboundOpts) HopIntervalOr() string {
	if o.HopInterval == "" {
		return "5-10"
	}
	return o.HopInterval
}

// Normalize fills in the derivable defaults and canonicalizes free-text fields, so
// Validate and the generators see one shape regardless of what the UI submitted.
// Called before Validate on every write.
func (in *Inbound) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Protocol = CanonicalProtocol(in.Protocol)
	o := &in.Opts
	o.Transport = strings.ToLower(strings.TrimSpace(o.Transport))
	o.Security = strings.ToLower(strings.TrimSpace(o.Security))
	o.SNI = strings.TrimSpace(o.SNI)
	o.Host = strings.TrimSpace(o.Host)
	o.Mode = strings.TrimSpace(o.Mode)
	o.ServiceName = strings.TrimSpace(o.ServiceName)
	o.RealityDest = strings.TrimSpace(o.RealityDest)
	// VLESS Encryption is VLESS's alone.
	o.EncMode = strings.ToLower(strings.TrimSpace(o.EncMode))
	if in.Protocol != InbVLESS || o.EncMode == "" {
		o.EncMode, o.Decryption, o.Encryption = "", "", ""
	}
	o.CDNHost = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(o.CDNHost), "."))
	if o.CDNHost == "" {
		o.CDNPort = 0
	}
	if (o.Security != SecTLS && in.Protocol != InbHysteria) || o.CDNHost != "" ||
		in.Protocol == InbShadowsocks || IsWireGuard(in.Protocol) { // Hysteria2 is always TLS
		o.PinCert = false
	}

	if in.Protocol == InbHysteria {
		// Hysteria2 is its own transport and always brings its own TLS; the UI never
		// offers a choice, so normalize away whatever was submitted.
		o.Transport = TrHysteria
		o.Security = SecTLS
		o.Flow, o.Path, o.Host, o.Mode, o.ServiceName, o.FP = "", "", "", "", "", ""
		o.RealityDest, o.RealityPrivateKey, o.RealityPublicKey, o.RealityShortID = "", "", "", ""
		o.XHTTPExtra, o.HeaderType, o.HeaderHosts, o.HeaderPaths = nil, "", nil, nil
		o.Authority, o.MultiMode = "", false
		if o.HopInterval == "" && o.HopEnd > in.Port {
			o.HopInterval = "5-10"
		}
		o.Obfs = strings.TrimSpace(o.Obfs)
		o.clearWireGuard()
		return
	}

	if IsWireGuard(in.Protocol) {
		in.normalizeWireGuard()
		return
	}
	o.clearWireGuard()

	if in.Protocol == InbShadowsocks {
		// Raw TCP with its own AEAD: no transport, security, TLS or REALITY to carry,
		// so normalize the whole lot away and keep only the method (defaulted) and the
		// server key. An unknown method falls back to the default rather than being
		// left to fail validation, matching how the other protocols self-correct.
		o.Transport = TrShadowsocks
		o.Security = SecNone
		o.Method = strings.ToLower(strings.TrimSpace(o.Method))
		if SSKeyLen(o.Method) == 0 {
			o.Method = DefaultSSMethod
		}
		o.ShadowKey = strings.TrimSpace(o.ShadowKey)
		o.Flow, o.Path, o.Host, o.Mode, o.ServiceName, o.FP = "", "", "", "", "", ""
		o.SNI = ""
		o.RealityDest, o.RealityPrivateKey, o.RealityPublicKey, o.RealityShortID = "", "", "", ""
		o.XHTTPExtra, o.Sockopt, o.TLSExtra = nil, nil, nil
		o.HeaderType, o.HeaderHosts, o.HeaderPaths = "", nil, nil
		o.Authority, o.MultiMode = "", false
		o.HopStart, o.HopEnd, o.HopInterval = 0, 0, ""
		return
	}
	// A non-Shadowsocks inbound carries no SS material.
	o.Method, o.ShadowKey = "", ""

	// The user types a path without the leading slash; store exactly one.
	if p := strings.TrimSpace(o.Path); p != "" {
		o.Path = "/" + strings.TrimLeft(p, "/")
	}
	// Vision is a raw-TCP flow. Anywhere else it is silently wrong, so it is set here
	// rather than trusted from the request.
	if in.Protocol == InbVLESS && o.Transport == TrTCP {
		o.Flow = VisionFlowName
	} else {
		o.Flow = ""
	}
	if o.Security != SecReality {
		o.RealityDest, o.RealityPrivateKey, o.RealityPublicKey = "", "", ""
		o.RealityShortID, o.RealityMaxTimeDiff = "", 0
	}
	// Transport-specific advanced fields are dropped when the transport changed away
	// from the one that uses them, so a stale value can't quietly reappear in the
	// generated config after an edit.
	if o.Transport != TrXHTTP {
		o.XHTTPExtra = nil
	}
	if o.Transport != TrTCP {
		o.HeaderType, o.HeaderHosts, o.HeaderPaths = "", nil, nil
	}
	if o.Transport != TrGRPC {
		o.Authority, o.MultiMode = "", false
	}
	o.HeaderType = strings.ToLower(strings.TrimSpace(o.HeaderType))
	o.Authority = strings.TrimSpace(o.Authority)
	o.HeaderHosts = trimStrings(o.HeaderHosts)
	o.HeaderPaths = normPaths(o.HeaderPaths)
	// An empty JSON object carries no setting; store nothing rather than "{}" so the
	// generated config stays free of inert blocks.
	o.XHTTPExtra = dropEmptyJSON(o.XHTTPExtra)
	o.Sockopt = dropEmptyJSON(o.Sockopt)
	o.TLSExtra = dropEmptyJSON(o.TLSExtra)
	// Hop and obfuscation fields belong to Hysteria2 only.
	o.HopStart, o.HopEnd, o.HopInterval, o.Obfs = 0, 0, "", ""
}

// ViaCDN reports whether clients reach the inbound through a CDN (CDNHost).
func (o InboundOpts) ViaCDN() bool { return o.CDNHost != "" }

// CDNPortOr is the port clients reach the CDN on.
func (o InboundOpts) CDNPortOr() int {
	if o.CDNPort > 0 {
		return o.CDNPort
	}
	return 443
}

// ClientEndpoint is where a client connects for this inbound: the CDN when there is
// one, the server itself otherwise.
func (in Inbound) ClientEndpoint(serverHost string) (string, int) {
	if in.Opts.ViaCDN() {
		return in.Opts.CDNHost, in.Opts.CDNPortOr()
	}
	return serverHost, in.Port
}

// ClientSecurity is the security a client speaks: TLS to a CDN whatever the CDN speaks
// to the inbound, the inbound's own otherwise.
func (o InboundOpts) ClientSecurity() string {
	if o.ViaCDN() {
		return SecTLS
	}
	return o.Security
}

// VLESS Encryption modes (InboundOpts.EncMode): how the handshake looks on the wire.
// native sends it as it is, xorpub masks the public key, random makes every byte look
// random — for a transport with no TLS around it.
const (
	EncNative = "native"
	EncXorPub = "xorpub"
	EncRandom = "random"
)

// ValidEncMode reports whether m is a VLESS Encryption mode ("" is off, and valid).
func ValidEncMode(m string) bool {
	switch m {
	case "", EncNative, EncXorPub, EncRandom:
		return true
	}
	return false
}

// VLESSEncStrings is the server's decryption and the clients' encryption for a mode
// and an X25519 keypair (base64url, raw): mieru-style dotted configs as `xray
// vlessenc` prints them — 600 s server tickets, 0-RTT clients.
func VLESSEncStrings(mode, priv, pub string) (decryption, encryption string) {
	return "mlkem768x25519plus." + mode + ".600s." + priv, "mlkem768x25519plus." + mode + ".0rtt." + pub
}

// VLESSEncKey is the key at the end of a decryption or encryption string.
func VLESSEncKey(s string) string {
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return s[i+1:]
	}
	return ""
}

// VisionFlowName is the VLESS flow used for raw-TCP Vision. It duplicates
// xray.VisionFlow deliberately: model must not import xray (xray imports model), and
// a wrong flow string is a silent auth failure rather than a compile error.
const VisionFlowName = "xtls-rprx-vision"

// Validate checks one inbound in isolation — everything except how it relates to the
// other inbounds on the same server (port collisions, duplicate names), which is
// ValidateInboundSet's job. Messages are user-facing.
func (in *Inbound) Validate() error {
	if in.Name == "" {
		return fieldErr("err.inboundNameRequired", "укажите название подключения")
	}
	if len([]rune(in.Name)) > 32 {
		return fieldErr("err.inboundNameTooLong2", "название подключения не длиннее 32 символов")
	}
	if !LaneNameRe.MatchString(in.Name) {
		return fieldErr("err.inboundNameCharset2", "недопустимое название {{name}} (буквы, цифры, эмодзи, переменные в фигурных скобках, пробел, . _ - ( ) ·)", map[string]any{"name": in.Name})
	}
	if lower := strings.ToLower(in.Name); lower == "auto" || lower == "direct" {
		return fieldErr("err.inboundNameReserved", "название {{name}} зарезервировано — выберите другое", map[string]any{"name": in.Name})
	}
	if !contains(InboundProtocols, in.Protocol) {
		return fieldErr("err.unknownProtocol", "неизвестный протокол {{value}}", map[string]any{"value": in.Protocol})
	}
	if in.Port < 1 || in.Port > 65535 {
		return fieldErr("err.inboundPortRange", "порт вне диапазона 1–65535")
	}
	o := in.Opts
	if !contains(InboundTransports(in.Protocol), o.Transport) {
		return fieldErr("err.transportUnsupported", "транспорт {{transport}} не поддерживается протоколом {{protocol}}", map[string]any{"transport": o.Transport, "protocol": in.Protocol})
	}
	if o.CDNHost != "" {
		if in.Protocol != InbVLESS && in.Protocol != InbTrojan {
			return fieldErr("err.cdnProtocol", "через CDN — только VLESS и Trojan")
		}
		// Not gRPC: Xray reads the client's address there only from x-real-ip, which a
		// CDN does not set, so every user would show as the CDN's edges — to the device
		// limit and to the source policy that firewalls what it sees.
		switch o.Transport {
		case TrWS, TrHTTPUpgrade, TrXHTTP:
		default:
			return fieldErr("err.cdnTransport", "через CDN — только WebSocket, HTTPUpgrade или XHTTP")
		}
		if o.Security == SecReality {
			return fieldErr("err.cdnReality", "REALITY через CDN не работает")
		}
		if !ValidServerHost(o.CDNHost) || net.ParseIP(o.CDNHost) != nil {
			return fieldErr("err.cdnHost", "домен CDN {{value}} не похож на домен", map[string]any{"value": o.CDNHost})
		}
		if o.CDNPort < 0 || o.CDNPort > 65535 {
			return fieldErr("err.portRange", "порт вне диапазона 1–65535")
		}
	}
	if !ValidEncMode(o.EncMode) {
		return fieldErr("err.encModeUnknown", "неизвестный режим VLESS Encryption {{value}}", map[string]any{"value": o.EncMode})
	}
	// Raw TCP without TLS is a plaintext proxy on a public port — unless VLESS
	// Encryption is what protects it.
	if o.Transport == TrTCP && o.Security == SecNone && o.EncMode == "" {
		return fieldErr("err.tcpNeedsEncryption", "без TLS сырой TCP возможен только с VLESS Encryption")
	}
	sec := InboundSecurities(in.Protocol, o.Transport)
	if !contains(sec, o.Security) {
		return fieldErr("err.securityUnsupported",
			"{{transport}} + {{protocol}} не поддерживает защиту {{security}} (доступно: {{available}})",
			map[string]any{
				"protocol": in.Protocol, "transport": o.Transport,
				"security": o.Security, "available": strings.Join(sec, ", "),
			})
	}

	if in.Protocol == InbHysteria {
		if o.HopEnd != 0 || o.HopStart != 0 {
			if o.HopStart < 1 || o.HopEnd > 65535 || o.HopStart > o.HopEnd {
				return fieldErr("err.badHopRange2", "неверный диапазон хопа")
			}
			if o.HopInterval != "" && !inboundHopRe.MatchString(o.HopInterval) {
				return fieldErr("err.badHopInterval", "неверный интервал хопа (нужно «N-M», напр. 5-10)")
			}
		}
		if o.Obfs != "" && !ValidObfsPassword(o.Obfs) {
			return fieldErr("err.badObfsPassword", "пароль обфускации: {{min}}–{{max}} символов, латиница, цифры и .~_-",
				map[string]any{"min": ObfsMinLen, "max": ObfsMaxLen})
		}
		return nil
	}

	if in.Protocol == InbShadowsocks {
		// The method is the only real choice; everything else (transport, security) is
		// derived. The server key is checked for presence and length, not trusted —
		// prepareInbound fills it, but an inbound arriving straight through the API
		// without it, or carrying a key from a different method's key size, must be
		// caught before it reaches Xray as a silent auth failure.
		if SSKeyLen(o.Method) == 0 {
			return fieldErr("err.ssMethodUnknown",
				"неизвестный метод Shadowsocks {{value}} (доступно: {{available}})",
				map[string]any{"value": o.Method, "available": strings.Join(SSMethods, ", ")})
		}
		if raw, err := base64.StdEncoding.DecodeString(o.ShadowKey); err != nil || len(raw) != SSKeyLen(o.Method) {
			return fieldErr("err.ssKeyBad", "серверный ключ Shadowsocks отсутствует или не той длины для метода")
		}
		return nil
	}

	if IsWireGuard(in.Protocol) {
		return in.validateWireGuard()
	}

	if o.FP != "" && !ValidFingerprint(o.FP) {
		return fieldErr("err.unknownFingerprint2", "неизвестный fingerprint {{value}}", map[string]any{"value": o.FP})
	}
	switch o.Transport {
	case TrWS, TrHTTPUpgrade, TrXHTTP:
		if o.Path == "" {
			return fieldErr("err.pathRequired", "укажите путь для транспорта {{transport}}", map[string]any{"transport": o.Transport})
		}
		if !inboundPathRe.MatchString(o.Path) {
			return fieldErr("err.badPath", "неверный путь (начинается с «/», допустимы латиница, цифры, - _ . /)")
		}
	case TrGRPC:
		if o.ServiceName == "" {
			return fieldErr("err.grpcServiceRequired", "укажите имя gRPC-сервиса")
		}
		if !inboundServiceRe.MatchString(o.ServiceName) {
			return fieldErr("err.badGrpcService", "неверное имя gRPC-сервиса (латиница, цифры, . _ -)")
		}
	}
	if o.Transport == TrXHTTP && o.Mode != "" &&
		!contains([]string{XHTTPAuto, XHTTPPacketUp, XHTTPStreamUp, XHTTPStreamOne}, o.Mode) {
		return fieldErr("err.unknownXhttpMode", "неизвестный режим XHTTP {{value}}", map[string]any{"value": o.Mode})
	}
	if err := validateJSONObject(o.XHTTPExtra, XHTTPExtraKeys, "XHTTP extra"); err != nil {
		return err
	}
	if err := validateJSONObject(o.Sockopt, SockoptKeys, "sockopt"); err != nil {
		return err
	}
	if err := validateJSONObject(o.TLSExtra, TLSExtraKeys, "TLS extra"); err != nil {
		return err
	}
	if o.HeaderType != "" && o.HeaderType != "http" {
		return fieldErr("err.unknownMasqType", "неизвестный тип маскировки {{value}} (доступно: http)", map[string]any{"value": o.HeaderType})
	}
	if o.HeaderType == "http" && len(o.HeaderHosts) == 0 {
		return fieldErr("err.masqHostRequired", "для HTTP-маскировки укажите хотя бы один хост")
	}
	for _, h := range o.HeaderHosts {
		if !RealityHostRe.MatchString(h) {
			return fieldErr("err.masqHostInvalid", "хост маскировки {{value}} не похож на настоящий домен", map[string]any{"value": h})
		}
	}
	for _, p := range o.HeaderPaths {
		if !inboundPathRe.MatchString(p) {
			return fieldErr("err.badMasqPath", "неверный путь маскировки {{value}}", map[string]any{"value": p})
		}
	}
	if o.Authority != "" && !RealityHostRe.MatchString(o.Authority) {
		return fieldErr("err.badAuthority", "authority {{value}} не похож на домен", map[string]any{"value": o.Authority})
	}
	if o.Security == SecReality {
		if len(o.RealityServerNames()) == 0 {
			return fieldErr("err.realityDestRequired2", "укажите домен маскировки REALITY")
		}
		for _, d := range o.RealityServerNames() {
			if !RealityHostRe.MatchString(d) {
				return fieldErr("err.realityDestInvalid2", "домен маскировки REALITY {{value}} не похож на настоящий", map[string]any{"value": d})
			}
		}
	}
	return nil
}

// trimStrings drops blank entries after trimming.
func trimStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// normPaths canonicalizes masquerade paths to exactly one leading slash.
func normPaths(in []string) []string {
	var out []string
	for _, s := range trimStrings(in) {
		out = append(out, "/"+strings.TrimLeft(s, "/"))
	}
	return out
}

// dropEmptyJSON turns an absent, null or empty-object blob into nil, so the
// generated config carries no inert blocks and the stored row stays comparable.
func dropEmptyJSON(b json.RawMessage) json.RawMessage {
	t := strings.TrimSpace(string(b))
	if t == "" || t == "null" || t == "{}" {
		return nil
	}
	return b
}

// HeaderPathsOr returns the masquerade paths, defaulting to "/" when none are given
// (Xray requires at least one, and "/" is what an ordinary request would carry).
func (o InboundOpts) HeaderPathsOr() []string {
	if len(o.HeaderPaths) == 0 {
		return []string{"/"}
	}
	return o.HeaderPaths
}

// RealityHostRe validates a REALITY donor: a real domain (≥1 dot) with an alphabetic
// TLD of 2+ letters. Rejects typos like "www.max.ru1", bare IPs and single labels.
var RealityHostRe = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

// Which generated subscription FORMATS can express a given protocol × transport.
//
// The universal base64 link list is a superset — it is just vless://, trojan:// and
// hysteria2:// URIs consumed by Xray-core clients (Happ, v2rayNG, Streisand, NekoBox
// …) — so everything the panel can build appears there. The two structured formats
// are narrower, and the gaps are real:
//
//   - sing-box has no XHTTP transport at all (upstream declined it), so an XHTTP
//     inbound simply cannot appear in a sing-box profile. That includes Hiddify,
//     which is sing-box-based.
//   - mihomo (Clash Meta) has xhttp, but only for VLESS, and it reaches HTTPUpgrade
//     only through a WebSocket option rather than as its own transport.
//
// An unsupported combination is SKIPPED in that format rather than emitted in some
// approximate shape: a client that rejects one malformed proxy usually rejects the
// whole profile, so a bad entry would cost the user every other server too.

// SupportsClash reports whether mihomo / Clash Meta can express this combination.
func SupportsClash(protocol, transport string) bool {
	switch protocol {
	case InbTurnVK, InbWireGuard:
		return false // a WireGuard config file (plus a TURN client), not a proxy entry
	case InbHysteria, InbShadowsocks:
		return true
	case InbVLESS:
		return transport == TrTCP || transport == TrWS || transport == TrGRPC || transport == TrXHTTP
	case InbTrojan:
		return transport == TrTCP || transport == TrWS || transport == TrGRPC
	}
	return false
}

// SupportsSingBox reports whether sing-box can express this combination.
func SupportsSingBox(protocol, transport string) bool {
	switch protocol {
	case InbTurnVK, InbWireGuard:
		return false // as in SupportsClash
	case InbHysteria, InbShadowsocks:
		return true
	case InbVLESS, InbTrojan:
		return transport == TrTCP || transport == TrWS || transport == TrGRPC || transport == TrHTTPUpgrade
	}
	return false
}

// UnsupportedFormats names the subscription formats that cannot carry this
// combination, for the editor to warn about before the operator saves.
func (in *Inbound) UnsupportedFormats() []string {
	// A WireGuard inbound is in no subscription format at all: users get a config file
	// for it from the subscription page. The editor says that on its own.
	if IsWireGuard(in.Protocol) {
		return nil
	}
	var out []string
	if !SupportsClash(in.Protocol, in.Opts.Transport) {
		out = append(out, "Clash / Mihomo")
	}
	if !in.SingBoxCarries() {
		out = append(out, "sing-box / Hiddify")
	}
	return out
}

// SingBoxCarries reports whether a sing-box profile can hold this inbound: a
// combination sing-box expresses (SupportsSingBox), no VLESS Encryption, which sing-box
// has not got, and no certificate pin — sing-box pins a public key's hash, which a
// node does not report.
func (in *Inbound) SingBoxCarries() bool {
	return SupportsSingBox(in.Protocol, in.Opts.Transport) && in.Opts.EncMode == "" && !in.Opts.PinCert
}

// ReservedPorts is the set of ports one server's custom inbounds may not take,
// keyed by what already holds them, for the error message. Built by the caller from
// the server's effective settings — see core.reservedPorts.
type ReservedPorts struct {
	TCP map[int]string
	UDP map[int]string
}

// NewReservedPorts is an empty set.
func NewReservedPorts() ReservedPorts {
	return ReservedPorts{TCP: map[int]string{}, UDP: map[int]string{}}
}

// HoldTCP / HoldUDP / Hold record who owns a port. Hold takes both transports and
// is for a listener that is not a socket of ours to reason about — the panel's own
// loopback port, Xray's internal API — where the safe answer is "nobody else".
//
// Two owners of the same number on the same transport are joined rather than
// overwritten: the built-in lanes routinely share a number (Vision on TCP/443 and
// the system proxy behind it), and keeping only the last one told the operator
// their port collides with the wrong thing.
func (r ReservedPorts) HoldTCP(port int, who string) { hold(r.TCP, port, who) }
func (r ReservedPorts) HoldUDP(port int, who string) { hold(r.UDP, port, who) }
func (r ReservedPorts) Hold(port int, who string) {
	r.HoldTCP(port, who)
	r.HoldUDP(port, who)
}

func hold(m map[int]string, port int, who string) {
	if port <= 0 || m == nil {
		return
	}
	if prev, ok := m[port]; ok {
		if prev == who || strings.Contains(prev, who) {
			return
		}
		m[port] = prev + " / " + who
		return
	}
	m[port] = who
}

// OnTCP / OnUDP report who holds a port on that transport.
func (r ReservedPorts) OnTCP(port int) (string, bool) { who, ok := r.TCP[port]; return who, ok }
func (r ReservedPorts) OnUDP(port int) (string, bool) { who, ok := r.UDP[port]; return who, ok }

// ProtoOf is the transport an inbound listens on: Hysteria2 is QUIC over UDP and
// WireGuard's relay takes DTLS over UDP; everything else the panel offers is TCP.
func ProtoOf(protocol string) string {
	if protocol == InbHysteria || IsWireGuard(protocol) {
		return "udp"
	}
	return "tcp"
}

// On reports who holds a port on one transport ("tcp" | "udp").
func (r ReservedPorts) On(proto string, port int) (string, bool) {
	if proto == "udp" {
		return r.OnUDP(port)
	}
	return r.OnTCP(port)
}

// ValidateInboundSet checks a server's whole inbound list together: the per-inbound
// rules, plus everything that is only visible across the set — duplicate display
// names (they become colliding sing-box/Clash tags), duplicate ports, ports already
// held by a built-in lane, and overlapping Hysteria2 hop ranges (two nftables
// funnels over the same UDP port would fight).
//
// takenNames are display names already in use on this server that are NOT in list —
// in practice the three built-in lanes' labels. A custom inbound that takes one of
// them produces two proxies with the same name in the generated Clash/sing-box
// document, and a client that sees a duplicate tag rejects the whole profile, so the
// user would lose every other server too.
//
// Only ENABLED inbounds are checked for PORT collisions: a disabled one occupies
// nothing, so parking a spare config on a busy port is allowed until it is switched
// on. Names are checked regardless — a disabled inbound is still shown in the editor,
// and letting two entries share a name only to fail on enable is worse.
func ValidateInboundSet(list []Inbound, reserved ReservedPorts, takenNames []string) error {
	if len(list) > MaxInboundsPerServer {
		return fieldErr("err.tooManyInbounds", "слишком много подключений: максимум {{max}}", map[string]any{"max": MaxInboundsPerServer})
	}
	names := map[string]bool{}
	for _, n := range takenNames {
		if n = strings.TrimSpace(n); n != "" {
			names[strings.ToLower(n)] = true
		}
	}
	ports := map[string]map[int]string{"tcp": {}, "udp": {}}
	type hopRange struct {
		name     string
		from, to int
	}
	var hops []hopRange

	for i := range list {
		in := &list[i]
		if err := in.Validate(); err != nil {
			return err
		}
		lower := strings.ToLower(in.Name)
		if names[lower] {
			return fieldErr("err.inboundNameDuplicate2", "название подключения {{name}} уже занято на этом сервере — сделайте их разными", map[string]any{"name": in.Name})
		}
		names[lower] = true
		if !in.Enabled {
			continue
		}
		// Per transport: a UDP listener and a TCP one may share a number, and the
		// built-in lanes do exactly that by default (Vision on TCP/443, Hysteria2 on
		// UDP/443). Refusing across transports turned a legal configuration into an
		// error the operator could not resolve.
		proto := ProtoOf(in.Protocol)
		if who, taken := reserved.On(proto, in.Port); taken {
			return fieldErr("err.portTakenBy", "порт {{port}} уже занят ({{who}}) — выберите другой", map[string]any{"port": in.Port, "who": who})
		}
		if who, dup := ports[proto][in.Port]; dup {
			return fieldErr("err.portTakenByInbound", "порт {{port}} уже занят подключением «{{who}}»", map[string]any{"port": in.Port, "who": who})
		}
		ports[proto][in.Port] = in.Name

		// WireGuard holds a second UDP port, Xray's loopback listener behind the relay.
		// Loopback or not, it is a socket on this box, and a port another listener has
		// stops Xray from starting.
		if in.Protocol == InbTurnVK {
			if who, taken := reserved.OnUDP(in.Opts.WGLocalPort); taken {
				return fieldErr("err.portTakenBy", "порт {{port}} уже занят ({{who}}) — выберите другой", map[string]any{"port": in.Opts.WGLocalPort, "who": who})
			}
			if who, dup := ports["udp"][in.Opts.WGLocalPort]; dup {
				return fieldErr("err.portTakenByInbound", "порт {{port}} уже занят подключением «{{who}}»", map[string]any{"port": in.Opts.WGLocalPort, "who": who})
			}
			ports["udp"][in.Opts.WGLocalPort] = in.Name
		}

		if in.UsesHopping() {
			from := in.Opts.HopStart
			if from <= in.Port {
				from = in.Port + 1
			}
			for _, h := range hops {
				if from <= h.to && h.from <= in.Opts.HopEnd {
					return fieldErr("err.hopRangeOverlap",
						"диапазон хопа {{from}}–{{to}} пересекается с «{{name}}» ({{otherFrom}}–{{otherTo}})",
						map[string]any{
							"from": from, "to": in.Opts.HopEnd,
							"name": h.name, "otherFrom": h.from, "otherTo": h.to,
						})
				}
			}
			hops = append(hops, hopRange{in.Name, from, in.Opts.HopEnd})
		}
	}
	// A hop range must not swallow another inbound's base port: the nftables redirect
	// would silently steal its traffic.
	// A hop range is a UDP funnel, so only UDP listeners can be swallowed by it.
	for _, h := range hops {
		for p, who := range ports["udp"] {
			if p >= h.from && p <= h.to && who != h.name {
				return fieldErr("err.hopRangeCoversInbound",
					"диапазон хопа «{{name}}» ({{from}}–{{to}}) накрывает порт {{port}} подключения «{{who}}»",
					map[string]any{"name": h.name, "from": h.from, "to": h.to, "port": p, "who": who})
			}
		}
		for p, who := range reserved.UDP {
			if p >= h.from && p <= h.to {
				return fieldErr("err.hopRangeCoversPort",
					"диапазон хопа «{{name}}» ({{from}}–{{to}}) накрывает порт {{port}} ({{who}})",
					map[string]any{"name": h.name, "from": h.from, "to": h.to, "port": p, "who": who})
			}
		}
	}
	return nil
}
