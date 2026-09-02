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
	m := nodeTestManager(t)
	syncedNode(t, m.store, "edge", "26.7.28")
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
	if _, err := m.CreateGroup("cdn", []string{model.InboundToken(in.ID)}); err != nil {
		t.Fatal(err)
	}

	st, err := m.ResetNodeConnections(context.Background(), id)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if st.HysteriaPort != 443 || st.HopStart != 443 || st.RealityPort != 8443 || !st.TLSMin13 {
		t.Fatalf("transport not reset: %+v", st)
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
