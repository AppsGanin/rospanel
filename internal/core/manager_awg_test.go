package core

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Switching the lane on mints the master's identity once; a user's key is minted
// once and reused; peers follow the working set and the access map; the client
// config carries both sides' keys, the parameters and the endpoint.
func TestAWGIdentityPeersAndClientConfig(t *testing.T) {
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
	users, _ := m.store.WorkingUsers(1)
	peers := m.awgPeers(model.LocalNodeID, users, nil)
	if len(peers) != 1 || peers[0].Email != model.UserEmail(a.ID) {
		t.Fatalf("peers: %+v", peers)
	}
	addr, _ := awg.ClientAddr(a.ID)
	if peers[0].Addr != addr {
		t.Errorf("peer address %v, want %v", peers[0].Addr, addr)
	}
	fresh, _ := m.store.GetUser(a.ID)
	if fresh.WGPrivateKey == "" {
		t.Fatal("the user's key was not stored")
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
	ns := nodeSettings(set, node)
	if ns.AWGPrivateKey != node.AWGPrivateKey || ns.AWGPort != 41000 || !ns.AWGEnabled {
		t.Fatalf("node settings: key ok=%v port=%d on=%v", ns.AWGPrivateKey == node.AWGPrivateKey, ns.AWGPort, ns.AWGEnabled)
	}
	users, _ := m.store.WorkingUsers(1)
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
