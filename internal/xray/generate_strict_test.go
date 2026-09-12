package xray

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// StrictEgress exists for one guarantee: traffic an operator routed to an egress
// never leaves through the server's own address because that egress is down. The
// default config fails open on purpose and generate_lanes_test.go pins that; these
// pin the opposite, and every one of them is a way the guarantee used to leak.

func genStrict(t *testing.T, set *model.Settings, proxies map[string][]model.ProxyEndpoint) *Config {
	t.Helper()
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, proxies)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return cfg
}

// balancer finds a balancer by tag.
func balancer(t *testing.T, cfg *Config, tag string) Balancer {
	t.Helper()
	for _, b := range cfg.Routing.Balancers {
		if b.Tag == tag {
			return b
		}
	}
	t.Fatalf("no balancer %q in %+v", tag, cfg.Routing.Balancers)
	return Balancer{}
}

// catchAllTarget is where the final network-wide rule sends everything unmatched,
// or "" when there is no such rule (so it falls through to the first outbound).
func catchAllTarget(cfg *Config) string {
	for _, r := range cfg.Routing.Rules {
		if r.Network == "tcp,udp" && len(r.Domain) == 0 && len(r.IP) == 0 && len(r.InboundTag) == 0 {
			if r.BalancerTag != "" {
				return r.BalancerTag
			}
			return r.OutboundTag
		}
	}
	return ""
}

// The core case from the issue: a lane whose upstreams are all dead. The balancer
// is what decides, and its fallback is what a client gets.
func TestStrictEgressDeadUpstreamsDropInsteadOfLeaving(t *testing.T) {
	rc := model.RoutingConfig{
		StrictEgress: true,
		Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"domain:.ru"}}},
		OperaDomains: []string{"domain:example.com"},
		RoutingOrder: []string{"ru", "warp", "opera", "direct"},
	}
	set := laneSettings(rc)
	set.OperaEnabled = true
	cfg := genStrict(t, set, map[string][]model.ProxyEndpoint{"ru": {ep("a")}})

	for _, tag := range []string{"pool-ru", operaBalancerTag} {
		if fb := balancer(t, cfg, tag).FallbackTag; fb != "block" {
			t.Errorf("%s falls back to %q with every member dead — want block", tag, fb)
		}
	}
}

// An omitted fallbackTag is not "no fallback": measured on Xray 26.7.28, a balancer
// with none and every member down routes to the first outbound, which is direct.
// So it must never be empty, strict or not, and never direct under strict.
func TestNoBalancerEverHasAnEmptyFallback(t *testing.T) {
	for _, strict := range []bool{false, true} {
		rc := model.RoutingConfig{
			StrictEgress: strict,
			Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"domain:.ru"}}},
			WarpDomains:  []string{"domain:w.example"},
			OperaDomains: []string{"domain:o.example"},
			RoutingOrder: []string{"ru", "warp", "opera", "direct"},
		}
		set := laneSettings(rc)
		set.OperaEnabled, set.WarpEnabled, set.WarpPrivateKey = true, true, "k"
		set.WarpAddressV4 = "172.16.0.2"
		cfg := genStrict(t, set, map[string][]model.ProxyEndpoint{"ru": {ep("a")}})
		if len(cfg.Routing.Balancers) != 3 {
			t.Fatalf("strict=%v: want 3 balancers, got %+v", strict, cfg.Routing.Balancers)
		}
		for _, b := range cfg.Routing.Balancers {
			if b.FallbackTag == "" {
				t.Errorf("strict=%v: balancer %s has no fallbackTag, which leaks to direct", strict, b.Tag)
			}
			if strict && b.FallbackTag == "direct" {
				t.Errorf("strict: balancer %s still falls back to direct", b.Tag)
			}
		}
	}
}

// A lane switched on and routed to, with no upstream resolved at all — every list
// URL failing is the usual cause. Off, it is inert and its destinations fall through
// to a later lane; strict, they are dropped where the lane stands in the order.
func TestStrictEgressDropsALaneWithNoUpstream(t *testing.T) {
	rc := model.RoutingConfig{
		StrictEgress: true,
		Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"domain:.ru"}}},
		RoutingOrder: []string{"ru", "warp", "opera", "direct"},
	}
	cfg := genStrict(t, laneSettings(rc), nil)
	if got := ruleTarget(cfg, "domain:.ru"); got != "block" {
		t.Errorf(".ru goes to %q with its lane unbuildable — want block", got)
	}
	if len(cfg.Routing.Balancers) != 0 {
		t.Errorf("an unbuildable lane still emitted a balancer (Xray rejects an empty one): %+v", cfg.Routing.Balancers)
	}
}

// The widest leak: everything unmatched was sent to a lane that cannot run.
func TestStrictEgressDropsEverythingForAnUnusableCatchAll(t *testing.T) {
	rc := model.RoutingConfig{
		StrictEgress: true,
		Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true}},
		RoutingOrder: []string{"warp", "opera", "direct", "ru"},
	}
	cfg := genStrict(t, laneSettings(rc), nil)
	if got := catchAllTarget(cfg); got != "block" {
		t.Errorf("catch-all goes to %q — want block, not the fall-through to direct", got)
	}

	// And the same config without the setting keeps today's behaviour.
	rc.StrictEgress = false
	if got := catchAllTarget(genStrict(t, laneSettings(rc), nil)); got != "" {
		t.Errorf("strict off: catch-all goes to %q — want the fall-through to direct", got)
	}
}

func TestStrictEgressDropsWarpWithoutAnAccount(t *testing.T) {
	rc := model.RoutingConfig{
		StrictEgress: true,
		WarpDomains:  []string{"domain:w.example"},
		RoutingOrder: []string{"warp", "opera", "direct"},
	}
	set := laneSettings(rc)
	set.WarpEnabled = true // on, but never registered
	cfg := genStrict(t, set, nil)
	if got := ruleTarget(cfg, "domain:w.example"); got != "block" {
		t.Errorf("a WARP destination goes to %q with no account — want block", got)
	}

	rc.RoutingOrder = []string{"opera", "direct", "warp"}
	set = laneSettings(rc)
	set.WarpEnabled = true
	if got := catchAllTarget(genStrict(t, set, nil)); got != "block" {
		t.Errorf("WARP as catch-all with no account goes to %q — want block", got)
	}
}

// A lane the operator switched off is not unavailable, it is not in use. Strict mode
// must not turn "I disabled this" into "everything it listed is now unreachable".
//
// "Inert" is exact: no rule at all. Checking only "not blocked" let a lane that had
// quietly become ACTIVE pass — it routes to its pool, which is not "block" either.
// And it is checked with and without upstreams, because without them the same slip
// shows up as a drop instead.
func TestStrictEgressLeavesSwitchedOffLanesInert(t *testing.T) {
	for name, proxies := range map[string]map[string][]model.ProxyEndpoint{
		"with upstreams":    {"ru": {ep("a")}},
		"without upstreams": nil,
	} {
		rc := model.RoutingConfig{
			StrictEgress: true,
			Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: false, Domains: []string{"domain:.ru"}}},
			WarpDomains:  []string{"domain:w.example"},
			RoutingOrder: []string{"ru", "warp", "opera", "direct"},
		}
		cfg := genStrict(t, laneSettings(rc), proxies)
		if got := ruleTarget(cfg, "domain:.ru"); got != "" {
			t.Errorf("%s: a switched-off lane routes .ru to %q — want no rule", name, got)
		}
		if has(outboundTags(cfg), "proxy-ru-0") {
			t.Errorf("%s: a switched-off lane emitted an outbound", name)
		}
		// WARP off keeps what it always did, which is not a drop.
		if got := ruleTarget(cfg, "domain:w.example"); got == "block" {
			t.Errorf("%s: switched-off WARP's destinations were blocked", name)
		}
	}
}

// A drop takes the unusable lane's place in the order, no more. A destination an
// earlier lane claims still gets that lane; only what this lane would have carried
// is dropped, and a later lane cannot quietly pick it up and send it out directly.
func TestStrictEgressDropSitsAtTheLanesPlaceInTheOrder(t *testing.T) {
	rc := model.RoutingConfig{
		StrictEgress: true,
		Lanes: []model.EgressLane{
			{ID: "nl", Name: "NL", Enabled: true, Domains: []string{"domain:nl.example"}},
			{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"domain:ru.example"}},
		},
		DirectDomains: []string{"domain:ru.example"}, // a later lane that would take it
		RoutingOrder:  []string{"nl", "ru", "warp", "opera", "direct"},
	}
	cfg := genStrict(t, laneSettings(rc), map[string][]model.ProxyEndpoint{"nl": {ep("a")}})
	if got := ruleTarget(cfg, "domain:nl.example"); got != "pool-nl" {
		t.Errorf("nl.example goes to %q — the working lane ahead of the dead one should keep it", got)
	}
	if got := ruleTarget(cfg, "domain:ru.example"); got != "block" {
		t.Errorf("ru.example goes to %q — the dead lane comes first, so direct must not get it", got)
	}
}
