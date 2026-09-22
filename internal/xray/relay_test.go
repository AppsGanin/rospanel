package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

const relayLink = "vless://0cef6f84-5100-4499-892b-cc5b3107d1e2@9.9.9.9:443?security=tls&sni=p.example&type=tcp#partner"

// relaySettings is a master with the TCP-TLS lane only (no QUIC users to sync), a
// block rule and a direct lane, so a relay rule's place among them can be seen.
func relaySettings() *model.Settings {
	set := baseSettings()
	set.HysteriaEnabled = false
	set.Routing.BlockDomains = []string{"domain:blocked.example"}
	set.Routing.DirectDomains = []string{"domain:ru"}
	// Direct ahead of WARP, so its rules are written out rather than being the
	// catch-all everything falls through to.
	set.Routing.RoutingOrder = []string{"direct", "warp"}
	return set
}

func relayUsers(ids ...int64) []model.User {
	uuids := map[int64]string{
		1: "6bbd16cd-dfc1-47c4-9426-59b57b92b173",
		2: "11111111-2222-4333-8444-555555555555",
		3: "aaaaaaaa-bbbb-0005-8ccc-dddddddddddd", // carries route 5 as it is
		4: "22222222-3333-4444-8555-666666666666",
	}
	var out []model.User
	for _, id := range ids {
		out = append(out, model.User{ID: id, UUID: uuids[id], Password: "p"})
	}
	return out
}

// relayAccess grants the TCP-TLS lane to everyone and external server 5 to the ids given.
func relayAccess(granted ...int64) map[int64]model.Access {
	out := map[int64]model.Access{}
	for id := int64(1); id <= 4; id++ {
		tokens := map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}
		if slices.Contains(granted, id) {
			tokens[model.ExtToken(5)] = true
		}
		out[id] = model.Access{Tokens: tokens}
	}
	return out
}

func relayOpts(granted ...int64) Options {
	return Options{
		PanelDest: "127.0.0.1:8080",
		Access:    relayAccess(granted...),
		Relays:    []Relay{{ExtID: 5, Lane: model.LaneVLESS, Link: relayLink}},
	}
}

func ruleIndex(rules []RouteRule, match func(RouteRule) bool) int {
	return slices.IndexFunc(rules, match)
}

// A relayed server is an outbound of its own and one rule: its route, on its lane, for
// the users granted it — after the block rules, ahead of every egress lane. A granted
// user whose own UUID already carries the route is left out: their own traffic would
// go to the partner.
func TestRelayRouting(t *testing.T) {
	cfg, err := Generate(relaySettings(), relayUsers(1, 2, 3), relayOpts(1, 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(cfg.Outbounds, func(o Outbound) bool { return o.Tag == "ext-5" }); i < 1 || cfg.Outbounds[i].Protocol != "vless" {
		t.Fatalf("outbounds %+v: want ext-5, a vless, after the default", cfg.Outbounds)
	}
	rules := cfg.Routing.Rules
	relay := ruleIndex(rules, func(r RouteRule) bool { return r.RuleTag == "relay-5" })
	if relay < 0 {
		t.Fatalf("no relay rule in %+v", rules)
	}
	r := rules[relay]
	if r.VlessRoute != "5" || !slices.Equal(r.InboundTag, []string{TagVLESS}) || r.OutboundTag != "ext-5" || !slices.Equal(r.User, []string{"u1"}) {
		t.Fatalf("relay rule %+v: want route 5 on vless-in to ext-5 for u1 alone", r)
	}
	blocked := ruleIndex(rules, func(r RouteRule) bool { return slices.Contains(r.Domain, "domain:blocked.example") })
	direct := ruleIndex(rules, func(r RouteRule) bool { return slices.Contains(r.Domain, "domain:ru") })
	if !(blocked < relay && relay < direct) {
		t.Fatalf("rule order block %d, relay %d, direct lane %d: want the relay between them", blocked, relay, direct)
	}
}

// Nobody granted: the rule stays, naming nobody. A rule with no users would have no
// user condition at all and take the route for everyone.
func TestRelayRuleWithNobodyGranted(t *testing.T) {
	cfg, err := Generate(relaySettings(), relayUsers(1, 2), relayOpts(), nil)
	if err != nil {
		t.Fatal(err)
	}
	i := ruleIndex(cfg.Routing.Rules, func(r RouteRule) bool { return r.RuleTag == "relay-5" })
	if i < 0 || !slices.Equal(cfg.Routing.Rules[i].User, []string{relayNobody}) {
		t.Fatalf("relay rule %+v: want it naming nobody", cfg.Routing.Rules)
	}
	// And the placeholder is nobody's email.
	if _, ok := model.UserIDOfEmail(relayNobody); ok {
		t.Fatal("the placeholder reads as a user")
	}
}

// Off the lane, off its relays; a link that cannot be expressed is left out whole.
func TestRelayNeedsItsLaneAndALink(t *testing.T) {
	set := relaySettings()
	set.VLESSEnabled = false
	cfg, err := Generate(set, relayUsers(1), relayOpts(1), nil)
	if err != nil {
		t.Fatal(err)
	}
	i := ruleIndex(cfg.Routing.Rules, func(r RouteRule) bool { return r.RuleTag == "relay-5" })
	if i < 0 || !slices.Equal(cfg.Routing.Rules[i].User, []string{relayNobody}) {
		t.Fatalf("with the lane off the relay lets in %+v", cfg.Routing.Rules)
	}
	opts := relayOpts(1)
	opts.Relays[0].Link = "socks5://1.2.3.4:1080"
	cfg, err = Generate(relaySettings(), relayUsers(1), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ruleIndex(cfg.Routing.Rules, func(r RouteRule) bool { return r.RuleTag == "relay-5" }) >= 0 {
		t.Fatal("a relay with no outbound got a rule")
	}
}

func relayCfgJSON(t *testing.T, users []model.User, opts Options) []byte {
	t.Helper()
	cfg, err := Generate(relaySettings(), users, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Only the users of relay rules differ: planUserChanges sees users alone, and the
// relay rules come back to be replaced. Anything else about the routing does not.
func TestRelayRulesChanged(t *testing.T) {
	a := relayCfgJSON(t, relayUsers(1, 2), relayOpts(1))
	b := relayCfgJSON(t, relayUsers(1, 2, 4), relayOpts(1, 4))
	if _, ok := planUserChanges(a, b); !ok {
		t.Fatal("a change of users and relay users is not users alone")
	}
	rules, changed, ok := relayRulesChanged(a, b)
	if !ok || !changed {
		t.Fatalf("relay users change: changed=%v ok=%v", changed, ok)
	}
	i := slices.IndexFunc(rules, func(r map[string]json.RawMessage) bool { return string(r["ruleTag"]) == `"relay-5"` })
	var users []string
	if i >= 0 {
		_ = json.Unmarshal(rules[i]["user"], &users)
	}
	if !slices.Equal(users, []string{"u1", "u4"}) {
		t.Fatalf("relay users to put in place %q, want u1 and u4", users)
	}
	if _, changed, ok := relayRulesChanged(a, a); !ok || changed {
		t.Fatal("the same config reads as changed")
	}
	set := relaySettings()
	set.Routing.DirectDomains = []string{"domain:su"}
	cfg, err := Generate(set, relayUsers(1, 2), relayOpts(1), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := json.Marshal(cfg)
	if _, ok := planUserChanges(a, c); ok {
		t.Fatal("a routing change passed for users alone")
	}
	if _, _, ok := relayRulesChanged(a, c); ok {
		t.Fatal("a routing change passed for relay users")
	}
}

// A node's pushed config that adds a user granted a relayed server goes in live: the
// user through the API, the relay rule replaced through the routing API, no restart.
func TestApplyRawLiveReplacesRelayUsers(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	const addr = "127.0.0.1:20085"
	a := relayCfgJSON(t, relayUsers(1, 2), relayOpts(1))
	if how, err := sup.ApplyRawLive(addr, a); err != nil || how != RawRestarted {
		t.Fatalf("first apply: %v %v", how, err)
	}
	waitFor(t, "xray to start", sup.Running)
	cliCalls(t, dir)

	b := relayCfgJSON(t, relayUsers(1, 2, 4), relayOpts(1, 4))
	if how, err := sup.ApplyRawLive(addr, b); err != nil || how != RawLive {
		t.Fatalf("users and relay users: %v %v, want live", how, err)
	}
	calls := cliCalls(t, dir)
	if len(calls) != 3 || calls[0] != "api adu" || calls[1] != "api adrules -append" {
		t.Fatalf("cli calls %q: want adu, then the rules replaced", calls)
	}
	sent := rulesSent(t, dir, 0)
	i := slices.IndexFunc(sent, func(r map[string]any) bool { return r["outboundTag"] == "ext-5" })
	if i < 0 || !slices.Equal(anyStrings(sent[i]["user"]), []string{"u1", "u4"}) {
		t.Fatalf("relay rule sent %v, want u1 and u4", sent)
	}
	if n := countLines(t, filepath.Join(dir, "starts.log"), "start"); n != 1 {
		t.Errorf("xray was started %d times, want once", n)
	}

	// The routing API refuses: the pushed config is applied by a restart instead.
	if err := writeFlag(dir, "refuse-adrules"); err != nil {
		t.Fatal(err)
	}
	c := relayCfgJSON(t, relayUsers(1, 2, 4), relayOpts(1))
	if how, err := sup.ApplyRawLive(addr, c); err != nil || how != RawRestarted {
		t.Fatalf("refused rules: %v %v, want the restart fallback", how, err)
	}
}

// The master's user sync hands the supervisor the config it generated: the relay users
// go in live, an unchanged routing costs nothing, and a routing that moved otherwise is
// refused for the full reload to handle.
func TestSyncRelayRules(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	const addr = "127.0.0.1:20085"
	gen := func(set *model.Settings, users []model.User, opts Options) *Config {
		cfg, err := Generate(set, users, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if err := sup.Apply(gen(relaySettings(), relayUsers(1, 2), relayOpts(1))); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "xray to start", sup.Running)
	cliCalls(t, dir)

	if err := sup.SyncRelayRules(addr, gen(relaySettings(), relayUsers(1, 2), relayOpts(1))); err != nil {
		t.Fatalf("unchanged: %v", err)
	}
	if calls := cliCalls(t, dir); len(calls) != 0 {
		t.Fatalf("an unchanged routing made calls %q", calls)
	}
	if err := sup.SyncRelayRules(addr, gen(relaySettings(), relayUsers(1, 2, 4), relayOpts(1, 4))); err != nil {
		t.Fatalf("relay users: %v", err)
	}
	if calls := cliCalls(t, dir); len(calls) != 2 || calls[0] != "api adrules -append" {
		t.Fatalf("cli calls %q: want the rules replaced", calls)
	}
	// Again with the same users: the replaced rules are what runs now.
	if err := sup.SyncRelayRules(addr, gen(relaySettings(), relayUsers(1, 2, 4), relayOpts(1, 4))); err != nil {
		t.Fatalf("same again: %v", err)
	}
	if calls := cliCalls(t, dir); len(calls) != 0 {
		t.Fatalf("rules already in place were replaced again: %q", calls)
	}
	set := relaySettings()
	set.Routing.DirectDomains = []string{"domain:su"}
	if err := sup.SyncRelayRules(addr, gen(set, relayUsers(1, 2, 4), relayOpts(1, 4))); err == nil {
		t.Fatal("a routing change beyond relay users was taken live")
	}

	// A panel that relays nothing: nothing to compare, nothing called.
	dir2 := t.TempDir()
	sup2 := NewSupervisor(fakeLiveXray(t, dir2), filepath.Join(dir2, "config.json"), dir2)
	t.Cleanup(sup2.Stop)
	plain := Options{PanelDest: "127.0.0.1:8080"}
	if err := sup2.Apply(gen(relaySettings(), relayUsers(1), plain)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "xray to start", sup2.Running)
	cliCalls(t, dir2)
	if err := sup2.SyncRelayRules(addr, gen(relaySettings(), relayUsers(1, 2), plain)); err != nil {
		t.Fatalf("no relays: %v", err)
	}
	if calls := cliCalls(t, dir2); len(calls) != 0 {
		t.Fatalf("no relays made calls %q", calls)
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func writeFlag(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), nil, 0o600)
}

// A deliberate restart of a running process asks first — the counters go with it — and
// a first start, with nothing running, does not.
func TestBeforeRestartHook(t *testing.T) {
	dir := t.TempDir()
	sup := NewSupervisor(fakeLiveXray(t, dir), filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	calls := 0
	sup.SetOnBeforeRestart(func() { calls++ })
	gen := func(domain string) *Config {
		set := relaySettings()
		set.Routing.DirectDomains = []string{domain}
		cfg, err := Generate(set, relayUsers(1), Options{PanelDest: "127.0.0.1:8080"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if err := sup.Apply(gen("domain:ru")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "xray to start", sup.Running)
	if calls != 0 {
		t.Fatalf("a first start asked %d times", calls)
	}
	if err := sup.Apply(gen("domain:su")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the restart", func() bool { return countLines(t, filepath.Join(dir, "starts.log"), "start") == 2 })
	if calls != 1 {
		t.Fatalf("a restart asked %d times, want once", calls)
	}
}
