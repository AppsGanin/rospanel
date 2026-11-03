package core

import (
	"context"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A node reset puts every knob of its connections back to the factory state and
// drops its custom inbounds with the grants that named them — through the same
// path a save takes, so it is validated like one.
func TestResetNodeConnectionsRestoresTheFactoryState(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	syncedNode(t, m.store, "edge", "26.10.10")
	nodes, err := m.store.ListNodes()
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	id := nodes[0].ID

	// Bend the node away from the defaults: a custom port set, REALITY off, a custom
	// inbound with a group grant.
	bent := DefaultConnections()
	bent.Protocols["reality"] = false
	bent.HysteriaPort, bent.HopStart, bent.HopEnd = 8443, 8443, 8443
	bent.RealityPort = 9443
	bent.TLSMin13 = false
	if err := m.ApplyNodeConnections(id, bent); err != nil {
		t.Fatalf("bend: %v", err)
	}
	in, err := m.store.CreateInbound(model.Inbound{
		ServerID: id, Name: "ws-cdn", Protocol: model.InbVLESS, Port: 2053, Enabled: true,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecNone, Path: "/cdn"},
	})
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if _, err := m.CreateGroup("cdn", []string{model.InboundToken(in.ID)}, 0); err != nil {
		t.Fatal(err)
	}

	st, err := m.ResetNodeConnections(context.Background(), id)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if st.HysteriaPort != 443 || st.HopStart != 443 || !st.TLSMin13 {
		t.Fatalf("transport not reset: %+v", st)
	}
	// The REALITY port is kept, as the keys are: every REALITY link carries it.
	if st.RealityPort != 9443 {
		t.Fatalf("REALITY port = %d, want the node's own 9443 kept", st.RealityPort)
	}
	for _, p := range st.Protocols {
		want := p.Key != "awg"
		if p.Enabled != want {
			t.Errorf("protocol %s enabled=%v, want %v", p.Key, p.Enabled, want)
		}
	}
	if left, _ := m.store.Inbounds(id); len(left) != 0 {
		t.Fatalf("custom inbounds survived the reset: %+v", left)
	}
	groups, _ := m.Groups()
	if len(groups) != 1 || len(groups[0].Grants) != 0 {
		t.Fatalf("grants not swept: %+v", groups)
	}
}

// A node that named itself before the panel moved its REALITY lane onto the TCP-TLS
// port must be able to give its own name up: an empty donor means "follow the panel's",
// and the very save that resolves the conflict must not be refused by it. Left as it
// was, the node's editor answered "your own name cannot mask on 443" to every save,
// including the one that would have cleared it.
func TestNodeCanLeaveItsNameForTheSharedPort(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	n, err := m.CreateNode("edge", "nl.example.com")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The panel's lane moves onto the TCP-TLS port; the node inherits that port.
	if err := m.store.SetRealityPorts(443, "max.ru"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetNode(n.ID); got.RealityDest != "nl.example.com" {
		t.Fatalf("node donor %q, want its own name before the save", got.RealityDest)
	}
	u := DefaultConnections()
	u.RealityPort = 443
	u.RealityDest = "" // follow the panel's donor from now on
	if err := m.ApplyNodeConnections(n.ID, u); err != nil {
		t.Fatalf("clearing the node's own donor was refused: %v", err)
	}
	if got, _ := m.store.GetNode(n.ID); got.RealityDest != "" {
		t.Fatalf("node donor %q, want it cleared", got.RealityDest)
	}
	set, _ := m.store.GetSettings()
	if got := nodeSettings(set, mustNode(t, m, n.ID)).RealitySNI(); got != "max.ru" {
		t.Fatalf("node SNI %q, want the panel's donor", got)
	}
}

func mustNode(t *testing.T, m *Manager, id int64) *model.Node {
	t.Helper()
	n, err := m.store.GetNode(id)
	if err != nil || n == nil {
		t.Fatalf("node %d: %v", id, err)
	}
	return n
}
