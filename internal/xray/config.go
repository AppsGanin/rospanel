// Package xray models the Xray-core config as typed Go structs (so field-name
// mistakes are compile errors) and supervises the Xray child process.
//
// Engine note: Xray-core is the single proxy engine for all three protocols
// (VLESS-Vision-TCP-443 with fallbacks, Trojan-WS-443 via fallback, and
// Hysteria2-UDP-60000).
package xray

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
	Network          string            `json:"network,omitempty"`  // "tcp" | "ws" | "hysteria" | "grpc"
	Security         string            `json:"security,omitempty"` // "tls" | "reality" | "" (none)
	TLSSettings      *TLSSettings      `json:"tlsSettings,omitempty"`
	WSSettings       *WSSettings       `json:"wsSettings,omitempty"`
	HysteriaSettings *HysteriaSettings `json:"hysteriaSettings,omitempty"`
	RealitySettings  *RealitySettings  `json:"realitySettings,omitempty"`
	GRPCSettings     *GRPCSettings     `json:"grpcSettings,omitempty"`
	Sockopt          *Sockopt          `json:"sockopt,omitempty"`
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
}

// GRPCSettings configures the gRPC transport.
type GRPCSettings struct {
	ServiceName string `json:"serviceName"`
}

// Sockopt carries socket-level inbound options. trustedXForwardedFor whitelists
// the upstream peers whose X-Forwarded-For header Xray will trust.
type Sockopt struct {
	TrustedXForwardedFor []string `json:"trustedXForwardedFor,omitempty"`
}

// WSSettings configures the WebSocket transport (Trojan-WS).
type WSSettings struct {
	Path string `json:"path,omitempty"`
	// AcceptProxyProtocol lets the loopback Trojan inbound read the real client
	// IP from the PROXY-protocol header the VLESS fallback prepends (xver).
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
}

// Certificate points at on-disk PEM files (shared cert for all listeners).
type Certificate struct {
	CertificateFile string `json:"certificateFile,omitempty"`
	KeyFile         string `json:"keyFile,omitempty"`
}
