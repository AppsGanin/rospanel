package core

import (
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
	}{{"hysteria", n.ID}, {model.LaneReality, n.ID}, {model.LaneVLESS, 999999}} {
		if err := m.SetExtSubscriptionRelay(id, c.lane, c.server); err == nil {
			t.Errorf("relay through %s on %d accepted", c.lane, c.server)
		}
	}
	if err := m.SetExtSubscriptionRelay(id, model.LaneVLESS, n.ID); err != nil {
		t.Fatalf("relay through the node's TCP-TLS: %v", err)
	}
	if got, _ := m.relaysFor(n.ID); len(got) != 1 || got[0].Lane != model.LaneVLESS || got[0].Link != link {
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
	if err := m.SetExtSubscriptionRelay(id, "", 0); err != nil {
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
