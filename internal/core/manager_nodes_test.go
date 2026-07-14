package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

func nodeTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// Bootstrap normally seeds a WS path; a bare test store has none, and
	// xray.Generate requires it. Set the minimum for config generation to succeed.
	if err := st.SetWSPath("/ws"); err != nil {
		t.Fatalf("seed ws path: %v", err)
	}
	return &Manager{
		store:   st,
		nodes:   newNodeRegistry(),
		opts:    xray.Options{PanelDest: "127.0.0.1:8080"},
		tz:      time.Local,
		applied: map[int64]struct{}{},
	}
}

func TestNodeSettingsOverrides(t *testing.T) {
	set := &model.Settings{
		Host: "panel.example.com", SNI: "panel.example.com",
		VLESSEnabled: true, TrojanEnabled: true, HysteriaEnabled: true, RealityEnabled: true,
		RealityPrivateKey: "panel-priv", RealityPublicKey: "panel-pub",
		XrayDNS: "8.8.8.8",
		Routing: model.RoutingConfig{
			BlockAds:     true,
			WarpDomains:  []string{"warp.example"},
			Lanes:        []model.EgressLane{{ID: "ru", Enabled: true}},
			RoutingOrder: []string{"ru", "direct"},
		},
	}
	no := false
	dns := "1.1.1.1"
	n := &model.Node{
		Host:              "nl1.example.com",
		RealityPrivateKey: "node-priv", RealityPublicKey: "node-pub",
		HysteriaEnabled: &no, // override: off on this node
		CertSelfSigned:  true,
		CertSHA256:      "deadbeef",
		XrayDNS:         &dns,
	}

	ns := nodeSettings(set, n)
	if ns.Host != "nl1.example.com" || ns.SNI != "nl1.example.com" {
		t.Fatalf("host/sni not overridden: %q/%q", ns.Host, ns.SNI)
	}
	if ns.RealityPrivateKey != "node-priv" || ns.RealityPublicKey != "node-pub" {
		t.Fatal("REALITY identity not overridden")
	}
	// Inherited protocol stays on; overridden one goes off.
	if !ns.VLESSEnabled || !ns.TrojanEnabled || !ns.RealityEnabled {
		t.Fatal("inherited protocols should stay enabled")
	}
	if ns.HysteriaEnabled {
		t.Fatal("hysteria override (off) not applied")
	}
	// Routing is the node's OWN (independent of the master): with no node routing,
	// it's empty — the master's rules are NOT inherited.
	if ns.Routing.BlockAds || len(ns.Routing.Lanes) != 0 {
		t.Fatalf("node routing should be empty (not inherited from master): %+v", ns.Routing)
	}
	// Egress is off by default for a node with no config.
	if ns.WarpEnabled || ns.OperaEnabled {
		t.Fatal("egress must be off by default on a node")
	}

	// A node WITH its own routing keeps its lanes (not stripped) and egress.
	rc := model.RoutingConfig{BlockAds: true, Lanes: []model.EgressLane{{ID: "ru", Enabled: true}}}
	n.Routing = &rc
	n.WarpEnabled = true
	n.OperaEnabled = true
	ns2 := nodeSettings(set, n)
	if !ns2.Routing.BlockAds || len(ns2.Routing.Lanes) != 1 {
		t.Fatalf("node's own routing not applied (lanes must survive): %+v", ns2.Routing)
	}
	if !ns2.WarpEnabled || !ns2.OperaEnabled {
		t.Fatal("node's own egress toggles not applied")
	}
	// TLS pin from the node's reported self-signed cert.
	if !ns.TLSInsecure || ns.TLSPinSHA256 != "deadbeef" {
		t.Fatalf("tls hints wrong: insecure=%v pin=%q", ns.TLSInsecure, ns.TLSPinSHA256)
	}
	// DNS override.
	if ns.XrayDNS != "1.1.1.1" {
		t.Fatalf("dns override = %q", ns.XrayDNS)
	}
	// The panel's own settings are untouched (shallow copy didn't mutate).
	if set.Host != "panel.example.com" || set.HysteriaEnabled != true {
		t.Fatal("nodeSettings mutated the global settings")
	}
}

func TestNodeDesiredStateHashStable(t *testing.T) {
	m := nodeTestManager(t)
	n, err := m.store.CreateNode("n1", "nl1.example.com", "nginx")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	s1, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatalf("desired state: %v", err)
	}
	s2, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatalf("desired state 2: %v", err)
	}
	if s1.Hash == "" || s1.Hash != s2.Hash {
		t.Fatalf("hash not stable: %q vs %q", s1.Hash, s2.Hash)
	}

	// Adding a working user changes the config → changes the hash.
	if _, err := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0); err != nil {
		t.Fatalf("create user: %v", err)
	}
	s3, err := m.NodeDesiredState(n)
	if err != nil {
		t.Fatalf("desired state 3: %v", err)
	}
	if s3.Hash == s1.Hash {
		t.Fatal("adding a user did not change the desired-state hash")
	}
}

func TestIngestNodeSyncIdempotent(t *testing.T) {
	m := nodeTestManager(t)
	u, _ := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0)
	n, _ := m.store.CreateNode("n1", "nl1.example.com", "")

	req := nodeapi.SyncRequest{
		ReportID: 5,
		Traffic:  []nodeapi.TrafficDelta{{UserID: u.ID, Up: 100, Down: 200}},
	}
	if _, err := m.IngestNodeSync(n, req); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// Re-fetch the node so LastReportID reflects the first ingest, then replay the
	// same report — it must NOT be counted again.
	n2, _ := m.store.GetNode(n.ID)
	if _, err := m.IngestNodeSync(n2, req); err != nil {
		t.Fatalf("ingest replay: %v", err)
	}
	got, _ := m.store.GetUser(u.ID)
	if got.UsedUp != 100 || got.UsedDown != 200 {
		t.Fatalf("replayed report double-counted: up=%d down=%d", got.UsedUp, got.UsedDown)
	}

	// A newer report advances the totals.
	req2 := nodeapi.SyncRequest{
		ReportID: 6,
		Traffic:  []nodeapi.TrafficDelta{{UserID: u.ID, Up: 10, Down: 20}},
	}
	n3, _ := m.store.GetNode(n.ID)
	if _, err := m.IngestNodeSync(n3, req2); err != nil {
		t.Fatalf("ingest 2: %v", err)
	}
	got2, _ := m.store.GetUser(u.ID)
	if got2.UsedUp != 110 || got2.UsedDown != 220 {
		t.Fatalf("new report not applied: up=%d down=%d", got2.UsedUp, got2.UsedDown)
	}
}

func TestNodeNameUniqueness(t *testing.T) {
	m := nodeTestManager(t)
	if _, err := m.CreateNode("US-1", "a.example.com"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// A second node with the same name (any case) is rejected — duplicate names would
	// collide Clash/sing-box tags and break the whole subscription.
	if _, err := m.CreateNode("us-1", "b.example.com"); err == nil {
		t.Fatal("duplicate node name should be rejected")
	}
	// A distinct name is fine.
	n2, err := m.CreateNode("US-2", "b.example.com")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	// Renaming n2 to collide is also rejected.
	if err := m.UpdateNode(n2.ID, store.NodeEdit{Name: "US-1", Host: "b.example.com"}); err == nil {
		t.Fatal("renaming to a duplicate should be rejected")
	}
}

func TestNodeWakeRegistry(t *testing.T) {
	r := newNodeRegistry()
	ch := r.wakeChan(1)
	select {
	case <-ch:
		t.Fatal("fresh wake channel should block")
	default:
	}
	r.wakeOne(1)
	select {
	case <-ch:
		// woken
	default:
		t.Fatal("wakeOne did not close the channel")
	}
	// A subsequent wakeChan hands out a fresh (open) channel.
	ch2 := r.wakeChan(1)
	select {
	case <-ch2:
		t.Fatal("replacement channel should block again")
	default:
	}
}
