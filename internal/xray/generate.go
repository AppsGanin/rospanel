package xray

import (
	"fmt"
	"hash/fnv"
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

	// Custom are this server's operator-defined inbounds (already filtered to the
	// enabled ones). Each gets its own listener beside the built-in lanes; none of
	// them share :443, so nothing here can disturb the Vision inbound or the decoy.
	Custom []model.Inbound

	// ServerID is the server this config is for (LocalNodeID for the master), used to
	// resolve per-user access to the built-in lanes. Access maps a user id to what
	// they may connect to; a user absent from it is unrestricted (see model.AccessOf).
	// A nil Access means the feature is off and every user reaches every lane — the
	// historical behaviour.
	ServerID int64
	Access   map[int64]model.Access
}

// allowsBuiltin reports whether a user may use a built-in lane on this server.
func (o Options) allowsBuiltin(userID int64, lane string) bool {
	return model.AccessOf(o.Access, userID).AllowsBuiltin(o.ServerID, lane)
}

// allowsInbound reports whether a user may use a custom inbound.
func (o Options) allowsInbound(userID, inboundID int64) bool {
	return model.AccessOf(o.Access, userID).AllowsInbound(inboundID)
}

// Generate builds the full Xray config from settings + enabled users.
//
// Layout (all on one box):
//   - VLESS-Vision inbound owns :443 + TLS. Its only fallback is the default one →
//     the Go panel (decoy/panel/sub), so :443 has no path that behaves differently
//     from an ordinary website under probing.
//   - VLESS-XHTTP-REALITY on its own port, borrowing the donor's TLS.
//   - Hysteria2 inbound on :<hysteria_port> (UDP), its own TLS; host nftables
//     redirects the hop range to it.
//   - Every operator-defined custom inbound, each on its own port (opts.Custom).
//   - One credential set per user (uuid for VLESS, password for Trojan/Hy2).
//
// proxies holds the live upstream proxies of each egress lane, keyed by lane ID.
func Generate(set *model.Settings, users []model.User, opts Options, proxies map[string][]model.ProxyEndpoint) (*Config, error) {
	if set.CertPath == "" || set.KeyPath == "" {
		return nil, fmt.Errorf("tls cert/key not configured")
	}
	if opts.PanelDest == "" {
		return nil, fmt.Errorf("panel fallback dest not configured")
	}

	// A disabled protocol keeps its inbound but gets an empty client list, so the
	// listener stays up while nobody can authenticate against it. The per-user access
	// filter drops a user's credential from a lane they aren't allowed — so a hidden
	// lane can't be reached with a hand-made link, not just hidden in the UI.
	vlessClients, hy2Clients, realityClients := protocolClients(set, users, opts.allowsBuiltin)

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
				// The single default fallback → the Go panel (decoy / panel /
				// subscription). xver=1 prepends the PROXY-protocol header so the
				// panel sees the real client IP (its proxyproto listener parses it).
				//
				// There is deliberately no path-keyed fallback any more. One used to
				// dispatch a secret path to a loopback Trojan-WS inbound, which made
				// that path an oracle: everything else on :443 answered like a website
				// and it did not. Now every request on this port reaches the decoy.
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

	inbounds := []Inbound{apiInbound, vless, hysteria}

	// VLESS + XHTTP + REALITY on its own port. Only emitted when enabled AND keys
	// are present (REALITY can't authenticate without them). It borrows the TLS of
	// RealityDest instead of our cert.
	if set.RealityEnabled && set.RealityPrivateKey != "" {
		inbounds = append(inbounds, realityInbound(set, realityClients, sniff))
	}

	// Operator-defined inbounds, each on its own port. They are generated last so a
	// malformed one can only ever be an extra listener that fails to bind — it can't
	// displace :443 or the REALITY lane.
	for _, in := range opts.Custom {
		inbounds = append(inbounds, customInbound(in, set, users, sniff, sharedCert, minTLS, opts))
	}

	// System proxies: this server's SOCKS/HTTP forward listeners, for traffic that
	// isn't a VPN client. Their traffic follows this server's routing (so it can
	// itself egress via WARP / Opera / the proxy pool), defaulting to direct.
	inbounds = append(inbounds, systemProxyInbounds(set, sniff)...)

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

	// The entrance to WARP for anything running ON this box — the panel itself, or
	// whatever the operator points at it. WARP is a WireGuard outbound with no address
	// of its own, so without this loopback SOCKS inbound there is simply no way for an
	// ordinary HTTP client to reach it.
	//
	// Tied to WARP being available, nothing else. The Routing page publishes this
	// address (model.Settings.WarpProxyURL) whenever it is up, so the inbound has to
	// exist for as long as that address is advertised — not only while some particular
	// consumer happens to be configured to use it.
	panelEgressWarp := warpTag == "warp"
	if panelEgressWarp {
		inbounds = append(inbounds, panelEgressInbound())
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
		probeURL, probeInterval := probeProfile(set.Host)
		observatory = &Observatory{
			SubjectSelector:   subjects,
			ProbeURL:          probeURL,
			ProbeInterval:     probeInterval,
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
		Routing:     compileRouting(expandGroups(rc, opts.Groups), order, warpTag, operaActive, panelEgressWarp, active),
		Observatory: observatory,
	}, nil
}

// probeTargets are the endpoints the Observatory health-probes an egress through.
// All answer 204 to anyone, and all are hit constantly by ordinary phones and
// laptops doing captive-portal detection — so the request itself is unremarkable
// wherever it comes from. One fixed choice across every install would not be: it
// turns "who talks to this exact URL on a schedule" into a fleet-wide query.
var probeTargets = []string{
	"https://www.gstatic.com/generate_204",
	"https://connectivitycheck.gstatic.com/generate_204",
	"https://www.google.com/generate_204",
	"https://cp.cloudflare.com/generate_204",
	"https://edge.microsoft.com/captiveportal/generate_204",
}

// probeProfile picks this server's probe endpoint and interval (45–89s), derived
// from its own host so two servers rarely share either.
//
// Derived rather than random because the result goes into the generated config,
// and the config's hash is what tells a node its configuration changed: a value
// redrawn on every generate would restart Xray on every node, every time. Keyed on
// the host so each node in a fleet still lands somewhere different.
func probeProfile(host string) (url, interval string) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(host))
	n := h.Sum64()
	return probeTargets[n%uint64(len(probeTargets))],
		fmt.Sprintf("%ds", 45+(n/uint64(len(probeTargets)))%45)
}

// systemProxyInbounds builds this server's forward-proxy listeners — a SOCKS5 and/or
// an HTTP inbound for traffic that is not a VPN client (a scraper, a bot, another
// RosPanel chaining its egress here).
//
// Both carry the server's single account and nothing else: these listeners are public
// and an unauthenticated one becomes somebody's spam relay within hours, with this
// server's IP on it. A configuration without credentials is refused before it reaches
// here (model.SystemProxy.Validate), so an account is always present.
//
// They sniff like the user-facing inbounds, so this traffic matches the same
// domain-based routing rules — which is what makes "it leaves where the VPN leaves"
// true, WARP and Opera lanes included.
func systemProxyInbounds(set *model.Settings, sniff *Sniffing) []Inbound {
	p := model.SystemProxy{
		SocksEnabled: set.ProxySocksEnabled, SocksPort: set.ProxySocksPort,
		HTTPEnabled: set.ProxyHTTPEnabled, HTTPPort: set.ProxyHTTPPort,
		Accounts: set.ProxyAccounts,
	}
	accounts := make([]ProxyUser, 0, len(p.Accounts))
	for _, a := range p.Accounts {
		if a.User == "" || a.Pass == "" {
			continue // a half-filled account would authenticate nobody
		}
		accounts = append(accounts, ProxyUser{User: a.User, Pass: a.Pass})
	}
	if len(accounts) == 0 {
		return nil // never open an anonymous proxy, whatever the flags say
	}
	var out []Inbound
	if p.SocksEnabled && p.SocksPort > 0 {
		out = append(out, Inbound{
			Tag:      systemSocksTag,
			Listen:   "0.0.0.0",
			Port:     p.SocksPort,
			Protocol: "socks",
			// UDP on: SOCKS5 clients that need it (DNS, QUIC, games) would otherwise
			// fail in ways that look like the destination is down.
			Settings: SocksInboundSettings{Auth: "password", Accounts: accounts, UDP: true},
			Sniffing: sniff,
		})
	}
	if p.HTTPEnabled && p.HTTPPort > 0 {
		out = append(out, Inbound{
			Tag:      systemHTTPTag,
			Listen:   "0.0.0.0",
			Port:     p.HTTPPort,
			Protocol: "http",
			Settings: HTTPInboundSettings{Accounts: accounts},
			Sniffing: sniff,
		})
	}
	return out
}

// The tags the system proxies' traffic carries into the routing rules.
const (
	systemSocksTag = "system-socks-in"
	systemHTTPTag  = "system-http-in"
)

// panelEgressTag identifies local traffic entering the WARP tunnel in the routing rules.
const panelEgressTag = "panel-egress-in"

// panelEgressInbound builds the loopback SOCKS inbound that serves as WARP's address:
// the tunnel is a WireGuard outbound, which nothing on the box can dial directly.
//
// Bound to 127.0.0.1, so unlike proxy mode it is not reachable from the network and
// needs no credentials — anything that can connect to it is already running on the
// box. No sniffing either: it is dispatched by inbound tag, not by domain, and the
// SOCKS request carries the hostname anyway.
func panelEgressInbound() Inbound {
	return Inbound{
		Tag:      panelEgressTag,
		Listen:   "127.0.0.1",
		Port:     model.PanelEgressPort,
		Protocol: "socks",
		// UDP off: the panel speaks HTTPS to Telegram over TCP and nothing else.
		Settings: SocksInboundSettings{Auth: "noauth", UDP: false},
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

// Inbound tags (also the keys the live add/remove-user API addresses). A custom
// inbound's tag is derived from its row id — see model.Inbound.Tag.
const (
	TagVLESS    = "vless-in"
	TagHysteria = "hysteria-in"
	TagReality  = "vless-reality-in"
)

// realityInbound builds the built-in VLESS + XHTTP + REALITY inbound from settings.
//
// XHTTP rather than gRPC: gRPC+REALITY is the most fingerprinted of the current
// combinations, and with a REALITY config present XHTTP's default mode resolves to
// stream-one — one HTTP request per connection, the smallest surface it offers.
func realityInbound(set *model.Settings, clients []VLESSClient, sniff *Sniffing) Inbound {
	return Inbound{
		Tag:      TagReality,
		Listen:   "0.0.0.0",
		Port:     set.RealityPort,
		Protocol: "vless",
		Settings: VLESSInboundSettings{Clients: clients, Decryption: "none"},
		StreamSettings: &StreamSettings{
			Network:  "xhttp",
			Security: "reality",
			RealitySettings: &RealitySettings{
				Show:         false,
				Dest:         set.RealitySNI() + ":443", // primary donor is dialed for probes
				ServerNames:  set.RealityServerNames(),  // all accepted SNIs
				PrivateKey:   set.RealityPrivateKey,
				ShortIds:     strings.Split(set.RealityShortID, ","),
				MaxTimeDiff:  set.RealityMaxTimeDiff, // anti-replay window (ms); 0 = off
				MinClientVer: MinClientVerAny,
			},
			XHTTPSettings: &XHTTPSettings{Path: set.RealityPathOr()},
		},
		Sniffing: sniff,
	}
}

// customInbound builds one operator-defined inbound. Which of the stream fields are
// populated is decided entirely by (protocol, transport, security), the same triple
// model.Inbound.Validate has already accepted — so anything reaching here is a
// combination the panel is willing to emit a client config for.
func customInbound(in model.Inbound, set *model.Settings, users []model.User,
	sniff *Sniffing, cert []Certificate, minTLS string, opts Options) Inbound {

	// Only the users allowed this inbound get a credential in it — the access gate.
	allowed := allowedUsers(users, func(u model.User) bool { return opts.allowsInbound(u.ID, in.ID) })

	out := Inbound{
		Tag:      in.Tag(),
		Listen:   "0.0.0.0",
		Port:     in.Port,
		Protocol: in.Protocol,
		Sniffing: sniff,
	}
	switch in.Protocol {
	case model.InbVLESS:
		out.Settings = VLESSInboundSettings{
			Clients:    customVLESSClients(in, allowed),
			Decryption: "none",
		}
	case model.InbTrojan:
		out.Settings = TrojanInboundSettings{Clients: customTrojanClients(allowed)}
	case model.InbHysteria:
		out.Protocol = "hysteria"
		out.Settings = HysteriaInboundSettings{Version: 2, Users: customHysteriaClients(allowed)}
	case model.InbShadowsocks:
		out.Settings = ShadowsocksInboundSettings{
			Method:   in.Opts.Method,
			Password: in.Opts.ShadowKey,
			Network:  "tcp,udp",
			Users:    customShadowsocksClients(in, allowed),
		}
	}
	out.StreamSettings = customStream(in, set, cert, minTLS)
	return out
}

// allowedUsers returns the subset of users the predicate admits.
func allowedUsers(users []model.User, allow func(model.User) bool) []model.User {
	out := make([]model.User, 0, len(users))
	for _, u := range users {
		if allow(u) {
			out = append(out, u)
		}
	}
	return out
}

// customStream builds the streamSettings for one custom inbound.
func customStream(in model.Inbound, set *model.Settings, cert []Certificate, minTLS string) *StreamSettings {
	// Shadowsocks-2022 is raw TCP with its own AEAD and no TLS/REALITY layer, so it
	// carries no streamSettings at all — the tcp+udp choice lives in its settings
	// (see ShadowsocksInboundSettings.Network). A streamSettings with network
	// "shadowsocks" would be a transport Xray doesn't know.
	if in.Protocol == model.InbShadowsocks {
		return nil
	}
	o := in.Opts
	st := &StreamSettings{Network: o.Transport}

	switch o.Transport {
	case model.TrTCP:
		if header := tcpMasquerade(o); header != nil {
			st.TCPSettings = &TCPSettings{Header: header}
		}
	case model.TrWS:
		st.WSSettings = &WSSettings{Path: o.Path, Host: o.Host}
	case model.TrHTTPUpgrade:
		st.HTTPUpgradeSettings = &HTTPUpgradeSettings{Path: o.Path, Host: o.Host}
	case model.TrXHTTP:
		st.XHTTPSettings = &XHTTPSettings{
			Path: o.Path, Host: o.Host, Mode: o.Mode, Extra: o.XHTTPExtra,
		}
	case model.TrGRPC:
		st.GRPCSettings = &GRPCSettings{
			ServiceName: o.ServiceName, Authority: o.Authority, MultiMode: o.MultiMode,
		}
	case model.TrHysteria:
		st.HysteriaSettings = &HysteriaSettings{Version: 2}
	}
	st.Sockopt = o.Sockopt

	switch o.Security {
	case model.SecTLS:
		st.Security = "tls"
		sni := o.SNI
		if sni == "" {
			sni = set.SNI
		}
		st.TLSSettings = &TLSSettings{
			ServerName:   sni,
			ALPN:         customALPN(o.Transport),
			MinVersion:   minTLS,
			Certificates: cert,
			Extra:        o.TLSExtra,
		}
		if in.Protocol == model.InbHysteria {
			// Hysteria2 is QUIC/HTTP3: the TLS layer MUST offer exactly h3, and a
			// minVersion floor is meaningless (QUIC is TLS 1.3 by construction).
			st.TLSSettings.ALPN = []string{"h3"}
			st.TLSSettings.MinVersion = ""
		}
	case model.SecReality:
		st.Security = "reality"
		st.RealitySettings = &RealitySettings{
			Show:         false,
			Dest:         o.RealitySNI() + ":443",
			ServerNames:  o.RealityServerNames(),
			PrivateKey:   o.RealityPrivateKey,
			ShortIds:     o.RealityShortIDs(),
			MaxTimeDiff:  o.RealityMaxTimeDiff,
			MinClientVer: MinClientVerAny,
		}
	}
	return st
}

// tcpMasquerade builds the raw-TCP HTTP header masquerade, or nil when the inbound
// asked for none. The request/response templates make the connection open like an
// ordinary HTTP exchange instead of going straight to proxy bytes.
//
// The header values are what the framing CLAIMS to be, so they are chosen to look
// like a plain browser fetch of the operator's stated hosts. Xray requires the same
// hosts and paths on the client, which is why they are mirrored into the share link.
func tcpMasquerade(o model.InboundOpts) *TCPHeader {
	if o.HeaderType != "http" {
		return nil
	}
	return &TCPHeader{
		Type: "http",
		Request: &HTTPRequest{
			Version: "1.1",
			Method:  "GET",
			Path:    o.HeaderPathsOr(),
			Headers: map[string][]string{
				"Host": o.HeaderHosts,
				"User-Agent": {
					"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
					"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
				},
				"Accept-Encoding": {"gzip, deflate"},
				"Connection":      {"keep-alive"},
				"Pragma":          {"no-cache"},
			},
		},
		Response: &HTTPResponse{
			Version: "1.1",
			Status:  "200",
			Reason:  "OK",
			Headers: map[string][]string{
				"Content-Type":      {"application/octet-stream", "video/mpeg"},
				"Transfer-Encoding": {"chunked"},
				"Connection":        {"keep-alive"},
				"Pragma":            {"no-cache"},
			},
		},
	}
}

// customALPN is the ALPN a TLS-secured custom inbound offers, matched to what its
// transport actually speaks: WebSocket and HTTPUpgrade need HTTP/1.1 to complete
// their upgrade, gRPC is HTTP/2-only, and the rest are happiest with both.
func customALPN(transport string) []string {
	switch transport {
	case model.TrWS, model.TrHTTPUpgrade:
		return []string{"http/1.1"}
	case model.TrGRPC:
		return []string{"h2"}
	default:
		return []string{"h2", "http/1.1"}
	}
}

// customVLESSClients builds a custom VLESS inbound's client list. The flow comes
// from the inbound (Vision on raw TCP, none elsewhere) rather than being assumed.
func customVLESSClients(in model.Inbound, users []model.User) []VLESSClient {
	out := make([]VLESSClient, 0, len(users))
	for _, u := range users {
		out = append(out, VLESSClient{ID: u.UUID, Flow: in.Opts.Flow, Email: model.UserEmail(u.ID)})
	}
	return out
}

// customTrojanClients builds a custom Trojan inbound's client list.
func customTrojanClients(users []model.User) []TrojanClient {
	out := make([]TrojanClient, 0, len(users))
	for _, u := range users {
		out = append(out, TrojanClient{Password: u.Password, Email: model.UserEmail(u.ID)})
	}
	return out
}

// customHysteriaClients builds a custom Hysteria2 inbound's user list.
func customHysteriaClients(users []model.User) []HysteriaClient {
	out := make([]HysteriaClient, 0, len(users))
	for _, u := range users {
		out = append(out, HysteriaClient{Auth: u.Password, Email: model.UserEmail(u.ID)})
	}
	return out
}

// customShadowsocksClients builds a Shadowsocks-2022 inbound's client list. The key
// is derived from the UUID rather than stored, so it moves with the credential the
// panel already rotates (see model.UserShadowKey).
//
// The list is never allowed to be empty, and that is a security property, not a
// tidiness one. Xray builds a Shadowsocks-2022 inbound in two very different ways: a
// non-empty `users` list is a multi-user server where the top-level key only selects
// a user, but an EMPTY list collapses to a SINGLE-user server whose access key IS
// that top-level key — the server key that sits in every client link this inbound
// ever issued. So "nobody may use this inbound" (an access group emptied, every user
// revoked) would silently become "anybody who ever held a link may", with no account
// behind it and no quota on it. When no real user is allowed we keep exactly one
// locked entry instead, so Xray stays multi-user and the only key that authenticates
// is one nobody has. Fail closed, like the empty client list on every other protocol.
func customShadowsocksClients(in model.Inbound, users []model.User) []ShadowsocksClient {
	out := make([]ShadowsocksClient, 0, len(users))
	for _, u := range users {
		out = append(out, ShadowsocksClient{
			Password: model.UserShadowKey(u.UUID, in.Opts.Method),
			Email:    model.UserEmail(u.ID),
		})
	}
	if len(out) == 0 {
		out = append(out, ShadowsocksClient{
			Password: model.LockedShadowKey(in.Opts.ShadowKey, in.Opts.Method),
			Email:    "ss-locked-" + in.Tag(),
		})
	}
	return out
}

// protocolClients builds the client lists for the three BUILT-IN lanes (a disabled
// one gets none, so nobody can authenticate against it). REALITY reuses the VLESS
// UUID but with no flow — Vision is raw-TCP only, not XHTTP.
//
// allowBuiltin decides whether a user's credential goes into a given built-in lane —
// the per-user access gate. Nil (the historical path) admits everyone.
func protocolClients(set *model.Settings, users []model.User, allowBuiltin func(userID int64, lane string) bool) ([]VLESSClient, []HysteriaClient, []VLESSClient) {
	if allowBuiltin == nil {
		allowBuiltin = func(int64, string) bool { return true }
	}
	vc := make([]VLESSClient, 0, len(users))
	hc := make([]HysteriaClient, 0, len(users))
	rc := make([]VLESSClient, 0, len(users))
	for _, u := range users {
		email := model.UserEmail(u.ID)
		if set.VLESSEnabled && allowBuiltin(u.ID, model.LaneVLESS) {
			vc = append(vc, VLESSClient{ID: u.UUID, Flow: VisionFlow, Email: email})
		}
		if set.HysteriaEnabled && allowBuiltin(u.ID, model.LaneHysteria) {
			hc = append(hc, HysteriaClient{Auth: u.Password, Email: email})
		}
		if set.RealityEnabled && allowBuiltin(u.ID, model.LaneReality) {
			rc = append(rc, VLESSClient{ID: u.UUID, Email: email})
		}
	}
	return vc, hc, rc
}

// UserInbounds builds inbound stubs (tag + protocol + clients) for the given users
// on every inbound that carries users — the built-in lanes AND each custom inbound.
// Used by the live add-user API (`xray api adu`) so a new user joins the running
// Xray without a restart.
//
// Driving this from the actual inbound list, rather than from the settings booleans,
// is what keeps custom inbounds in step: a user added while one exists reaches it
// immediately instead of at the next full restart, and — the half that matters more
// — a user removed is gone from it just as fast.
// serverID + access gate which inbounds each added user is placed into, exactly as
// the full generator does — so a live add can never put a user into a lane their
// groups don't grant.
func UserInbounds(set *model.Settings, custom []model.Inbound, users []model.User, serverID int64, access map[int64]model.Access) []Inbound {
	allowBuiltin := func(userID int64, lane string) bool {
		return model.AccessOf(access, userID).AllowsBuiltin(serverID, lane)
	}
	vc, _, rc := protocolClients(set, users, allowBuiltin)
	// `xray api adu` parses each entry as a full InboundDetour, so a valid Port is
	// required even though only the users are applied (matched by tag).
	var in []Inbound
	if len(vc) > 0 {
		in = append(in, Inbound{Tag: TagVLESS, Port: set.VLESSPort, Protocol: "vless", Settings: VLESSInboundSettings{Clients: vc, Decryption: "none"}})
	}
	// Hysteria2 is deliberately absent: `xray api adu` rejects a QUIC inbound with
	// "unsupported inbound type". Its user set is swapped by rebuilding the whole
	// inbound instead — see HysteriaInbounds / Supervisor.ReplaceInbounds.
	if len(rc) > 0 {
		in = append(in, Inbound{Tag: TagReality, Port: set.RealityPort, Protocol: "vless", Settings: VLESSInboundSettings{Clients: rc, Decryption: "none"}})
	}
	if len(users) == 0 {
		return in
	}
	for _, c := range custom {
		allowed := allowedUsers(users, func(u model.User) bool {
			return model.AccessOf(access, u.ID).AllowsInbound(c.ID)
		})
		if len(allowed) == 0 {
			continue
		}
		stub := Inbound{Tag: c.Tag(), Port: c.Port, Protocol: c.Protocol}
		switch c.Protocol {
		case model.InbVLESS:
			stub.Settings = VLESSInboundSettings{Clients: customVLESSClients(c, allowed), Decryption: "none"}
		case model.InbTrojan:
			stub.Settings = TrojanInboundSettings{Clients: customTrojanClients(allowed)}
		case model.InbShadowsocks:
			// Shadowsocks-2022 DOES implement AddUser/RemoveUser, so `xray api adu`
			// works on it — unlike Hysteria2. Leaving it in the default branch below was
			// a live-update hole in one direction only: EnabledInboundTags lists it for
			// `rmu` (which works), so a REVOKED user was dropped live, but a newly
			// allowed user was never added live and couldn't connect until the next full
			// restart. adu parses the whole entry, so method/key/network must be here for
			// it to build, even though only the users are applied by tag.
			stub.Settings = ShadowsocksInboundSettings{
				Method:   c.Opts.Method,
				Password: c.Opts.ShadowKey,
				Network:  "tcp,udp",
				Users:    customShadowsocksClients(c, allowed),
			}
		case model.InbHysteria:
			// Same as the built-in lane above: rebuilt, not live-updated.
			continue
		default:
			continue
		}
		in = append(in, stub)
	}
	return in
}

// HysteriaInbounds picks the generated inbounds whose users Xray cannot live-update,
// so the caller can rebuild them through the API instead of restarting everything.
//
// Selected by protocol rather than by tag: the built-in lane and every operator
// -defined Hysteria2 inbound have the same limitation, and testing only the built-in
// one would leave a revoked user tunnelling through a custom QUIC inbound.
func HysteriaInbounds(cfg *Config) []Inbound {
	var out []Inbound
	for _, in := range cfg.Inbounds {
		if in.Protocol == "hysteria" {
			out = append(out, in)
		}
	}
	return out
}

// EnabledInboundTags lists the inbound tags that currently carry users (the targets
// for live user removal via `xray api rmu`) — built-in lanes plus custom inbounds.
func EnabledInboundTags(set *model.Settings, custom []model.Inbound) []string {
	var tags []string
	if set.VLESSEnabled {
		tags = append(tags, TagVLESS)
	}
	// Hysteria2 is NOT here. `xray api rmu` reports success on a QUIC inbound while
	// removing nothing, so listing it would have the panel believe it revoked access
	// it still grants. Its user set is swapped by rebuilding the inbound instead.
	if set.RealityEnabled {
		tags = append(tags, TagReality)
	}
	for _, c := range custom {
		if c.Protocol == model.InbHysteria {
			continue // rebuilt, not live-updated (see above)
		}
		tags = append(tags, c.Tag())
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
			// Userspace WireGuard, NOT a kernel TUN device. Xray picks kernel mode
			// whenever it has CAP_NET_ADMIN — which our systemd unit grants it for
			// nftables and the BBR sysctls — and that mode LEAKS: every Xray start
			// adds an `ip -6 rule` pair and a routing table it never removes. The
			// panel restarts Xray on each config change, so the leak grows all day
			// until Xray dies on "failed to find available ipv6 table index" and
			// refuses to start at all. That takes down every VPN lane, not just WARP.
			// Observed on the test box: 30 stale rules and a dead Xray.
			//
			// Userspace mode is slower than kernel TUN, and that is the right trade:
			// a WARP lane at reduced throughput beats a panel that stops serving
			// after enough edits.
			NoKernelTun: true,
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
	"192.0.0.0/24", // IETF protocol assignments
	"192.168.0.0/16",
	"198.18.0.0/15", // benchmarking range — routers on it answer as themselves
	"224.0.0.0/4",   // multicast: not a destination a tunnelled client has business with
	"240.0.0.0/4",   // reserved
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	// NB: do NOT add "::ffff:0:0/96" here. Xray parses an IPv4-mapped literal back to
	// a 4-byte address and then rejects any prefix over 32, so the whole routing config
	// fails to build — and since a panel restart stops Xray BEFORE the new config is
	// validated, that is a full outage, not a no-op. Xray already normalises v4-in-v6
	// to IPv4, so the IPv4 rows above cover the mapped form. TestPrivateEgressCIDRsAreValid
	// pins this.
}

// privateEgressDomains closes the NAME half of the same floor. The CIDR list above only
// matches once a destination is an IP, and the "direct" outbound is a bare freedom that
// dials the HOSTNAME through the OS resolver. Under DomainStrategy IPIfNonMatch, a name
// Xray's own DNS fails to resolve (a public resolver returns NXDOMAIN for "localhost",
// and a custom DNS server is a first-class panel setting) matches no IP rule at all and
// falls through to direct — which then resolves it via /etc/hosts and connects. That is a
// tunnel straight to the Xray gRPC control API on 127.0.0.1:10085, whose HandlerService
// can add and remove users on the live proxy. Blocking the names as well means the rule
// hits before any resolution, so neither a DNS miss nor a rebinding answer can slip past.
var privateEgressDomains = []string{
	"full:localhost",
	"full:localhost.localdomain",
	"full:ip6-localhost",
	"full:ip6-loopback",
	`regexp:\.localhost$`, // RFC 6761 reserves .localhost for loopback
	// Cloud metadata by name — the IP (169.254.169.254) is already blocked above, and
	// these are the hostnames that resolve to it.
	"full:metadata.google.internal",
	"full:instance-data.ec2.internal",
}

func compileRouting(rc model.RoutingConfig, order []string, warpTag string, operaActive, panelEgressWarp bool, active map[string]bool) *Routing {
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
	// The name half of the same floor — see privateEgressDomains. Must stay adjacent to
	// (and as early as) the CIDR rule: a domain rule matches before any DNS resolution,
	// which is exactly the gap the IP list cannot cover.
	addDomainRule(out, "block", privateEgressDomains)

	// Local traffic that asked for WARP by name, dispatched ahead of every operator
	// rule: whoever dialled this inbound picked the tunnel deliberately, and a
	// block/lane rule written for CLIENT traffic has no business redirecting it or
	// black-holing it. It stays BELOW the private-address floor above on purpose — an
	// entrance on loopback gets no more reach into the LAN through Xray than a VPN
	// client does.
	if panelEgressWarp {
		out.Rules = append(out.Rules, RouteRule{
			Type:        "field",
			InboundTag:  []string{panelEgressTag},
			OutboundTag: "warp",
		})
	}

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
