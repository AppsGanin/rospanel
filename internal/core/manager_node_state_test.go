package core

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// stateNode makes a node serving VLESS and Hysteria2 with the transport inherited from
// the panel, so panel-wide settings reach its config.
func stateNode(t *testing.T, m *Manager) *model.Node {
	t.Helper()
	n, err := m.store.CreateNode("state", "state.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := m.store.UpdateNode(n.ID, store.NodeEdit{Name: n.Name, Host: n.Host, VLESS: &yes, Hysteria: &yes}); err != nil {
		t.Fatal(err)
	}
	return freshNode(t, m, n.ID)
}

func freshNode(t *testing.T, m *Manager, id int64) *model.Node {
	t.Helper()
	n, err := m.store.GetNode(id)
	if err != nil || n == nil {
		t.Fatalf("node %d: %v", id, err)
	}
	return n
}

func memoOf(m *Manager, id int64) (nodeStateMemo, bool) {
	m.nodeStateMu.Lock()
	defer m.nodeStateMu.Unlock()
	memo, ok := m.nodeStates[id]
	return memo, ok
}

// A node that has the state it should have is told so without its state being built
// again — across its own syncs, a wake that changed nothing and the shared inputs
// ageing out — and every input that does change reaches it: users, settings, the node
// itself, its inbounds, its proxies, the geo groups, blocked addresses, and the memo's
// own age.
func TestNodeStateIsRebuiltOnlyWhenAnInputChanges(t *testing.T) {
	m := nodeTestManager(t)
	for _, name := range []string{"a", "b"} {
		if _, err := m.store.CreateUser(name, "uuid-"+name, "pw-"+name, "tok-"+name, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	n := stateNode(t, m)
	// A node that has reported its certificate, as a live one has.
	sha := strings.Repeat("ab", 32)
	if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: "3.3.0", CertSHA256: sha, CertIssuer: "R11",
		CertExpiresAt: time.Now().Add(60 * 24 * time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	n = freshNode(t, m, n.ID)

	first, err := m.NodeStateChange(n, "")
	if err != nil || first == nil {
		t.Fatalf("a node with no state was given none: %v", err)
	}
	if full, err := m.NodeDesiredState(n); err != nil || full.Hash != first.Hash {
		t.Fatalf("the state given is not the desired state: %v", err)
	}
	have := first.Hash
	memo, ok := memoOf(m, n.ID)
	if !ok || memo.hash != have {
		t.Fatalf("nothing remembered for the node: %+v", memo)
	}
	builtAt := memo.at

	current := func(when string, n *model.Node) {
		t.Helper()
		st, err := m.NodeStateChange(n, have)
		if err != nil {
			t.Fatal(err)
		}
		if st != nil {
			t.Fatalf("%s: a current node was given a state", when)
		}
		if memo, _ := memoOf(m, n.ID); !memo.at.Equal(builtAt) {
			t.Fatalf("%s: the state was built again with nothing changed", when)
		}
	}
	current("the same sync again", n)

	// Its own syncs rewrite the node's status; none of it is config.
	if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: n.NodeVersion, XrayVersion: "26.6.27", XrayRunning: true, ConfigHash: have,
		// A certificate that reads a moment later, as an agent's sync does: its expiry
		// and issuer are status, not config.
		CertSHA256: sha, CertIssuer: "R12", CertExpiresAt: n.CertExpiresAt + 3600,
	}); err != nil {
		t.Fatal(err)
	}
	synced := freshNode(t, m, n.ID)
	if synced.CertIssuer != "R12" || synced.CertExpiresAt != n.CertExpiresAt+3600 {
		t.Fatalf("the status report did not land: %q %d", synced.CertIssuer, synced.CertExpiresAt)
	}
	synced.LastReportID = 99
	current("after a status report", synced)

	m.notifyNodes()
	current("after a wake that changed nothing", synced)

	m.nodeInputsMu.Lock()
	m.nodeInputsCache.at = time.Now().Add(-nodeInputsTTL - time.Second)
	m.nodeInputsMu.Unlock()
	current("after the shared inputs aged out unchanged", synced)

	// changed: the input moved, so the node gets the new state — the one a full build
	// gives now.
	changed := func(when string, n *model.Node) {
		t.Helper()
		st, err := m.NodeStateChange(n, have)
		if err != nil {
			t.Fatal(err)
		}
		if st == nil {
			t.Fatalf("%s: the node was told it is current", when)
		}
		full, err := m.NodeDesiredState(n)
		if err != nil || full.Hash != st.Hash {
			t.Fatalf("%s: the state given is not the desired state (%v)", when, err)
		}
		have = st.Hash
		memo, _ := memoOf(m, n.ID)
		builtAt = memo.at
		time.Sleep(time.Millisecond) // keep build times apart
	}
	// rebuilt: the input moved but may leave the output as it was; what matters is that
	// the remembered answer was not trusted.
	rebuilt := func(when string, n *model.Node) {
		t.Helper()
		st, err := m.NodeStateChange(n, have)
		if err != nil {
			t.Fatal(err)
		}
		memo, _ := memoOf(m, n.ID)
		if memo.at.Equal(builtAt) {
			t.Fatalf("%s: the remembered answer was reused", when)
		}
		if st != nil {
			have = st.Hash
		}
		builtAt = memo.at
		time.Sleep(time.Millisecond)
	}
	time.Sleep(time.Millisecond)

	if _, err := m.store.CreateUser("c", "uuid-c", "pw-c", "tok-c", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.notifyNodes()
	changed("a new user", synced)

	if err := m.store.SetHysteriaPorts(40443, 0, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	changed("a panel setting the node inherits", synced)

	yes := true
	if err := m.store.UpdateNode(n.ID, store.NodeEdit{Name: n.Name, Host: "moved.example.com", VLESS: &yes, Hysteria: &yes}); err != nil {
		t.Fatal(err)
	}
	synced = freshNode(t, m, n.ID)
	changed("the node's own edit", synced)

	if _, err := m.store.CreateInbound(model.Inbound{
		ServerID: n.ID, Name: "ws", Protocol: model.InbVLESS, Port: 2053, Enabled: true,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecNone, Path: "/w"},
	}); err != nil {
		t.Fatal(err)
	}
	changed("a custom inbound", synced)

	m.setNodeProxies(n.ID, map[string][]model.ProxyEndpoint{"lane": {{Protocol: "socks", Address: "198.51.100.9", Port: 1080}}})
	rebuilt("the node's proxies", synced)

	m.dropGeoCache()
	rebuilt("a geo refresh", synced)

	// A block is announced by no wake; it arrives when the shared inputs age out.
	if err := m.store.BlockIP(model.BlockedIP{IP: "203.0.113.7", Reason: "test", At: time.Now().Unix(), Until: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	m.nodeInputsMu.Lock()
	m.nodeInputsCache.at = time.Now().Add(-nodeInputsTTL - time.Second)
	m.nodeInputsMu.Unlock()
	changed("a blocked address", synced)

	m.nodeStateMu.Lock()
	memo = m.nodeStates[n.ID]
	memo.at = time.Now().Add(-nodeStateMemoAge - time.Second)
	m.nodeStates[n.ID] = memo
	builtAt = memo.at
	m.nodeStateMu.Unlock()
	rebuilt("a remembered answer past its age", synced)

	// A node reporting some other state is given the desired one, remembered or not.
	if st, err := m.NodeStateChange(synced, "stale-hash"); err != nil || st == nil || st.Hash != have {
		t.Fatalf("a node with a stale state was not given the current one: %v %v", st, err)
	}
}

// Every field of a node and of the settings is part of the fingerprint, unexported
// JSON or not, except what a sync rewrites; a field added later is covered without
// anyone listing it.
func TestNodeStateKeyCoversEveryField(t *testing.T) {
	m := nodeTestManager(t)
	n := stateNode(t, m)
	x, err := m.readNodeStateInputs(n)
	if err != nil {
		t.Fatal(err)
	}
	base, ok := nodeStateKey(n, x, m.opts)
	if !ok {
		t.Fatal("a plain node's inputs could not be fingerprinted")
	}
	volatile := map[string]bool{"LastSeen": true, "XrayVersion": true, "XrayRunning": true, "ConfigHash": true, "LastReportID": true,
		"CertIssuer": true, "CertExpiresAt": true}

	nodeType := reflect.TypeOf(model.Node{})
	for i := range nodeType.NumField() {
		f := nodeType.Field(i)
		changedNode := *n
		if !mutateField(reflect.ValueOf(&changedNode).Elem().Field(i)) {
			t.Fatalf("node field %s: the test cannot change it", f.Name)
		}
		key, ok := nodeStateKey(&changedNode, x, m.opts)
		if !ok {
			t.Fatalf("node field %s: fingerprint refused", f.Name)
		}
		if volatile[f.Name] != (key == base) {
			t.Errorf("node field %s: fingerprint changed=%v, want %v", f.Name, key != base, !volatile[f.Name])
		}
	}

	bookkeeping := map[string]bool{"ConfigRevision": true, "LastConfigError": true, "UpdatedAt": true}
	setType := reflect.TypeOf(model.Settings{})
	for i := range setType.NumField() {
		f := setType.Field(i)
		changedSet := *x.set
		if !mutateField(reflect.ValueOf(&changedSet).Elem().Field(i)) {
			t.Fatalf("settings field %s: the test cannot change it", f.Name)
		}
		y := *x
		y.set = &changedSet
		key, ok := nodeStateKey(n, &y, m.opts)
		if !ok {
			t.Fatalf("settings field %s: fingerprint refused", f.Name)
		}
		if bookkeeping[f.Name] != (key == base) {
			t.Errorf("settings field %s: fingerprint changed=%v, want %v", f.Name, key != base, !bookkeeping[f.Name])
		}
	}

	for name, change := range map[string]func(y *nodeStateInputs, o *xray.Options){
		"inputs version": func(y *nodeStateInputs, _ *xray.Options) { in := *y.in; in.version++; y.in = &in },
		"geo generation": func(y *nodeStateInputs, _ *xray.Options) { y.geoGen++ },
		"inbounds":       func(y *nodeStateInputs, _ *xray.Options) { y.inbounds = append(y.inbounds, model.Inbound{Port: 1}) },
		"proxies": func(y *nodeStateInputs, _ *xray.Options) {
			y.proxies = map[string][]model.ProxyEndpoint{"x": {{Port: 1}}}
		},
		"panel address": func(_ *nodeStateInputs, o *xray.Options) { o.PanelDest = "127.0.0.1:9" },
	} {
		y, opts := *x, m.opts
		change(&y, &opts)
		if key, ok := nodeStateKey(n, &y, opts); !ok || key == base {
			t.Errorf("%s: fingerprint unchanged (ok=%v)", name, ok)
		}
	}

	// Nothing degraded is remembered.
	y := *x
	y.inbErr = errors.New("unreadable")
	if _, ok := nodeStateKey(n, &y, m.opts); ok {
		t.Error("inputs with unreadable inbounds were fingerprinted")
	}
	y = *x
	y.geoMoved = true
	if _, ok := nodeStateKey(n, &y, m.opts); ok {
		t.Error("inputs whose groups changed while read were fingerprinted")
	}
	y = *x
	in := *x.in
	in.version = 0
	y.in = &in
	if _, ok := nodeStateKey(n, &y, m.opts); ok {
		t.Error("inputs from a failed read were fingerprinted")
	}
}

// mutateField changes a settable value to something it was not.
func mutateField(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 7)
	case reflect.String:
		v.SetString(v.String() + "~")
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		if v.Len() == 0 {
			m.SetMapIndex(reflect.Zero(v.Type().Key()), reflect.Zero(v.Type().Elem()))
		}
		v.Set(m)
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		} else {
			v.Set(reflect.Zero(v.Type()))
		}
	case reflect.Struct:
		if t, ok := v.Interface().(time.Time); ok {
			v.Set(reflect.ValueOf(t.Add(time.Hour)))
			return true
		}
		for i := range v.NumField() {
			if v.Field(i).CanSet() && mutateField(v.Field(i)) {
				return true
			}
		}
		return false
	default:
		return false
	}
	return true
}

// The digest is canonical: maps in any insertion order read the same, nil and empty
// are told apart, and what cannot be encoded is refused rather than skipped.
func TestDigesterIsCanonical(t *testing.T) {
	sum := func(v any) ([sha256.Size]byte, bool) {
		d := digester{h: sha256.New(), ok: true}
		d.value(reflect.ValueOf(v))
		var out [sha256.Size]byte
		copy(out[:], d.h.Sum(nil))
		return out, d.ok
	}
	a := map[string]int{}
	b := map[string]int{}
	for i, k := range []string{"x", "y", "z", "w", "v"} {
		a[k] = i
	}
	for _, k := range []string{"v", "w", "z", "y", "x"} {
		b[k] = map[string]int{"x": 0, "y": 1, "z": 2, "w": 3, "v": 4}[k]
	}
	if ka, _ := sum(a); ka != func() [sha256.Size]byte { k, _ := sum(b); return k }() {
		t.Error("the same map read differently")
	}
	b["x"] = 9
	if ka, _ := sum(a); ka == func() [sha256.Size]byte { k, _ := sum(b); return k }() {
		t.Error("maps with the same keys and different values read the same")
	}
	nilSlice, _ := sum(struct{ S []string }{})
	emptySlice, _ := sum(struct{ S []string }{S: []string{}})
	if nilSlice == emptySlice {
		t.Error("a nil slice and an empty one read the same")
	}
	one, _ := sum(struct{ A, B string }{"ab", ""})
	other, _ := sum(struct{ A, B string }{"a", "b"})
	if one == other {
		t.Error("strings run together")
	}
	if _, ok := sum(struct{ F func() }{}); ok {
		t.Error("a func was encoded")
	}
}

// Two reads are the same only when every credential, cap, block and access entry is.
func TestSameNodeInputs(t *testing.T) {
	base := func() *nodeInputs {
		return &nodeInputs{
			users:   []model.User{{ID: 1, UUID: "u1", Password: "p1", WGPrivateKey: "k1", AWGSlot: 2}, {ID: 2, UUID: "u2", Password: "p2"}},
			access:  map[int64]model.Access{1: {Tokens: map[string]bool{"t": true}}},
			speed:   map[string]int{"u1": 100},
			blocked: []string{"203.0.113.1"},
		}
	}
	if !sameNodeInputs(base(), base()) {
		t.Fatal("identical reads differ")
	}
	for name, change := range map[string]func(in *nodeInputs){
		"user id":       func(in *nodeInputs) { in.users[1].ID = 3 },
		"uuid":          func(in *nodeInputs) { in.users[0].UUID = "x" },
		"password":      func(in *nodeInputs) { in.users[1].Password = "x" },
		"tunnel key":    func(in *nodeInputs) { in.users[1].WGPrivateKey = "k2" },
		"tunnel slot":   func(in *nodeInputs) { in.users[0].AWGSlot = 3 },
		"one user more": func(in *nodeInputs) { in.users = append(in.users, model.User{ID: 9}) },
		"access":        func(in *nodeInputs) { in.access[1].Tokens["t"] = false },
		"access gone":   func(in *nodeInputs) { in.access = nil },
		"speed cap":     func(in *nodeInputs) { in.speed["u1"] = 200 },
		"blocked":       func(in *nodeInputs) { in.blocked = append(in.blocked, "203.0.113.2") },
	} {
		in := base()
		change(in)
		if sameNodeInputs(base(), in) {
			t.Errorf("%s: a changed read counts as the same", name)
		}
	}
}

// Groups that could not be parsed when a node's state was built — the lists not
// downloaded yet, as on a fresh install — and parse now are a change: the node's
// routing gains them without anyone dropping a cache.
func TestNodeStateFollowsIPListsArriving(t *testing.T) {
	m := nodeTestManager(t)
	dir := t.TempDir()
	m.sup = xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	n := stateNode(t, m)
	yes := true
	rc := model.RoutingConfig{BlockDomains: []string{"iplist:global/lt-test"}}
	if err := m.store.UpdateNode(n.ID, store.NodeEdit{Name: n.Name, Host: n.Host, VLESS: &yes, Hysteria: &yes, Routing: &rc}); err != nil {
		t.Fatal(err)
	}
	n = freshNode(t, m, n.ID)
	const listed = "blocked-by-list.example"

	first, err := m.NodeStateChange(n, "")
	if err != nil || first == nil {
		t.Fatalf("no state: %v", err)
	}
	if bytes.Contains(first.XrayConfig, []byte(listed)) {
		t.Fatal("a list that does not exist yet reached the config")
	}
	if st, _ := m.NodeStateChange(n, first.Hash); st != nil {
		t.Fatal("a current node was given a state")
	}

	list := `{"site": {"group": "lt-test", "domains": ["` + listed + `"]}}`
	if err := os.WriteFile(filepath.Join(dir, "iplist-global.json"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := m.NodeStateChange(n, first.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || !bytes.Contains(st.XrayConfig, []byte(listed)) {
		t.Fatal("the list that arrived did not reach the node")
	}
}

// A node's config is built alone. What one build holds is the panel's memory peak, and
// a change that reaches the fleet wakes every node at once — five builds side by side
// is what ran the panel out of memory at 50,000 users.
func TestNodeConfigsAreBuiltOneAtATime(t *testing.T) {
	m := nodeTestManager(t)
	if _, err := m.store.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	n := stateNode(t, m)
	m.buildMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := m.NodeDesiredState(n)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a state was built while another build held the gate: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	m.buildMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the build never ran after the gate was released")
	}
}
