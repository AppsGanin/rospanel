package xray

import (
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
	if rule.OutboundTag != "warp" {
		t.Errorf("the entrance is routed to %q, want warp", rule.OutboundTag)
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
	for _, o := range cfg.Outbounds {
		if o.Tag != "warp" {
			continue
		}
		wg, ok := o.Settings.(WireGuardSettings)
		if !ok {
			t.Fatalf("warp settings are %T, not WireGuardSettings", o.Settings)
		}
		if !wg.NoKernelTun {
			t.Error("noKernelTun is off — Xray will take a kernel TUN and leak a routing table per restart")
		}
		return
	}
	t.Fatal("no warp outbound generated")
}
