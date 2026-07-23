package xray

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// laneSettings is a minimal Settings that Generate accepts, carrying rc.
func laneSettings(rc model.RoutingConfig) *model.Settings {
	return &model.Settings{
		CertPath: "/tmp/cert.pem",
		KeyPath:  "/tmp/key.pem",
		WSPath:   "/ws",
		Routing:  rc,
	}
}

func genLanes(t *testing.T, rc model.RoutingConfig, proxies map[string][]model.ProxyEndpoint) *Config {
	t.Helper()
	cfg, err := Generate(laneSettings(rc), nil, Options{PanelDest: "127.0.0.1:8080"}, proxies)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return cfg
}

func ep(host string) model.ProxyEndpoint {
	return model.ProxyEndpoint{Protocol: "socks", Address: host, Port: 1080}
}

// outboundTags returns the tags of every outbound, in order.
func outboundTags(cfg *Config) []string {
	var out []string
	for _, o := range cfg.Outbounds {
		out = append(out, o.Tag)
	}
	return out
}

// ruleTarget finds the rule matching a domain and returns the tag it routes to
// (balancer tag preferred, else outbound tag). Empty when no rule matches.
func ruleTarget(cfg *Config, domain string) string {
	for _, r := range cfg.Routing.Rules {
		for _, d := range r.Domain {
			if d == domain {
				if r.BalancerTag != "" {
					return r.BalancerTag
				}
				return r.OutboundTag
			}
		}
	}
	return ""
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Two lanes with their own proxies and their own rules must egress independently:
// each gets its own outbounds, its own balancer, and its own routing rule. This is
// the headline feature — ".ru through proxy A, .com through proxy B".
func TestGenerateIndependentLanes(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "ru", Name: "RU", Enabled: true, Manual: []string{"socks5://a:1080"}, Domains: []string{"domain:.ru"}},
			{ID: "en", Name: "EN", Enabled: true, Manual: []string{"socks5://b:1080"}, Domains: []string{"domain:.com"}},
		},
		RoutingOrder: []string{"ru", "en", "warp", "opera", "direct"},
	}
	cfg := genLanes(t, rc, map[string][]model.ProxyEndpoint{
		"ru": {ep("a")},
		"en": {ep("b")},
	})

	tags := outboundTags(cfg)
	for _, want := range []string{"proxy-ru-0", "proxy-en-0"} {
		if !has(tags, want) {
			t.Errorf("outbound %q missing; got %v", want, tags)
		}
	}
	if got := ruleTarget(cfg, "domain:.ru"); got != "pool-ru" {
		t.Errorf(".ru routes to %q, want pool-ru", got)
	}
	if got := ruleTarget(cfg, "domain:.com"); got != "pool-en" {
		t.Errorf(".com routes to %q, want pool-en", got)
	}

	// Each lane's balancer must select ONLY its own members.
	if len(cfg.Routing.Balancers) != 2 {
		t.Fatalf("got %d balancers, want 2", len(cfg.Routing.Balancers))
	}
	for _, b := range cfg.Routing.Balancers {
		want := "proxy-" + strings.TrimPrefix(b.Tag, "pool-") + "-"
		if len(b.Selector) != 1 || b.Selector[0] != want {
			t.Errorf("balancer %s selector = %v, want [%s]", b.Tag, b.Selector, want)
		}
		if b.FallbackTag != "direct" {
			t.Errorf("balancer %s fallback = %q, want direct", b.Tag, b.FallbackTag)
		}
	}

	// Both lanes are health-probed.
	if cfg.Observatory == nil {
		t.Fatal("no observatory")
	}
	for _, want := range []string{"proxy-ru-", "proxy-en-"} {
		if !has(cfg.Observatory.SubjectSelector, want) {
			t.Errorf("observatory subject %q missing; got %v", want, cfg.Observatory.SubjectSelector)
		}
	}
}

// A balancer selects its members by TAG PREFIX. Lane IDs are dash-free, so a
// lane's selector can never swallow another lane's proxies — even when one ID is
// a prefix of the other ("ru" vs "ru2").
func TestLaneSelectorsDoNotOverlap(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"a.example"}},
			{ID: "ru2", Name: "RU2", Enabled: true, Domains: []string{"b.example"}},
		},
	}
	cfg := genLanes(t, rc, map[string][]model.ProxyEndpoint{
		"ru":  {ep("a")},
		"ru2": {ep("b")},
	})

	for _, b := range cfg.Routing.Balancers {
		sel := b.Selector[0]
		for _, o := range cfg.Outbounds {
			if !strings.HasPrefix(o.Tag, sel) {
				continue
			}
			// Every outbound this balancer selects must belong to its own lane.
			wantLane := strings.TrimPrefix(b.Tag, "pool-")
			if !strings.HasPrefix(o.Tag, "proxy-"+wantLane+"-") {
				t.Errorf("balancer %s (selector %q) also selects %q from another lane", b.Tag, sel, o.Tag)
			}
		}
	}
}

// A lane with no live proxies must not emit a balancer (Xray rejects an empty
// one) and must not emit rules — its traffic falls through to the next lane.
func TestLaneWithoutProxiesIsInert(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"domain:.ru"}},
		},
		RoutingOrder: []string{"ru", "warp", "opera", "direct"},
	}
	cfg := genLanes(t, rc, nil) // no proxies resolved for the lane

	if len(cfg.Routing.Balancers) != 0 {
		t.Errorf("got %d balancers for a lane with no proxies, want 0", len(cfg.Routing.Balancers))
	}
	if got := ruleTarget(cfg, "domain:.ru"); got != "" {
		t.Errorf(".ru routed to %q, want no rule (fall through to direct)", got)
	}
	if cfg.Observatory != nil {
		t.Errorf("observatory probes an inactive lane: %v", cfg.Observatory.SubjectSelector)
	}
}

// A disabled lane emits nothing even when it has proxies.
func TestDisabledLaneIsInert(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "ru", Name: "RU", Enabled: false, Domains: []string{"domain:.ru"}},
		},
	}
	cfg := genLanes(t, rc, map[string][]model.ProxyEndpoint{"ru": {ep("a")}})

	if has(outboundTags(cfg), "proxy-ru-0") {
		t.Error("disabled lane emitted an outbound")
	}
	if got := ruleTarget(cfg, "domain:.ru"); got != "" {
		t.Errorf(".ru routed to %q, want no rule", got)
	}
}

// A lane placed last in the order is the catch-all: everything unmatched leaves
// through it, via a network-wide rule.
func TestLaneAsCatchAll(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true}},
		RoutingOrder: []string{"warp", "opera", "direct", "ru"},
	}
	cfg := genLanes(t, rc, map[string][]model.ProxyEndpoint{"ru": {ep("a")}})

	var found bool
	for _, r := range cfg.Routing.Rules {
		if r.Network == "tcp,udp" && r.BalancerTag == "pool-ru" {
			found = true
		}
	}
	if !found {
		t.Errorf("no catch-all rule to pool-ru; rules: %+v", cfg.Routing.Rules)
	}
}

// An unhealthy catch-all lane (no proxies) must fall through to direct rather
// than black-hole every connection.
func TestInactiveCatchAllLaneFallsThroughToDirect(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true}},
		RoutingOrder: []string{"warp", "opera", "direct", "ru"},
	}
	cfg := genLanes(t, rc, nil)

	for _, r := range cfg.Routing.Rules {
		if r.Network == "tcp,udp" {
			t.Errorf("inactive catch-all lane still emitted a catch-all rule: %+v", r)
		}
	}
}

func TestNormalizeOrder(t *testing.T) {
	tests := []struct {
		name  string
		order []string
		lanes []string
		want  []string
	}{
		{
			name:  "empty order → lanes first, then built-ins",
			lanes: []string{"ru"},
			want:  []string{"ru", "warp", "opera", "direct"},
		},
		{
			name:  "a new lane is inserted before the catch-all",
			order: []string{"warp", "opera", "direct"},
			lanes: []string{"ru"},
			want:  []string{"warp", "opera", "ru", "direct"},
		},
		{
			name:  "a deleted lane is dropped from the order",
			order: []string{"gone", "warp", "opera", "direct"},
			lanes: []string{},
			want:  []string{"warp", "opera", "direct"},
		},
		{
			name:  "the operator's precedence is preserved",
			order: []string{"ru", "en", "direct", "warp", "opera"},
			lanes: []string{"ru", "en"},
			want:  []string{"ru", "en", "direct", "warp", "opera"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOrder(tt.order, tt.lanes)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("normalizeOrder(%v, %v) = %v, want %v", tt.order, tt.lanes, got, tt.want)
			}
		})
	}
}

// The Observatory is the one thing the server itself dials on a fixed schedule.
// One hardcoded URL at one hardcoded interval across every install makes "who
// probes this exact endpoint every 60s" a fleet-wide query, so both are derived
// from the server's own host — but derived, not random: the generated config's
// hash is what tells a node its configuration changed, and a value redrawn on
// every generate would restart Xray on every node, every time.
func TestProbeProfileIsStablePerHostAndVariesAcrossHosts(t *testing.T) {
	url1, iv1 := probeProfile("nl1.example.com")
	url2, iv2 := probeProfile("nl1.example.com")
	if url1 != url2 || iv1 != iv2 {
		t.Fatalf("same host gave different profiles: %s/%s vs %s/%s", url1, iv1, url2, iv2)
	}

	urls, intervals := map[string]bool{}, map[string]bool{}
	hosts := []string{
		"nl1.example.com", "de2.example.com", "fi3.example.com", "us4.example.com",
		"se5.example.com", "203.0.113.9", "198.51.100.4", "192.0.2.77",
	}
	for _, h := range hosts {
		u, iv := probeProfile(h)
		if !slices.Contains(probeTargets, u) {
			t.Errorf("host %q probes %q, which is not a listed target", h, u)
		}
		d, err := time.ParseDuration(iv)
		if err != nil {
			t.Errorf("host %q interval %q does not parse: %v", h, iv, err)
			continue
		}
		if d < 45*time.Second || d > 90*time.Second {
			t.Errorf("host %q interval %v outside 45–90s", h, d)
		}
		urls[u], intervals[iv] = true, true
	}
	if len(urls) < 2 {
		t.Errorf("all %d hosts landed on one probe URL", len(hosts))
	}
	if len(intervals) < 2 {
		t.Errorf("all %d hosts landed on one interval", len(hosts))
	}
}
