package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Nodes that speak nodeapi.DeltaRev are sent their state in parts, and then only the
// users that changed (see nodeapi/split.go for the parts).
//
// What a change touched comes from the fleet-wide inputs every node's state is built
// from (nodeInputs): each read that finds them different takes a new version, and the
// journal below records which users differ between each version and the one before.
// A node is told which version it holds (the tag), so a later sync can be answered with
// the rows of the users touched since — generated for those users alone, which places
// them exactly as a whole state would (xray.TestSkeletonDoesNotDependOnTheUsers).
//
// A change can only be sent over the same structure: the tag also carries the node's
// structure key, and anything else about the node or the panel that moves — a lane, an
// inbound, a setting, a geo refresh — sends the state whole. So does a panel restart,
// a node too far behind the journal, or a change touching more users than a node would
// take live. A whole state is built only to be compared first: a node that already has
// its content (ContentHash) is only told the new tag.
//
// Nothing a node is sent as a change is checked against what it holds on the spot. That
// is checked every nodeStateMemoAge instead, when the state is built whole and compared
// with the node's own hash of its content, and a node that drifted is sent it whole.

// splitDeltaMax is the most users one change may touch. Past it the node would rebuild
// its Xray rather than change the users live (xray.liveUserChangesMax), so the whole
// state costs it nothing more, and the panel nothing it would not spend on the rows.
var splitDeltaMax = 5000 // a variable for the tests alone

// journalMaxIDs and journalMaxEntries bound the journal. A node further behind than it
// reaches is sent its state whole.
const (
	journalMaxIDs     = 50000
	journalMaxEntries = 4096
)

// inputsJournal records, for each version of the fleet-wide inputs, the users whose part
// of any node's state may differ from the version before. Guarded by nodeInputsMu.
type inputsJournal struct {
	// floor is the oldest version a change can be told from: entries cover every version
	// after it, and nothing before.
	floor   uint64
	entries []journalEntry
	ids     int
}

type journalEntry struct {
	version uint64
	touched []int64 // ascending
}

// record notes the version next was just given, against last, the read before it.
func (j *inputsJournal) record(last, next *nodeInputs) {
	want := j.floor + 1
	if n := len(j.entries); n > 0 {
		want = j.entries[n-1].version + 1
	}
	touched, ok := []int64(nil), last != nil && last.version != 0 && next.version == want
	if ok {
		touched, ok = diffNodeInputs(last, next)
	}
	if !ok {
		// Nothing to tell the change from: start over at this version.
		j.floor, j.entries, j.ids = next.version, nil, 0
		return
	}
	j.entries = append(j.entries, journalEntry{version: next.version, touched: touched})
	j.ids += len(touched)
	for len(j.entries) > 1 && (len(j.entries) > journalMaxEntries || j.ids > journalMaxIDs) {
		j.ids -= len(j.entries[0].touched)
		j.floor = j.entries[0].version
		j.entries = slices.Delete(j.entries, 0, 1)
	}
}

// since returns every user touched after version base up to cur, ascending. ok is false
// when the journal does not cover all of it.
func (j *inputsJournal) since(base, cur uint64) ([]int64, bool) {
	if base < j.floor || base > cur {
		return nil, false
	}
	if base == cur {
		return nil, true
	}
	if n := len(j.entries); n == 0 || j.entries[n-1].version < cur {
		return nil, false
	}
	var out []int64
	for _, e := range j.entries {
		if e.version > base && e.version <= cur {
			out = append(out, e.touched...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), true
}

// diffNodeInputs lists the users whose part of a node's state may differ between two
// reads: in one working set and not the other, with other credentials, another access,
// another speed cap. The blocked addresses are not a user's and are sent with every
// change. ok is false when the reads cannot be compared.
func diffNodeInputs(a, b *nodeInputs) ([]int64, bool) {
	touched := map[int64]struct{}{}
	i, j := 0, 0
	for i < len(a.users) || j < len(b.users) {
		switch {
		case i > 0 && i < len(a.users) && a.users[i-1].ID >= a.users[i].ID,
			j > 0 && j < len(b.users) && b.users[j-1].ID >= b.users[j].ID:
			return nil, false // not in id order: cannot be walked together
		case j == len(b.users) || i < len(a.users) && a.users[i].ID < b.users[j].ID:
			touched[a.users[i].ID] = struct{}{}
			i++
		case i == len(a.users) || b.users[j].ID < a.users[i].ID:
			touched[b.users[j].ID] = struct{}{}
			j++
		default:
			x, y := &a.users[i], &b.users[j]
			if x.UUID != y.UUID || x.Password != y.Password || x.WGPrivateKey != y.WGPrivateKey || x.AWGSlot != y.AWGSlot {
				touched[x.ID] = struct{}{}
			}
			i++
			j++
		}
	}
	for id := range a.access {
		if !sameAccess(model.AccessOf(a.access, id), model.AccessOf(b.access, id)) {
			touched[id] = struct{}{}
		}
	}
	for id := range b.access {
		if !sameAccess(model.AccessOf(a.access, id), model.AccessOf(b.access, id)) {
			touched[id] = struct{}{}
		}
	}
	for _, caps := range []map[string]int{a.speed, b.speed} {
		for email := range caps {
			if a.speed[email] == b.speed[email] {
				continue
			}
			id, ok := model.UserIDOfEmail(email)
			if !ok {
				return nil, false
			}
			touched[id] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(touched)), true
}

func sameAccess(x, y model.Access) bool {
	return x.All == y.All && (x.All || maps.Equal(x.Tokens, y.Tokens))
}

// nodeSplitMemo is what the panel last verified about a node's split state.
type nodeSplitMemo struct {
	structure [sha256.Size]byte
	// skeleton is the hash of the skeleton the node was sent with that structure: a
	// change is only sent over the same one.
	skeleton [sha256.Size]byte
	checked  time.Time
}

// splitTag names a state: this panel process, the inputs version, the structure.
func (m *Manager) splitTag(version uint64, structure [sha256.Size]byte) string {
	return fmt.Sprintf("%s.%d.%s", m.bootID(), version, hex.EncodeToString(structure[:]))
}

// parseSplitTag reads a tag this process made.
func (m *Manager) parseSplitTag(tag string) (version uint64, structure [sha256.Size]byte, ok bool) {
	parts := strings.Split(tag, ".")
	if len(parts) != 3 || parts[0] != m.bootID() {
		return 0, structure, false
	}
	version, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, structure, false
	}
	b, err := hex.DecodeString(parts[2])
	if err != nil || len(b) != len(structure) {
		return 0, structure, false
	}
	copy(structure[:], b)
	return version, structure, true
}

// bootID tells this panel process apart from the ones before it: a tag from another
// process names inputs versions this one never had.
func (m *Manager) bootID() string {
	m.bootOnce.Do(func() {
		var b [8]byte
		_, _ = rand.Read(b[:])
		m.boot = hex.EncodeToString(b[:])
	})
	return m.boot
}

// NodePush is what a node's sync is answered with: nothing, its whole config, its whole
// split state, or a change to the split state it holds. At most one is set.
type NodePush struct {
	State *nodeapi.NodeState
	Split *nodeapi.SplitState
	Delta *nodeapi.StateDelta
}

// NodeHas is what a node says it holds (see nodeapi.SyncRequest).
type NodeHas struct {
	ConfigHash string
	DeltaRev   int
	StateTag   string
}

// NodeSyncPush is NodeStatePush for a node that may speak nodeapi.DeltaRev: the state it
// should be sent, if any, and done, as NodeStatePush describes. A change holds nothing.
func (m *Manager) NodeSyncPush(ctx context.Context, n *model.Node, has NodeHas) (NodePush, func(), error) {
	if has.DeltaRev != nodeapi.DeltaRev {
		state, done, err := m.NodeStatePush(ctx, n, has.ConfigHash)
		return NodePush{State: state}, done, err
	}
	x, err := m.readNodeStateInputs(n)
	if err != nil {
		return NodePush{}, noRelease, err
	}
	structure, keyed := nodeStructureKey(n, x, m.opts)
	version := x.in.version
	tag := ""
	if keyed {
		tag = m.splitTag(version, structure)
		m.nodeStateMu.Lock()
		memo, ok := m.nodeSplits[n.ID]
		m.nodeStateMu.Unlock()
		if ok && memo.structure == structure && time.Since(memo.checked) < nodeStateMemoAge {
			if has.StateTag == tag {
				return NodePush{}, noRelease, nil
			}
			if delta, ok := m.splitDelta(n, x, has.StateTag, tag, memo); ok {
				return NodePush{Delta: delta}, noRelease, nil
			}
		}
	}

	if err := m.stateGate.acquire(ctx); err != nil {
		return NodePush{}, noRelease, err
	}
	var once sync.Once
	done := func() { once.Do(m.stateGate.release) }
	split, skeleton, complete, splittable, err := m.buildNodeSplit(n, x)
	if err != nil {
		done()
		return NodePush{}, noRelease, err
	}
	if !splittable {
		// A config the parts cannot carry goes whole, as to an agent that cannot take
		// parts: the agent takes both.
		done()
		logWarn("node state: the config cannot be sent in parts, sending it whole", "node", n.ID)
		state, stateDone, err := m.NodeStatePush(ctx, n, has.ConfigHash)
		return NodePush{State: state}, stateDone, err
	}
	if !keyed || !complete {
		tag = "" // a degraded state is not one a change can follow
	} else {
		m.nodeStateMu.Lock()
		if m.nodeSplits == nil {
			m.nodeSplits = map[int64]nodeSplitMemo{}
		}
		m.nodeSplits[n.ID] = nodeSplitMemo{structure: structure, skeleton: skeleton, checked: time.Now()}
		m.nodeStateMu.Unlock()
	}
	if split.Hash == has.ConfigHash {
		// The node holds this content already, whatever it was called.
		done()
		if has.StateTag == tag {
			return NodePush{}, noRelease, nil
		}
		return NodePush{Delta: &nodeapi.StateDelta{From: has.StateTag, Tag: tag}}, noRelease, nil
	}
	split.Tag = tag
	return NodePush{Split: split}, done, nil
}

// splitDelta is the change from the state a node holds, tagged from, to the one tagged
// to — or false when it cannot be sent as a change.
func (m *Manager) splitDelta(n *model.Node, x *nodeStateInputs, from, to string, memo nodeSplitMemo) (*nodeapi.StateDelta, bool) {
	base, structure, ok := m.parseSplitTag(from)
	if !ok || structure != memo.structure {
		return nil, false
	}
	m.nodeInputsMu.Lock()
	touched, ok := m.nodeJournal.since(base, x.in.version)
	m.nodeInputsMu.Unlock()
	if !ok || len(touched) > splitDeltaMax {
		return nil, false
	}
	subset := make([]model.User, 0, len(touched))
	for _, u := range x.in.users {
		if _, found := slices.BinarySearch(touched, u.ID); found {
			subset = append(subset, u)
		}
	}
	b, err := m.generateNodeState(n, x, subset)
	if err != nil || !b.complete {
		return nil, false
	}
	sc, ok := xray.SplitUsers(b.cfg, b.custom, subset)
	if !ok {
		return nil, false
	}
	skeleton, err := encodeSkeleton(sc, b.meta)
	if err != nil || sha256.Sum256(skeleton) != memo.skeleton {
		return nil, false
	}
	rows, ids, err := splitRows(sc, subset, b.meta, x.in.speed)
	if err != nil {
		return nil, false
	}
	blocked, err := encodeBlocked(b.meta)
	if err != nil {
		return nil, false
	}
	delta := &nodeapi.StateDelta{From: from, Tag: to, Upsert: rows, Blocked: blocked}
	for _, id := range touched {
		if _, placed := slices.BinarySearch(ids, id); !placed {
			delta.Remove = append(delta.Remove, id)
		}
	}
	m.served.change(n.ID, ids, delta.Remove, time.Now().Unix(), x.servedSeq)
	return delta, true
}

// buildNodeSplit builds a node's whole split state. skeleton is the hash of its skeleton;
// complete is as buildNodeState describes; splittable is false for a config the parts
// cannot carry (xray.SplitUsers).
//
// The caller holds the state gate.
func (m *Manager) buildNodeSplit(n *model.Node, x *nodeStateInputs) (split *nodeapi.SplitState, skeleton [sha256.Size]byte, complete, splittable bool, err error) {
	b, err := m.generateNodeState(n, x, x.in.users)
	if err != nil {
		return nil, skeleton, false, false, err
	}
	sc, ok := xray.SplitUsers(b.cfg, b.custom, x.in.users)
	if !ok {
		return nil, skeleton, false, false, nil
	}
	skel, err := encodeSkeleton(sc, b.meta)
	if err != nil {
		return nil, skeleton, false, false, err
	}
	rows, ids, err := splitRows(sc, x.in.users, b.meta, x.in.speed)
	if err != nil {
		return nil, skeleton, false, false, err
	}
	blocked, err := encodeBlocked(b.meta)
	if err != nil {
		return nil, skeleton, false, false, err
	}
	m.served.note(n.ID, ids, false, time.Now().Unix(), x.servedSeq)
	return &nodeapi.SplitState{
		Hash:     nodeapi.ContentHash(skel, rows, blocked),
		Skeleton: skel,
		Rows:     rows,
		Blocked:  blocked,
	}, sha256.Sum256(skel), b.complete, true, nil
}

// encodeSkeleton encodes a split config's skeleton with the meta it goes with, less
// what comes from the rows and from the blocked addresses.
func encodeSkeleton(sc *xray.SplitConfig, meta nodeapi.NodeMeta) (json.RawMessage, error) {
	cfg, err := json.Marshal(sc.Skeleton)
	if err != nil {
		return nil, err
	}
	meta.SpeedLimits, meta.BlockedIPs, meta.BlockTTLHours, meta.BannedIPs = nil, nil, 0, nil
	if meta.AWG != nil {
		tunnel := *meta.AWG
		tunnel.Peers = nil
		meta.AWG = &tunnel
	}
	return json.Marshal(nodeapi.Skeleton{Config: cfg, Slots: sc.Slots, Meta: meta})
}

func encodeBlocked(meta nodeapi.NodeMeta) (json.RawMessage, error) {
	return json.Marshal(nodeapi.Blocked{IPs: meta.BlockedIPs, TTLHours: meta.BlockTTLHours, Banned: meta.BannedIPs})
}

// splitRows encodes the row of every user a split config lets in — on an inbound, or as
// a peer of the tunnel — ascending by id, and returns their ids with them. users must be
// ascending by id, as the working set is read.
func splitRows(sc *xray.SplitConfig, users []model.User, meta nodeapi.NodeMeta, speed map[string]int) ([]json.RawMessage, []int64, error) {
	peers := map[string]nodeapi.AWGPeer{}
	if meta.AWG != nil {
		for _, p := range meta.AWG.Peers {
			peers[p.Email] = p
		}
	}
	var rows []json.RawMessage
	var ids []int64
	for i := range users {
		u := &users[i]
		if i > 0 && users[i-1].ID >= u.ID {
			return nil, nil, fmt.Errorf("users out of order at %d", u.ID)
		}
		email := model.UserEmail(u.ID)
		slots := sc.Placed[u.ID]
		peer, tunnelled := peers[email]
		if len(slots) == 0 && !tunnelled {
			continue
		}
		row := nodeapi.UserRow{ID: u.ID, Slots: slots, Speed: speed[email]}
		for _, si := range slots {
			need := xray.SlotNeeds(sc.Slots[si])
			if need.UUID {
				row.UUID = u.UUID
			}
			if need.Password {
				row.Password = u.Password
			}
			if need.Tunnel {
				// SplitUsers matched this user's entry against the same identity, so
				// it is there to take.
				row.WGKey, row.WGAddr, _ = xray.TunnelIdentity(u)
			}
		}
		if tunnelled {
			row.AWGKey, row.AWGAddr = peer.PublicKey, peer.Addr
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, raw)
		ids = append(ids, u.ID)
	}
	return rows, ids, nil
}
