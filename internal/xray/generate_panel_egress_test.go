package xray

import (
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// warpSettings is baseSettings with a provisioned WARP account, the state the
// generator requires before it will emit the warp outbound at all.
func warpSettings() *model.Settings {
	s := baseSettings()
	s.WarpEnabled, s.WarpPrivateKey = true, "k"
	s.WarpEndpoint, s.WarpAddressV4 = "engage.cloudflareclient.com:2408", "172.16.0.2/32"
	s.WarpPublicKey = "pub"
	return s
}

func findRule(cfg *Config, inboundTag string) *RouteRule {
	for i := range cfg.Routing.Rules {
		for _, tag := range cfg.Routing.Rules[i].InboundTag {
			if tag == inboundTag {
				return &cfg.Routing.Rules[i]
			}
		}
	}
	return nil
}

// This inbound is the only way anything on the box can reach WARP, which is a
// WireGuard outbound with no address of its own. It is tied to WARP being available
// and nothing else — the Routing page publishes its address whenever WARP is up, so
// it has to exist for as long as that address is advertised.
func TestPanelEgressInboundEmittedWheneverWarpIsUp(t *testing.T) {
	set := warpSettings()

	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	in := findInbound(cfg, panelEgressTag)
	if in == nil {
		t.Fatal("the WARP entrance is missing — nothing on the box could reach the tunnel")
	}
	// Loopback only. Bound to 0.0.0.0 this would be an open, unauthenticated SOCKS
	// proxy on the public internet.
	if in.Listen != "127.0.0.1" {
		t.Errorf("listening on %q — an unauthenticated SOCKS inbound must never leave loopback", in.Listen)
	}
	if in.Port != model.PanelEgressPort {
		t.Errorf("port %d, want %d — this is the port WarpProxyURL advertises", in.Port, model.PanelEgressPort)
	}
	if in.Protocol != "socks" {
		t.Errorf("protocol %q, want socks", in.Protocol)
	}

	rule := findRule(cfg, panelEgressTag)
	if rule == nil {
		t.Fatal("no routing rule for the entrance — its traffic would follow the operator's lanes instead of WARP")
	}
	if rule.BalancerTag != warpBalancerTag {
		t.Errorf("the entrance is routed to %q/%q, want the %s balancer",
			rule.OutboundTag, rule.BalancerTag, warpBalancerTag)
	}
}

// The Telegram proxy setting must NOT decide whether this exists. It used to, and
// that coupling meant saving an unrelated Telegram field rewrote the Xray config and
// restarted every VPN lane.
func TestPanelEgressInboundIgnoresTheTelegramSetting(t *testing.T) {
	for _, mode := range []string{"", model.TGProxyDirect, model.TGProxyCustom} {
		set := warpSettings()
		set.TGProxyMode = mode
		cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
		if err != nil {
			t.Fatalf("generate(%q): %v", mode, err)
		}
		if findInbound(cfg, panelEgressTag) == nil {
			t.Errorf("Telegram mode %q removed the WARP entrance", mode)
		}
	}
}

// No WARP, no entrance. An inbound with nothing behind it would accept connections
// and send them straight back out direct — a silent no-op wearing the appearance of a
// tunnel, and an address the Routing page would be wrong to advertise.
func TestPanelEgressInboundAbsentWhenWarpUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*model.Settings)
	}{
		{"warp disabled", func(s *model.Settings) { s.WarpEnabled = false }},
		{"warp not registered", func(s *model.Settings) { s.WarpPrivateKey = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := warpSettings()
			tc.mut(set)
			cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if findInbound(cfg, panelEgressTag) != nil {
				t.Error("emitted an entrance with no WARP behind it")
			}
			if findRule(cfg, panelEgressTag) != nil {
				t.Error("emitted a routing rule for an inbound that does not exist")
			}
		})
	}
}

// The panel's own egress must not be able to reach the LAN or the cloud metadata
// endpoint through Xray either — the security floor has to stay above its rule.
func TestPanelEgressRuleSitsBelowThePrivateAddressFloor(t *testing.T) {
	set := warpSettings()
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	blockAt, panelAt := -1, -1
	for i, r := range cfg.Routing.Rules {
		if blockAt < 0 && r.OutboundTag == "block" && len(r.IP) > 0 {
			blockAt = i
		}
		if len(r.InboundTag) > 0 && r.InboundTag[0] == panelEgressTag {
			panelAt = i
		}
	}
	if blockAt < 0 || panelAt < 0 {
		t.Fatalf("rules not found (private-block=%d, panel-egress=%d)", blockAt, panelAt)
	}
	if blockAt > panelAt {
		t.Errorf("the private-address block is at %d, after the entrance rule at %d — "+
			"a local process could dial the LAN through Xray", blockAt, panelAt)
	}
}

// WARP must run in USERSPACE WireGuard, never a kernel TUN device. Xray picks kernel
// mode whenever it has CAP_NET_ADMIN — which the panel's systemd unit grants it — and
// that mode leaks an `ip -6 rule` pair plus a routing table on every start. The panel
// restarts Xray on each config change, so the leak grows until Xray refuses to boot
// with "failed to find available ipv6 table index", taking down every lane. Observed
// live: 30 stale rules and a dead Xray.
func TestWarpOutboundStaysOutOfTheKernel(t *testing.T) {
	set := warpSettings()
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	members := 0
	for _, o := range cfg.Outbounds {
		if !strings.HasPrefix(o.Tag, warpTagPrefix) {
			continue
		}
		members++
		wg, ok := o.Settings.(WireGuardSettings)
		if !ok {
			t.Fatalf("%s settings are %T, not WireGuardSettings", o.Tag, o.Settings)
		}
		if !wg.NoKernelTun {
			t.Errorf("noKernelTun is off on %s — Xray will take a kernel TUN and leak a routing table per restart", o.Tag)
		}
	}
	if members == 0 {
		t.Fatal("no warp outbound generated")
	}
}

// The security floor must block the loopback by NAME as well as by IP. The "direct"
// outbound is a bare freedom that dials the hostname through the OS resolver, so under
// DomainStrategy IPIfNonMatch a name the configured DNS fails to resolve (a public
// resolver has no answer for "localhost") matches no IP rule and falls through to direct,
// which then reaches 127.0.0.1:10085 — the Xray control API that can add and remove users.
func TestPrivateEgressFloorBlocksLoopbackByName(t *testing.T) {
	cfg, err := Generate(warpSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	ipAt, domainAt, laneAt := -1, -1, -1
	var blocked []string
	for i, r := range cfg.Routing.Rules {
		switch {
		case r.OutboundTag == "block" && len(r.IP) > 0 && ipAt < 0:
			ipAt = i
		case r.OutboundTag == "block" && len(r.Domain) > 0 && domainAt < 0:
			domainAt, blocked = i, r.Domain
		}
		// The first rule that sends client traffic OUT is where the floor stops applying.
		if laneAt < 0 && (r.OutboundTag == "direct" || r.BalancerTag == warpBalancerTag) && len(r.InboundTag) == 0 {
			laneAt = i
		}
	}
	if ipAt < 0 || domainAt < 0 {
		t.Fatalf("floor rules not found (ip=%d, domain=%d)", ipAt, domainAt)
	}
	if laneAt >= 0 && domainAt > laneAt {
		t.Errorf("the loopback-name block is at %d, after an egress lane at %d", domainAt, laneAt)
	}
	for _, want := range []string{"full:localhost", "full:metadata.google.internal"} {
		if !slices.Contains(blocked, want) {
			t.Errorf("%q is not blocked by name: %v", want, blocked)
		}
	}
}

// Every CIDR in the security floor must be one Xray will actually accept, because a
// routing config it refuses to build is not a degraded config — a panel restart stops
// Xray before the new config is validated, so Xray simply never comes back and the box
// serves nothing. Xray parses an IPv4-mapped literal (::ffff:a.b.c.d) back to a 4-byte
// address and then caps the prefix at 32, which is how "::ffff:0:0/96" took a live master
// down: it looks like a reasonable IPv6 row and is rejected as an illegal IPv4 one.
func TestPrivateEgressCIDRsAreValid(t *testing.T) {
	for _, entry := range privateEgressCIDRs {
		ip, netw, err := net.ParseCIDR(entry)
		if err != nil {
			t.Errorf("%s: not a CIDR: %v", entry, err)
			continue
		}
		ones, _ := netw.Mask.Size()
		// To4() is non-nil for a dotted address AND for the ::ffff: mapped form — the
		// same test Xray applies before enforcing its 32-bit ceiling.
		if ip.To4() != nil && ones > 32 {
			t.Errorf("%s: Xray reads this as IPv4 and rejects a /%d (max /32)", entry, ones)
		}
	}
}

// The WARP peer is written as an address. Xray resolves a WireGuard endpoint through
// its own dns block, so a hostname makes the tunnel depend on the operator's
// resolvers: on a box whose resolvers are filtered, Xray reports "failed to set
// endpoint … app/dns: record not found" for every handshake and WARP stays down
// while every other lane looks fine.
func TestWarpPeerEndpointIsAnAddress(t *testing.T) {
	set := warpSettings() // carries the hostname an older registration stored

	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, ep := range warpEndpoints(t, cfg) {
		host, _, err := net.SplitHostPort(ep)
		if err != nil {
			t.Errorf("WARP endpoint %q is not host:port: %v", ep, err)
			continue
		}
		if net.ParseIP(host) == nil {
			t.Errorf("WARP endpoint %q still carries a hostname — the lane dies whenever Xray's resolvers do", ep)
		}
	}
}

// warpEndpoints returns the peer address of every WARP member, one per outbound.
func warpEndpoints(t *testing.T, cfg *Config) []string {
	t.Helper()
	var eps []string
	for _, o := range cfg.Outbounds {
		if !strings.HasPrefix(o.Tag, warpTagPrefix) {
			continue
		}
		peers := o.Settings.(WireGuardSettings).Peers
		if len(peers) != 1 {
			t.Fatalf("%s has %d peers, want 1 — several peers in one outbound do not fail over, they overwrite each other", o.Tag, len(peers))
		}
		eps = append(eps, peers[0].Endpoint)
	}
	if len(eps) == 0 {
		t.Fatal("no warp outbound generated")
	}
	return eps
}

// Losing one Cloudflare range or one UDP port must not take the lane down, so the
// members spread over both. They are separate outbounds on purpose: a second peer
// inside one wireguard outbound is not a second route to the same place — the peers
// share a public key and an allowed-IP range, so the last one simply wins.
func TestWarpSpreadsOverSeveralEndpoints(t *testing.T) {
	cfg, err := Generate(warpSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	eps := warpEndpoints(t, cfg)
	if len(eps) < 2 {
		t.Fatalf("only %d WARP endpoint(s): %v — one is a single point of failure", len(eps), eps)
	}
	hosts, ports := map[string]bool{}, map[string]bool{}
	for _, ep := range eps {
		host, port, err := net.SplitHostPort(ep)
		if err != nil {
			t.Fatalf("endpoint %q is not host:port: %v", ep, err)
		}
		if hosts[host] && ports[port] {
			t.Errorf("endpoint %q repeats a host and a port already in the pool %v — it adds no cover", ep, eps)
		}
		hosts[host], ports[port] = true, true
	}
	if len(hosts) < 2 || len(ports) < 2 {
		t.Errorf("pool %v covers %d address(es) and %d port(s); a provider dropping either dimension takes the lane with it",
			eps, len(hosts), len(ports))
	}
}

// The members are only useful if something works out which of them are reachable —
// leastPing with no probe data picks at random, so most picks would be dead on a
// network that dropped an endpoint.
func TestWarpBalancerIsHealthProbedAndCannotLeaveTheTunnel(t *testing.T) {
	cfg, err := Generate(warpSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var b *Balancer
	for i := range cfg.Routing.Balancers {
		if cfg.Routing.Balancers[i].Tag == warpBalancerTag {
			b = &cfg.Routing.Balancers[i]
		}
	}
	if b == nil {
		t.Fatalf("no %s balancer — the lane would be pinned to one endpoint", warpBalancerTag)
	}
	if !slices.Contains(b.Selector, warpTagPrefix) {
		t.Errorf("balancer selector %v does not select the WARP members", b.Selector)
	}
	// A tunnel that silently turns into the server's own address is worse than one
	// that fails: the destination sees the real IP of a client who asked to hide it.
	// An EMPTY fallback is the leaking case, not the safe one — Xray then falls
	// through to the first outbound in the config, which is direct (measured against
	// 26.7.28: warp=off in 0.1 s with every member unreachable). So the fallback has
	// to name a WARP member.
	if !strings.HasPrefix(b.FallbackTag, warpTagPrefix) {
		t.Errorf("the WARP balancer falls back to %q — traffic that asked for the tunnel would leave unprotected", b.FallbackTag)
	}
	if cfg.Observatory == nil {
		t.Fatal("no observatory — the balancer would have no idea which endpoints are alive")
	}
	if !slices.Contains(cfg.Observatory.SubjectSelector, warpTagPrefix) {
		t.Errorf("observatory probes %v, which leaves the WARP members unprobed", cfg.Observatory.SubjectSelector)
	}
}
