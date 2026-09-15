package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"math"
	"reflect"
	"sort"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// A node's desired state is asked for on every sync — once when the request lands and
// again when its hold ends — and on every wake, which reaches every node at once. Built
// in full each time, that is the whole Xray config generated, marshalled and hashed
// just to learn it has not changed. In a load test with 50,000 users on five nodes and
// no config changing, that was a quarter of the panel's CPU, and ~150 MB in use
// whenever the nodes happened to ask together.
//
// So each node keeps the fingerprint of everything its last state was built from and
// the hash that came out. A node asking with that hash, while the fingerprint still
// matches, is told nothing changed without anything being built. Only the hash is
// kept, never the config: holding one per node would be the memory this saves.

// nodeStateMemoAge bounds how long a remembered hash answers for a node. The
// fingerprint covers every input the state is built from; this is for the one nobody
// has thought of yet, so that it reaches the nodes in minutes rather than never.
const nodeStateMemoAge = 5 * time.Minute

type nodeStateMemo struct {
	key  [sha256.Size]byte
	hash string
	at   time.Time
}

// nodeStateInputs is everything a node's desired state is built from, read once so the
// fingerprint and the build see the same values.
type nodeStateInputs struct {
	set      *model.Settings
	in       *nodeInputs
	geoGen   uint64
	geoMoved bool
	opts     xray.Options
	inbounds []model.Inbound
	inbErr   error
	proxies  map[string][]model.ProxyEndpoint
}

func (m *Manager) readNodeStateInputs(n *model.Node) (*nodeStateInputs, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	in, err := m.nodeInputs()
	if err != nil {
		return nil, err
	}
	x := &nodeStateInputs{set: set, in: in}
	// The groups can change while they are read — parsed for the first time inside
	// genOpts, or dropped by a refresh — and then the generation read on either side of
	// them says nothing about the groups in hand: such a read is not fingerprinted.
	x.geoGen = m.geoGeneration()
	x.opts = m.genOpts()
	x.geoMoved = m.geoGeneration() != x.geoGen
	x.inbounds, x.inbErr = m.store.EnabledInbounds(n.ID)
	x.proxies = m.getNodeProxies(n.ID)
	return x, nil
}

// NodeStateChange returns the state a node should be given, or nil when the state it
// has — the hash it reported — is already the one it should have.
func (m *Manager) NodeStateChange(n *model.Node, have string) (*nodeapi.NodeState, error) {
	x, err := m.readNodeStateInputs(n)
	if err != nil {
		return nil, err
	}
	key, keyed := nodeStateKey(n, x, m.opts)
	if keyed && have != "" {
		m.nodeStateMu.Lock()
		memo, ok := m.nodeStates[n.ID]
		m.nodeStateMu.Unlock()
		if ok && memo.key == key && memo.hash == have && time.Since(memo.at) < nodeStateMemoAge {
			return nil, nil
		}
	}
	state, complete, err := m.buildNodeState(n, x)
	if err != nil {
		return nil, err
	}
	if keyed && complete {
		m.nodeStateMu.Lock()
		if m.nodeStates == nil {
			m.nodeStates = map[int64]nodeStateMemo{}
		}
		m.nodeStates[n.ID] = nodeStateMemo{key: key, hash: state.Hash, at: time.Now()}
		m.nodeStateMu.Unlock()
	}
	if state.Hash == have {
		return nil, nil
	}
	return state, nil
}

// nodeStateKey fingerprints a node's state inputs. It reports false when there is
// nothing trustworthy to fingerprint: a soft read failure (the build that follows is a
// degraded one, not to be remembered), groups that changed while being read, or a
// value it cannot encode.
func nodeStateKey(n *model.Node, x *nodeStateInputs, managerOpts xray.Options) ([sha256.Size]byte, bool) {
	var key [sha256.Size]byte
	if x.inbErr != nil || x.in.version == 0 || x.geoMoved {
		return key, false
	}
	node := *n
	// What a sync rewrites about the node, none of which the state is built from (the
	// version and the certificate's fingerprint are: they stay). Anything else about the
	// node is in the fingerprint — a field added later included — so a mistake here
	// costs rebuilds, never a stale config.
	node.LastSeen, node.XrayVersion, node.XrayRunning, node.ConfigHash, node.LastReportID = 0, "", false, "", 0
	node.CertIssuer, node.CertExpiresAt = "", 0
	// And the master's own bookkeeping: every full reconcile of the master bumps the
	// revision and the timestamp, and only the health report reads the three.
	set := *x.set
	set.ConfigRevision, set.LastConfigError, set.UpdatedAt = 0, "", time.Time{}
	// The groups are large and change only on a geo refresh, which the generation
	// counts; everything else about the manager's options is fingerprinted as is.
	opts := managerOpts
	opts.Groups = nil
	d := digester{h: sha256.New(), ok: true}
	d.value(reflect.ValueOf(struct {
		Inputs   uint64
		Geo      uint64
		Settings model.Settings
		Node     model.Node
		Inbounds []model.Inbound
		Proxies  map[string][]model.ProxyEndpoint
		Opts     xray.Options
		Pinned   string
	}{x.in.version, x.geoGen, set, node, x.inbounds, x.proxies, opts, xray.PinnedVersion}))
	if !d.ok {
		return key, false
	}
	copy(key[:], d.h.Sum(nil))
	return key, true
}

// digester writes a canonical encoding of a value: every field, exported or not and
// whatever its JSON tag says (keys and secrets are json:"-" and very much part of a
// config), maps in key order, nil told apart from empty. A kind it cannot encode
// (func, channel) clears ok.
type digester struct {
	h     hash.Hash
	depth int
	ok    bool
}

func (d *digester) num(kind reflect.Kind, u uint64) {
	var b [9]byte
	b[0] = byte(kind)
	binary.LittleEndian.PutUint64(b[1:], u)
	_, _ = d.h.Write(b[:])
}

func (d *digester) value(v reflect.Value) {
	if !d.ok {
		return
	}
	if d.depth > 64 { // no state input nests this deep; a cycle does
		d.ok = false
		return
	}
	d.depth++
	defer func() { d.depth-- }()
	switch k := v.Kind(); k {
	case reflect.Invalid:
		d.num(k, 0)
	case reflect.Bool:
		var u uint64
		if v.Bool() {
			u = 1
		}
		d.num(k, u)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		d.num(k, uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		d.num(k, v.Uint())
	case reflect.Float32, reflect.Float64:
		d.num(k, math.Float64bits(v.Float()))
	case reflect.Complex64, reflect.Complex128:
		c := v.Complex()
		d.num(k, math.Float64bits(real(c)))
		d.num(k, math.Float64bits(imag(c)))
	case reflect.String:
		s := v.String()
		d.num(k, uint64(len(s)))
		_, _ = io.WriteString(d.h, s)
	case reflect.Slice, reflect.Array:
		if k == reflect.Slice && v.IsNil() {
			d.num(k, math.MaxUint64)
			return
		}
		d.num(k, uint64(v.Len()))
		for i := range v.Len() {
			d.value(v.Index(i))
		}
	case reflect.Map:
		if v.IsNil() {
			d.num(k, math.MaxUint64)
			return
		}
		d.num(k, uint64(v.Len()))
		type entry struct {
			key []byte
			val reflect.Value
		}
		entries := make([]entry, 0, v.Len())
		for it := v.MapRange(); it.Next(); {
			kd := digester{h: sha256.New(), ok: true, depth: d.depth}
			kd.value(it.Key())
			if !kd.ok {
				d.ok = false
				return
			}
			entries = append(entries, entry{kd.h.Sum(nil), it.Value()})
		}
		sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].key, entries[j].key) < 0 })
		for _, e := range entries {
			_, _ = d.h.Write(e.key)
			d.value(e.val)
		}
	case reflect.Struct:
		d.num(k, uint64(v.NumField()))
		for i := range v.NumField() {
			d.value(v.Field(i))
		}
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			d.num(k, math.MaxUint64)
			return
		}
		d.num(k, 1)
		if k == reflect.Interface {
			t := v.Elem().Type().String()
			d.num(k, uint64(len(t)))
			_, _ = io.WriteString(d.h, t)
		}
		d.value(v.Elem())
	default:
		d.ok = false
	}
}
