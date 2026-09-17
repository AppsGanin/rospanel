package xray

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

func wireGuardFixture() model.Inbound {
	in := model.Inbound{
		ID: 21, Enabled: true, Name: "Calls", Protocol: model.InbWireGuard, Port: 56000,
		Opts: model.InboundOpts{WGPrivateKey: testWGKey(9), WGPublicKey: testWGKey(10), WGLocalPort: 20021},
	}
	in.Normalize()
	return in
}

// The inbound listens on loopback at its local port — the relay holds the public one —
// and holds, as peers, exactly the users allowed on it who have a tunnel identity, each
// with their own /32 and their email.
func TestWireGuardInboundShape(t *testing.T) {
	wg := wireGuardFixture()
	users := []model.User{
		{ID: 1, UUID: "u-1", WGPrivateKey: testWGKey(1), AWGSlot: 2},
		{ID: 2, UUID: "u-2", WGPrivateKey: testWGKey(2), AWGSlot: 3}, // not allowed
		{ID: 3, UUID: "u-3"}, // no identity yet
	}
	access := map[int64]model.Access{
		2: {Tokens: map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}},
	}
	cfg, err := Generate(baseSettings(), users, Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{wg}, Access: access}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := findInbound(cfg, wg.Tag())
	if got == nil {
		t.Fatal("wireguard inbound missing")
	}
	if got.Listen != "127.0.0.1" || got.Port != 20021 || got.Protocol != "wireguard" || got.StreamSettings != nil {
		t.Errorf("inbound = %s %s:%d stream=%v", got.Protocol, got.Listen, got.Port, got.StreamSettings)
	}
	s, ok := got.Settings.(WireGuardInboundSettings)
	if !ok {
		t.Fatalf("settings %T", got.Settings)
	}
	if s.SecretKey != wg.Opts.WGPrivateKey || s.MTU != WireGuardTURNMTU || !slices.Equal(s.Address, []string{awg.ServerAddr.String()}) {
		t.Errorf("settings = %+v", s)
	}
	pub, _ := awg.PublicKey(testWGKey(1))
	want := []WireGuardInboundPeer{{PublicKey: pub, AllowedIPs: []string{"10.66.0.2/32"}, Email: "u1"}}
	if len(s.Peers) != 1 || s.Peers[0].PublicKey != want[0].PublicKey || !slices.Equal(s.Peers[0].AllowedIPs, want[0].AllowedIPs) || s.Peers[0].Email != "u1" {
		t.Errorf("peers = %+v, want %+v", s.Peers, want)
	}

	// Xray reads the peer list as "peers" with an email on each; its parser makes each a user.
	raw, _ := json.Marshal(got.Settings)
	var doc struct {
		Peers []map[string]any `json:"peers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Peers) != 1 || doc.Peers[0]["email"] != "u1" || doc.Peers[0]["allowedIPs"] == nil {
		t.Errorf("settings JSON = %s", raw)
	}

	// Its users go through SyncWireGuard, not the CLI: out of the adu stubs and the rmu tags.
	if tags := EnabledInboundTags(baseSettings(), []model.Inbound{wg}); slices.Contains(tags, wg.Tag()) {
		t.Errorf("rmu targets include the wireguard inbound: %v", tags)
	}
	for _, in := range UserInbounds(baseSettings(), []model.Inbound{wg}, users, model.LocalNodeID, nil) {
		if in.Tag == wg.Tag() {
			t.Error("adu stubs include the wireguard inbound")
		}
	}
	if w := WireGuardInbounds(cfg); len(w) != 1 || w[0].Tag != wg.Tag() {
		t.Errorf("WireGuardInbounds = %+v", w)
	}
	// Its peers count among the users the config was given — u1 once more than without it.
	without, _ := Generate(baseSettings(), users, Options{PanelDest: "127.0.0.1:8080", Access: access}, nil)
	count := func(c *Config) int {
		emails, ok := c.ClientEmails()
		if !ok {
			t.Fatal("ClientEmails could not read the config")
		}
		n := 0
		for _, e := range emails {
			if e == "u1" {
				n++
			}
		}
		return n
	}
	if with, base := count(cfg), count(without); with != base+1 {
		t.Errorf("u1 listed %d times with the inbound and %d without", with, base)
	}
}

// On a node, a pushed config that differs in a WireGuard inbound's peers alone is a user
// change to apply live — through syncWireGuardLocked, not the CLI.
func TestPlanUserChangesTakesWireGuardPeers(t *testing.T) {
	doc := func(peers ...string) []byte {
		var list []any
		for _, e := range peers {
			list = append(list, map[string]any{"publicKey": testWGKey(byte(len(e))), "allowedIPs": []string{"10.66.0.2/32"}, "email": e})
		}
		b, _ := json.Marshal(map[string]any{
			"inbounds": []any{map[string]any{"tag": "custom-21", "listen": "127.0.0.1", "port": 20021, "protocol": "wireguard",
				"settings": map[string]any{"secretKey": testWGKey(9), "mtu": 1280, "peers": list}}},
		})
		return b
	}
	changes, ok := planUserChanges(doc("u1", "u2"), doc("u1", "u33"))
	if !ok || len(changes) != 1 {
		t.Fatalf("plan: ok=%v changes=%+v", ok, changes)
	}
	c := changes[0]
	if !c.wireGuard || c.hysteria || c.key != "peers" || !slices.Equal(c.remove, []string{"u2"}) || len(c.add) != 1 {
		t.Errorf("change = %+v", c)
	}

	// The peers the process started with, read back as the sync compares them.
	live, err := readWireGuardLive(doc("u1", "u2"))
	if err != nil || len(live["custom-21"]) != 2 {
		t.Fatalf("readWireGuardLive = %+v, %v", live, err)
	}
	if _, err := readWireGuardLive(doc("u1", "u1")); err == nil {
		t.Error("a shared email must not be managed live")
	}
}
