package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Custom-inbound protocols. Deliberately a short list: these three cover every
// transport the panel can also emit a working client config for, and each reuses a
// credential the user already has (VLESS → UUID, Trojan/Hysteria2 → Password), so
// adding an inbound never needs a new per-user secret.
const (
	InbVLESS    = "vless"
	InbTrojan   = "trojan"
	InbHysteria = "hysteria2"
)

// Custom-inbound transports. Hysteria2 has none of these — it is its own QUIC
// transport — and stores TransportHysteria so the stored value is never empty.
const (
	TrTCP         = "tcp"
	TrWS          = "ws"
	TrXHTTP       = "xhttp"
	TrGRPC        = "grpc"
	TrHTTPUpgrade = "httpupgrade"
	TrHysteria    = "hysteria"
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
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"` // LocalNodeID (0) = the master
	Enabled  bool   `json:"enabled"`
	Sort     int    `json:"sort"`
	Name     string `json:"name"` // node label shown in the client
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
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

	// VLESS Encryption (Xray's post-quantum handshake, `xray vlessenc`). Reserved:
	// nothing generates it yet, but current Xray deprecates VLESS-without-flow and
	// points at this as the migration, so the field exists to avoid re-migrating
	// every stored inbound when it lands.
	Decryption string `json:"decryption,omitempty"`
	Encryption string `json:"encryption,omitempty"`

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
		return fmt.Errorf("%s: ожидается JSON-объект (%v)", label, err)
	}
	var unknown []string
	for k := range fields {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%s: Xray не знает эти параметры и молча их проигнорирует — %s",
			label, strings.Join(unknown, ", "))
	}
	return nil
}

// inboundNameRe matches an inbound display name: the same charset the built-in
// connection names allow, because both end up as a sing-box tag / Clash node name
// and must not carry quotes, braces or colons that would break those documents.
var inboundNameRe = regexp.MustCompile(`^[\p{L}\p{N} _.()\-]+$`)

// inboundPathRe matches a ws/httpupgrade/xhttp request path.
var inboundPathRe = regexp.MustCompile(`^/[A-Za-z0-9_\-./]{0,64}$`)

// inboundServiceRe matches a gRPC service name.
var inboundServiceRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// inboundHopRe matches the port-hopping interval "min-max" (seconds).
var inboundHopRe = regexp.MustCompile(`^\d+-\d+$`)

// InboundProtocols lists the offered protocols, in UI order.
var InboundProtocols = []string{InbVLESS, InbTrojan, InbHysteria}

// InboundTransports returns the transports valid for a protocol. Hysteria2 has
// exactly one (its own QUIC), so the UI shows no transport control for it.
func InboundTransports(protocol string) []string {
	switch protocol {
	case InbVLESS, InbTrojan:
		return []string{TrTCP, TrWS, TrXHTTP, TrGRPC, TrHTTPUpgrade}
	case InbHysteria:
		return []string{TrHysteria}
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
	switch transport {
	case TrTCP:
		if protocol == InbVLESS {
			return []string{SecTLS, SecReality}
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
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	o := &in.Opts
	o.Transport = strings.ToLower(strings.TrimSpace(o.Transport))
	o.Security = strings.ToLower(strings.TrimSpace(o.Security))
	o.SNI = strings.TrimSpace(o.SNI)
	o.Host = strings.TrimSpace(o.Host)
	o.Mode = strings.TrimSpace(o.Mode)
	o.ServiceName = strings.TrimSpace(o.ServiceName)
	o.RealityDest = strings.TrimSpace(o.RealityDest)

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
		return
	}

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
	// Hop fields belong to Hysteria2 only.
	o.HopStart, o.HopEnd, o.HopInterval = 0, 0, ""
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
		return fmt.Errorf("укажи название подключения")
	}
	if len([]rune(in.Name)) > 32 {
		return fmt.Errorf("название подключения не длиннее 32 символов")
	}
	if !inboundNameRe.MatchString(in.Name) {
		return fmt.Errorf("недопустимое название %q (буквы, цифры, пробел, . _ - ( ))", in.Name)
	}
	if lower := strings.ToLower(in.Name); lower == "auto" || lower == "direct" {
		return fmt.Errorf("название %q зарезервировано — выбери другое", in.Name)
	}
	if !contains(InboundProtocols, in.Protocol) {
		return fmt.Errorf("неизвестный протокол %q", in.Protocol)
	}
	if in.Port < 1 || in.Port > 65535 {
		return fmt.Errorf("порт вне диапазона 1–65535")
	}
	o := in.Opts
	if !contains(InboundTransports(in.Protocol), o.Transport) {
		return fmt.Errorf("транспорт %q не поддерживается протоколом %s", o.Transport, in.Protocol)
	}
	sec := InboundSecurities(in.Protocol, o.Transport)
	if !contains(sec, o.Security) {
		return fmt.Errorf("%s + %s не поддерживает защиту %q (доступно: %s)",
			in.Protocol, o.Transport, o.Security, strings.Join(sec, ", "))
	}

	if in.Protocol == InbHysteria {
		if o.HopEnd != 0 || o.HopStart != 0 {
			if o.HopStart < 1 || o.HopEnd > 65535 || o.HopStart > o.HopEnd {
				return fmt.Errorf("неверный диапазон хопа")
			}
			if o.HopInterval != "" && !inboundHopRe.MatchString(o.HopInterval) {
				return fmt.Errorf("неверный интервал хопа (нужно «N-M», напр. 5-10)")
			}
		}
		return nil
	}

	if o.FP != "" && !ValidFingerprint(o.FP) {
		return fmt.Errorf("неизвестный fingerprint %q", o.FP)
	}
	switch o.Transport {
	case TrWS, TrHTTPUpgrade, TrXHTTP:
		if o.Path == "" {
			return fmt.Errorf("укажи путь для транспорта %s", o.Transport)
		}
		if !inboundPathRe.MatchString(o.Path) {
			return fmt.Errorf("неверный путь (начинается с «/», допустимы латиница, цифры, - _ . /)")
		}
	case TrGRPC:
		if o.ServiceName == "" {
			return fmt.Errorf("укажи имя gRPC-сервиса")
		}
		if !inboundServiceRe.MatchString(o.ServiceName) {
			return fmt.Errorf("неверное имя gRPC-сервиса (латиница, цифры, . _ -)")
		}
	}
	if o.Transport == TrXHTTP && o.Mode != "" &&
		!contains([]string{XHTTPAuto, XHTTPPacketUp, XHTTPStreamUp, XHTTPStreamOne}, o.Mode) {
		return fmt.Errorf("неизвестный режим XHTTP %q", o.Mode)
	}
	if err := validateJSONObject(o.XHTTPExtra, XHTTPExtraKeys, "XHTTP extra"); err != nil {
		return err
	}
	if err := validateJSONObject(o.Sockopt, SockoptKeys, "sockopt"); err != nil {
		return err
	}
	if err := validateJSONObject(o.TLSExtra, TLSExtraKeys, "доп. TLS"); err != nil {
		return err
	}
	if o.HeaderType != "" && o.HeaderType != "http" {
		return fmt.Errorf("неизвестный тип маскировки %q (доступно: http)", o.HeaderType)
	}
	if o.HeaderType == "http" && len(o.HeaderHosts) == 0 {
		return fmt.Errorf("для HTTP-маскировки укажи хотя бы один хост")
	}
	for _, h := range o.HeaderHosts {
		if !RealityHostRe.MatchString(h) {
			return fmt.Errorf("хост маскировки %q не похож на настоящий домен", h)
		}
	}
	for _, p := range o.HeaderPaths {
		if !inboundPathRe.MatchString(p) {
			return fmt.Errorf("неверный путь маскировки %q", p)
		}
	}
	if o.Authority != "" && !RealityHostRe.MatchString(o.Authority) {
		return fmt.Errorf("authority %q не похож на домен", o.Authority)
	}
	if o.Security == SecReality {
		if len(o.RealityServerNames()) == 0 {
			return fmt.Errorf("укажи домен маскировки REALITY")
		}
		for _, d := range o.RealityServerNames() {
			if !RealityHostRe.MatchString(d) {
				return fmt.Errorf("домен маскировки REALITY %q не похож на настоящий", d)
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
	case InbHysteria:
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
	case InbHysteria:
		return true
	case InbVLESS, InbTrojan:
		return transport == TrTCP || transport == TrWS || transport == TrGRPC || transport == TrHTTPUpgrade
	}
	return false
}

// UnsupportedFormats names the subscription formats that cannot carry this
// combination, for the editor to warn about before the operator saves.
func (in *Inbound) UnsupportedFormats() []string {
	var out []string
	if !SupportsClash(in.Protocol, in.Opts.Transport) {
		out = append(out, "Clash / Mihomo")
	}
	if !SupportsSingBox(in.Protocol, in.Opts.Transport) {
		out = append(out, "sing-box / Hiddify")
	}
	return out
}

// ReservedPorts is the set of ports one server's custom inbounds may not take,
// keyed by what already holds them, for the error message. Built by the caller from
// the server's effective settings — see core.reservedPorts.
type ReservedPorts map[int]string

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
		return fmt.Errorf("слишком много подключений: максимум %d", MaxInboundsPerServer)
	}
	names := map[string]bool{}
	for _, n := range takenNames {
		if n = strings.TrimSpace(n); n != "" {
			names[strings.ToLower(n)] = true
		}
	}
	ports := map[int]string{}
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
			return fmt.Errorf("название подключения %q уже занято на этом сервере — сделай их разными", in.Name)
		}
		names[lower] = true
		if !in.Enabled {
			continue
		}
		if who, taken := reserved[in.Port]; taken {
			return fmt.Errorf("порт %d уже занят (%s) — выбери другой", in.Port, who)
		}
		if who, dup := ports[in.Port]; dup {
			return fmt.Errorf("порт %d уже занят подключением «%s»", in.Port, who)
		}
		ports[in.Port] = in.Name

		if in.UsesHopping() {
			from := in.Opts.HopStart
			if from <= in.Port {
				from = in.Port + 1
			}
			for _, h := range hops {
				if from <= h.to && h.from <= in.Opts.HopEnd {
					return fmt.Errorf("диапазон хопа %d–%d пересекается с «%s» (%d–%d)",
						from, in.Opts.HopEnd, h.name, h.from, h.to)
				}
			}
			hops = append(hops, hopRange{in.Name, from, in.Opts.HopEnd})
		}
	}
	// A hop range must not swallow another inbound's base port: the nftables redirect
	// would silently steal its traffic.
	for _, h := range hops {
		for p, who := range ports {
			if p >= h.from && p <= h.to && who != h.name {
				return fmt.Errorf("диапазон хопа «%s» (%d–%d) накрывает порт %d подключения «%s»",
					h.name, h.from, h.to, p, who)
			}
		}
		for p, who := range reserved {
			if p >= h.from && p <= h.to {
				return fmt.Errorf("диапазон хопа «%s» (%d–%d) накрывает порт %d (%s)",
					h.name, h.from, h.to, p, who)
			}
		}
	}
	return nil
}
