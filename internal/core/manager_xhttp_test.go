package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// The Connections editor switches VLESS-XHTTP-TLS and names it like any lane, and shows
// its path; the switch decides whether xhttp-in is generated.
func TestXHTTPSwitchAndName(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	if err := m.store.EnsureXHTTPPath(auth.RandomXHTTPPath); err != nil {
		t.Fatal(err)
	}
	row := func() ConnInfo {
		set, err := m.store.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range buildConnectionsStatus(set).Protocols {
			if p.Key == "xhttp" {
				return p
			}
		}
		t.Fatal("no xhttp row in the editor")
		return ConnInfo{}
	}
	hasInbound := func() bool {
		set, err := m.store.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		set.CertPath, set.KeyPath = "/c.pem", "/k.pem"
		opts, err := m.genOptsFor(model.LocalNodeID)
		if err != nil {
			t.Fatal(err)
		}
		opts.PanelDest = "127.0.0.1:8080"
		cfg, err := xray.Generate(set, []model.User{{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}}, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _, ok := xray.XHTTPRoute(cfg)
		return ok
	}

	if r := row(); !r.Enabled || len(r.Note) < 1+model.MinXHTTPPathLen || !hasInbound() {
		t.Fatalf("on by default with a path: %+v, inbound %v", r, hasInbound())
	}
	u := DefaultConnections()
	u.Protocols["reality"] = false // the live donor probe stays out of the test
	u.Protocols["xhttp"] = false
	if err := m.ApplyConnections(u); err != nil {
		t.Fatal(err)
	}
	if row().Enabled || hasInbound() {
		t.Fatal("switched off, the lane is still on")
	}
	u.Protocols["xhttp"] = true
	u.Names = map[string]string{"xhttp": "XHTTP NL"}
	if err := m.ApplyConnections(u); err != nil {
		t.Fatal(err)
	}
	if r := row(); !r.Enabled || r.DisplayName != "XHTTP NL" || !hasInbound() {
		t.Fatalf("switched on and named: %+v", r)
	}
	// The path is made once: a second boot keeps it, or every link would break.
	before := row().Note
	if err := m.store.EnsureXHTTPPath(auth.RandomXHTTPPath); err != nil || row().Note != before {
		t.Fatalf("the path changed on a second boot: %q → %q (%v)", before, row().Note, err)
	}
}

// A node's agent has to route the path before the lane reaches its users' profiles.
func TestNodeXHTTPNeedsTheAgent(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	if err := m.store.EnsureXHTTPPath(auth.RandomXHTTPPath); err != nil {
		t.Fatal(err)
	}
	n, err := m.CreateNode("edge", "edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	report := func(serves bool) {
		if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
			LastSeen: time.Now().Unix(), NodeVersion: "test", XrayRunning: true,
			CertSHA256: "aa", ServesXHTTP: serves,
		}); err != nil {
			t.Fatal(err)
		}
	}
	lane := func() bool {
		sets, err := m.NodeLinkSettings()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range sets {
			if s.ServerID == n.ID {
				return s.XHTTPOn()
			}
		}
		t.Fatal("the node is not in the list")
		return false
	}
	report(false)
	if lane() {
		t.Fatal("handed out for an agent that never said it routes the path")
	}
	report(true)
	if !lane() {
		t.Fatal("not handed out once the agent routes the path")
	}
}
