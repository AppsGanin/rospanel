// Package model holds the core domain types shared across the panel.
package model

import (
	"fmt"
	"strings"
	"time"
)

// TLSModeACME is the only TLS mode: ACME (Let's Encrypt or ZeroSSL) for a domain or IP.
const TLSModeACME = "acme"

// ACME CA providers.
const (
	ACMEProviderLE      = "letsencrypt" // default; no EAB required
	ACMEProviderZeroSSL = "zerossl"     // requires EAB credentials from zerossl.com
)

// Protocol display names. These appear in lockstep across the share-link "#label",
// the sing-box/Clash node tag/name, the subscription page, and the Connections UI;
// keeping them here as the single source stops those copies from drifting apart.
const (
	ProtoVLESS    = "VLESS-TCP-TLS"
	ProtoReality  = "VLESS-GRPC-REALITY"
	ProtoTrojan   = "TROJAN-WS"
	ProtoHysteria = "HYSTERIA-UDP"
)

// User is a VPN user. In v1 one user = one credential set applied across all
// enabled protocols (M1 only wires VLESS).
type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	UUID      string    `json:"uuid"`       // VLESS
	Password  string    `json:"-"`          // Trojan + Hysteria2 (shared); embedded in links only
	SubToken  string    `json:"-"`          // subscription capability token
	Status    string    `json:"status"`     // active | disabled | expired | limited
	Enabled   bool      `json:"enabled"`    // manual on/off toggle (independent of Status)
	DataLimit int64     `json:"data_limit"` // bytes, 0 = unlimited
	ExpireAt  int64     `json:"expire_at"`  // unix seconds, 0 = never
	UsedUp    int64     `json:"used_up"`
	UsedDown  int64     `json:"used_down"`
	LastUp    int64     `json:"-"` // last raw Xray uplink counter
	LastDown  int64     `json:"-"` // last raw Xray downlink counter
	CreatedAt time.Time `json:"created_at"`

	ResetPeriod string `json:"reset_period"` // none | daily | weekly | monthly | yearly
	LastResetAt int64  `json:"-"`            // unix of the last automatic quota reset
	LastSeen    int64  `json:"last_seen"`    // unix of last activity (0 = never); 0 ⇒ offline
}

// UserEmail returns the identifier a user is keyed by inside Xray — "u<id>" —
// which appears in access logs, per-user stats, and every protocol's client
// entry. This is the single source of that format.
func UserEmail(id int64) string { return fmt.Sprintf("u%d", id) }

// Connection is a per-source-IP record of a user's connections.
type Connection struct {
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
	Count    int64  `json:"count"`
}

// DailyPoint is one day's traffic total (for charts).
type DailyPoint struct {
	Day  string `json:"day"` // YYYY-MM-DD (UTC)
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// UserTotal is a user's traffic total over a period (for the per-user table).
type UserTotal struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

// Settings is the singleton (id=1) panel configuration. The DB is the source of
// truth; the Xray config.json is always derived from it.
type Settings struct {
	ID              int64     `json:"-"`
	Host            string    `json:"host"`     // public domain or IP used in share links
	SNI             string    `json:"sni"`      // TLS server name (link + cert SAN)
	TLSMode         string    `json:"tls_mode"` // always "acme"
	ACMEEmail       string    `json:"acme_email"`
	ACMEProvider    string    `json:"acme_provider"` // "letsencrypt" | "zerossl"
	ZeroSSLEABKID   string    `json:"-"`             // ZeroSSL External Account Binding KID
	ZeroSSLEABHMAC  string    `json:"-"`             // ZeroSSL EAB HMAC key (base64url)
	CertPath        string    `json:"cert_path"`
	KeyPath         string    `json:"key_path"`
	VLESSPort       int       `json:"vless_port"`
	ConfigRevision  int64     `json:"config_revision"`
	LastConfigError string    `json:"last_config_error"`
	UpdatedAt       time.Time `json:"updated_at"`
	PanelSecretPath string    `json:"-"` // never serialized to clients
	DecoyTemplate   string    `json:"decoy_template"`
	WSPath          string    `json:"-"` // Trojan-WS path matched by VLESS fallbacks
	TrojanPort      int       `json:"-"` // loopback Trojan inbound port
	HysteriaPort    int       `json:"hysteria_port"`
	HopStart        int       `json:"hop_start"`
	HopEnd          int       `json:"hop_end"`
	// HopInterval is the port-hopping rotation interval in seconds ("min-max"),
	// embedded in the Hysteria2 share link's quicParams.
	HopInterval string `json:"-"`

	// Per-protocol toggles for the Connections panel. A disabled protocol drops
	// out of user subscriptions/share links and its clients are removed from the
	// Xray inbound (the listener stays up but rejects everyone).
	VLESSEnabled    bool `json:"-"`
	TrojanEnabled   bool `json:"-"`
	HysteriaEnabled bool `json:"-"`

	// VLESS + gRPC + REALITY inbound (separate port). REALITY borrows the TLS of a
	// real site (RealityDest) instead of our cert. Keys/shortId/serviceName are
	// generated by the panel; the public key + shortId go into share links.
	RealityEnabled     bool   `json:"-"`
	RealityPort        int    `json:"-"`
	RealityDest        string `json:"-"` // target site / SNI(s), e.g. "max.ru"
	RealityPrivateKey  string `json:"-"` // X25519 private (base64 raw-url)
	RealityPublicKey   string `json:"-"` // X25519 public (base64 raw-url), in links (pbk)
	RealityShortID     string `json:"-"` // hex shortId, in links (sid)
	RealityServiceName string `json:"-"` // gRPC service name

	// Proxy mode: a socks/http forward-proxy inbound so other RosPanel servers can
	// chain their egress through this one (point their proxy pool at host:port).
	ProxyModeEnabled bool   `json:"-"`
	ProxyModeType    string `json:"-"` // "socks" | "http"
	ProxyModePort    int    `json:"-"`
	ProxyModeUser    string `json:"-"`
	ProxyModePass    string `json:"-"`

	// First-run wizard state. SetupDone gates the wizard; Timezone is the IANA
	// zone anchoring the local-day boundary for stats (empty ⇒ server local).
	SetupDone bool   `json:"-"`
	Timezone  string `json:"-"`

	// Subscription delivery settings (Settings → Подписки).
	SubPath           string `json:"-"` // public subscription URL prefix /<sub_path>/<token>
	SubBase64         bool   `json:"-"` // base64-encode the universal link list
	SubEmailInName    bool   `json:"-"` // append the user name to protocol tags
	SubTitle          string `json:"-"` // profile title (empty ⇒ host)
	SubRouting        bool   `json:"-"` // attach auto-routing headers
	SubRoutingHapp    string `json:"-"` // Happ routing config URL
	SubRoutingIncy    string `json:"-"` // INCY routing config URL
	SubRoutingMihomo  string `json:"-"` // Mihomo (Clash Meta) routing config URL
	SubUpdateInterval int    `json:"-"` // subscription auto-update interval (hours)

	XrayDNS string `json:"-"` // upstream DNS servers for Xray (newline/comma separated)

	// Per-connection uTLS fingerprint embedded in share links (fp=). Hysteria2
	// (QUIC) has none.
	VLESSFp   string `json:"-"`
	TrojanFp  string `json:"-"`
	RealityFp string `json:"-"`

	// Anti-DPI / anti-censorship transport hardening (Settings → Подключения).
	// TLSFragment / BlockQUIC shape the GENERATED client configs (sing-box only —
	// no server change); TLSMin13 / RealityMaxTimeDiff / RealityDestPort change the
	// SERVER inbound config.
	TLSFragment        bool `json:"-"` // sing-box ClientHello fragmentation (Vision + Trojan-WS)
	TLSMin13           bool `json:"-"` // require TLS 1.3 on the :443 inbound
	BlockQUIC          bool `json:"-"` // drop untunneled browser QUIC (UDP/443) in client configs
	RealityMaxTimeDiff int  `json:"-"` // REALITY anti-replay window in ms (0 = off)

	// Opera VPN egress: the opera-proxy helper binary exposes a local HTTP proxy
	// (127.0.0.1:OperaPort) we add as the "opera" routing lane. Country is the
	// Opera VPN region (EU|AS|AM).
	OperaEnabled bool   `json:"-"`
	OperaCountry string `json:"-"`
	OperaPort    int    `json:"-"`

	// Cloudflare WARP outbound (WireGuard). When enabled and registered, routing
	// rules with action "warp" egress through it.
	WarpEnabled    bool   `json:"-"`
	WarpPrivateKey string `json:"-"` // our WG secret key (base64)
	WarpPublicKey  string `json:"-"` // Cloudflare's WG public key (base64)
	WarpEndpoint   string `json:"-"` // host:port of the WARP peer
	WarpAddressV4  string `json:"-"` // assigned interface IPv4
	WarpAddressV6  string `json:"-"` // assigned interface IPv6
	WarpReserved   string `json:"-"` // client id as "a,b,c"

	Routing RoutingConfig `json:"-"` // structured routing config (Settings → Роутинг)

	// Computed per request (NOT stored). When the active cert isn't CA-trusted (a
	// self-signed fallback), TLSInsecure is set and TLSPinSHA256 carries the hex
	// SHA-256 of that cert so Xray links can pin it (pinnedPeerCertSha256) — clients
	// then trust this exact cert. sing-box/clash use TLSInsecure (skip verify).
	TLSInsecure  bool   `json:"-"`
	TLSPinSHA256 string `json:"-"`
}

// WarpRegistered reports whether a WARP account has been provisioned.
func (s *Settings) WarpRegistered() bool { return s.WarpPrivateKey != "" }

// OperaCountries are the Opera VPN regions opera-proxy accepts.
var OperaCountries = []string{"EU", "AS", "AM"}

// OperaCountryOr returns the configured Opera VPN region, defaulting to "EU"
// for an empty or unknown value.
func (s *Settings) OperaCountryOr() string {
	for _, c := range OperaCountries {
		if s.OperaCountry == c {
			return c
		}
	}
	return "EU"
}

// OperaPortOr returns the local Opera proxy port, defaulting to 18080.
func (s *Settings) OperaPortOr() int {
	if s.OperaPort > 0 {
		return s.OperaPort
	}
	return 18080
}

// SubPathOr returns the subscription URL prefix, defaulting to "sub".
func (s *Settings) SubPathOr() string {
	if p := strings.TrimSpace(s.SubPath); p != "" {
		return p
	}
	return "sub"
}

// RealitySID returns the primary (first) REALITY shortId — the one embedded in
// share links and client configs (RealityShortID stores a comma-separated set
// the server accepts).
func (s *Settings) RealitySID() string {
	if i := strings.IndexByte(s.RealityShortID, ','); i >= 0 {
		return s.RealityShortID[:i]
	}
	return s.RealityShortID
}

// RealityServerNames returns the donor SNIs the REALITY inbound accepts
// (RealityDest stores a comma-separated set; the first is the primary).
func (s *Settings) RealityServerNames() []string {
	var out []string
	for _, d := range strings.Split(s.RealityDest, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// RealitySNI returns the primary (first) donor domain — used as the dialed dest
// and the sni= in share links.
func (s *Settings) RealitySNI() string {
	if ns := s.RealityServerNames(); len(ns) > 0 {
		return ns[0]
	}
	return strings.TrimSpace(s.RealityDest)
}

// fpOr returns fp, defaulting to "firefox" when empty.
func fpOr(fp string) string {
	if fp != "" {
		return fp
	}
	return "firefox"
}

// VLESSFP / TrojanFP / RealityFP return the per-connection uTLS fingerprint for
// share links, each defaulting to "firefox".
func (s *Settings) VLESSFP() string   { return fpOr(s.VLESSFp) }
func (s *Settings) TrojanFP() string  { return fpOr(s.TrojanFp) }
func (s *Settings) RealityFP() string { return fpOr(s.RealityFp) }

// Fingerprints are the uTLS ClientHello fingerprints offered in the UI.
var Fingerprints = []string{
	"firefox", "chrome", "safari", "edge", "ios", "android", "random", "randomized",
}

// ValidFingerprint reports whether fp is an offered uTLS fingerprint.
func ValidFingerprint(fp string) bool {
	for _, f := range Fingerprints {
		if f == fp {
			return true
		}
	}
	return false
}

// RoutingConfig is the structured routing configuration (Settings → Роутинг).
// Each field is a category of destinations handled the same way; domain entries
// are raw Xray matchers (plain host, "domain:", "keyword:", "regexp:",
// "geosite:", "ext:file:tag") and IP entries are CIDRs or "geoip:xx".
type RoutingConfig struct {
	BlockBittorrent bool     `json:"block_bittorrent"`
	BlockAds        bool     `json:"block_ads"` // block geosite:category-ads-all
	BlockIPs        []string `json:"block_ips"` // CIDRs or geoip:xx
	BlockDomains    []string `json:"block_domains"`
	WarpDomains     []string `json:"warp_domains"`  // routed through Cloudflare WARP
	WarpIPs         []string `json:"warp_ips"`      // CIDRs or geoip:xx, via WARP
	OperaDomains    []string `json:"opera_domains"` // routed through Opera VPN
	OperaIPs        []string `json:"opera_ips"`     // CIDRs or geoip:xx, via Opera VPN
	DirectDomains   []string `json:"direct_domains"`
	DirectIPs       []string `json:"direct_ips"`

	// RoutingOrder is the precedence of the egress lanes (a permutation of
	// "direct"/"proxy"/"warp"); first-match-wins. The LAST lane is the catch-all
	// ("everything else") — its specific rules are subsumed by a final rule.
	RoutingOrder []string `json:"routing_order"`

	// Outbound proxy pool: traffic matching ProxyDomains/ProxyIPs is load-balanced
	// across the proxies fetched from ProxyURLs (each a list, one proxy per line)
	// plus the ProxyManual entries.
	ProxyURLs    []string `json:"proxy_urls"`
	ProxyManual  []string `json:"proxy_manual"`
	ProxyDomains []string `json:"proxy_domains"`
	ProxyIPs     []string `json:"proxy_ips"`

	// ProxyRefreshMinutes is how often the URL-sourced proxy list is re-fetched.
	// 0 means the default (30 min) — kept so configs saved before this was
	// selectable keep auto-refreshing; a negative value means "never".
	ProxyRefreshMinutes int `json:"proxy_refresh_minutes"`
}

// ProxyEndpoint is one outbound proxy in the pool (parsed from a "scheme://
// [user:pass@]host:port" line). Protocol is normalized to "socks" or "http".
type ProxyEndpoint struct {
	Protocol string
	Address  string
	Port     int
	User     string
	Pass     string
}
