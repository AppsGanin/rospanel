package xray

import (
	"fmt"
	"net"
	"strings"

	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
)

// parseDNS splits the operator's DNS setting (servers separated by newlines or
// commas) into a trimmed, non-empty list.
func parseDNS(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' '
	}) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// VisionFlow is the VLESS flow used for raw-TCP Vision.
const VisionFlow = "xtls-rprx-vision"

// APIPort is the loopback port for the Xray gRPC StatsService.
const APIPort = 10085

// Options carries non-DB generation parameters.
type Options struct {
	// PanelDest is where the VLESS default fallback forwards non-proxy traffic
	// (the Go panel's loopback HTTP address, e.g. "127.0.0.1:8080").
	PanelDest string

	// Groups resolves the "iplist:<source>/<group>" routing entries to their
	// domains/CIDRs. Parsed from the on-disk iplist databases and cached by the
	// caller (they only change on a geo refresh). Nil or missing groups simply
	// drop those rules — see expandGroups.
	Groups geo.GroupSet
}

// Generate builds the full Xray config from settings + enabled users.
//
// Layout (all on one box):
//   - VLESS-Vision inbound owns :443 + TLS. fallbacks route by path → loopback
//     Trojan-WS inbound; default → the Go panel (decoy/panel/sub).
//   - Trojan inbound on 127.0.0.1:<trojan_port>, WS transport, no TLS (the 443
//     inbound already terminated it).
//   - Hysteria2 inbound on :<hysteria_port> (UDP), its own TLS; host nftables
//     redirects the hop range to it.
//   - One credential set per user (uuid for VLESS, password for Trojan/Hy2).
//
// proxies holds the live upstream proxies of each egress lane, keyed by lane ID.
func Generate(set *model.Settings, users []model.User, opts Options, proxies map[string][]model.ProxyEndpoint) (*Config, error) {
	if set.CertPath == "" || set.KeyPath == "" {
		return nil, fmt.Errorf("tls cert/key not configured")
	}
	if set.WSPath == "" {
		return nil, fmt.Errorf("ws path not configured")
	}
	if opts.PanelDest == "" {
		return nil, fmt.Errorf("panel fallback dest not configured")
	}

	// A disabled protocol keeps its inbound but gets an empty client list, so the
	// listener stays up while nobody can authenticate against it.
	vlessClients, trojanClients, hy2Clients, realityClients := protocolClients(set, users)

	sharedCert := []Certificate{{CertificateFile: set.CertPath, KeyFile: set.KeyPath}}

	// Sniffing on every user-facing inbound so domain routing rules can match the
	// real destination (HTTP host / TLS SNI / QUIC). "fakedns" is intentionally
	// omitted: it's a TUN/client mechanism and no fakedns server is configured, so
	// it was an inert (and confusing) destOverride on a server inbound.
	sniff := &Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}

	// rejectUnknownSni drops TLS probes whose SNI doesn't match the cert — but only
	// when the host is a domain. On a bare IP browsers send no SNI, so enabling it
	// there would reject the decoy/panel and lock the admin out over :443.
	rejectSNI := net.ParseIP(set.SNI) == nil && set.SNI != ""

	// TLS floor on :443. Default 1.2 keeps the decoy reachable by old TLS-1.2-only
	// clients (so the box still looks like an ordinary site); the operator can raise
	// it to 1.3. Vision already mandates 1.3, so VLESS clients are unaffected.
	minTLS := "1.2"
	if set.TLSMin13 {
		minTLS = "1.3"
	}

	vless := Inbound{
		Tag:      "vless-in",
		Listen:   "0.0.0.0",
		Port:     set.VLESSPort,
		Protocol: "vless",
		Settings: VLESSInboundSettings{
			Clients:    vlessClients,
			Decryption: "none",
			Fallbacks: []Fallback{
				// WebSocket path → loopback Trojan inbound. xver=1 forwards the
				// real client IP via PROXY protocol (Trojan inbound accepts it).
				{Path: set.WSPath, Dest: set.TrojanPort, Xver: 1},
				// Everything else → the Go panel (decoy / panel / subscription).
				// xver=1 prepends the PROXY-protocol header so the panel sees the
				// real client IP (its proxyproto listener parses it).
				{Dest: opts.PanelDest, Xver: 1},
			},
		},
		StreamSettings: &StreamSettings{
			Network:  "tcp",
			Security: "tls",
			TLSSettings: &TLSSettings{
				ServerName:       set.SNI,
				RejectUnknownSni: rejectSNI,
				ALPN:             []string{"h2", "http/1.1"},
				MinVersion:       minTLS,
				Certificates:     sharedCert,
			},
		},
		Sniffing: sniff,
	}

	trojan := Inbound{
		Tag:      "trojan-in",
		Listen:   "127.0.0.1",
		Port:     set.TrojanPort,
		Protocol: "trojan",
		Settings: TrojanInboundSettings{Clients: trojanClients},
		StreamSettings: &StreamSettings{
			Network:    "ws",
			WSSettings: &WSSettings{Path: set.WSPath, AcceptProxyProtocol: true},
			// The only upstream is the VLESS fallback over loopback, so trust the
			// real client IP it forwards only from 127.0.0.1 (silences Xray's
			// insecure-X-Forwarded-For warning).
			Sockopt: &Sockopt{TrustedXForwardedFor: []string{"127.0.0.1"}},
		},
		Sniffing: sniff,
	}

	hysteria := Inbound{
		Tag:      "hysteria-in",
		Listen:   "0.0.0.0",
		Port:     set.HysteriaPort,
		Protocol: "hysteria",
		Settings: HysteriaInboundSettings{Version: 2, Users: hy2Clients},
		StreamSettings: &StreamSettings{
			Network:  "hysteria",
			Security: "tls",
			// Hysteria2 runs over QUIC/HTTP3 — the TLS layer MUST offer ALPN "h3"
			// or the handshake fails with "no application protocol".
			TLSSettings:      &TLSSettings{ServerName: set.SNI, ALPN: []string{"h3"}, Certificates: sharedCert},
			HysteriaSettings: &HysteriaSettings{Version: 2},
		},
		Sniffing: sniff,
	}

	apiInbound := Inbound{
		Tag:      "api",
		Listen:   "127.0.0.1",
		Port:     APIPort,
		Protocol: "dokodemo-door",
		Settings: DokodemoSettings{Address: "127.0.0.1"},
	}

	inbounds := []Inbound{apiInbound, vless, trojan, hysteria}

	// VLESS + gRPC + REALITY on its own port. Only emitted when enabled AND keys
	// are present (REALITY can't authenticate without them). It borrows the TLS of
	// RealityDest instead of our cert.
	if set.RealityEnabled && set.RealityPrivateKey != "" {
		inbounds = append(inbounds, realityInbound(set, realityClients, sniff))
	}

	// Proxy mode: a socks/http forward-proxy inbound other RosPanel servers chain
	// through. Its traffic follows this server's routing (so it can itself egress
	// via WARP / the proxy pool), defaulting to direct.
	if set.ProxyModeEnabled {
		inbounds = append(inbounds, proxyModeInbound(set, sniff))
	}

	// Optional DNS block: upstream resolvers configured by the operator.
	var dns *DNS
	if servers := parseDNS(set.XrayDNS); len(servers) > 0 {
		dns = &DNS{Servers: servers}
	}

	rc := set.Routing
	outbounds := []Outbound{
		{Tag: "direct", Protocol: "freedom"},
		{Tag: "block", Protocol: "blackhole"},
	}

	// Cloudflare WARP egress (WireGuard). Only emitted when enabled AND a WARP
	// account has been provisioned; otherwise "warp" rules fall back to direct.
	warpTag := "direct"
	if set.WarpEnabled && set.WarpRegistered() {
		outbounds = append(outbounds, warpOutbound(set))
		warpTag = "warp"
	}

	// Opera VPN egress: an http outbound to the local helper. The lane is routed
	// through a single-member balancer with an Observatory health-probe (below),
	// so if the free VPN upstream goes unreachable the lane auto-falls-back to
	// "direct" and auto-recovers — instead of black-holing traffic. A lane is
	// "active" only when enabled AND referenced by a rule (else the balancer would
	// be unused).
	if set.OperaEnabled {
		outbounds = append(outbounds, operaOutbound(set))
	}
	order := normalizeOrder(rc.RoutingOrder, rc.LaneIDs())
	catchAll := order[len(order)-1]
	operaActive := set.OperaEnabled && (len(rc.OperaDomains) > 0 || len(rc.OperaIPs) > 0 || catchAll == "opera")

	// Egress lanes: one outbound per upstream proxy (tag "proxy-<lane>-<n>"), one
	// balancer per lane load-balancing across that lane's live proxies. A lane is
	// only active when it's enabled, HAS proxies, and something routes to it —
	// otherwise its balancer would be empty (Xray rejects that) or unused.
	active := make(map[string]bool, len(rc.Lanes))
	for _, lane := range rc.Lanes {
		pool := proxies[lane.ID]
		if !lane.Enabled || len(pool) == 0 {
			continue
		}
		if len(lane.Domains) == 0 && len(lane.IPs) == 0 && catchAll != lane.ID {
			continue
		}
		active[lane.ID] = true
		outbounds = append(outbounds, proxyOutbounds(lane.ID, pool)...)
	}

	// One Observatory probes every health-checked egress (every active lane + Opera)
	// so their balancers can drop to "direct" on a failed probe and recover.
	var subjects []string
	for _, lane := range rc.Lanes {
		if active[lane.ID] {
			subjects = append(subjects, laneTagPrefix(lane.ID))
		}
	}
	if operaActive {
		subjects = append(subjects, "opera")
	}
	var observatory *Observatory
	if len(subjects) > 0 {
		observatory = &Observatory{
			SubjectSelector:   subjects,
			ProbeURL:          "https://www.google.com/generate_204",
			ProbeInterval:     "1m",
			EnableConcurrency: true,
		}
	}

	return &Config{
		Log:   &Log{Loglevel: "warning"},
		Stats: &Stats{},
		API:   &API{Tag: "api", Services: []string{"StatsService", "HandlerService"}},
		Policy: &Policy{
			// statsUser* must stay on (per-user traffic accounting). connIdle reaps
			// idle connections; bufferSize=512KB bounds per-connection memory under
			// many flows. Both are conservative (no throughput loss / no dropping of
			// legitimately-idle tunnels); going lower trades throughput for RAM.
			Levels: map[string]LevelPolicy{"0": {
				StatsUserUplink: true, StatsUserDownlink: true,
				ConnIdle:   300,
				BufferSize: 512,
			}},
			System: &SystemPolicy{
				StatsInboundUplink: true, StatsInboundDownlink: true,
				StatsOutboundUplink: true, StatsOutboundDownlink: true,
			},
		},
		DNS:         dns,
		Inbounds:    inbounds,
		Outbounds:   outbounds,
		Routing:     compileRouting(expandGroups(rc, opts.Groups), order, warpTag, operaActive, active),
		Observatory: observatory,
	}, nil
}

// proxyModeInbound builds the forward-proxy inbound (proxy mode): socks or http,
// with optional username/password auth.
func proxyModeInbound(set *model.Settings, sniff *Sniffing) Inbound {
	hasAuth := set.ProxyModeUser != "" || set.ProxyModePass != ""
	accounts := []ProxyUser{{User: set.ProxyModeUser, Pass: set.ProxyModePass}}

	proto := "socks"
	var settings any = SocksInboundSettings{Auth: "noauth", UDP: true}
	if set.ProxyModeType == "http" {
		proto = "http"
		s := HTTPInboundSettings{}
		if hasAuth {
			s.Accounts = accounts
		}
		settings = s
	} else if hasAuth {
		settings = SocksInboundSettings{Auth: "password", Accounts: accounts, UDP: true}
	}
	return Inbound{
		Tag:      "proxy-mode-in",
		Listen:   "0.0.0.0",
		Port:     set.ProxyModePort,
		Protocol: proto,
		Settings: settings,
		Sniffing: sniff,
	}
}

// laneTagPrefix is the outbound-tag prefix of one egress lane's proxies, and the
// selector its balancer + the Observatory pick those members by. Lane IDs carry
// no dashes (model.ValidLaneID), so the trailing "-" terminates the prefix
// unambiguously: "proxy-ru-" can never match a member of another lane.
func laneTagPrefix(laneID string) string { return "proxy-" + laneID + "-" }

// laneBalancerTag is the routing target for a lane's traffic.
func laneBalancerTag(laneID string) string { return "pool-" + laneID }

// operaBalancerTag is a single-member balancer wrapping the Opera outbound: an
// Observatory health-probe lets it fall back to "direct" when the free VPN
// upstream is unreachable, and recover when it's back.
const operaBalancerTag = "opera-out"

// proxyOutbounds builds one socks/http outbound per proxy of a lane.
func proxyOutbounds(laneID string, proxies []model.ProxyEndpoint) []Outbound {
	out := make([]Outbound, 0, len(proxies))
	for i, p := range proxies {
		srv := ProxyServer{Address: p.Address, Port: p.Port}
		if p.User != "" || p.Pass != "" {
			srv.Users = []ProxyUser{{User: p.User, Pass: p.Pass}}
		}
		out = append(out, Outbound{
			Tag:      fmt.Sprintf("%s%d", laneTagPrefix(laneID), i),
			Protocol: p.Protocol,
			Settings: ProxyOutboundSettings{Servers: []ProxyServer{srv}},
		})
	}
	return out
}

// Inbound tags (also the keys the live add/remove-user API addresses).
const (
	TagVLESS    = "vless-in"
	TagTrojan   = "trojan-in"
	TagHysteria = "hysteria-in"
	TagReality  = "vless-reality-in"
)

// realityInbound builds the VLESS + gRPC + REALITY inbound from settings.
func realityInbound(set *model.Settings, clients []VLESSClient, sniff *Sniffing) Inbound {
	return Inbound{
		Tag:      TagReality,
		Listen:   "0.0.0.0",
		Port:     set.RealityPort,
		Protocol: "vless",
		Settings: VLESSInboundSettings{Clients: clients, Decryption: "none"},
		StreamSettings: &StreamSettings{
			Network:  "grpc",
			Security: "reality",
			RealitySettings: &RealitySettings{
				Show:        false,
				Dest:        set.RealitySNI() + ":443", // primary donor is dialed for probes
				ServerNames: set.RealityServerNames(),  // all accepted SNIs
				PrivateKey:  set.RealityPrivateKey,
				ShortIds:    strings.Split(set.RealityShortID, ","),
				MaxTimeDiff: set.RealityMaxTimeDiff, // anti-replay window (ms); 0 = off
			},
			GRPCSettings: &GRPCSettings{ServiceName: set.RealityServiceName},
		},
		Sniffing: sniff,
	}
}

// protocolClients builds the per-protocol client lists for the enabled protocols
// (a disabled protocol gets none, so nobody can authenticate against it). REALITY
// reuses the VLESS UUID but with no flow (Vision is raw-TCP only, not gRPC).
func protocolClients(set *model.Settings, users []model.User) ([]VLESSClient, []TrojanClient, []HysteriaClient, []VLESSClient) {
	vc := make([]VLESSClient, 0, len(users))
	tc := make([]TrojanClient, 0, len(users))
	hc := make([]HysteriaClient, 0, len(users))
	rc := make([]VLESSClient, 0, len(users))
	for _, u := range users {
		email := model.UserEmail(u.ID)
		if set.VLESSEnabled {
			vc = append(vc, VLESSClient{ID: u.UUID, Flow: VisionFlow, Email: email})
		}
		if set.TrojanEnabled {
			tc = append(tc, TrojanClient{Password: u.Password, Email: email})
		}
		if set.HysteriaEnabled {
			hc = append(hc, HysteriaClient{Auth: u.Password, Email: email})
		}
		if set.RealityEnabled {
			rc = append(rc, VLESSClient{ID: u.UUID, Email: email})
		}
	}
	return vc, tc, hc, rc
}

// UserInbounds builds inbound stubs (tag + protocol + clients) for the given
// users on the enabled protocols. Used by the live add-user API (`xray api adu`)
// so new users join the running Xray without a restart.
func UserInbounds(set *model.Settings, users []model.User) []Inbound {
	vc, tc, hc, rc := protocolClients(set, users)
	// `xray api adu` parses each entry as a full InboundDetour, so a valid Port is
	// required even though only the users are applied (matched by tag).
	var in []Inbound
	if len(vc) > 0 {
		in = append(in, Inbound{Tag: TagVLESS, Port: set.VLESSPort, Protocol: "vless", Settings: VLESSInboundSettings{Clients: vc, Decryption: "none"}})
	}
	if len(tc) > 0 {
		in = append(in, Inbound{Tag: TagTrojan, Listen: "127.0.0.1", Port: set.TrojanPort, Protocol: "trojan", Settings: TrojanInboundSettings{Clients: tc}})
	}
	if len(hc) > 0 {
		in = append(in, Inbound{Tag: TagHysteria, Port: set.HysteriaPort, Protocol: "hysteria", Settings: HysteriaInboundSettings{Version: 2, Users: hc}})
	}
	if len(rc) > 0 {
		in = append(in, Inbound{Tag: TagReality, Port: set.RealityPort, Protocol: "vless", Settings: VLESSInboundSettings{Clients: rc, Decryption: "none"}})
	}
	return in
}

// EnabledInboundTags lists the inbound tags that currently carry users (the
// targets for live user removal via `xray api rmu`).
func EnabledInboundTags(set *model.Settings) []string {
	var tags []string
	if set.VLESSEnabled {
		tags = append(tags, TagVLESS)
	}
	if set.TrojanEnabled {
		tags = append(tags, TagTrojan)
	}
	if set.HysteriaEnabled {
		tags = append(tags, TagHysteria)
	}
	if set.RealityEnabled {
		tags = append(tags, TagReality)
	}
	return tags
}

// warpOutbound builds the WireGuard outbound to Cloudflare WARP from settings.
func warpOutbound(set *model.Settings) Outbound {
	addrs := []string{set.WarpAddressV4 + "/32"}
	if set.WarpAddressV6 != "" {
		addrs = append(addrs, set.WarpAddressV6+"/128")
	}
	var reserved []int
	for _, p := range strings.Split(set.WarpReserved, ",") {
		if p = strings.TrimSpace(p); p != "" {
			var n int
			if _, err := fmt.Sscanf(p, "%d", &n); err == nil {
				reserved = append(reserved, n)
			}
		}
	}
	return Outbound{
		Tag:      "warp",
		Protocol: "wireguard",
		Settings: WireGuardSettings{
			SecretKey: set.WarpPrivateKey,
			Address:   addrs,
			Reserved:  reserved,
			MTU:       1280,
			Peers: []WireGuardPeer{{
				PublicKey:  set.WarpPublicKey,
				Endpoint:   set.WarpEndpoint,
				AllowedIPs: []string{"0.0.0.0/0", "::/0"},
			}},
		},
	}
}

// operaOutbound builds the http outbound to the local opera-proxy helper, which
// in turn forwards through Opera's VPN. The helper listens on loopback only.
func operaOutbound(set *model.Settings) Outbound {
	return Outbound{
		Tag:      "opera",
		Protocol: "http",
		Settings: ProxyOutboundSettings{
			Servers: []ProxyServer{{Address: "127.0.0.1", Port: set.OperaPortOr()}},
		},
	}
}

// healthBalancer is a single-/multi-member balancer whose Observatory probe lets
// it drop to "direct" when its members are unhealthy.
func healthBalancer(tag, selector string) Balancer {
	return Balancer{
		Tag:         tag,
		Selector:    []string{selector},
		Strategy:    &BalancerStrategy{Type: "leastPing"},
		FallbackTag: "direct",
	}
}

// compileRouting turns the structured routing config into Xray field rules
// (first-match-wins, evaluated in category precedence: block → direct → IPv4 →
// WARP). The api inbound is dispatched to the StatsService first; unmatched
// traffic falls through to the first real outbound (direct). warpTag is the
// outbound the WARP lane egresses through; the proxy/Opera lanes go through
// health-probed balancers (when active) that fall back to direct on failure.
// privateEgressCIDRs are destination ranges a tunnelled client is never allowed to
// reach: loopback, RFC1918 private space, link-local (covers the 169.254.169.254
// cloud-metadata endpoint), CGNAT, and their IPv6 equivalents. Explicit CIDRs (not
// geoip:private) so the rule needs no geo database to be present and can never
// silently no-op if geoip.dat is missing at boot.
var privateEgressCIDRs = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

func compileRouting(rc model.RoutingConfig, order []string, warpTag string, operaActive bool, active map[string]bool) *Routing {
	out := &Routing{DomainStrategy: "IPIfNonMatch"}
	// Each lane's proxies / Opera sit behind health-probed balancers; leastPing (via
	// the Observatory) routes to a live member, else falls back to direct.
	for _, lane := range rc.Lanes {
		if active[lane.ID] {
			out.Balancers = append(out.Balancers, healthBalancer(laneBalancerTag(lane.ID), laneTagPrefix(lane.ID)))
		}
	}
	if operaActive {
		out.Balancers = append(out.Balancers, healthBalancer(operaBalancerTag, "opera"))
	}
	// Dispatch the stats api inbound to the api handler before anything else.
	out.Rules = append(out.Rules, RouteRule{
		Type:        "field",
		InboundTag:  []string{"api"},
		OutboundTag: "api",
	})

	// Security floor: a VPN client must not be able to address the server's own
	// loopback (the Xray gRPC control API on 127.0.0.1:10085, the loopback panel),
	// the private LAN/neighbours, or the cloud metadata endpoint (169.254.169.254 →
	// IAM-credential theft) through the tunnel. The "direct" freedom outbound would
	// otherwise dial any of these. This rule sits right after the api dispatch (so
	// legitimate api traffic still reaches its handler) and ahead of every egress
	// lane, so no operator rule can accidentally re-expose these ranges. It only
	// blocks traffic a client explicitly addresses to these IPs — the VLESS→Trojan
	// and VLESS→panel loopback fallbacks happen inside Xray, not via this path, so
	// normal proxying to public sites is unaffected.
	addIPRule(out, "block", privateEgressCIDRs)

	// Block lane is always the highest priority.
	if rc.BlockBittorrent {
		out.Rules = append(out.Rules, RouteRule{
			Type: "field", OutboundTag: "block", Protocol: []string{"bittorrent"},
		})
	}
	if rc.BlockAds {
		addDomainRule(out, "block", []string{"geosite:category-ads-all"})
	}
	addDomainRule(out, "block", rc.BlockDomains)
	addIPRule(out, "block", rc.BlockIPs)

	// Egress lanes in the configured precedence (first-match-wins).
	byID := make(map[string]model.EgressLane, len(rc.Lanes))
	for _, l := range rc.Lanes {
		byID[l.ID] = l
	}
	emitLane := func(lane string) {
		switch lane {
		case "direct":
			addDomainRule(out, "direct", rc.DirectDomains)
			addIPRule(out, "direct", rc.DirectIPs)
		case "warp":
			addDomainRule(out, warpTag, rc.WarpDomains)
			addIPRule(out, warpTag, rc.WarpIPs)
		case "opera":
			if operaActive {
				addBalancerRule(out, operaBalancerTag, rc.OperaDomains, rc.OperaIPs)
			}
		default: // a proxy lane; an inactive one emits nothing and falls through
			if l, ok := byID[lane]; ok && active[lane] {
				addBalancerRule(out, laneBalancerTag(lane), l.Domains, l.IPs)
			}
		}
	}
	// Every lane but the last emits its specific rules; the last lane is the
	// catch-all for "everything else".
	for _, lane := range order[:len(order)-1] {
		emitLane(lane)
	}
	switch last := order[len(order)-1]; last {
	case "warp":
		if warpTag == "warp" {
			out.Rules = append(out.Rules, RouteRule{Type: "field", Network: "tcp,udp", OutboundTag: "warp"})
		}
	case "opera":
		if operaActive {
			out.Rules = append(out.Rules, RouteRule{Type: "field", Network: "tcp,udp", BalancerTag: operaBalancerTag})
		}
	case "direct":
		// The natural fallthrough to the first outbound (direct) — no rule needed.
	default:
		if active[last] {
			out.Rules = append(out.Rules, RouteRule{Type: "field", Network: "tcp,udp", BalancerTag: laneBalancerTag(last)})
		}
		// An inactive catch-all lane (disabled / no live proxies) also falls through
		// to direct, so its traffic keeps flowing instead of black-holing.
	}
	return out
}

// addBalancerRule appends domain + IP rules routing matched traffic to a
// balancer tag (the health-probed proxy/Opera/Hola pools).
func addBalancerRule(out *Routing, balancerTag string, domains, ips []string) {
	if d := normDomains(domains); len(d) > 0 {
		out.Rules = append(out.Rules, RouteRule{Type: "field", BalancerTag: balancerTag, Domain: d})
	}
	if i := trimList(ips); len(i) > 0 {
		out.Rules = append(out.Rules, RouteRule{Type: "field", BalancerTag: balancerTag, IP: i})
	}
}

// normalizeOrder returns a routing order containing every existing lane exactly
// once: the proxy lanes of the config (laneIDs) plus the built-in warp/opera/
// direct. It preserves the operator's saved precedence, drops entries for lanes
// that no longer exist, and inserts any lane the saved order is missing (a lane
// added since it was saved, or "opera" for a config from before that lane
// existed) just before the catch-all last lane, so the catch-all stays put.
//
// The result always has at least the built-in lanes, so callers may index its
// last element without a length check.
func normalizeOrder(order, laneIDs []string) []string {
	// Default precedence: proxy lanes first, then warp → opera → direct.
	known := append(append([]string(nil), laneIDs...), model.BuiltinLanes()...)
	valid := make(map[string]bool, len(known))
	for _, l := range known {
		valid[l] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range order {
		if valid[l] && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	var missing []string
	for _, l := range known {
		if !seen[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return out
	}
	if len(out) == 0 {
		return missing // empty (or fully stale) saved order → default precedence
	}
	// Insert missing lanes before the catch-all (last) lane.
	last := out[len(out)-1]
	res := make([]string, 0, len(out)+len(missing))
	res = append(res, out[:len(out)-1]...)
	res = append(res, missing...)
	res = append(res, last)
	return res
}

// trimList drops blank entries (after trimming) from a list.
func trimList(entries []string) []string {
	var out []string
	for _, e := range entries {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// addDomainRule appends a domain-matching field rule for the given outbound.
// Entries are normalized: a bare host becomes a "domain:" match; entries with a
// recognized prefix (domain:/keyword:/regexp:/geosite:/ext:/full:) pass through.
func addDomainRule(out *Routing, outbound string, entries []string) {
	domains := normDomains(entries)
	if len(domains) == 0 {
		return
	}
	out.Rules = append(out.Rules, RouteRule{Type: "field", OutboundTag: outbound, Domain: domains})
}

// addIPRule appends an IP-matching field rule. CIDRs and "geoip:xx" pass through.
func addIPRule(out *Routing, outbound string, entries []string) {
	ips := trimList(entries)
	if len(ips) == 0 {
		return
	}
	out.Rules = append(out.Rules, RouteRule{Type: "field", OutboundTag: outbound, IP: ips})
}

func normDomains(entries []string) []string {
	var out []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.ContainsRune(e, ':') {
			e = "domain:" + e
		}
		out = append(out, e)
	}
	return out
}
