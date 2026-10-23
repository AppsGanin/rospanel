package core

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A subscription is relayed only through a relay lane that runs on a server that
// exists; once it is, the servers are that server's to carry, and its config has them.
func TestExtRelayReachesTheServersConfig(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	n := servingNode(t, m, "n1", "nl1.example.com") // TCP-TLS on, REALITY off
	id, err := m.store.CreateExtSubscription("partner", "vless://x@1.2.3.4:443#a", model.ExtIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	link := "vless://0cef6f84-5100-4499-892b-cc5b3107d1e2@9.9.9.9:443?security=tls&sni=p.example&type=tcp#Partner"
	if _, _, _, err := m.store.ReplaceExtServers(id, []model.ExtServer{{SubID: id, Key: "k", Name: "Partner", Protocol: "vless", Host: "9.9.9.9", Port: 443, Link: link}}, 1); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		lane   string
		server int64
	}{{"hysteria", n.ID}, {model.LaneReality, n.ID}} {
		if _, err := m.SetExtSubscriptionRelays(id, []model.ExtRelayVia{{ServerID: c.server, Lane: c.lane}}, nil); err == nil {
			t.Errorf("relay through %s on %d accepted", c.lane, c.server)
		}
	}
	// A server that is gone is dropped from the relays, not an error that blocks the save.
	if sub, err := m.SetExtSubscriptionRelays(id, []model.ExtRelayVia{{ServerID: 999999, Lane: model.LaneVLESS}}, nil); err != nil || len(sub.Relays) != 0 {
		t.Fatalf("relay through a missing server: %+v %v", sub, err)
	}
	if _, err := m.SetExtSubscriptionRelays(id, []model.ExtRelayVia{{ServerID: n.ID, Lane: model.LaneVLESS}}, nil); err != nil {
		t.Fatalf("relay through the node's TCP-TLS: %v", err)
	}
	if got, _ := m.relaysFor(n.ID); len(got) != 1 || !slices.Equal(got[0].Lanes, []string{model.LaneVLESS}) || got[0].Link != link {
		t.Fatalf("the node carries %+v", got)
	}
	if got, _ := m.relaysFor(model.LocalNodeID); len(got) != 0 {
		t.Fatalf("the master carries %+v", got)
	}
	servers, _ := m.store.ExtServers()
	extID := servers[0].ID
	state, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(state.XrayConfig)
	for _, want := range []string{`"ext-` + strconv.FormatInt(extID, 10) + `"`, `"relay-` + strconv.FormatInt(extID, 10) + `"`, `"vlessRoute"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("node config lacks %s", want)
		}
	}
	// Handed out directly again: gone from the node.
	if _, err := m.SetExtSubscriptionRelays(id, nil, nil); err != nil {
		t.Fatal(err)
	}
	state, err = m.NodeDesiredState(n)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state.XrayConfig), `"relay-`) {
		t.Error("the relay stayed in the node's config")
	}
}

// Relayed through two of our servers, each carries the servers; the routing may name
// only a lane every one of them has, and Opera is refused for a partner over UDP.
func TestExtRelayThroughSeveralServers(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	n := servingNode(t, m, "n1", "nl1.example.com")
	id, err := m.store.CreateExtSubscription("partner", "vless://x@1.2.3.4:443#a", model.ExtIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.store.ReplaceExtServers(id, []model.ExtServer{
		{SubID: id, Key: "k", Name: "P", Protocol: "vless", Host: "9.9.9.9", Port: 443,
			Link: "vless://0cef6f84-5100-4499-892b-cc5b3107d1e2@9.9.9.9:443?security=tls&type=tcp#P"},
		{SubID: id, Key: "h", Name: "H", Protocol: "hysteria2", Host: "9.9.9.8", Port: 443, Link: "hysteria2://pw@9.9.9.8:443#H"},
	}, 1); err != nil {
		t.Fatal(err)
	}
	sub, err := m.SetExtSubscriptionRelays(id, []model.ExtRelayVia{
		{ServerID: n.ID, Lane: model.LaneVLESS}, {ServerID: model.LocalNodeID, Lane: model.LaneVLESS}, {ServerID: n.ID, Lane: model.LaneVLESS},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Relays) != 2 || sub.Relays[0].ServerID != model.LocalNodeID {
		t.Fatalf("relays %+v, want the two, sorted, the duplicate dropped", sub.Relays)
	}
	for _, server := range []int64{model.LocalNodeID, n.ID} {
		if got, _ := m.relaysFor(server); len(got) != 2 {
			t.Errorf("server %d carries %+v", server, got)
		}
	}

	// A lane only the master has is not on every relay server.
	set, _ := m.store.GetSettings()
	set.Routing.Lanes = []model.EgressLane{{ID: "nl", Name: "NL", Enabled: true}}
	if err := m.store.SetRoutingConfig(set.Routing); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetExtSubscriptionRouting(id, model.ExtRouting{Rest: "nl"}); validationCode(err) != "err.extRouteTarget" {
		t.Fatalf("a lane the node lacks: %v", err)
	}
	if _, err := m.SetExtSubscriptionRouting(id, model.ExtRouting{Dial: "opera"}); validationCode(err) != "err.extDialOperaUDP" {
		t.Fatalf("Opera for a Hysteria2 partner: %v", err)
	}
	if _, err := m.SetExtSubscriptionRouting(id, model.ExtRouting{Rules: []model.ExtRule{{IPs: []string{"not-an-ip"}, Target: "direct"}}}); validationCode(err) != "err.extRuleEntry" {
		t.Fatalf("a bad IP entry: %v", err)
	}
	got, err := m.SetExtSubscriptionRouting(id, model.ExtRouting{
		Rules: []model.ExtRule{{Domains: []string{" geosite:category-ru ", ""}, IPs: []string{"geoip:ru"}, Target: "direct"}, {}},
		Rest:  "partner", Dial: "warp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Routing.Rules) != 1 || got.Routing.Rules[0].Domains[0] != "geosite:category-ru" || got.Routing.Rest != "" || got.Routing.Dial != "warp" {
		t.Fatalf("stored routing %+v", got.Routing)
	}
	if r, _ := m.relaysFor(n.ID); r[0].Routing.Dial != "warp" {
		t.Fatalf("the node's relays lack the routing: %+v", r)
	}
}

// Deleting a node takes the relays through it along: none is left to reach nobody.
func TestDeleteNodeDropsItsRelays(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	n := servingNode(t, m, "n1", "nl1.example.com")
	id, err := m.store.CreateExtSubscription("partner", "vless://x@1.2.3.4:443#a", model.ExtIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetExtSubscriptionRelays(id, []model.ExtRelayVia{{ServerID: n.ID, Lane: model.LaneVLESS}, {ServerID: model.LocalNodeID, Lane: model.LaneVLESS}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	sub, _ := m.store.ExtSubscription(id)
	if len(sub.Relays) != 1 || sub.Relays[0].ServerID != model.LocalNodeID {
		t.Fatalf("relays after the node went: %+v", sub.Relays)
	}
}
