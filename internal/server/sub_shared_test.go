package server

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// What every subscription shares is read once for a few seconds — and read afresh the
// moment the panel or the API changes something, or once the few seconds are up.
func TestSubscriptionInputsAreSharedUntilAChange(t *testing.T) {
	rt, st := rolesTestRouter(t)
	inbounds := func() int {
		t.Helper()
		return len(rt.sharedSubInputs().inbounds[model.LocalNodeID])
	}
	if n := inbounds(); n != 0 {
		t.Fatalf("a fresh panel has %d custom inbounds", n)
	}
	// Written straight to the store: no request counted it, so the shared read stands.
	if _, err := st.CreateInbound(model.Inbound{ServerID: model.LocalNodeID, Enabled: true, Name: "A", Protocol: "vless", Port: 10001}); err != nil {
		t.Fatal(err)
	}
	if n := inbounds(); n != 0 {
		t.Errorf("the shared read was not what answered: %d inbounds", n)
	}
	// A request that may have changed something: the next read is fresh.
	rt.writes.Add(1)
	if n := inbounds(); n != 1 {
		t.Errorf("a change through the panel did not reach the next subscription: %d inbounds", n)
	}
	// And a read past its age is fresh with no request at all.
	if _, err := st.CreateInbound(model.Inbound{ServerID: model.LocalNodeID, Enabled: true, Name: "B", Protocol: "vless", Port: 10002}); err != nil {
		t.Fatal(err)
	}
	rt.subShared.mu.Lock()
	rt.subShared.at = time.Now().Add(-subSharedTTL)
	rt.subShared.mu.Unlock()
	if n := inbounds(); n != 2 {
		t.Errorf("a read as old as its age was served: %d inbounds", n)
	}
}

// A subscription changes what it is handed on the way to the client, so each gets its
// own copy of the shared read.
func TestSubscriptionInputsAreCopiesPerRequest(t *testing.T) {
	rt, st := rolesTestRouter(t)
	if _, err := st.CreateInbound(model.Inbound{ServerID: model.LocalNodeID, Enabled: true, Name: "A", Protocol: "vless", Port: 10001,
		Opts: model.InboundOpts{HeaderHosts: []string{"h.example"}}}); err != nil {
		t.Fatal(err)
	}
	// A node that has reported in, so the node list is not empty.
	n, err := st.CreateNode("nl", "nl.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{LastSeen: time.Now().Unix(), NodeVersion: "3.3.0", CertSHA256: strings.Repeat("ab", 32)}); err != nil {
		t.Fatal(err)
	}
	first := rt.sharedSubInputs()  // read from the store
	second := rt.sharedSubInputs() // served from the kept read
	if len(first.nodes) != 1 || len(first.inbounds[model.LocalNodeID]) != 1 {
		t.Fatalf("the fixture is not there to change: %d nodes, inbounds %+v", len(first.nodes), first.inbounds)
	}
	for _, got := range []subSharedRead{first, second} {
		got.inbounds[model.LocalNodeID][0].Name = "changed by a request"
		got.inbounds[model.LocalNodeID][0].Opts.HeaderHosts[0] = "changed.example"
		got.inbounds[model.LocalNodeID] = append(got.inbounds[model.LocalNodeID], model.Inbound{Name: "appended"})
		got.inbounds[99] = []model.Inbound{{Name: "added"}}
		got.nodes[0].NodeLabel = "changed by a request"
		got.nodes[0].Routing.BlockDomains = append(got.nodes[0].Routing.BlockDomains, "appended.example")
	}
	third := rt.sharedSubInputs()
	if list := third.inbounds[model.LocalNodeID]; len(list) != 1 || list[0].Name != "A" || len(third.inbounds) != 1 ||
		!slices.Equal(list[0].Opts.HeaderHosts, []string{"h.example"}) {
		t.Errorf("a request's changes reached the next one: %+v", third.inbounds)
	}
	if third.nodes[0].NodeLabel != "nl" || len(third.nodes[0].Routing.BlockDomains) != 0 {
		t.Errorf("a request's changes to a node reached the next one: label %q, block %v",
			third.nodes[0].NodeLabel, third.nodes[0].Routing.BlockDomains)
	}
}

// A read that fails in part serves its own request degraded, as each read always did,
// and is not kept: the next request tries again rather than living with the gap.
func TestAFailedSubscriptionReadIsNotKept(t *testing.T) {
	rt, st := rolesTestRouter(t)
	if _, err := st.CreateInbound(model.Inbound{ServerID: model.LocalNodeID, Enabled: true, Name: "A", Protocol: "vless", Port: 10001}); err != nil {
		t.Fatal(err)
	}
	// Break one of the three reads only: the table it reads goes, through a second
	// connection to the same file.
	raw, err := sql.Open("sqlite", filepath.Join(rt.dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DROP TABLE ext_servers`); err != nil {
		t.Fatal(err)
	}
	got := rt.sharedSubInputs()
	if got.ext != nil {
		t.Errorf("external servers from a table that is gone: %v", got.ext)
	}
	if len(got.inbounds[model.LocalNodeID]) != 1 {
		t.Errorf("one failed part took the others with it: %+v", got.inbounds)
	}
	rt.subShared.mu.Lock()
	kept := rt.subShared.read
	rt.subShared.mu.Unlock()
	if kept != nil {
		t.Error("a read that failed in part was kept for the next requests")
	}
}
