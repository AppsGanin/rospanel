package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/nodestate"
)

// splitNode is a node's side of the exchange, run on the agent's own code (nodestate).
type splitNode struct {
	t     *testing.T
	m     *Manager
	id    int64
	parts *nodestate.Parts
	last  NodePush // what the last sync was sent
}

func (s *splitNode) has() NodeHas {
	h := NodeHas{DeltaRev: nodeapi.DeltaRev}
	if s.parts != nil {
		h.ConfigHash, h.StateTag = s.parts.Held.Hash, s.parts.Held.Tag
	}
	return h
}

// sync asks the panel for what the node should get and takes it, as the agent would.
// It returns what came: "", "split", "delta" (users or blocks changed) or "rename".
func (s *splitNode) sync() string {
	s.t.Helper()
	n, err := s.m.store.GetNode(s.id)
	if err != nil || n == nil {
		s.t.Fatalf("node %d: %v", s.id, err)
	}
	push, done, err := s.m.NodeSyncPush(context.Background(), n, s.has())
	done()
	if err != nil {
		s.t.Fatal(err)
	}
	s.last = push
	switch {
	case push.State != nil:
		s.t.Fatal("a node that speaks parts was sent a whole config")
	case push.Split != nil:
		held, err := nodestate.FromSplit(push.Split)
		if err != nil {
			s.t.Fatal(err)
		}
		if s.parts, err = nodestate.Decode(held); err != nil {
			s.t.Fatal(err)
		}
		return "split"
	case push.Delta != nil:
		if s.parts == nil {
			s.t.Fatal("a change came before any state")
		}
		next, err := nodestate.Apply(s.parts, push.Delta)
		if err != nil {
			s.t.Fatal(err)
		}
		changed := len(push.Delta.Upsert) > 0 || len(push.Delta.Remove) > 0 ||
			!bytes.Equal(next.Held.Blocked, s.parts.Held.Blocked)
		s.parts = next
		if changed {
			return "delta"
		}
		return "rename"
	}
	return ""
}

// check compares what the node holds with what the panel would push whole: the same Xray
// config, the same meta, and the hash the panel computes over the whole split state.
func (s *splitNode) check(step string) {
	s.t.Helper()
	n, _ := s.m.store.GetNode(s.id)
	legacy, err := s.m.NodeDesiredState(n)
	if err != nil {
		s.t.Fatal(err)
	}
	got, err := nodestate.Assemble(s.parts)
	if err != nil {
		s.t.Fatalf("%s: assemble: %v", step, err)
	}
	if !reflect.DeepEqual(decodedJSON(s.t, got.XrayConfig), decodedJSON(s.t, legacy.XrayConfig)) {
		s.t.Fatalf("%s: the node's config differs from the panel's\n got %.1500s\nwant %.1500s", step, got.XrayConfig, legacy.XrayConfig)
	}
	// Speed caps travel only for users the node lets in; the whole config lists every
	// capped user, wherever they may connect.
	want := legacy.Meta
	placed := map[string]bool{}
	for _, r := range s.parts.Rows {
		placed[model.UserEmail(r.ID)] = true
	}
	var caps map[string]int
	for email, kbps := range want.SpeedLimits {
		if placed[email] {
			if caps == nil {
				caps = map[string]int{}
			}
			caps[email] = kbps
		}
	}
	want.SpeedLimits = caps
	if !reflect.DeepEqual(got.Meta, want) {
		s.t.Fatalf("%s: the node's meta differs\n got %+v\nwant %+v", step, got.Meta, want)
	}
	x, err := s.m.readNodeStateInputs(n)
	if err != nil {
		s.t.Fatal(err)
	}
	whole, _, _, splittable, err := s.m.buildNodeSplit(n, x)
	if err != nil || !splittable {
		s.t.Fatalf("%s: whole split: %v, splittable %v", step, err, splittable)
	}
	if whole.Hash != s.parts.Held.Hash {
		s.t.Fatalf("%s: the changes did not add up to the whole state (hash %s, want %s)", step,
			nodestate.Short(s.parts.Held.Hash), nodestate.Short(whole.Hash))
	}
}

func decodedJSON(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// expect runs a sync after a change and checks both what came and what the node holds.
func (s *splitNode) expect(step, want string) {
	s.t.Helper()
	s.m.notifyNodes() // every change the fleet must see wakes it
	if got := s.sync(); got != want {
		s.t.Fatalf("%s: the node was sent %q, want %q", step, got, want)
	}
	s.check(step)
	if again := s.sync(); again != "" {
		s.t.Fatalf("%s: a second sync was sent %q, want nothing", step, again)
	}
}

func splitTestNode(t *testing.T, m *Manager) *splitNode {
	t.Helper()
	n := servingNode(t, m, "n", "n.example.com")
	if err := m.store.SetNodeProtocols(n.ID, true, true, false); err != nil {
		t.Fatal(err)
	}
	custom := model.Inbound{ServerID: n.ID, Enabled: true, Name: "ss", Protocol: model.InbShadowsocks, Port: 9500,
		Opts: model.InboundOpts{Method: model.SS2022AES128, ShadowKey: "AAAAAAAAAAAAAAAAAAAAAA=="}}
	custom.Normalize()
	if _, err := m.store.CreateInbound(custom); err != nil {
		t.Fatal(err)
	}
	return &splitNode{t: t, m: m, id: n.ID}
}

// A node that speaks parts follows the panel through every kind of change — each sent as
// only the users it touched while it can be, whole when it cannot — and after each one
// holds exactly the state the panel would have sent whole.
func TestNodeSplitStateFollowsThePanel(t *testing.T) {
	m := nodeTestManager(t)
	node := splitTestNode(t, m)
	var users []int64
	for i := range 4 {
		u, err := m.store.CreateUser(fmt.Sprintf("u%d", i), fmt.Sprintf("uuid-%d", i), fmt.Sprintf("pw-%d", i), fmt.Sprintf("tok-%d", i), 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u.ID)
	}

	if got := node.sync(); got != "split" {
		t.Fatalf("first sync: %q, want the whole state", got)
	}
	node.check("first sync")
	if len(node.parts.Rows) != 4 {
		t.Fatalf("the node holds %d users, want 4", len(node.parts.Rows))
	}
	if got := node.sync(); got != "" {
		t.Fatalf("an unchanged node was sent %q", got)
	}

	u5, _ := m.store.CreateUser("u5", "uuid-5", "pw-5", "tok-5", 0, 0, 0)
	node.expect("a user created", "delta")
	changed := func(step string, upsert, remove []int64) {
		t.Helper()
		d := node.last.Delta
		if d == nil {
			t.Fatalf("%s: no change was sent last", step)
		}
		var ids []int64
		for _, raw := range d.Upsert {
			var r nodeapi.UserRow
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, r.ID)
		}
		if !slices.Equal(ids, upsert) || !slices.Equal(d.Remove, remove) {
			t.Fatalf("%s: the change carried rows %v and removals %v, want %v and %v", step, ids, d.Remove, upsert, remove)
		}
	}
	// What a change carries: the users it touched, and no one else.
	u7, _ := m.store.CreateUser("u7", "uuid-7", "pw-7", "tok-7", 0, 0, 0)
	m.notifyNodes()
	if got := node.sync(); got != "delta" {
		t.Fatalf("another user created: %q", got)
	}
	changed("a user created", []int64{u7.ID}, nil)
	node.check("another user created")
	if err := m.store.SetUserEnabled(users[0], false); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	if got := node.sync(); got != "delta" {
		t.Fatalf("a user disabled: %q", got)
	}
	changed("a user disabled", nil, []int64{users[0]})
	node.check("a user disabled")
	g, err := m.store.CreateGroup("elsewhere", []string{model.BuiltinToken(999, model.LaneVLESS)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetGroupMembers(g.ID, []int64{users[1]}); err != nil {
		t.Fatal(err)
	}
	node.expect("a user's groups took them off the node", "delta")
	if err := m.store.SetUserSpeedLimit(users[2], 2048); err != nil {
		t.Fatal(err)
	}
	node.expect("a speed cap", "delta")
	if err := m.store.BlockIP(model.BlockedIP{IP: "198.51.100.66", Reason: "asn", At: time.Now().Unix(), Until: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	node.expect("an address blocked", "delta")
	if got := node.parts.Blocked.IPs; !slices.Equal(got, []string{"198.51.100.66"}) {
		t.Fatalf("the node's blocked addresses: %v", got)
	}
	if _, err := m.store.UnblockIP("198.51.100.66"); err != nil {
		t.Fatal(err)
	}
	node.expect("the block lifted", "delta")
	if len(node.parts.Blocked.IPs) != 0 {
		t.Fatalf("a lifted block is still on the node: %v", node.parts.Blocked.IPs)
	}

	// Several versions between two syncs: one change carries them all.
	u6, _ := m.store.CreateUser("u6", "uuid-6", "pw-6", "tok-6", 0, 0, 0)
	m.notifyNodes()
	if _, err := m.nodeInputs(); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetUserEnabled(u5.ID, false); err != nil {
		t.Fatal(err)
	}
	node.expect("two versions behind", "delta")
	if node.parts.Rows[len(node.parts.Rows)-1].ID != u6.ID {
		t.Fatal("the later user is not on the node")
	}

	// Anything about the node itself sends the state whole.
	if err := m.store.SetNodeProtocols(node.id, true, false, false); err != nil {
		t.Fatal(err)
	}
	node.expect("a lane switched off", "split")

	// A whole state that never reached the node: it still holds the old structure, and a
	// later change must not be laid over it.
	if err := m.store.SetNodeProtocols(node.id, true, true, false); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	lost, _ := m.store.GetNode(node.id)
	if push, done, err := m.NodeSyncPush(context.Background(), lost, node.has()); err != nil || push.Split == nil {
		t.Fatalf("the structure change was not sent whole: %+v %v", push, err)
	} else {
		done() // and dropped on the way
	}
	if err := m.store.SetUserEnabled(u5.ID, true); err != nil {
		t.Fatal(err)
	}
	node.expect("a change after a whole state was lost", "split")

	// A skeleton that is not the one the node was sent — an input no structure key
	// covers — is not changed in place either.
	if err := m.store.SetUserEnabled(u5.ID, false); err != nil {
		t.Fatal(err)
	}
	m.nodeStateMu.Lock()
	memo := m.nodeSplits[node.id]
	memo.skeleton[0] ^= 1
	m.nodeSplits[node.id] = memo
	m.nodeStateMu.Unlock()
	node.expect("a skeleton that moved", "split")

	// A node further behind than the journal reaches is sent it whole.
	if err := m.store.SetUserEnabled(users[3], false); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	cur, _ := m.nodeInputs()
	m.nodeInputsMu.Lock()
	m.nodeJournal.floor, m.nodeJournal.entries, m.nodeJournal.ids = cur.version, nil, 0
	m.nodeInputsMu.Unlock()
	node.expect("behind the journal", "split")

	// A change touching more users than a node takes live goes whole too.
	saved := splitDeltaMax
	splitDeltaMax = 1
	t.Cleanup(func() { splitDeltaMax = saved })
	for _, id := range users[1:3] {
		if err := m.store.SetUserEnabled(id, false); err != nil {
			t.Fatal(err)
		}
	}
	node.expect("a change too large to send as one", "split")
	splitDeltaMax = saved

	// Every so often the whole state is built and compared: a node that holds it is
	// only told so, and one that drifted is sent it whole.
	m.nodeStateMu.Lock()
	memo = m.nodeSplits[node.id]
	memo.checked = time.Now().Add(-nodeStateMemoAge)
	m.nodeSplits[node.id] = memo
	m.nodeStateMu.Unlock()
	if got := node.sync(); got != "" {
		t.Fatalf("a current node checked again was sent %q", got)
	}
	drifted := *node.parts.Held
	drifted.Rows = drifted.Rows[1:]
	drifted.Hash = nodeapi.ContentHash(drifted.Skeleton, drifted.Rows, drifted.Blocked)
	if node.parts, err = nodestate.Decode(&drifted); err != nil {
		t.Fatal(err)
	}
	if got := node.sync(); got != "" {
		t.Fatalf("a drifted node was sent %q before its check was due", got)
	}
	m.nodeStateMu.Lock()
	memo = m.nodeSplits[node.id]
	memo.checked = time.Now().Add(-nodeStateMemoAge)
	m.nodeSplits[node.id] = memo
	m.nodeStateMu.Unlock()
	if got := node.sync(); got != "split" {
		t.Fatalf("a drifted node at its check was sent %q, want the whole state", got)
	}
	node.check("after a drift")

	// A panel restart names every state anew: a node holding the right content is only
	// told the new name, and then takes changes again.
	restarted := &Manager{store: m.store, nodes: newNodeRegistry(), opts: m.opts, tz: m.tz,
		applied: map[int64]struct{}{}, nodeGeoFiles: m.nodeGeoFiles, nodeHostStats: m.nodeHostStats, nodeSyncFails: m.nodeSyncFails}
	node.m = restarted
	if got := node.sync(); got != "rename" {
		t.Fatalf("after a restart the node was sent %q, want only a new name", got)
	}
	if got := node.sync(); got != "" {
		t.Fatalf("after the rename the node was sent %q", got)
	}
	if err := restarted.store.SetUserEnabled(users[3], true); err != nil {
		t.Fatal(err)
	}
	node.expect("a change after the restart", "delta")
}

// A node whose agent does not speak this revision of parts gets the whole config, as it
// always did.
func TestOlderAgentsGetTheWholeConfig(t *testing.T) {
	m := nodeTestManager(t)
	node := splitTestNode(t, m)
	n, _ := m.store.GetNode(node.id)
	for _, rev := range []int{0, nodeapi.DeltaRev + 1} {
		push, done, err := m.NodeSyncPush(context.Background(), n, NodeHas{DeltaRev: rev})
		done()
		if err != nil {
			t.Fatal(err)
		}
		if push.State == nil || push.Split != nil || push.Delta != nil {
			t.Errorf("revision %d: %+v, want the whole config", rev, push)
		}
	}
}

// Which users each kind of change to the fleet-wide inputs touches.
func TestDiffNodeInputs(t *testing.T) {
	base := func() *nodeInputs {
		return &nodeInputs{
			users: []model.User{{ID: 1, UUID: "a", Password: "p"}, {ID: 3, UUID: "c"}, {ID: 5, UUID: "e"}},
			access: map[int64]model.Access{
				3: {Tokens: map[string]bool{"x": true}},
			},
			speed:   map[string]int{"u5": 100},
			blocked: []string{"198.51.100.1"},
		}
	}
	for _, c := range []struct {
		name   string
		change func(*nodeInputs)
		want   []int64
	}{
		{"nothing", func(*nodeInputs) {}, nil},
		{"a user added", func(in *nodeInputs) { in.users = append(in.users, model.User{ID: 7}) }, []int64{7}},
		{"a user added in between", func(in *nodeInputs) {
			in.users = []model.User{in.users[0], {ID: 2}, in.users[1], in.users[2]}
		}, []int64{2}},
		{"a user gone", func(in *nodeInputs) { in.users = in.users[1:] }, []int64{1}},
		{"a password", func(in *nodeInputs) { in.users[0].Password = "q" }, []int64{1}},
		{"a uuid", func(in *nodeInputs) { in.users[1].UUID = "z" }, []int64{3}},
		{"a tunnel key", func(in *nodeInputs) { in.users[2].WGPrivateKey = "k" }, []int64{5}},
		{"a tunnel slot", func(in *nodeInputs) { in.users[2].AWGSlot = 9 }, []int64{5}},
		{"access narrowed", func(in *nodeInputs) { in.access[1] = model.Access{Tokens: map[string]bool{}} }, []int64{1}},
		{"access widened", func(in *nodeInputs) { delete(in.access, 3) }, []int64{3}},
		{"access token", func(in *nodeInputs) { in.access[3] = model.Access{Tokens: map[string]bool{"y": true}} }, []int64{3}},
		{"access unchanged in another form", func(in *nodeInputs) { in.access[5] = model.Access{All: true} }, nil},
		{"a cap raised", func(in *nodeInputs) { in.speed["u5"] = 200 }, []int64{5}},
		{"a cap lifted", func(in *nodeInputs) { in.speed = nil }, []int64{5}},
		{"a cap added", func(in *nodeInputs) { in.speed["u1"] = 50 }, []int64{1}},
		{"a block", func(in *nodeInputs) { in.blocked = nil }, nil},
	} {
		b := base()
		c.change(b)
		got, ok := diffNodeInputs(base(), b)
		if !ok || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, ok, c.want)
		}
	}
	unsorted := base()
	unsorted.users[0], unsorted.users[1] = unsorted.users[1], unsorted.users[0]
	if _, ok := diffNodeInputs(base(), unsorted); ok {
		t.Error("users out of order were compared")
	}
}

// The journal answers for exactly the versions it holds.
func TestInputsJournal(t *testing.T) {
	var j inputsJournal
	read := func(v uint64, ids ...int64) *nodeInputs {
		in := &nodeInputs{version: v}
		for _, id := range ids {
			in.users = append(in.users, model.User{ID: id})
		}
		return in
	}
	j.record(nil, read(1, 1))
	j.record(read(1, 1), read(2, 1, 2))
	j.record(read(2, 1, 2), read(3, 2))
	for _, c := range []struct {
		base, cur uint64
		want      []int64
		ok        bool
	}{
		{1, 3, []int64{1, 2}, true},
		{2, 3, []int64{1}, true},
		{1, 2, []int64{2}, true},
		{3, 3, nil, true},
		{0, 3, nil, false}, // before the journal
		{3, 2, nil, false}, // from the future
		{1, 4, nil, false}, // a version it has not seen
	} {
		got, ok := j.since(c.base, c.cur)
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("since(%d, %d) = %v %v, want %v %v", c.base, c.cur, got, ok, c.want, c.ok)
		}
	}
	// A version out of sequence starts over.
	j.record(read(3, 2), read(5, 2))
	if _, ok := j.since(3, 5); ok {
		t.Error("a gap in the versions was bridged")
	}
	// Bounded: the oldest entries go.
	var k inputsJournal
	k.record(nil, read(1))
	for v := uint64(2); v <= uint64(journalMaxEntries)+3; v++ {
		k.record(read(v-1, int64(v-1)), read(v, int64(v)))
	}
	if len(k.entries) != journalMaxEntries {
		t.Errorf("%d entries kept, want %d", len(k.entries), journalMaxEntries)
	}
	if _, ok := k.since(1, uint64(journalMaxEntries)+3); ok {
		t.Error("a version the journal dropped was answered for")
	}
}

// The health report reads a node that holds its state in parts as current by the hash of
// the parts, as it reads any other node by the whole config's.
func TestNodeConfigHealthReadsStatesInParts(t *testing.T) {
	m := nodeTestManager(t)
	node := splitTestNode(t, m)
	if _, err := m.store.CreateUser("u", "uuid-1", "pw-1", "tok-1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := node.sync(); got != "split" {
		t.Fatalf("first sync: %q", got)
	}
	report := func(hash string) HealthCheck {
		t.Helper()
		if err := m.store.UpdateNodeStatus(node.id, model.NodeStatusUpdate{LastSeen: time.Now().Unix(), ConfigHash: hash}); err != nil {
			t.Fatal(err)
		}
		n, _ := m.store.GetNode(node.id)
		return m.nodeConfigHealth(n, true)
	}
	if c := report(node.parts.Held.Hash); c.DetailKey != "health.nodeConfigCurrent" {
		t.Errorf("a node holding its parts: %s", c.DetailKey)
	}
	n, _ := m.store.GetNode(node.id)
	legacy, _ := m.NodeDesiredState(n)
	if c := report(legacy.Hash); c.DetailKey != "health.nodeConfigCurrent" {
		t.Errorf("a node holding the whole config: %s", c.DetailKey)
	}
	if c := report("stale"); c.DetailKey != "health.nodeConfigPending" {
		t.Errorf("a node behind: %s", c.DetailKey)
	}
}
