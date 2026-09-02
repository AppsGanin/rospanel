// Package xray models the Xray-core config as typed Go structs (so field-name
// mistakes are compile errors) and supervises the Xray child process.
//
// Engine note: Xray-core is the single proxy engine for every lane — the three
// built-in ones (VLESS-Vision-TCP-443 with a fallback to the panel, VLESS-XHTTP-
// REALITY on its own port, Hysteria2-UDP with port-hopping) and every operator-
// defined custom inbound.
package xray

import (
	"encoding/json"
	"fmt"
)

// Config is the top-level Xray configuration document.
type Config struct {
	Log         *Log         `json:"log,omitempty"`
	Stats       *Stats       `json:"stats,omitempty"`
	API         *API         `json:"api,omitempty"`
	Policy      *Policy      `json:"policy,omitempty"`
	DNS         *DNS         `json:"dns,omitempty"`
	Inbounds    []Inbound    `json:"inbounds"`
	Outbounds   []Outbound   `json:"outbounds"`
	Routing     *Routing     `json:"routing,omitempty"`
	Observatory *Observatory `json:"observatory,omitempty"`
}

// Observatory periodically probes the proxy-pool outbounds (subjectSelector tag
// prefixes) so the balancer's leastPing strategy can skip dead/slow proxies.
type Observatory struct {
	SubjectSelector   []string `json:"subjectSelector"`
	ProbeURL          string   `json:"probeUrl,omitempty"`
	ProbeInterval     string   `json:"probeInterval,omitempty"`
	EnableConcurrency bool     `json:"enableConcurrency,omitempty"`
}

// DNS is the Xray DNS block. Servers are upstream resolvers — plain IPs
// ("1.1.1.1"), DoH URLs ("https://dns.google/dns-query"), or "localhost".
type DNS struct {
	Servers []string `json:"servers,omitempty"`
}

// Stats enables the stats engine (marshals to {}).
type Stats struct{}

// API exposes gRPC services (StatsService) on the api-tagged inbound.
type API struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

// Policy turns on per-user up/down counters.
type Policy struct {
	Levels map[string]LevelPolicy `json:"levels,omitempty"`
	System *SystemPolicy          `json:"system,omitempty"`
}

// LevelPolicy enables per-user traffic stats for a policy level and bounds a
// connection's memory: ConnIdle reaps idle connections; BufferSize caps the
// per-connection buffer (KB) so many concurrent flows can't balloon RSS.
type LevelPolicy struct {
	StatsUserUplink   bool `json:"statsUserUplink"`
	StatsUserDownlink bool `json:"statsUserDownlink"`
	ConnIdle          int  `json:"connIdle,omitempty"`
	BufferSize        int  `json:"bufferSize,omitempty"`
}

// SystemPolicy enables system-wide traffic stats.
type SystemPolicy struct {
	StatsInboundUplink    bool `json:"statsInboundUplink"`
	StatsInboundDownlink  bool `json:"statsInboundDownlink"`
	StatsOutboundUplink   bool `json:"statsOutboundUplink"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink"`
}

// DokodemoSettings is the settings block for the api inbound.
type DokodemoSettings struct {
	Address string `json:"address"`
}

// Routing holds the ordered field rules. Unmatched traffic uses the first
// outbound (direct). Balancers group several outbounds into one egress.
type Routing struct {
	DomainStrategy string      `json:"domainStrategy,omitempty"`
	Rules          []RouteRule `json:"rules,omitempty"`
	Balancers      []Balancer  `json:"balancers,omitempty"`
}

// Balancer load-balances across the outbounds whose tag matches a Selector
// prefix. leastPing + the Observatory route to the fastest live one; FallbackTag
// is used when none are healthy.
type Balancer struct {
	Tag         string            `json:"tag"`
	Selector    []string          `json:"selector"`
	Strategy    *BalancerStrategy `json:"strategy,omitempty"`
	FallbackTag string            `json:"fallbackTag,omitempty"`
}

// BalancerStrategy selects the balancing algorithm ("leastPing" | "random" | …).
type BalancerStrategy struct {
	Type string `json:"type"`
}

// RouteRule is one Xray field rule. Same-field values are OR'd; different fields
// are AND'd. Traffic goes to OutboundTag, or BalancerTag (a proxy pool).
type RouteRule struct {
	Type        string   `json:"type"` // always "field"
	InboundTag  []string `json:"inboundTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	Network     string   `json:"network,omitempty"` // "tcp,udp" — catch-all matcher
	Protocol    []string `json:"protocol,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
}

// Log configures Xray logging.
type Log struct {
	Loglevel string `json:"loglevel,omitempty"`
}

// Inbound is one listening proxy endpoint. Settings is protocol-specific.
type Inbound struct {
	Tag            string          `json:"tag,omitempty"`
	Listen         string          `json:"listen,omitempty"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       any             `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Sniffing       *Sniffing       `json:"sniffing,omitempty"`
}

// Sniffing inspects proxied connections to recover their real destination (HTTP
// host / TLS SNI / QUIC) so domain routing rules can match it.
type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride,omitempty"`
}

// Outbound is one egress. Settings is protocol-specific (nil for freedom/blackhole).
type Outbound struct {
	Tag      string `json:"tag,omitempty"`
	Protocol string `json:"protocol"`
	Settings any    `json:"settings,omitempty"`
}

// ProxyOutboundSettings is the "settings" object for a socks/http proxy outbound.
type ProxyOutboundSettings struct {
	Servers []ProxyServer `json:"servers"`
}

// ProxyServer is one upstream proxy (with optional auth).
type ProxyServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []ProxyUser `json:"users,omitempty"`
}

// ProxyUser is the username/password for an authenticated proxy.
type ProxyUser struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// SocksInboundSettings is the "settings" object for a socks forward-proxy inbound
// (proxy mode). Auth is "password" when accounts are present, else "noauth".
type SocksInboundSettings struct {
	Auth     string      `json:"auth"`
	Accounts []ProxyUser `json:"accounts,omitempty"`
	UDP      bool        `json:"udp"`
}

// HTTPInboundSettings is the "settings" object for an http forward-proxy inbound.
type HTTPInboundSettings struct {
	Accounts []ProxyUser `json:"accounts,omitempty"`
}

// WireGuardSettings is the "settings" object for a wireguard outbound (used for
// the Cloudflare WARP egress).
type WireGuardSettings struct {
	SecretKey string          `json:"secretKey"`
	Address   []string        `json:"address"`
	Peers     []WireGuardPeer `json:"peers"`
	Reserved  []int           `json:"reserved,omitempty"`
	MTU       int             `json:"mtu,omitempty"`
	// NoKernelTun forces the userspace (netstack) WireGuard implementation instead
	// of a real kernel TUN device. See warpOutbound for why this is not optional.
	NoKernelTun bool `json:"noKernelTun,omitempty"`
}

// WireGuardPeer is one WireGuard peer (Cloudflare's WARP endpoint).
type WireGuardPeer struct {
	PublicKey  string   `json:"publicKey"`
	Endpoint   string   `json:"endpoint"`
	AllowedIPs []string `json:"allowedIPs,omitempty"`
}

// VLESSInboundSettings is the "settings" object for a VLESS inbound.
type VLESSInboundSettings struct {
	Clients    []VLESSClient `json:"clients"`
	Decryption string        `json:"decryption"` // always "none" for VLESS
	Fallbacks  []Fallback    `json:"fallbacks,omitempty"`
}

// VLESSClient is one VLESS user.
type VLESSClient struct {
	ID    string `json:"id"`             // UUID
	Flow  string `json:"flow,omitempty"` // "xtls-rprx-vision"
	Email string `json:"email,omitempty"`
}

// Fallback routes non-VLESS traffic on the shared port. Dest is an int port or
// "host:port" string. Used in M2+ to send browser/Trojan traffic to the panel.
type Fallback struct {
	Path string `json:"path,omitempty"`
	Dest any    `json:"dest"`
	Xver int    `json:"xver,omitempty"`
}

// StreamSettings configures transport + transport-level security.
type StreamSettings struct {
	// Network is the transport: "tcp" | "ws" | "xhttp" | "grpc" | "httpupgrade" |
	// "hysteria".
	Network             string               `json:"network,omitempty"`
	Security            string               `json:"security,omitempty"` // "tls" | "reality" | "" (none)
	TLSSettings         *TLSSettings         `json:"tlsSettings,omitempty"`
	TCPSettings         *TCPSettings         `json:"tcpSettings,omitempty"`
	WSSettings          *WSSettings          `json:"wsSettings,omitempty"`
	XHTTPSettings       *XHTTPSettings       `json:"xhttpSettings,omitempty"`
	HTTPUpgradeSettings *HTTPUpgradeSettings `json:"httpupgradeSettings,omitempty"`
	HysteriaSettings    *HysteriaSettings    `json:"hysteriaSettings,omitempty"`
	RealitySettings     *RealitySettings     `json:"realitySettings,omitempty"`
	GRPCSettings        *GRPCSettings        `json:"grpcSettings,omitempty"`
	// Sockopt is the operator's socket-option block, passed through verbatim. Raw
	// rather than typed: the field set is long, entirely server-local, and validated
	// against a key whitelist before it gets here (model.SockoptKeys), so a typed
	// mirror would be twenty fields of pure transcription with nothing to check.
	Sockopt json.RawMessage `json:"sockopt,omitempty"`
}

// XHTTPSettings configures the XHTTP transport (Xray's HTTP-shaped transport, the
// successor to SplitHTTP). Mode is "" (Xray's default), "packet-up", "stream-up" or
// "stream-one"; with REALITY the default resolves to stream-one — one HTTP request
// per connection, which is both the smallest surface and the only shape that would
// survive path-based dispatch.
//
// Host is the Host header the client sends; empty means the client uses the address
// it dialled, which is what we want when the inbound isn't behind a CDN.
//
// Extra is the operator's advanced block, passed through verbatim. Xray reads `extra`
// as a COMPLETE XHTTP config and then overwrites its host/path/mode from the outer
// three, which is what lets the same object be handed to the client in the share
// link's extra= parameter with no translation.
type XHTTPSettings struct {
	Path  string          `json:"path,omitempty"`
	Host  string          `json:"host,omitempty"`
	Mode  string          `json:"mode,omitempty"`
	Extra json.RawMessage `json:"extra,omitempty"`
}

// TCPSettings configures the raw-TCP transport. Header carries the optional HTTP
// masquerade — the framing that makes a raw proxy stream open like an ordinary HTTP
// exchange. Left nil the transport sends proxy bytes straight away.
type TCPSettings struct {
	Header *TCPHeader `json:"header,omitempty"`
}

// TCPHeader is the raw-TCP masquerade. Type is "none" or "http"; for "http" the
// request/response templates decide what the framing claims to be.
type TCPHeader struct {
	Type     string        `json:"type"`
	Request  *HTTPRequest  `json:"request,omitempty"`
	Response *HTTPResponse `json:"response,omitempty"`
}

// HTTPRequest is the client-to-server half of the raw-TCP HTTP masquerade.
type HTTPRequest struct {
	Version string              `json:"version,omitempty"`
	Method  string              `json:"method,omitempty"`
	Path    []string            `json:"path,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
}

// HTTPResponse is the server-to-client half.
type HTTPResponse struct {
	Version string              `json:"version,omitempty"`
	Status  string              `json:"status,omitempty"`
	Reason  string              `json:"reason,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
}

// HTTPUpgradeSettings configures the HTTPUpgrade transport — a WebSocket-shaped
// upgrade without the WebSocket framing, so it fronts the same CDNs but carries no
// per-frame masking.
type HTTPUpgradeSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
	// AcceptProxyProtocol reads the real client IP from a PROXY-protocol header
	// prepended by an upstream that forwards to this inbound.
	AcceptProxyProtocol bool `json:"acceptProxyProtocol,omitempty"`
}

// RealitySettings configures the REALITY security layer. Instead of presenting
// our own cert, the inbound forwards the TLS handshake of a real site (Dest /
// ServerNames) and authenticates clients via the X25519 key + shortId.
type RealitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest"`        // "host:port" of the borrowed site
	Xver        int      `json:"xver"`        // PROXY protocol version (0 = off)
	ServerNames []string `json:"serverNames"` // accepted SNIs (the borrowed site)
	PrivateKey  string   `json:"privateKey"`  // X25519 private (base64 raw-url)
	ShortIds    []string `json:"shortIds"`
	// MaxTimeDiff is the anti-replay window in ms: a client whose handshake clock
	// differs by more than this is rejected, so a probe can't replay a captured
	// REALITY auth later. 0 (omitted) disables the check.
	MaxTimeDiff int `json:"maxTimeDiff,omitempty"`
	// MinClientVer is the oldest client the handshake will serve. A client below it
	// is not told anything — it is handed the donor site, exactly like a probe, so
	// the app just fails to connect. Always sent: see MinClientVerAny.
	MinClientVer string `json:"minClientVer,omitempty"`
}

// MinClientVerAny keeps REALITY serving every client version, which is what this
// panel did before Xray 26.7.28 and what it keeps doing.
//
// Xray 26.7.28 began defaulting "minClientVer" to 26.3.27 when the field is absent.
// Inheriting that would have cut off every user whose app ships an older core —
// silently, on an Xray upgrade, with the connection looking dead rather than
// refused. So the floor is stated rather than inherited.
const MinClientVerAny = "0.0.0"

// GRPCSettings configures the gRPC transport. Authority overrides the :authority
// pseudo-header; MultiMode multiplexes several streams over one connection.
//
// Note: current Xray marks the gRPC transport itself deprecated in favour of XHTTP
// stream-up over H2 (it prints a warning on every start). It stays available because
// existing clients speak it, but new lanes are better off on XHTTP.
type GRPCSettings struct {
	ServiceName string `json:"serviceName"`
	Authority   string `json:"authority,omitempty"`
	MultiMode   bool   `json:"multiMode,omitempty"`
}

// WSSettings configures the WebSocket transport.
type WSSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
	// AcceptProxyProtocol reads the real client IP from a PROXY-protocol header
	// prepended by an upstream that forwards to this inbound.
	AcceptProxyProtocol bool `json:"acceptProxyProtocol,omitempty"`
}

// HysteriaSettings is the Hysteria2 transport block. Xray models Hysteria2 as a
// streamSettings transport (network "hysteria"); per-user auth lives in the
// inbound's settings.clients[].auth.
type HysteriaSettings struct {
	Version int `json:"version"` // must be 2
}

// TrojanInboundSettings is the "settings" object for a Trojan inbound.
type TrojanInboundSettings struct {
	Clients []TrojanClient `json:"clients"`
}

// TrojanClient is one Trojan user.
type TrojanClient struct {
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
}

// ShadowsocksInboundSettings is the "settings" object for a Shadowsocks-2022
// multi-user inbound. Method and Password (the server key) sit at the top level;
// each user carries its own key. Network "tcp,udp" is set explicitly so the inbound
// relays UDP (DNS, QUIC, Telegram calls) as well as TCP — Xray's default for
// Shadowsocks is TCP only, which would quietly drop every datagram.
//
// The per-user list is "users" (like Hysteria2, NOT "clients" like VLESS/Trojan).
// Xray's parser accepts "clients" as an alias too, but "users" is the canonical name
// its own docs use — depend on that, not on the alias surviving a future release.
type ShadowsocksInboundSettings struct {
	Method   string              `json:"method"`
	Password string              `json:"password"` // server key
	Network  string              `json:"network"`  // "tcp,udp"
	Users    []ShadowsocksClient `json:"users"`
}

// ShadowsocksClient is one Shadowsocks-2022 user: their own key plus the email tag
// that attributes traffic for per-user stats and access logging, exactly like the
// other protocols' client structs. For a 2022 method Xray reads this as the user key
// alone; the client is what joins it to the server key.
type ShadowsocksClient struct {
	Password string `json:"password"` // user key
	Email    string `json:"email,omitempty"`
}

// HysteriaInboundSettings is the "settings" object for a Hysteria2 inbound.
// Per Xray's schema the per-user list is "users" (NOT "clients" like Trojan/
// VLESS) — using the wrong key leaves Xray with no users, so traffic isn't
// attributed to an email (breaking per-user stats and IP/access logging).
type HysteriaInboundSettings struct {
	Version int              `json:"version"` // must be 2
	Users   []HysteriaClient `json:"users"`
}

// HysteriaClient is one Hysteria2 user.
// Xray's infra/conf parser maps the JSON "auth" field directly to the
// Account.Auth protobuf field — using "password" silently gives empty auth.
type HysteriaClient struct {
	Auth  string `json:"auth"`
	Email string `json:"email,omitempty"`
}

// TLSSettings configures the TLS layer for an inbound.
type TLSSettings struct {
	ServerName       string        `json:"serverName,omitempty"`
	RejectUnknownSni bool          `json:"rejectUnknownSni,omitempty"`
	ALPN             []string      `json:"alpn,omitempty"`
	MinVersion       string        `json:"minVersion,omitempty"`
	Certificates     []Certificate `json:"certificates,omitempty"`

	// Extra are additional tlsSettings keys the operator supplied. Merged at
	// marshal time (see MarshalJSON) rather than stored as a sibling block, because
	// Xray reads one flat tlsSettings object — there is nowhere for a second one to
	// go. Validated against model.TLSExtraKeys before it reaches here, so it cannot
	// contain the fields above that the panel owns.
	Extra json.RawMessage `json:"-"`
}

// MarshalJSON emits the derived fields with the operator's extra keys folded in.
//
// Written by hand because the alternative is making the whole block a raw blob, which
// would cost every other call site its compile-time field checking — the reason these
// structs are typed at all. The alias type breaks the recursion.
func (t TLSSettings) MarshalJSON() ([]byte, error) {
	type plain TLSSettings // no MarshalJSON on the alias ⇒ no recursion
	base, err := json.Marshal(plain(t))
	if err != nil {
		return nil, err
	}
	if len(t.Extra) == 0 {
		return base, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(t.Extra, &extra); err != nil {
		return nil, fmt.Errorf("tls extra: %w", err)
	}
	// The operator's keys lose to the derived ones on collision. They are whitelisted
	// not to overlap, so this only ever matters if that list and this struct drift —
	// and then the panel's own value is the safe one to keep.
	for k, v := range extra {
		if _, taken := merged[k]; !taken {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// Certificate points at on-disk PEM files (shared cert for all listeners).
type Certificate struct {
	CertificateFile string `json:"certificateFile,omitempty"`
	KeyFile         string `json:"keyFile,omitempty"`
}
