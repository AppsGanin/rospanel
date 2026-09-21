package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The reconcile loop wakes the nodes only when what they are served has moved, so
// fleetChanged has to see every part of it move: who is in the config, what each of
// them may reach, how fast they may go, and which addresses are refused. And it has to
// stay quiet for an edit nodes cannot see — which is most of them.
func TestFleetChangedSeesEveryInputTheNodesAreServed(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	u, err := m.store.CreateUser("a", "uuid-a", "pw-a", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !fleetChangedNow(t, m) {
		t.Fatal("the first read has nothing to compare against and must count as a change")
	}
	if fleetChangedNow(t, m) {
		t.Fatal("a second read of the same inputs reported a change")
	}
	for _, tc := range []struct {
		name string
		do   func()
	}{
		{"a user is added", func() {
			if _, err := m.store.CreateUser("b", "uuid-b", "pw-b", "tok-b", 0, 0, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"a user's limits cut them off", func() {
			if err := m.store.SetUserEnabled(u.ID, false); err != nil {
				t.Fatal(err)
			}
		}},
		{"and let them back in", func() {
			if err := m.store.SetUserEnabled(u.ID, true); err != nil {
				t.Fatal(err)
			}
		}},
		{"a speed cap is set", func() {
			if err := m.store.SetUserSpeedLimit(u.ID, 4000); err != nil {
				t.Fatal(err)
			}
		}},
		{"a user's access is restricted", func() {
			g, err := m.store.CreateGroup("restricted", []string{"vless"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.store.SetUserGroups(u.ID, []int64{g.ID}); err != nil {
				t.Fatal(err)
			}
		}},
		{"an address is refused", func() {
			if err := m.store.BlockIP(model.BlockedIP{IP: "198.51.100.7", Reason: "test", At: time.Now().Unix(), Until: time.Now().Add(time.Hour).Unix()}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.do()
			if !fleetChangedNow(t, m) {
				t.Error("the nodes were not woken for a change they are served")
			}
			if fleetChangedNow(t, m) {
				t.Error("the same inputs read again reported a change")
			}
		})
	}

	// And what the nodes are not served: an operator's edit of a card.
	for _, tc := range []struct {
		name string
		do   func()
	}{
		{"a note", func() {
			if err := m.store.SetUserNote(u.ID, "called about billing"); err != nil {
				t.Fatal(err)
			}
		}},
		{"a data limit nobody has reached", func() {
			if err := m.store.SetUserLimits(u.ID, 100<<30, 0, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"a name", func() {
			if err := m.store.SetUserName(u.ID, "a (renamed)"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.do()
			if fleetChangedNow(t, m) {
				t.Error("the nodes were woken for an edit they cannot see")
			}
		})
	}
}

// fleetChangedNow asks the way the reconcile loop does: read the working set, then ask.
func fleetChangedNow(t *testing.T, m *Manager) bool {
	t.Helper()
	ws, err := m.readWorkingSet()
	if err != nil {
		t.Fatal(err)
	}
	return m.fleetChanged(ws)
}

// The loop reads the working set once and both of its questions are answered from that
// read: the snapshot the nodes are given is built on the working set handed in, not on
// a second scan of every user. A cap that is only in the read handed in is the proof.
func TestTheFleetSnapshotIsBuiltOnTheReadItIsGiven(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	u, err := m.store.CreateUser("a", "uuid-a", "pw-a", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := m.readWorkingSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.ids) != 1 || ws.ids[0] != u.ID {
		t.Fatalf("working set %v, want the one user", ws.ids)
	}
	ws.caps = map[int64]int{u.ID: 777} // in no table: only a second scan would lose it
	if !m.fleetChanged(ws) {
		t.Fatal("the first read has nothing to compare against and must count as a change")
	}
	in, err := m.nodeInputs() // the snapshot just left behind, same generation and fresh
	if err != nil {
		t.Fatal(err)
	}
	if got := in.speed[model.UserEmail(u.ID)]; got != 777 {
		t.Errorf("the nodes were given cap %d, want the 777 of the read handed in", got)
	}
	if in.gen != ws.gen || !in.at.Equal(ws.at) {
		t.Errorf("snapshot stamped gen %d at %v, want the read's gen %d at %v", in.gen, in.at, ws.gen, ws.at)
	}
}

// A working set is stamped with the wake generation from before it was read, so a wake
// that lands while it is being read leaves what is built on it one generation behind —
// and the nodes' next ask reads again instead of trusting it.
func TestAWakeDuringTheReadIsNotHiddenByIt(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	if _, err := m.store.CreateUser("a", "uuid-a", "pw-a", "tok-a", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	ws, err := m.readWorkingSet()
	if err != nil {
		t.Fatal(err)
	}
	m.notifyNodes() // a wake after the generation was taken
	m.fleetChanged(ws)
	if _, err := m.store.CreateUser("b", "uuid-b", "pw-b", "tok-b", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	in, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(in.users) != 2 {
		t.Errorf("a snapshot older than the latest wake was served: %d users, want 2", len(in.users))
	}
}

// The shared snapshot reads the working users' credentials only when the working set
// has moved. Reusing them is what makes a wake cheap, and it is only sound while
// nothing can change a credential under an id that stays — so the one thing that can,
// claiming a tunnel identity, must reach the nodes all the same.
func TestTheSharedSnapshotReadsCredentialsOnlyWhenTheSetMoves(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	for _, name := range []string{"a", "b"} {
		if _, err := m.store.CreateUser(name, "uuid-"+name, "pw-"+name, "tok-"+name, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	first, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	m.notifyNodes() // a wake: the next read cannot use the cached snapshot
	again, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.users) != 2 || &again.users[0] != &first.users[0] {
		t.Error("the credentials were read again for the same working set")
	}
	if again.version != first.version {
		t.Errorf("unchanged inputs changed version: %d then %d", first.version, again.version)
	}

	if _, err := m.store.CreateUser("c", "uuid-c", "pw-c", "tok-c", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	grown, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(grown.users) != 3 {
		t.Fatalf("the added user is missing: %d users", len(grown.users))
	}
	if grown.version == first.version {
		t.Error("a changed working set kept the version the nodes' states are keyed on")
	}
	if grown.users[2].UUID != "uuid-c" || grown.users[2].Password != "pw-c" {
		t.Errorf("the added user's credentials came back as %+v", grown.users[2])
	}

	// A tunnel identity is claimed for users already in the set: their credentials
	// changed under ids that did not, and the snapshot must not be reused past it.
	users := append([]model.User(nil), grown.users...)
	if err := m.claimAWG([]*model.User{&users[0], &users[1], &users[2]}); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	claimed, err := m.nodeInputs()
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range claimed.users {
		if u.WGPrivateKey == "" || u.AWGSlot == 0 {
			t.Errorf("user %d reached the nodes without the identity just claimed: %+v", i, u)
		}
	}
}

// The live user-sync asks whether the working set moved before it reads a single
// credential, and that question has to be the one the sync itself would answer: a set
// of the same size with a different member in it is a change.
func TestWorkingIDsChangedIsTheSyncsOwnDiff(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	m.applied = map[int64]struct{}{1: {}, 2: {}, 3: {}}
	for _, tc := range []struct {
		name string
		ids  []int64
		want bool
	}{
		{"the same set", []int64{1, 2, 3}, false},
		{"one gone", []int64{1, 2}, true},
		{"one added", []int64{1, 2, 3, 4}, true},
		{"one swapped for another", []int64{1, 2, 4}, true},
		{"none left", nil, true},
	} {
		if got := m.workingIDsChanged(tc.ids); got != tc.want {
			t.Errorf("%s: changed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A sync that dies part-way leaves the applied set unknown, so it counts as a change:
// the nodes are woken rather than left with whatever they had.
func TestSyncUsersOnceReportsAPanicAsAChange(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t) // no supervisor: the sync panics on the first call into it
	if !m.syncUsersOnce(nil, nil) {
		t.Error("a sync that panicked reported that nothing changed")
	}
}
