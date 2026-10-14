package xray

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestComplementCIDRsLeavesOutOnlyTheEntry(t *testing.T) {
	entry := []netip.Addr{netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::5")}
	got := complementCIDRs(entry)
	var prefixes []netip.Prefix
	for _, s := range got {
		prefixes = append(prefixes, netip.MustParsePrefix(s))
	}
	covers := func(a netip.Addr) int {
		n := 0
		for _, p := range prefixes {
			if p.Contains(a) {
				n++
			}
		}
		return n
	}
	for _, a := range entry {
		if covers(a) != 0 {
			t.Fatalf("the entry %s is in its own complement", a)
		}
	}
	for _, s := range []string{"203.0.113.6", "203.0.113.8", "1.1.1.1", "255.255.255.255", "0.0.0.0", "2001:db8::4", "2001:db8::6", "::1"} {
		if n := covers(netip.MustParseAddr(s)); n != 1 {
			t.Errorf("%s is covered %d times, want once", s, n)
		}
	}
	if len(got) != 32+128 {
		t.Errorf("%d prefixes, want 32 + 128", len(got))
	}
	// No IPv6 entry address: the whole family is someone else's.
	if v4 := complementCIDRs(entry[:1]); !slices.Contains(v4, "::/0") {
		t.Errorf("an IPv4-only entry left IPv6 open: %v", v4[len(v4)-3:])
	}
}

// The exit takes the entry on its bridge lane and drops that credential from anywhere
// else; the entry leaves through the exit, never directly.
func TestGenerateCascadeEnds(t *testing.T) {
	set := baseSettings()
	set.RealityEnabled, set.RealityPrivateKey = true, "priv"
	bridge := Bridge{Email: model.BridgeEmail(5), UUID: "11111111-2222-4333-8444-555555555555", Lane: model.BridgeReality, Sources: []string{"203.0.113.7"}}
	exit, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080", Bridges: []Bridge{bridge}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := findInbound(exit, TagReality)
	clients := in.Settings.(VLESSInboundSettings).Clients
	if len(clients) != 1 || clients[0].Email != bridge.Email || clients[0].ID != bridge.UUID {
		t.Fatalf("reality clients = %+v", clients)
	}
	var src *RouteRule
	for i, r := range exit.Routing.Rules {
		if slices.Equal(r.User, []string{bridge.Email}) {
			src = &exit.Routing.Rules[i]
			if i > 2 {
				t.Errorf("the source rule is rule %d; it must come before every lane", i)
			}
		}
	}
	if src == nil || src.OutboundTag != "block" || len(src.Source) == 0 || slices.Contains(src.Source, "203.0.113.7/32") {
		t.Fatalf("source rule = %+v", src)
	}

	entrySet := baseSettings()
	entrySet.Routing = model.RoutingConfig{
		RoutingOrder: []string{"direct", "warp", "opera", "nl"},
		Lanes:        []model.EgressLane{{ID: "nl", Name: "NL", Enabled: true, Via: []model.CascadeExit{{Server: 0}}}},
	}
	cascade := map[string][]model.ProxyEndpoint{"nl": {{Protocol: "vless", Address: "nl.example.com", Port: 8443,
		Link: "vless://11111111-2222-4333-8444-555555555555@nl.example.com:8443?encryption=none&security=reality&type=xhttp&path=%2Fx&pbk=pub&sid=aa&sni=max.ru&fp=chrome"}}}
	entry, err := Generate(entrySet, nil, Options{PanelDest: "127.0.0.1:8080", Cascade: cascade}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var bal *Balancer
	for i := range entry.Routing.Balancers {
		if entry.Routing.Balancers[i].Tag == laneBalancerTag("nl") {
			bal = &entry.Routing.Balancers[i]
		}
	}
	if bal == nil || bal.FallbackTag != laneTagPrefix("nl")+"0" {
		t.Fatalf("cascade balancer = %+v, want one falling back to its own first exit, never direct", bal)
	}
	catchAll := -1
	for i, r := range entry.Routing.Rules {
		if r.Network == "tcp,udp" && r.BalancerTag == laneBalancerTag("nl") {
			catchAll = i
		}
	}
	if catchAll < 0 {
		t.Fatal("no catch-all rule through the cascade")
	}
	b, _ := json.Marshal(entry)
	if !json.Valid(b) || !slices.ContainsFunc(entry.Outbounds, func(o Outbound) bool { return o.Tag == laneTagPrefix("nl")+"1" || o.Tag == laneTagPrefix("nl")+"0" }) {
		t.Errorf("no outbound to the exit: %v", entry.Outbounds)
	}
}

// An exit node with bridges still gets its users as a delta: the bridge entries ride
// in the skeleton's slots and come back after the users, where Generate wrote them.
func TestSplitKeepsBridgeEntries(t *testing.T) {
	set, custom := splitFixture()
	users, access := splitUsers(7, 7)
	bridges := []Bridge{
		{Email: model.BridgeEmail(5), UUID: "11111111-2222-4333-8444-555555555555", Lane: model.BridgeReality, Sources: []string{"203.0.113.7"}},
		{Email: model.BridgeEmail(6), Password: "secret", Lane: model.BridgeHysteria, Sources: []string{"203.0.113.8"}},
	}
	cfg, err := Generate(set, users, Options{PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Custom: custom, Access: access, Bridges: bridges}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(cfg)
	sc, ok := SplitUsers(cfg, custom, users)
	if !ok {
		t.Fatal("a config with bridges could not be split")
	}
	skel, _ := json.Marshal(sc.Skeleton)
	got, err := RenderUsers(skel, sc.Slots, rowsOf(sc, users))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded(t, got), decoded(t, want)) {
		t.Errorf("the rendered config differs from the generated one")
	}
}

// Review fixes on the generator's side.
func TestBridgeGuardsInTheGenerator(t *testing.T) {
	set := baseSettings()
	set.RealityEnabled, set.RealityPrivateKey, set.HysteriaEnabled = true, "priv", false
	two := []Bridge{
		{Email: model.BridgeEmail(5), UUID: "11111111-2222-4333-8444-555555555555", Lane: model.BridgeReality, Sources: []string{"203.0.113.7"}},
		{Email: model.BridgeEmail(5), Password: "p", Lane: model.BridgeHysteria, Sources: []string{"203.0.113.7"}},
	}
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080", Bridges: two}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One source rule per entry: two under one tag break the live rule replacement.
	tags := map[string]int{}
	for _, r := range cfg.Routing.Rules {
		tags[r.RuleTag]++
	}
	if tags["bridge-src-"+model.BridgeEmail(5)] != 1 {
		t.Errorf("source rules for one entry: %d", tags["bridge-src-"+model.BridgeEmail(5)])
	}
	// Hysteria2 is off on this exit: the bridge is not let in through it.
	if hy := findInbound(cfg, TagHysteria); hy != nil {
		if b, _ := json.Marshal(hy.Settings); strings.Contains(string(b), model.BridgeEmail(5)) {
			t.Error("a bridge in a lane that is off")
		}
	}

	// A lane that became a cascade carries nothing of the pool it had before.
	lanes := []model.EgressLane{{ID: "nl", Enabled: true, Via: []model.CascadeExit{{Server: 0}}}}
	stale := map[string][]model.ProxyEndpoint{"nl": {{Protocol: "socks", Address: "198.51.100.1", Port: 1080}}}
	if got := withCascade(stale, nil, lanes); len(got["nl"]) != 0 {
		t.Errorf("a cascade with no exit kept a stale proxy: %v", got["nl"])
	}
}
