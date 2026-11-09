package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// The Connections editor switches TrustTunnel and names it like any lane: the switch
// decides whether trusttunnel-in is generated, the name is the link's label, and a name
// another lane already shows is refused.
func TestTrustTunnelSwitchAndName(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	row := func() *ConnInfo {
		set, err := m.store.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range buildConnectionsStatus(set).Protocols {
			if p.Key == "trusttunnel" {
				return &buildConnectionsStatus(set).Protocols[i]
			}
		}
		t.Fatal("no trusttunnel row in the editor")
		return nil
	}
	hasInbound := func() bool {
		set, err := m.store.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		set.CertPath, set.KeyPath = "/c.pem", "/k.pem" // only generated, never loaded
		opts, err := m.genOptsFor(model.LocalNodeID)
		if err != nil {
			t.Fatal(err)
		}
		opts.PanelDest = "127.0.0.1:8080"
		cfg, err := xray.Generate(set, []model.User{{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}}, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		clients, addr := xray.TrustTunnelClients(cfg)
		return addr != "" && len(clients) == 1 && clients[0].Email == "u7"
	}

	u := DefaultConnections()
	u.Protocols["reality"] = false // the live donor probe stays out of the test
	u.Protocols["trusttunnel"] = false
	if err := m.ApplyConnections(u); err != nil {
		t.Fatal(err)
	}
	if row().Enabled || hasInbound() {
		t.Fatal("switched off, the lane is still on")
	}

	u.Protocols["trusttunnel"] = true
	u.Names = map[string]string{"trusttunnel": "TT Netherlands"}
	if err := m.ApplyConnections(u); err != nil {
		t.Fatal(err)
	}
	if r := row(); !r.Enabled || r.DisplayName != "TT Netherlands" || !hasInbound() {
		t.Fatalf("switched on and named: %+v, inbound %v", r, hasInbound())
	}

	u.Names = map[string]string{"trusttunnel": model.ProtoNaive}
	if err := m.ApplyConnections(u); err == nil {
		t.Fatal("a name NaiveProxy already shows was accepted")
	}
}
