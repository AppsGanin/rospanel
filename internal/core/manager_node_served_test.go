package core

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/abuse"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
)

// servedTestManager is a node-test manager that can also record connections and
// destinations, with everything a report can name buffered where a test can look.
func servedTestManager(t *testing.T) *Manager {
	t.Helper()
	m := nodeTestManager(t)
	m.accLast = map[string]int64{}
	m.accPending = map[accPendingKey]store.ConnectionHit{}
	m.abusePending = map[abusePendingKey]store.AbuseHit{}
	m.abuseAlerted = map[abuseAlertKey]struct{}{}
	st := abuse.NewStore(t.TempDir())
	st.Matcher().SetIP(abuse.CatBadIP, []string{"203.0.113.0/24"})
	m.abuse = st
	return m
}

// report is one sync naming a user in every way a report can: traffic, a connection
// and a blocklisted destination.
func report(id int64, reportID int64, ip string) nodeapi.SyncRequest {
	return nodeapi.SyncRequest{
		ReportID: reportID,
		Traffic:  []nodeapi.TrafficDelta{{UserID: id, Up: 100, Down: 200}},
		Conns:    []nodeapi.ConnSample{{Email: model.UserEmail(id), IP: ip}},
		Sites:    []nodeapi.SiteSample{{UserID: id, Host: "203.0.113.7", Count: 1}},
	}
}

// named reports what a node's report changed about a user: bytes counted, whether a
// connection was taken, whether a destination was.
func named(t *testing.T, m *Manager, nodeID, userID int64) (used int64, conn, site bool) {
	t.Helper()
	u, err := m.store.GetUser(userID)
	if err != nil || u == nil {
		t.Fatalf("user %d: %v", userID, err)
	}
	m.online.mu.Lock()
	_, conn = m.online.seen[nodeID][userID]
	m.online.mu.Unlock()
	m.abuseMu.Lock()
	for k := range m.abusePending {
		if k.userID == userID {
			site = true
		}
	}
	m.abuseMu.Unlock()
	return u.UsedUp + u.UsedDown, conn, site
}

// A node is believed about the users its config lets in, and about no one else: not a
// user the access groups keep off it, not a user who does not exist.
func TestNodeReportNamesOnlyItsOwnUsers(t *testing.T) {
	m := servedTestManager(t)
	a := servingNode(t, m, "a", "a.example.com")
	b := servingNode(t, m, "b", "b.example.com")
	everywhere, _ := m.store.CreateUser("everywhere", "uuid-1", "pw1", "tok-1", 0, 0, 0)
	onlyB, _ := m.store.CreateUser("only-b", "uuid-2", "pw2", "tok-2", 0, 0, 0)
	g, err := m.store.CreateGroup("b-only", []string{model.BuiltinToken(b.ID, model.LaneVLESS)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetGroupMembers(g.ID, []int64{onlyB.ID}); err != nil {
		t.Fatal(err)
	}

	for i, id := range []int64{everywhere.ID, onlyB.ID, 999} {
		if _, err := m.IngestNodeSync(a, report(id, int64(i+1), "198.51.100.1")); err != nil {
			t.Fatal(err)
		}
	}
	if used, conn, site := named(t, m, a.ID, everywhere.ID); used != 300 || !conn || !site {
		t.Errorf("node a's own user: %d bytes, connection %v, destination %v", used, conn, site)
	}
	if used, conn, site := named(t, m, a.ID, onlyB.ID); used != 0 || conn || site {
		t.Errorf("a user node a was never given: %d bytes, connection %v, destination %v", used, conn, site)
	}
	m.online.mu.Lock()
	_, ghost := m.online.seen[a.ID][999]
	m.online.mu.Unlock()
	if ghost {
		t.Error("a connection was taken for a user that does not exist")
	}
	// The same user from the node that does serve them is believed.
	if _, err := m.IngestNodeSync(b, report(onlyB.ID, 1, "198.51.100.2")); err != nil {
		t.Fatal(err)
	}
	if used, conn, site := named(t, m, b.ID, onlyB.ID); used != 300 || !conn || !site {
		t.Errorf("node b's own user: %d bytes, connection %v, destination %v", used, conn, site)
	}
}

// Someone taken off a node is believed for an hour after the state without them was
// built — what they used before the node applied it arrives later — and not after.
func TestNodeReportGraceForAUserWhoLeft(t *testing.T) {
	m := servedTestManager(t)
	n := servingNode(t, m, "n", "n.example.com")
	u, _ := m.store.CreateUser("leaving", "uuid-1", "pw1", "tok-1", 0, 0, 0)
	if _, err := m.NodeStateChange(n, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetUserEnabled(u.ID, false); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	if _, err := m.NodeStateChange(n, ""); err != nil {
		t.Fatal(err)
	}
	if s := m.served.get(n.ID); slices.Contains(s.ids, u.ID) {
		t.Fatal("the fixture did not take the user off the node")
	}
	if _, err := m.IngestNodeSync(n, report(u.ID, 1, "198.51.100.1")); err != nil {
		t.Fatal(err)
	}
	if used, conn, _ := named(t, m, n.ID, u.ID); used != 300 || !conn {
		t.Errorf("inside the grace: %d bytes, connection %v", used, conn)
	}

	// The same user, left longer ago than the grace.
	m.served.mu.Lock()
	m.served.nodes[n.ID].left[u.ID] = time.Now().Add(-nodeServedGrace - time.Second).Unix()
	m.served.mu.Unlock()
	fresh, _ := m.store.GetNode(n.ID)
	if _, err := m.IngestNodeSync(fresh, report(u.ID, 2, "198.51.100.9")); err != nil {
		t.Fatal(err)
	}
	if used, _, _ := named(t, m, n.ID, u.ID); used != 300 {
		t.Errorf("past the grace the node still spent %d bytes of the user's quota", used-300)
	}
}

// A report the panel cannot check is not taken — and its traffic is not acknowledged,
// so the node keeps it and sends it again.
func TestUncheckableNodeReportIsNotAcknowledged(t *testing.T) {
	m := servedTestManager(t)
	n := servingNode(t, m, "n", "n.example.com")
	u, _ := m.store.CreateUser("u", "uuid-1", "pw1", "tok-1", 0, 0, 0)
	// No state can be built: the database is gone.
	if err := m.store.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := m.IngestNodeSync(n, report(u.ID, 7, "198.51.100.1"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.AckReport != 0 {
		t.Errorf("a report nobody could check was acknowledged (%d): the node would drop its traffic", resp.AckReport)
	}
	m.online.mu.Lock()
	_, conn := m.online.seen[n.ID][u.ID]
	m.online.mu.Unlock()
	if conn || len(m.abusePending) != 0 {
		t.Errorf("an unchecked report was taken: connection %v, destinations %d", conn, len(m.abusePending))
	}
}

// The tag a connection is recorded under is the panel's own spelling of the user, not
// whatever spelling the node sent.
func TestNodeConnectionTagIsTheUsersOwn(t *testing.T) {
	m := servedTestManager(t)
	n := servingNode(t, m, "n", "n.example.com")
	u, _ := m.store.CreateUser("u", "uuid-1", "pw1", "tok-1", 0, 0, 0)
	req := nodeapi.SyncRequest{Conns: []nodeapi.ConnSample{
		{Email: "u00" + model.UserEmail(u.ID)[1:], IP: "198.51.100.1"},
		{Email: "u+1", IP: "198.51.100.2"},
	}}
	if _, err := m.IngestNodeSync(n, req); err != nil {
		t.Fatal(err)
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	if _, ok := m.accLast[model.UserEmail(u.ID)+"|198.51.100.1"]; !ok {
		t.Errorf("the connection was not recorded under %s: %v", model.UserEmail(u.ID), m.accLast)
	}
	if len(m.accLast) != 1 {
		t.Errorf("a malformed tag was taken: %v", m.accLast)
	}
}

// Who left, and when: a user is remembered from the state they left, kept for the
// grace, and forgotten the moment they are back. A state whose users could not be read
// believes everyone and says nothing about who left.
func TestServedRegistryRemembersWhoLeft(t *testing.T) {
	var r servedRegistry
	grace := int64(nodeServedGrace / time.Second)
	r.note(1, []int64{1, 2, 3}, false, 1000, 1)
	r.note(1, []int64{2, 3, 4}, false, 2000, 2)
	s := r.get(1)
	for _, c := range []struct {
		id   int64
		at   int64
		want bool
	}{
		{4, 2000, true}, {2, 2000, true},
		{1, 2000 + grace, true}, {1, 2001 + grace, false},
		{5, 2000, false},
	} {
		if got := s.allows(c.id, c.at); got != c.want {
			t.Errorf("user %d at %d: %v, want %v", c.id, c.at, got, c.want)
		}
	}
	// Back again: no longer "left", so a later departure starts its own grace.
	r.note(1, []int64{1, 2, 3, 4}, false, 3000, 3)
	if _, ok := r.get(1).left[1]; ok {
		t.Error("a user back on the node is still remembered as having left")
	}
	r.note(1, []int64{2, 3, 4}, false, 9000, 4)
	if !r.get(1).allows(1, 9000+grace) {
		t.Error("the second departure did not start a grace of its own")
	}
	// Departures older than the grace are dropped when the next state is noted.
	r.note(1, []int64{2, 3, 4}, false, 9001+grace, 5)
	if _, ok := r.get(1).left[1]; ok {
		t.Error("a departure past the grace was kept")
	}
	// An unreadable state believes everyone and records no departures.
	r.note(2, []int64{1}, false, 100, 6)
	r.note(2, nil, true, 200, 7)
	if !r.get(2).allows(77, 200) {
		t.Error("an unreadable state refused a user")
	}
	r.note(2, []int64{5}, false, 300, 8)
	if _, ok := r.get(2).left[1]; ok {
		t.Error("a departure was taken from an unreadable state")
	}
	// What a reader holds does not change under it.
	held := r.get(1)
	ids := slices.Clone(held.ids)
	r.note(1, []int64{9}, false, 10000+grace, 9)
	if !slices.Equal(held.ids, ids) || len(held.left) != 0 {
		t.Errorf("a held state changed: %v, left %v", held.ids, held.left)
	}
}

// States for one node are built side by side, and the one read first can finish last:
// it must not put back the users of an older read.
func TestServedRegistryKeepsTheNewestRead(t *testing.T) {
	var r servedRegistry
	older, newer := r.stamp(), r.stamp()
	r.note(1, []int64{1, 2}, false, 100, newer) // the newer read finishes first
	r.note(1, []int64{1}, false, 101, older)
	if s := r.get(1); !slices.Equal(s.ids, []int64{1, 2}) || len(s.left) != 0 {
		t.Errorf("an older read replaced a newer one: %v, left %v", s.ids, s.left)
	}
	r.note(1, []int64{2}, false, 102, newer) // the same read again is taken
	if s := r.get(1); !slices.Equal(s.ids, []int64{2}) || s.left[1] != 102 {
		t.Errorf("a state from the newest read was not taken: %v, left %v", s.ids, s.left)
	}
	// And reads of a node's state inputs are stamped in the order they start.
	m := nodeTestManager(t)
	n := servingNode(t, m, "n", "n.example.com")
	a, _ := m.readNodeStateInputs(n)
	b, _ := m.readNodeStateInputs(n)
	if !(a.servedSeq < b.servedSeq) {
		t.Errorf("reads stamped %d then %d", a.servedSeq, b.servedSeq)
	}
}

// Reports from two nodes, states being built for them and the users changing, all at
// once: under -race, and with every report about the node's own user counted.
func TestNodeReportsUnderConcurrency(t *testing.T) {
	m := servedTestManager(t)
	a := servingNode(t, m, "a", "a.example.com")
	b := servingNode(t, m, "b", "b.example.com")
	u, _ := m.store.CreateUser("u", "uuid-1", "pw1", "tok-1", 0, 0, 0)
	var wg sync.WaitGroup
	for _, n := range []*model.Node{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(1); i <= 20; i++ {
				req := nodeapi.SyncRequest{ReportID: i, Traffic: []nodeapi.TrafficDelta{{UserID: u.ID, Up: 1}}}
				if _, err := m.IngestNodeSync(n, req); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 20 {
			if _, err := m.store.CreateUser("churn", "uuid-c"+string(rune('a'+i)), "pw", "tok-c"+string(rune('a'+i)), 0, 0, 0); err != nil {
				t.Error(err)
				return
			}
			m.notifyNodes()
			if _, err := m.NodeStateChange(a, ""); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	if got, _ := m.store.GetUser(u.ID); got.UsedUp != 40 {
		t.Errorf("counted %d of 40 bytes the nodes' own user used", got.UsedUp)
	}
}

// A peer of the node's AmneziaWG tunnel is one of its users, with no Xray lane at all:
// its traffic and connections come in the same reports.
func TestNodeReportBelievesTunnelPeers(t *testing.T) {
	m := nodeTestManager(t)
	m.nodeAWG = map[int64]nodeAWGState{}
	u, _ := m.store.CreateUser("u", "uuid-1", "pw1", "tok-1", 0, 0, 0)
	n, err := m.store.CreateNode("nl", "nl.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	set, _ := m.store.GetSettings()
	if err := m.ensureMasterAWGIdentity(set, false); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureNodeAWGIdentity(n, false); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeProtocols(n.ID, false, false, false); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeAWGEnabled(n.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeConnections(n.ID, &model.NodeConnections{AWGPort: 41000}); err != nil {
		t.Fatal(err)
	}
	node, _ := m.store.GetNode(n.ID)
	node.NodeVersion = "3.0.0" // reads the 3.1 parameters
	resp, err := m.IngestNodeSync(node, nodeapi.SyncRequest{
		ReportID: 1, Traffic: []nodeapi.TrafficDelta{{UserID: u.ID, Up: 5, Down: 6}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := m.served.get(n.ID); s == nil || !slices.Equal(s.ids, []int64{u.ID}) {
		t.Fatalf("the tunnel's peers are not the node's users: %+v", s)
	}
	if got, _ := m.store.GetUser(u.ID); resp.AckReport != 1 || got.UsedUp != 5 || got.UsedDown != 6 {
		t.Errorf("a tunnel peer's traffic: ack %d, %d up / %d down", resp.AckReport, got.UsedUp, got.UsedDown)
	}
}
