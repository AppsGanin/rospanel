package core

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Switching the lane on mints the master's identity once; a user's key is minted
// once and reused; peers follow the working set and the access map; the client
// config carries both sides' keys, the parameters and the endpoint.
func TestAWGIdentityPeersAndClientConfig(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	a, _ := m.CreateUser(ctx, "a", 0, 0)
	b, _ := m.CreateUser(ctx, "b", 0, 0)
	if err := m.SetUserEnabled(ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}

	set, _ := m.store.GetSettings()
	if err := m.ensureMasterAWGIdentity(set, false); err != nil {
		t.Fatal(err)
	}
	if set.AWGPrivateKey == "" || set.AWGPublicKey == "" || set.AWGParams.IsZero() {
		t.Fatalf("identity not minted: %+v", set.AWGParams)
	}
	pub := set.AWGPublicKey
	if err := m.ensureMasterAWGIdentity(set, false); err != nil || set.AWGPublicKey != pub {
		t.Error("a second ensure must keep the identity")
	}
	if err := m.ensureMasterAWGIdentity(set, true); err != nil || set.AWGPublicKey == pub {
		t.Error("regen must mint a new identity")
	}
	again, _ := m.store.GetSettings()
	if again.AWGPublicKey != set.AWGPublicKey || again.AWGPrivateKey != set.AWGPrivateKey {
		t.Error("identity not persisted (private key must round-trip through encryption)")
	}
	if err := awg.Params(awgParams(again.AWGParams)).Validate(); err != nil {
		t.Errorf("stored params invalid: %v", err)
	}

	// Peers: the working set (b is disabled) filtered by access.
	users, _ := m.store.WorkingCredentials(1)
	peers := m.awgPeers(model.LocalNodeID, users, nil)
	if len(peers) != 1 || peers[0].Email != model.UserEmail(a.ID) {
		t.Fatalf("peers: %+v", peers)
	}
	fresh, _ := m.store.GetUser(a.ID)
	if fresh.WGPrivateKey == "" || fresh.AWGSlot == 0 {
		t.Fatalf("the user's key or slot was not stored (slot %d)", fresh.AWGSlot)
	}
	addr, _ := awg.ClientAddr(fresh.AWGSlot)
	if peers[0].Addr != addr {
		t.Errorf("peer address %v, want %v", peers[0].Addr, addr)
	}
	if derived, _ := awg.PublicKey(fresh.WGPrivateKey); derived != peers[0].PublicKey {
		t.Error("peer public key does not match the stored private key")
	}
	// Restricted access: a token map without the lane leaves the user out.
	restricted := map[int64]model.Access{a.ID: {Tokens: map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}}}
	if p := m.awgPeers(model.LocalNodeID, users, restricted); len(p) != 0 {
		t.Errorf("a user without the awg lane became a peer: %+v", p)
	}

	// Client config for the master.
	set.AWGEnabled, set.AWGPort, set.Host = true, 40000, "vpn.example.com"
	set.ServerID = model.LocalNodeID
	conf, err := m.AWGClientConfig(fresh, set)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PrivateKey = " + fresh.WGPrivateKey, "Address = " + addr.String() + "/32",
		"PublicKey = " + set.AWGPublicKey, "Endpoint = vpn.example.com:40000", "Jc = ", "H4 = ",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	if _, err := m.AWGClientConfig(fresh, &model.Settings{AWGEnabled: false}); err == nil {
		t.Error("a config for a server with the lane off was produced")
	}
	// With no AmneziaWG DNS of its own the config follows the server's DNS
	// settings, keeping the plain resolvers and skipping the DoH URLs.
	serverDNS := set.XrayDNS
	set.XrayDNS = "https://dns.example/dns-query\n9.9.9.9\n149.112.112.112\n8.8.4.4"
	withDNS, err := m.AWGClientConfig(fresh, set)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withDNS, "DNS = 9.9.9.9, 149.112.112.112\n") {
		t.Errorf("in-tunnel DNS should come from the server settings:\n%s", withDNS)
	}
	set.AWGDNS = "1.0.0.1"
	if own, _ := m.AWGClientConfig(fresh, set); !strings.Contains(own, "DNS = 1.0.0.1\n") {
		t.Errorf("an explicit AmneziaWG DNS must win:\n%s", own)
	}
	set.AWGDNS, set.XrayDNS = "", "https://dns.example/dns-query"
	if none, _ := m.AWGClientConfig(fresh, set); !strings.Contains(none, "DNS = "+awg.DefaultDNS+"\n") {
		t.Errorf("nothing usable should fall back to the default:\n%s", none)
	}
	set.XrayDNS = serverDNS
	// The same user gets the same key on a second render.
	conf2, _ := m.AWGClientConfig(fresh, set)
	if conf2 != conf {
		t.Error("client config not stable across renders")
	}
}

// A node's tunnel state names its own identity, never the master's, and only the
// users allowed on that node's lane.
func TestNodeAWGStateUsesTheNodesIdentity(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "u", 0, 0)
	n, err := m.CreateNode("nl", "nl.example.com")
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
	if n.AWGPublicKey == "" || n.AWGPublicKey == set.AWGPublicKey {
		t.Fatalf("node identity: %q (master %q)", n.AWGPublicKey, set.AWGPublicKey)
	}
	on := true
	if err := m.store.SetNodeAWGEnabled(n.ID, on); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeConnections(n.ID, &model.NodeConnections{AWGPort: 41000}); err != nil {
		t.Fatal(err)
	}
	node, _ := m.store.GetNode(n.ID)
	// The parameters generated above are 3.1, so the node has to be new enough to
	// read them — see TestA31TunnelIsWithheldFromAnAgentThatCannotReadIt.
	node.NodeVersion = "3.0.0"
	ns := nodeSettings(set, node)
	if ns.AWGPrivateKey != node.AWGPrivateKey || ns.AWGPort != 41000 || !ns.AWGEnabled {
		t.Fatalf("node settings: key ok=%v port=%d on=%v", ns.AWGPrivateKey == node.AWGPrivateKey, ns.AWGPort, ns.AWGEnabled)
	}
	users, _ := m.store.WorkingCredentials(1)
	st := m.nodeAWGState(node, ns, users, nil)
	if st == nil || st.Port != 41000 || st.PrivateKey != node.AWGPrivateKey || len(st.Peers) != 1 || st.Peers[0].Email != model.UserEmail(u.ID) {
		t.Fatalf("node awg state: %+v", st)
	}
	if err := st.Params.Validate(); err != nil {
		t.Errorf("node params: %v", err)
	}
	// Off on the node ⇒ no state, even with the master's lane on.
	ns.AWGEnabled = false
	if st := m.nodeAWGState(node, ns, users, nil); st != nil {
		t.Error("state produced for a node with the lane off")
	}
}

// A node still on a 2.x agent reads h1–h4 as numbers. Hand it a 3.1 block, whose
// headers are ranges, and its decode of the WHOLE sync response fails — so it
// stops syncing at all rather than merely losing its tunnel. The panel withholds
// the state instead, and the node keeps running what it already has.
func TestA31TunnelIsWithheldFromAnAgentThatCannotReadIt(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	if _, err := m.CreateUser(ctx, "u", 0, 0); err != nil {
		t.Fatal(err)
	}
	n, err := m.CreateNode("nl", "nl.example.com")
	if err != nil {
		t.Fatal(err)
	}
	set, _ := m.store.GetSettings()
	if err := m.ensureNodeAWGIdentity(n, false); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeAWGEnabled(n.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeConnections(n.ID, &model.NodeConnections{AWGPort: 41000}); err != nil {
		t.Fatal(err)
	}
	node, _ := m.store.GetNode(n.ID)
	ns := nodeSettings(set, node)
	users, _ := m.store.WorkingCredentials(1)

	for _, v := range []string{"", "2.14.2", "v2.9.0"} {
		node.NodeVersion = v
		if st := m.nodeAWGState(node, ns, users, nil); st != nil {
			t.Errorf("agent %q was handed a 3.1 tunnel it cannot decode", v)
		}
	}
	for _, v := range []string{"3.0.0", "v3.1.0", "4.0.0"} {
		node.NodeVersion = v
		if st := m.nodeAWGState(node, ns, users, nil); st == nil {
			t.Errorf("agent %q was refused a tunnel it can read", v)
		}
	}

	// A block from before 3.1 has no ranges in it, so every agent can read it and
	// none of them is refused — the gate is about the parameters, not the version.
	node.AWGParams = model.AWGParams{Jc: 4, Jmin: 50, Jmax: 1000, S1: 30, S2: 40,
		H1: "11", H2: "12", H3: "13", H4: "14"}
	node.NodeVersion = "2.14.2"
	if st := m.nodeAWGState(node, ns, users, nil); st == nil {
		t.Error("a pre-3.1 block was withheld from an agent that can read it")
	}
}

// A user's tunnel address is the slot they were handed, the same in the peer list and
// in their config, and a deleted user's slot goes to the next user who needs one.
func TestAWGAddressesFollowSlotsNotIDs(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	mk := func(name string) int64 {
		t.Helper()
		u, err := m.store.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	build := func() map[string]string {
		t.Helper()
		users, err := m.store.WorkingCredentials(1)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		seen := map[string]bool{}
		for _, p := range m.awgPeers(model.LocalNodeID, users, nil) {
			if seen[p.Addr.String()] {
				t.Fatalf("two peers on %s", p.Addr)
			}
			seen[p.Addr.String()] = true
			out[p.Email] = p.Addr.String()
		}
		return out
	}
	first := build()
	if len(first) != 3 {
		t.Fatalf("peers: %v", first)
	}
	if again := build(); !reflect.DeepEqual(again, first) {
		t.Fatalf("addresses moved between builds: %v then %v", first, again)
	}

	// The config a user downloads carries the address their peer entry has.
	set, _ := m.store.GetSettings()
	set.AWGEnabled, set.AWGPort, set.Host, set.AWGPublicKey = true, 40000, "vpn.example.com", "c2VydmVyLXB1YmxpYy1rZXktcGxhY2Vob2xkZXItMzI="
	u, _ := m.store.GetUser(c)
	conf, err := m.AWGClientConfig(u, set)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf, "Address = "+first[model.UserEmail(c)]+"/32") {
		t.Fatalf("config address differs from the peer's %s:\n%s", first[model.UserEmail(c)], conf)
	}

	// b leaves; the next user takes b's address and nobody else moves.
	if err := m.store.DeleteUser(b); err != nil {
		t.Fatal(err)
	}
	d := mk("d")
	m.nodeInputsCache = &nodeInputs{}
	after := build()
	if m.nodeInputsCache != nil {
		t.Error("claims were made but the shared node inputs were kept")
	}
	if after[model.UserEmail(d)] != first[model.UserEmail(b)] {
		t.Errorf("d got %s, want b's freed %s", after[model.UserEmail(d)], first[model.UserEmail(b)])
	}
	for _, id := range []int64{a, c} {
		if after[model.UserEmail(id)] != first[model.UserEmail(id)] {
			t.Errorf("user %d moved from %s to %s", id, first[model.UserEmail(id)], after[model.UserEmail(id)])
		}
	}
}

// A user who arrives with a tunnel key of their own — imported from another RosPanel —
// has no slot yet. Their first peer build gives them one and keeps their key, so the
// config they bring with them keeps its identity.
func TestImportedAWGKeyGetsASlot(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	priv, pub, err := awg.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.store.ImportUser(store.ImportedUser{
		Name: "moved", UUID: "uuid-moved", Password: "pw", SubToken: "tok-moved", Enabled: true, WGPrivateKey: priv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.AWGSlot != 0 || u.WGPrivateKey != priv {
		t.Fatalf("imported user: slot %d, key kept %v", u.AWGSlot, u.WGPrivateKey == priv)
	}
	users, _ := m.store.WorkingCredentials(1)
	peers := m.awgPeers(model.LocalNodeID, users, nil)
	if len(peers) != 1 || peers[0].PublicKey != pub {
		t.Fatalf("the imported user is not a peer with their own key: %+v", peers)
	}
	stored, _ := m.store.GetUser(u.ID)
	if stored.AWGSlot == 0 || stored.WGPrivateKey != priv {
		t.Fatalf("after the build: slot %d, key kept %v", stored.AWGSlot, stored.WGPrivateKey == priv)
	}
}
