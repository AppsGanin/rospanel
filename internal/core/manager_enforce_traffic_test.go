package core

import (
	"testing"
	"time"
)

// TestEnforceAfterTrafficSyncsWhenTheWorkingSetMoves: after traffic lands, a user who
// crossed their quota must leave the proxy config, so the pass asks for a user sync —
// and a pass in which nobody crossed anything asks for none, or every stats poll and
// node report would reload the config for nothing.
func TestEnforceAfterTrafficSyncsWhenTheWorkingSetMoves(t *testing.T) {
	m := bulkTestManager(t)
	m.reconcileCh = make(chan struct{}, 1)
	mkUser(t, m, "steady", 0)
	capped, err := m.store.CreateUser("capped", "uuid-capped", "pw", "tok-capped", 1000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	working, err := m.store.WorkingCredentials(time.Now().Unix())
	if err != nil || len(working) != 2 {
		t.Fatalf("working set before: %d users (err %v)", len(working), err)
	}
	m.setApplied(working)

	signalled := func() bool {
		select {
		case <-m.reconcileCh:
			return true
		default:
			return false
		}
	}
	if err := m.enforceAfterTraffic(nil); err != nil {
		t.Fatal(err)
	}
	if signalled() {
		t.Fatal("a pass with the working set unchanged asked for a user sync")
	}

	if err := m.store.UpdateTraffic(capped.ID, 600, 600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.enforceAfterTraffic(nil); err != nil {
		t.Fatal(err)
	}
	if !signalled() {
		t.Fatal("a user over their quota did not trigger a user sync")
	}
}
