package xray

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func genScoped(t *testing.T, rc model.RoutingConfig, members map[int64][]int64, proxies map[string][]model.ProxyEndpoint) *Config {
	t.Helper()
	cfg, err := Generate(laneSettings(rc), nil, Options{PanelDest: "127.0.0.1:8080", GroupMembers: members}, proxies)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return cfg
}

// rulesTo lists the rules sending traffic to a balancer.
func rulesTo(cfg *Config, balancer string) []RouteRule {
	var out []RouteRule
	for _, r := range cfg.Routing.Rules {
		if r.BalancerTag == balancer {
			out = append(out, r)
		}
	}
	return out
}

// One entry, two tariffs, two exits: each group's whole traffic goes its own way, after
// the direct rules, and nobody else's is touched.
func TestLaneScopedToGroupsTakesItsMembersTraffic(t *testing.T) {
	rc := model.RoutingConfig{
		DirectDomains: []string{"domain:ru"},
		Lanes: []model.EgressLane{
			{ID: "a", Name: "A", Enabled: true, Manual: []string{"socks5://a:1080"}, Groups: []int64{1}},
			{ID: "b", Name: "B", Enabled: true, Manual: []string{"socks5://b:1080"}, Groups: []int64{2, 3}},
		},
		RoutingOrder: []string{"direct", "a", "b", "warp", "opera"},
	}
	cfg := genScoped(t, rc, map[int64][]int64{1: {5, 7}, 2: {9}, 3: {7, 9}}, map[string][]model.ProxyEndpoint{
		"a": {ep("a")}, "b": {ep("b")},
	})
	a, b := rulesTo(cfg, laneBalancerTag("a")), rulesTo(cfg, laneBalancerTag("b"))
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("rules: a=%+v b=%+v", a, b)
	}
	if a[0].Network != "tcp,udp" || !slices.Equal(a[0].User, []string{"u5", "u7"}) {
		t.Errorf("lane a rule = %+v", a[0])
	}
	if !slices.Equal(b[0].User, []string{"u7", "u9"}) {
		t.Errorf("lane b users = %v, want each member once, ascending", b[0].User)
	}
	idx := func(want RouteRule) int {
		return slices.IndexFunc(cfg.Routing.Rules, func(r RouteRule) bool { return r.RuleTag == want.RuleTag })
	}
	direct := slices.IndexFunc(cfg.Routing.Rules, func(r RouteRule) bool { return r.OutboundTag == "direct" && has(r.Domain, "domain:ru") })
	if direct < 0 || direct > idx(a[0]) || idx(a[0]) > idx(b[0]) {
		t.Errorf("order: direct=%d a=%d b=%d", direct, idx(a[0]), idx(b[0]))
	}
}

// Domains on a scoped lane match only its members' connections.
func TestLaneScopedToGroupsKeepsItsDestinations(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "yt", Name: "YT", Enabled: true, Manual: []string{"socks5://a:1080"}, Domains: []string{"geosite:youtube"}, Groups: []int64{1}},
		},
		RoutingOrder: []string{"yt", "warp", "opera", "direct"},
	}
	cfg := genScoped(t, rc, map[int64][]int64{1: {5}}, map[string][]model.ProxyEndpoint{"yt": {ep("a")}})
	r := rulesTo(cfg, laneBalancerTag("yt"))
	if len(r) != 1 || !has(r[0].Domain, "geosite:youtube") || !slices.Equal(r[0].User, []string{"u5"}) || r[0].Network != "" {
		t.Fatalf("rules = %+v", r)
	}
}

// A rule with no users would match everyone: a scoped lane whose groups are empty (or
// gone) names nobody instead, and stays built, so the first member joining changes the
// user list alone.
func TestLaneScopedToEmptyGroupsRoutesNobody(t *testing.T) {
	for _, order := range [][]string{{"x", "warp", "opera", "direct"}, {"warp", "opera", "direct", "x"}} {
		rc := model.RoutingConfig{
			Lanes: []model.EgressLane{
				{ID: "x", Name: "X", Enabled: true, Manual: []string{"socks5://a:1080"}, Groups: []int64{4}},
			},
			RoutingOrder: order,
		}
		cfg := genScoped(t, rc, map[int64][]int64{1: {5}}, map[string][]model.ProxyEndpoint{"x": {ep("a")}})
		for _, r := range cfg.Routing.Rules {
			if r.Network != "" && len(r.User) == 0 && r.OutboundTag != "block" {
				t.Fatalf("order %v: rule %+v takes everyone", order, r)
			}
		}
		r := rulesTo(cfg, laneBalancerTag("x"))
		if len(r) != 1 || !slices.Equal(r[0].User, []string{relayNobody}) {
			t.Fatalf("order %v: rules = %+v, want one naming nobody", order, r)
		}
	}
}

// A change of members alone is a change of the scoped rules' user lists alone: what the
// running process takes through the routing API, with no restart.
func TestScopedMembersChangeIsLive(t *testing.T) {
	rc := model.RoutingConfig{
		DirectDomains: []string{"domain:ru"},
		Lanes: []model.EgressLane{
			{ID: "a", Name: "A", Enabled: true, Manual: []string{"socks5://a:1080"}, Groups: []int64{1}},
			{ID: "b", Name: "B", Enabled: true, Manual: []string{"socks5://b:1080"}, Domains: []string{"domain:yt.com"}, IPs: []string{"1.1.1.1"}, Groups: []int64{2}},
		},
		RoutingOrder: []string{"direct", "a", "b", "warp", "opera"},
	}
	pool := map[string][]model.ProxyEndpoint{"a": {ep("a")}, "b": {ep("b")}}
	gen := func(members map[int64][]int64) []byte {
		data, err := json.Marshal(genScoped(t, rc, members, pool))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := gen(map[int64][]int64{1: {5}})
	for name, after := range map[string][]byte{
		"joined":       gen(map[int64][]int64{1: {5, 7}, 2: {9}}),
		"left":         gen(nil),
		"other lane":   gen(map[int64][]int64{1: {5}, 2: {3}}),
		"same members": gen(map[int64][]int64{1: {5}, 3: {8}}),
	} {
		changes, ok := planUserChanges(before, after)
		if !ok || len(changes) != 0 {
			t.Fatalf("%s: planUserChanges ok=%v changes=%d, want users of no inbound moved", name, ok, len(changes))
		}
		_, changed, ok := relayRulesChanged(before, after)
		if !ok || changed != (name != "same members") {
			t.Fatalf("%s: relayRulesChanged changed=%v ok=%v", name, changed, ok)
		}
	}
}

// Every scoped rule carries a tag of its own, so the rules are replaceable one by one.
func TestScopedRuleTagsAreUnique(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "a", Name: "A", Enabled: true, Manual: []string{"socks5://a:1080"}, Domains: []string{"domain:x.com"}, IPs: []string{"1.1.1.1"}, Groups: []int64{1}},
			{ID: "b", Name: "B", Enabled: true, Manual: []string{"socks5://b:1080"}, Groups: []int64{1}},
		},
		RoutingOrder: []string{"a", "warp", "opera", "direct", "b"},
	}
	cfg := genScoped(t, rc, map[int64][]int64{1: {5}}, map[string][]model.ProxyEndpoint{"a": {ep("a")}, "b": {ep("b")}})
	seen := map[string]bool{}
	scoped := 0
	for _, r := range cfg.Routing.Rules {
		if seen[r.RuleTag] {
			t.Fatalf("duplicate rule tag %q", r.RuleTag)
		}
		seen[r.RuleTag] = true
		if len(r.User) > 0 {
			scoped++
			if !hasLiveUsers(r.RuleTag) {
				t.Errorf("scoped rule %+v is not live", r)
			}
		}
	}
	if scoped != 3 {
		t.Fatalf("scoped rules = %d, want 3 (domain + IP of a, catch-all of b)", scoped)
	}
}

// A scoped catch-all is everything else of its members only.
func TestScopedCatchAllIsItsMembersOnly(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "x", Name: "X", Enabled: true, Manual: []string{"socks5://a:1080"}, Domains: []string{"domain:a.com"}, Groups: []int64{1}},
		},
		RoutingOrder: []string{"warp", "opera", "direct", "x"},
	}
	cfg := genScoped(t, rc, map[int64][]int64{1: {5}}, map[string][]model.ProxyEndpoint{"x": {ep("a")}})
	r := rulesTo(cfg, laneBalancerTag("x"))
	if len(r) != 1 || r[0].Network != "tcp,udp" || !slices.Equal(r[0].User, []string{"u5"}) {
		t.Fatalf("rules = %+v", r)
	}
}

// A scoped cascade with no upstream drops its members' traffic, not everyone's.
func TestScopedStrictLaneBlocksItsMembersOnly(t *testing.T) {
	rc := model.RoutingConfig{
		StrictEgress: true,
		Lanes: []model.EgressLane{
			{ID: "x", Name: "X", Enabled: true, Manual: []string{"socks5://a:1080"}, Groups: []int64{1}},
		},
		RoutingOrder: []string{"x", "warp", "opera", "direct"},
	}
	cfg := genScoped(t, rc, map[int64][]int64{1: {5}}, nil)
	var found bool
	for _, r := range cfg.Routing.Rules {
		if r.OutboundTag == "block" && r.Network == "tcp,udp" {
			if !slices.Equal(r.User, []string{"u5"}) {
				t.Fatalf("block-all rule %+v reaches beyond the members", r)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no block rule for the members of a lane that cannot run")
	}
}

// An iplist entry that did not load leaves the lane without destinations; that must not
// turn it into "all of the members' traffic".
func TestScopedLaneWithUnloadedListIsNotAllTraffic(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes: []model.EgressLane{
			{ID: "x", Name: "X", Enabled: true, Manual: []string{"socks5://a:1080"}, Domains: []string{"iplist:none/none"}, Groups: []int64{1}},
		},
		RoutingOrder: []string{"x", "warp", "opera", "direct"},
	}
	cfg := genScoped(t, rc, map[int64][]int64{1: {5}}, map[string][]model.ProxyEndpoint{"x": {ep("a")}})
	for _, r := range rulesTo(cfg, laneBalancerTag("x")) {
		if r.Network != "" {
			t.Fatalf("rule %+v takes all traffic", r)
		}
	}
}

func TestScopedMembers(t *testing.T) {
	got := ScopedMembers([]model.EgressLane{{Groups: []int64{2}}, {}}, map[int64][]int64{1: {1}, 2: {3}})
	if len(got) != 1 || !slices.Equal(got[2], []int64{3}) {
		t.Fatalf("ScopedMembers = %v", got)
	}
	if ScopedMembers([]model.EgressLane{{}}, map[int64][]int64{1: {1}}) != nil {
		t.Fatal("no scoped lane should read nothing")
	}
}

// scopedCfg is a whole config with one lane scoped to group 1, for the supervisor tests.
func scopedCfg(t *testing.T, members ...int64) *Config {
	t.Helper()
	rc := model.RoutingConfig{
		Lanes:        []model.EgressLane{{ID: "a", Name: "A", Enabled: true, Manual: []string{"socks5://a:1080"}, Groups: []int64{1}}},
		RoutingOrder: []string{"a", "warp", "opera", "direct"},
	}
	users := []model.User{{ID: 5, UUID: "00000000-0000-4000-8000-000000000005", Password: "p5"}, {ID: 7, UUID: "00000000-0000-4000-8000-000000000007", Password: "p7"}}
	cfg, err := Generate(laneSettings(rc), users, Options{PanelDest: "127.0.0.1:8080", GroupMembers: map[int64][]int64{1: members}},
		map[string][]model.ProxyEndpoint{"a": {ep("a")}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return cfg
}

// The master: a group's members changing reaches the running Xray through the routing
// API, not a restart that would cut everyone.
func TestApplyChangesScopedMembersWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts.log")
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	if err := sup.Apply(scopedCfg(t, 5)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	waitFor(t, "xray to start", func() bool { return countLines(t, starts, "start") == 1 && sup.Running() })
	cliCalls(t, dir)

	if err := sup.Apply(scopedCfg(t, 5, 7)); err != nil {
		t.Fatalf("member joined: %v", err)
	}
	if n := countLines(t, starts, "start"); n != 1 {
		t.Fatalf("xray was restarted (%d starts) for a member joining", n)
	}
	sent := rulesSent(t, dir, 0)
	i := slices.IndexFunc(sent, func(r map[string]any) bool { return r["balancerTag"] == laneBalancerTag("a") })
	if i < 0 || !slices.Equal(anyStrings(sent[i]["user"]), []string{"u5", "u7"}) {
		t.Fatalf("rules sent %v, want the lane's rule for u5 and u7", sent)
	}
}

// A node: the same, through the config the panel pushes.
func TestApplyRawLiveChangesScopedMembers(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	const addr = "127.0.0.1:20085"
	raw := func(members ...int64) []byte {
		data, err := json.MarshalIndent(scopedCfg(t, members...), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if how, err := sup.ApplyRawLive(addr, raw(5)); err != nil || how != RawRestarted {
		t.Fatalf("first apply: %v %v", how, err)
	}
	waitFor(t, "xray to start", sup.Running)
	if how, err := sup.ApplyRawLive(addr, raw()); err != nil || how != RawLive {
		t.Fatalf("member left: %v %v, want live", how, err)
	}
	if n := countLines(t, filepath.Join(dir, "starts.log"), "start"); n != 1 {
		t.Errorf("xray was started %d times, want once", n)
	}
}
