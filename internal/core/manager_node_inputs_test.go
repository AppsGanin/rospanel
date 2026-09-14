package core

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// Every node's desired state is built from one read of the fleet-wide inputs, until a
// wake says something changed or the read ages out — and a state built after either
// carries the change.
func TestNodeInputsAreSharedUntilAWake(t *testing.T) {
	m := nodeTestManager(t)
	yes := true
	mkNode := func(name, host string) {
		n, err := m.store.CreateNode(name, host, "")
		if err != nil {
			t.Fatal(err)
		}
		n.VLESSEnabled = &yes
	}
	mkNode("a", "a.example.com")
	mkNode("b", "b.example.com")

	first, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.nodeInputs(); again != first {
		t.Fatal("a second node re-read the inputs with nothing changed")
	}

	if _, err := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	woken, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	if woken == first || len(woken.users) != 1 {
		t.Fatalf("after a wake the inputs still hold %d users (same snapshot: %v)", len(woken.users), woken == first)
	}

	// One node's wake counts too: its state must not be built from a read before it.
	n, _ := m.store.GetNode(1)
	if n == nil {
		t.Fatal("node 1 missing")
	}
	m.nodes.wakeOne(n.ID)
	if one, _ := m.nodeInputs(); one == woken {
		t.Fatal("inputs read before a single node's wake were reused")
	}

	// What changes with no wake is picked up once the read ages out.
	cached, _ := m.nodeInputs()
	if _, err := m.store.CreateUser("u2", "uuid-u2", "pw", "tok-u2", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.nodeInputsMu.Lock()
	m.nodeInputsCache.at = time.Now().Add(-nodeInputsTTL - time.Second)
	m.nodeInputsMu.Unlock()
	aged, _ := m.nodeInputs()
	if aged == cached || len(aged.users) != 2 {
		t.Fatalf("aged-out inputs were reused: %d users", len(aged.users))
	}
}

// Two servers minting a tunnel key for the same new user at once end up with the same
// key: the first stored wins and the second takes it.
func TestUserWGKeyFirstMintWins(t *testing.T) {
	m := nodeTestManager(t)
	u, err := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b := *u, *u // two builds holding the same snapshot, both without a key
	ka, err := m.userWGKey(&a)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := m.userWGKey(&b)
	if err != nil {
		t.Fatal(err)
	}
	if ka == "" || ka != kb {
		t.Fatalf("two mints for one user gave different keys")
	}
	stored, _ := m.store.GetUser(u.ID)
	if stored.WGPrivateKey != ka {
		t.Fatal("the key handed out is not the one stored")
	}
}

// Nodes building their tunnels at the same time from the one shared snapshot mint keys
// for users who have none. Each works on its own copy of the users (the race detector
// holds that), and both hand out the same key per user.
func TestConcurrentAWGNodesShareUserKeys(t *testing.T) {
	m := nodeTestManager(t)
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("u%d", i)
		if _, err := m.store.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	var nodes []*model.Node
	for _, host := range []string{"a.example.com", "b.example.com"} {
		n, err := m.store.CreateNode(host, host, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := m.ensureNodeAWGIdentity(n, false); err != nil {
			t.Fatal(err)
		}
		if err := m.store.SetNodeAWGEnabled(n.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := m.store.SetNodeConnections(n.ID, &model.NodeConnections{AWGPort: 41000}); err != nil {
			t.Fatal(err)
		}
		n, _ = m.store.GetNode(n.ID)
		n.NodeVersion = "3.0.0"
		nodes = append(nodes, n)
	}

	states := make([]*nodeapi.NodeState, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := m.NodeDesiredState(n)
			if err != nil {
				t.Error(err)
				return
			}
			states[i] = st
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	peers := func(st *nodeapi.NodeState) map[string]string {
		out := map[string]string{}
		if st.Meta.AWG != nil {
			for _, p := range st.Meta.AWG.Peers {
				out[p.Email] = p.PublicKey
			}
		}
		return out
	}
	a, b := peers(states[0]), peers(states[1])
	if len(a) != 20 || !reflect.DeepEqual(a, b) {
		t.Fatalf("the two nodes disagree on user keys: %d vs %d peers", len(a), len(b))
	}
}

// Node traffic reports no longer run an enforcement pass each: the first schedules
// one, the ones that land before it runs share it, and a report during or after the
// pass schedules the next.
func TestNodeTrafficSharesOneEnforcementPass(t *testing.T) {
	saved := trafficEnforceDelay
	trafficEnforceDelay = 150 * time.Millisecond
	t.Cleanup(func() { trafficEnforceDelay = saved })

	m := nodeTestManager(t)
	m.reconcileCh = make(chan struct{}, 1)
	// Cleanups run last-registered first: the scheduled pass finishes before the store
	// closes and before the delay is put back.
	t.Cleanup(m.wg.Wait)
	u, err := m.store.CreateUser("capped", "uuid-c", "pw", "tok-c", 1000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	n, err := m.store.CreateNode("n1", "nl1.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	working, _ := m.store.WorkingCredentials(time.Now().Unix())
	m.setApplied(working)

	for i := int64(1); i <= 3; i++ {
		req := nodeapi.SyncRequest{ReportID: i, Traffic: []nodeapi.TrafficDelta{{UserID: u.ID, Up: 400, Down: 0}}}
		if _, err := m.IngestNodeSync(n, req); err != nil {
			t.Fatal(err)
		}
	}
	if !m.enforcePending.Load() {
		t.Fatal("a node report scheduled no enforcement pass")
	}
	select {
	case <-m.reconcileCh:
		t.Fatal("the pass ran inside the report instead of being shared")
	default:
	}
	// The user is over quota after the third report; the shared pass finds it.
	select {
	case <-m.reconcileCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduled pass never ran")
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.enforcePending.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the pass ran but a later report would still be folded into it")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A report after the pass gets a pass of its own. The applied set was never updated
	// here, so that pass asks for a user sync again.
	if _, err := m.IngestNodeSync(n, nodeapi.SyncRequest{ReportID: 4, Traffic: []nodeapi.TrafficDelta{{UserID: u.ID, Up: 1}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.reconcileCh:
	case <-time.After(5 * time.Second):
		t.Fatal("a report after the pass did not get one of its own")
	}
}
