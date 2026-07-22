package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/abuse"
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

// TestRoutingPropagatesMasterAndNode is the end-to-end routing check: the master's
// routing goes into the master's Xray config, a node's OWN routing goes into the exact
// JSON the panel pushes to that node (NodeDesiredState), and neither inherits the
// other's rule. This is the "прокидывает роут на мастере и нодах" guarantee.
func TestRoutingPropagatesMasterAndNode(t *testing.T) {
	m := nodeTestManager(t)
	const masterMark = "master-route-marker.example"
	const nodeMark = "node-route-marker.example"

	// Master routing: block masterMark. Persist it as the panel's global routing.
	if err := m.store.SetRoutingConfig(model.RoutingConfig{BlockDomains: []string{masterMark}}); err != nil {
		t.Fatalf("set master routing: %v", err)
	}

	// A node with its OWN, different routing: block nodeMark.
	n, err := m.store.CreateNode("route-node", "nl.example.com", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	yes := true
	nodeRC := model.RoutingConfig{BlockDomains: []string{nodeMark}}
	if err := m.store.UpdateNode(n.ID, store.NodeEdit{
		Name: n.Name, Host: n.Host, VLESS: &yes, Routing: &nodeRC,
	}); err != nil {
		t.Fatalf("set node routing: %v", err)
	}
	n2, _ := m.store.GetNode(n.ID)

	set, _ := m.store.GetSettings()
	// The bare test store has no cert on disk; the master generator needs cert paths
	// set (the node path uses sentinels internally). Only the paths matter here.
	set.CertPath, set.KeyPath = "/tmp/cert.pem", "/tmp/key.pem"
	users, _ := m.store.WorkingUsers(time.Now().Unix())

	// Master config = what the panel's own Xray runs.
	masterCfg, err := xray.Generate(set, users, m.opts, nil)
	if err != nil {
		t.Fatalf("generate master config: %v", err)
	}
	mj, _ := json.Marshal(masterCfg)

	// Node config = exactly the JSON the panel pushes to that node.
	state, err := m.NodeDesiredState(n2)
	if err != nil {
		t.Fatalf("node desired state: %v", err)
	}
	nj := state.XrayConfig

	// Master carries its OWN route and NOT the node's.
	if !bytes.Contains(mj, []byte(masterMark)) {
		t.Fatal("master routing rule missing from the master's config")
	}
	if bytes.Contains(mj, []byte(nodeMark)) {
		t.Fatal("a node's route leaked into the master config")
	}
	// Node carries its OWN route and does NOT inherit the master's.
	if !bytes.Contains(nj, []byte(nodeMark)) {
		t.Fatal("node routing rule missing from the config the panel pushes to the node")
	}
	if bytes.Contains(nj, []byte(masterMark)) {
		t.Fatal("the master's route leaked into the node config (a node must not inherit master routing)")
	}
}

func TestNodeSettingsOverrides(t *testing.T) {
	set := &model.Settings{
		Host: "panel.example.com", SNI: "panel.example.com",
		VLESSEnabled: true, TrojanEnabled: true, HysteriaEnabled: true, RealityEnabled: true,
		RealityPrivateKey: "panel-priv", RealityPublicKey: "panel-pub",
		XrayDNS: "8.8.8.8",
		// Master-only local proxy: must NEVER leak into a node's config.
		ProxyModeEnabled: true, ProxyModeType: "socks", ProxyModePort: 1080,
		ProxyModeUser: "master-user", ProxyModePass: "master-pass",
		Routing: model.RoutingConfig{
			BlockAds:     true,
			WarpDomains:  []string{"warp.example"},
			Lanes:        []model.EgressLane{{ID: "ru", Enabled: true}},
			RoutingOrder: []string{"ru", "direct"},
		},
	}
	yes := true
	no := false
	dns := "1.1.1.1"
	n := &model.Node{
		Host:              "nl1.example.com",
		RealityPrivateKey: "node-priv", RealityPublicKey: "node-pub",
		// A node's protocols are its OWN (no inheritance): explicit on/off per node.
		VLESSEnabled: &yes, TrojanEnabled: &yes, RealityEnabled: &yes,
		HysteriaEnabled: &no,
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
	// Protocols are the node's own: its enabled ones on, its disabled one off.
	if !ns.VLESSEnabled || !ns.TrojanEnabled || !ns.RealityEnabled {
		t.Fatal("node's own enabled protocols should be on")
	}
	if ns.HysteriaEnabled {
		t.Fatal("node's own disabled protocol should be off")
	}
	// No inheritance: an unset protocol is OFF even though the master has it on.
	bare := &model.Node{Host: "n2.example.com"}
	nsBare := nodeSettings(set, bare)
	if nsBare.VLESSEnabled || nsBare.TrojanEnabled || nsBare.HysteriaEnabled || nsBare.RealityEnabled {
		t.Fatal("a node with unset protocols must be all-off (no inheritance from master)")
	}
	// No DNS inheritance either: unset ⇒ empty, not the master's "8.8.8.8".
	if nsBare.XrayDNS != "" {
		t.Fatalf("unset node DNS must be empty (no inheritance), got %q", nsBare.XrayDNS)
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
	// Proxy mode is master-only: the inbound and the master's credentials must never
	// reach a node's config.
	if ns.ProxyModeEnabled || ns.ProxyModePort != 0 || ns.ProxyModeUser != "" || ns.ProxyModePass != "" {
		t.Fatalf("master proxy mode leaked into node config: %+v", ns)
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
	// A node's protocols are its own and default off; enable one so working users
	// actually land in its config (else adding a user changes nothing).
	yes := true
	n.VLESSEnabled = &yes

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

// abuseNodeManager builds a node-test manager wired with an abuse matcher primed
// with the given known-bad addresses/ranges.
func abuseNodeManager(t *testing.T, badIPs []string) *Manager {
	t.Helper()
	m := nodeTestManager(t)
	m.abusePending = make(map[abusePendingKey]store.AbuseHit)
	m.abuseAlerted = make(map[abuseAlertKey]struct{})
	st := abuse.NewStore(t.TempDir())
	st.Matcher().SetIP(abuse.CatBadIP, badIPs)
	m.abuse = st
	return m
}

// TestIngestNodeAbuseMatches: a node's reported destinations are matched against the
// blocklists, attributed to the reporting node.
func TestIngestNodeAbuseMatches(t *testing.T) {
	m := abuseNodeManager(t, []string{"203.0.113.0/24"})
	u, _ := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0)
	n, _ := m.store.CreateNode("n1", "nl1.example.com", "")

	if _, err := m.IngestNodeSync(n, nodeapi.SyncRequest{
		ReportID: 1,
		Sites: []nodeapi.SiteSample{
			{UserID: u.ID, Host: "203.0.113.5", Count: 3},
			{UserID: u.ID, Host: "8.8.8.8", Count: 40}, // clean, ignored
		},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	m.FlushAbuse()
	rows, _ := m.store.AbuseByUser(u.ID, 10)
	if len(rows) != 1 || rows[0].Domain != "203.0.113.5" || rows[0].NodeID != n.ID {
		t.Fatalf("node match wrong: %+v", rows)
	}
}

// TestIngestNodeAbuseRejectsUnknownUsers: a node must not be able to buffer matches
// against fabricated user ids — the EXISTS guard drops them at write time, but not
// before they cost buffer space.
func TestIngestNodeAbuseRejectsUnknownUsers(t *testing.T) {
	m := abuseNodeManager(t, []string{"203.0.113.0/24"})
	u, _ := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0)
	n, _ := m.store.CreateNode("n1", "nl1.example.com", "")

	rows := []nodeapi.SiteSample{{UserID: u.ID, Host: "203.0.113.5", Count: 1}}
	for i := range 5000 { // ids that do not exist
		rows = append(rows, nodeapi.SiteSample{
			UserID: int64(1_000_000 + i), Host: "203.0.113.5", Count: 100,
		})
	}
	if _, err := m.IngestNodeSync(n, nodeapi.SyncRequest{ReportID: 1, Sites: rows}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// Only the real user's match was buffered.
	if got := len(m.abusePending); got != 1 {
		t.Fatalf("buffered %d matches, want 1 (fabricated users must be dropped)", got)
	}
}

// TestIngestNodeAbuseTruncates: an oversized batch is truncated so nothing scales
// with what the node chose to send.
func TestIngestNodeAbuseTruncates(t *testing.T) {
	m := abuseNodeManager(t, blMany(maxNodeSiteRows*3))
	u, _ := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0)
	n, _ := m.store.CreateNode("n1", "nl1.example.com", "")

	rows := make([]nodeapi.SiteSample, 0, maxNodeSiteRows*3)
	for i := range maxNodeSiteRows * 3 {
		rows = append(rows, nodeapi.SiteSample{
			UserID: u.ID, Host: fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256), Count: 1,
		})
	}
	if _, err := m.IngestNodeSync(n, nodeapi.SyncRequest{ReportID: 1, Sites: rows}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// The per-sync abuse cap bounds it further, but the point is it never scales with
	// the batch the node sent.
	if got := len(m.abusePending); got > maxNodeSiteRows {
		t.Fatalf("buffered %d from an oversized batch", got)
	}
}

// TestIngestNodeSitesBoundsAbuseContribution: one node sync must not be able to
// fill the shared match buffer with blocklisted domains and starve the master's own
// locally-observed matches (the feeds are public, so a node can pick known hits).
func TestIngestNodeSitesBoundsAbuseContribution(t *testing.T) {
	m := nodeTestManager(t)
	m.abusePending = make(map[abusePendingKey]store.AbuseHit)
	m.abuseAlerted = make(map[abuseAlertKey]struct{})
	st := abuse.NewStore(t.TempDir())
	// Every host the node sends is a known-bad match.
	st.Matcher().SetIP(abuse.CatBadIP, blMany(maxNodeSiteRows))
	m.abuse = st

	u, _ := m.store.CreateUser("u1", "uuid-u1", "pw", "tok-u1", 0, 0, 0)
	n, _ := m.store.CreateNode("n1", "nl1.example.com", "")

	rows := make([]nodeapi.SiteSample, 0, maxNodeSiteRows)
	for i := range maxNodeSiteRows {
		rows = append(rows, nodeapi.SiteSample{
			UserID: u.ID, Host: fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256), Count: 1,
		})
	}
	if _, err := m.IngestNodeSync(n, nodeapi.SyncRequest{ReportID: 1, Sites: rows}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got := len(m.abusePending); got > abuseNodeMax {
		t.Fatalf("one sync buffered %d matches, node cap is %d — buffer starvation open", got, abuseNodeMax)
	}
	// And the master's own local match still finds room afterward.
	m.recordAbuse(u.ID, "10.0.0.0")
	if len(m.abusePending) == 0 {
		t.Fatal("local match had no room after a node flood")
	}
}

// blMany builds n distinct known-bad /32s, for the flood/starvation tests.
func blMany(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256))
	}
	return out
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
