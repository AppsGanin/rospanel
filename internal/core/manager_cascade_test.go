package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

func TestBridgeCredsAreStablePerPairAndVersion4(t *testing.T) {
	set := &model.Settings{RealityPrivateKey: "k1"}
	a1, p1 := bridgeCreds(set, 5, 0)
	a2, p2 := bridgeCreds(set, 5, 0)
	if a1 != a2 || p1 != p2 {
		t.Fatal("the same pair got different credentials")
	}
	if b, _ := bridgeCreds(set, 6, 0); b == a1 {
		t.Fatal("two entries share a credential")
	}
	if b, _ := bridgeCreds(set, 0, 5); b == a1 {
		t.Fatal("the reverse pair shares the credential")
	}
	if b, _ := bridgeCreds(&model.Settings{RealityPrivateKey: "k2"}, 5, 0); b == a1 {
		t.Fatal("a new master key kept the old credential")
	}
	if len(a1) != 36 || a1[14] != '4' || !strings.ContainsAny(a1[19:20], "89ab") {
		t.Fatalf("not a version-4 UUID: %s", a1)
	}
}

// cascadeFixture: the master is the exit (REALITY on), a node in Russia the entry.
func cascadeFixture(t *testing.T) (*Manager, *model.Node) {
	t.Helper()
	m := nodeTestManager(t)
	if err := m.store.SetTLS("nl.example.com", "nl.example.com", "acme", "/tmp/c.pem", "/tmp/k.pem"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetRealityKeys("master-priv", "master-pub", "ab", "/rp"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetProtocolEnabled("reality", true); err != nil {
		t.Fatal(err)
	}
	syncedNode(t, m.store, "ru", "26.10.10")
	nodes, _ := m.store.ListNodes()
	n := &nodes[0]
	yes := true
	rc := model.RoutingConfig{
		RoutingOrder: []string{"direct", "warp", "opera", "nl"},
		Lanes:        []model.EgressLane{{ID: "nl", Name: "NL", Enabled: true, Via: []model.CascadeExit{{Server: model.LocalNodeID}}}},
	}
	if err := m.store.UpdateNode(n.ID, store.NodeEdit{Name: n.Name, Host: n.Host, VLESS: &yes, Routing: &rc}); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeSeenIP(n.ID, "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	n, _ = m.store.GetNode(n.ID)
	return m, n
}

func TestCascadeEndsAreBuiltForBothServers(t *testing.T) {
	t.Parallel()
	m, n := cascadeFixture(t)
	uuid, _ := bridgeCreds(mustSettings(t, m), n.ID, model.LocalNodeID)

	// The entry leaves through the master over REALITY, with the bridge credential.
	state, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatal(err)
	}
	cfg := state.XrayConfig
	for _, want := range []string{uuid, "nl.example.com", `"pool-nl"`, `"fallbackTag":"proxy-nl-0"`} {
		if !bytes.Contains(cfg, []byte(want)) {
			t.Errorf("the entry's config lacks %s", want)
		}
	}

	// The master takes the entry in, from the entry's addresses only.
	opts, err := m.genOptsFor(model.LocalNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Bridges) != 1 {
		t.Fatalf("bridges = %+v", opts.Bridges)
	}
	b := opts.Bridges[0]
	if b.Email != model.BridgeEmail(n.ID) || b.UUID != uuid || b.Lane != model.BridgeReality {
		t.Errorf("bridge = %+v", b)
	}
	if !slices.Equal(b.Sources, []string{"198.51.100.9", "203.0.113.1"}) {
		t.Errorf("sources = %v, want the host and the address the node reached the panel from", b.Sources)
	}
	// And the master has no cascade of its own.
	if len(opts.Cascade) != 0 {
		t.Errorf("master cascade = %v", opts.Cascade)
	}
}

// The entry's config follows the exit: a new REALITY key on the master reaches it
// through the fingerprint, not after some unrelated change.
func TestCascadeFollowsTheExit(t *testing.T) {
	t.Parallel()
	m, n := cascadeFixture(t)
	before, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetRealityKeys("master-priv2", "master-pub2", "ab", "/rp"); err != nil {
		t.Fatal(err)
	}
	after, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatal(err)
	}
	if before.Hash == after.Hash || !bytes.Contains(after.XrayConfig, []byte("master-pub2")) {
		t.Fatal("the entry kept a link to the exit's old key")
	}
}

func TestValidateCascade(t *testing.T) {
	t.Parallel()
	m, n := cascadeFixture(t)
	lane := func(via ...int64) *model.RoutingConfig {
		var exits []model.CascadeExit
		for _, id := range via {
			exits = append(exits, model.CascadeExit{Server: id})
		}
		return &model.RoutingConfig{Lanes: []model.EgressLane{{ID: "x", Name: "X", Enabled: true, Via: exits}}}
	}
	code := func(err error) string {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return ve.Code
		}
		return ""
	}
	if err := m.validateCascade(n.ID, lane(model.LocalNodeID), nil); err != nil {
		t.Fatalf("a valid cascade refused: %v", err)
	}
	if c := code(m.validateCascade(n.ID, lane(n.ID), nil)); c != "err.cascadeSelf" {
		t.Errorf("self: %q", c)
	}
	if c := code(m.validateCascade(n.ID, lane(999), nil)); c != "err.cascadeNoServer" {
		t.Errorf("missing: %q", c)
	}
	vision := lane(model.LocalNodeID)
	vision.Lanes[0].Via[0].Bridge = model.BridgeHysteria
	if err := m.store.SetProtocolEnabled("hysteria2", false); err != nil {
		t.Fatal(err)
	}
	if c := code(m.validateCascade(n.ID, vision, nil)); c != "err.cascadeLaneOff" {
		t.Errorf("bridge lane off: %q", c)
	}
}

func mustSettings(t *testing.T, m *Manager) *model.Settings {
	t.Helper()
	s, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Switching off the lane an entry reaches its exit over would leave the entry's
// cascade with no exit and its traffic stopped, so that save is refused.
func TestExitKeepsItsBridgeLane(t *testing.T) {
	t.Parallel()
	m, _ := cascadeFixture(t)
	set := mustSettings(t, m)
	same := *set
	if err := m.exitKeepsBridges(model.LocalNodeID, set, &same); err != nil {
		t.Fatalf("an unchanged save refused: %v", err)
	}
	off := *set
	off.RealityEnabled = false
	var ve *ValidationError
	if err := m.exitKeepsBridges(model.LocalNodeID, set, &off); !errors.As(err, &ve) || ve.Code != "err.cascadeExitLaneOff" {
		t.Fatalf("REALITY off on the exit: %v", err)
	}
	// Vision off is fine: nobody bridges over it.
	noVision := *set
	noVision.VLESSEnabled = false
	if err := m.exitKeepsBridges(model.LocalNodeID, set, &noVision); err != nil {
		t.Fatalf("a lane no bridge uses: %v", err)
	}
}

// A server lists every bridge connection under its own name, and says which are on —
// by the same test the save applies, so a picker never offers what a save refuses.
func TestBridgeOptions(t *testing.T) {
	set := &model.Settings{
		Host: "nl.example.com", SNI: "nl.example.com", VLESSEnabled: true, VLESSPort: 443,
		RealityEnabled: true, RealityPublicKey: "pub", RealityPort: 8443, RealityName: "Быстрый",
	}
	got := bridgeOptions(set)
	want := []BridgeOption{
		{Bridge: model.BridgeReality, Name: "Быстрый", Enabled: true},
		{Bridge: model.BridgeVision, Name: model.ProtoVLESS, Enabled: true},
		{Bridge: model.BridgeHysteria, Name: model.ProtoHysteria, Enabled: false},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("bridges = %+v\nwant %+v", got, want)
	}
}

// Review fixes: what an exit's state may not break, and what a save may not refuse.
func TestCascadeGuards(t *testing.T) {
	t.Parallel()
	m, n := cascadeFixture(t)
	code := func(err error) string {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return ve.Code
		}
		return fmt.Sprint(err)
	}

	// An unchanged lane is not re-checked: a rename of the entry is not refused because
	// an exit went away since.
	gone := &model.RoutingConfig{Lanes: []model.EgressLane{{ID: "x", Name: "X", Enabled: true, Via: []model.CascadeExit{{Server: 999}}}}}
	if err := m.validateCascade(n.ID, gone, gone); err != nil {
		t.Errorf("an unchanged lane refused: %v", err)
	}

	// A loop: the master (exit of n) with a cascade back through n.
	back := &model.RoutingConfig{Lanes: []model.EgressLane{{ID: "y", Name: "Y", Enabled: true, Via: []model.CascadeExit{{Server: n.ID, Bridge: model.BridgeVision}}}}}
	if c := code(m.validateCascade(model.LocalNodeID, back, nil)); c != "err.cascadeLoop" {
		t.Errorf("loop: %q", c)
	}

	// The master is n's exit: it cannot be switched off from under n…
	if c := code(m.exitInUse(model.LocalNodeID)); c != "err.cascadeExitInUse" {
		t.Errorf("exit in use: %q", c)
	}
	// …and n itself is no exit, so it can.
	if err := m.exitInUse(n.ID); err != nil {
		t.Errorf("an entry refused as an exit: %v", err)
	}
}

func TestNodeEditCannotSwitchOffAnExitsBridge(t *testing.T) {
	t.Parallel()
	m, entry := cascadeFixture(t)
	// A second node, the exit of a cascade on the master over its REALITY.
	syncedNode(t, m.store, "de", "26.10.10")
	nodes, _ := m.store.ListNodes()
	var exit *model.Node
	for i := range nodes {
		if nodes[i].ID != entry.ID {
			exit = &nodes[i]
		}
	}
	yes := true
	if err := m.store.UpdateNode(exit.ID, store.NodeEdit{Name: exit.Name, Host: exit.Host, Reality: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SaveNodeReality(exit.ID, "p", "P", "ab", "/x"); err != nil {
		t.Fatal(err)
	}
	rc := model.RoutingConfig{Lanes: []model.EgressLane{{ID: "de", Name: "DE", Enabled: true, Via: []model.CascadeExit{{Server: exit.ID}}}}}
	if err := m.store.SetRoutingConfig(rc); err != nil {
		t.Fatal(err)
	}
	no := false
	err := m.nodeEditKeepsBridges(exit.ID, store.NodeEdit{Name: exit.Name, Host: exit.Host, Reality: &no})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "err.cascadeExitLaneOff" {
		t.Fatalf("switching off the exit's REALITY: %v", err)
	}
}

func TestSeenIPKeepsOnePerFamily(t *testing.T) {
	t.Parallel()
	m, n := cascadeFixture(t)
	m.NoteNodeSeenIP(n, "2001:db8::7")
	n, _ = m.store.GetNode(n.ID)
	if n.SeenIP != "198.51.100.9 2001:db8::7" {
		t.Fatalf("seen = %q", n.SeenIP)
	}
	// Reaching the panel over IPv4 again changes nothing.
	m.NoteNodeSeenIP(n, "198.51.100.9")
	n, _ = m.store.GetNode(n.ID)
	if n.SeenIP != "198.51.100.9 2001:db8::7" {
		t.Fatalf("seen after v4 again = %q", n.SeenIP)
	}
	addrs := m.entryAddrs(cascadeServer{set: &model.Settings{Host: n.Host}, node: n})
	if !slices.Equal(addrs, []string{"198.51.100.9", "2001:db8::7", "203.0.113.1"}) {
		t.Fatalf("entry addresses = %v", addrs)
	}
}

// A self-signed master is reached by its pin: a Vision bridge to it without one would
// fail the entry's certificate check (subscriptions get it from applyTLSHints).
func TestCascadeToASelfSignedMasterCarriesThePin(t *testing.T) {
	t.Parallel()
	m, n := cascadeFixture(t)
	dir := t.TempDir()
	certPEM, keyPEM, err := tlsutil.GenerateSelfSigned("144.31.159.81")
	if err != nil {
		t.Fatal(err)
	}
	m.tls.CertPath, m.tls.KeyPath = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(m.tls.CertPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.tls.KeyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	pin := m.CertPinSHA256()
	if pin == "" {
		t.Fatal("no pin read from the certificate")
	}
	set := mustSettings(t, m)
	nodes, _ := m.store.ListNodes()
	servers := m.cascadeServers(set, nodes)
	l := bridgeLink(model.BridgeVision, probeUser, servers[model.LocalNodeID].set)
	if !strings.Contains(l, pin) {
		t.Fatalf("a Vision link to a self-signed master has no pin: %s", l)
	}
	_ = n
}
