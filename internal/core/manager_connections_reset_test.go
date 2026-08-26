package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func testConnManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test-conn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{
		store: st,
		nodes: newNodeRegistry(),
	}
	return m
}

func TestResetConnectionsMaster(t *testing.T) {
	m := testConnManager(t)

	// 1. Mutate settings away from defaults
	if err := m.store.SetProtocolEnabled("vless", false); err != nil {
		t.Fatalf("disable vless: %v", err)
	}
	if err := m.store.SetProtocolNames("CustomVLESS", "CustomREALITY", "CustomHy2"); err != nil {
		t.Fatalf("set names: %v", err)
	}
	if err := m.store.SetFingerprints("chrome", "chrome"); err != nil {
		t.Fatalf("set fp: %v", err)
	}
	if err := m.store.SetHysteriaPorts(50000, 50000, 50010, "10-30"); err != nil {
		t.Fatalf("set hy ports: %v", err)
	}
	if err := m.store.SetRealityPorts(9443, "custom.donor.com"); err != nil {
		t.Fatalf("set reality: %v", err)
	}
	if err := m.store.SetAntiDPI(false, false, false, 60000); err != nil {
		t.Fatalf("set anti-dpi: %v", err)
	}

	// Add a custom inbound to master
	_, err := m.CreateInbound(context.Background(), model.Inbound{
		ServerID: model.LocalNodeID,
		Name:     "TestInbound",
		Protocol: "vless",
		Port:     12345,
		Opts: model.InboundOpts{
			Transport: "ws",
			Security:  "tls",
			Path:      "/ws-path",
		},
	})
	if err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	// 2. Perform ResetConnections
	status, err := m.ResetConnections()
	if err != nil {
		t.Fatalf("ResetConnections: %v", err)
	}

	// 3. Verify master status is back to factory defaults
	if status.HysteriaPort != 443 || status.HopStart != 443 || status.HopEnd != 443 {
		t.Errorf("expected HysteriaPort=443, got %d", status.HysteriaPort)
	}
	if status.RealityPort != 8443 || status.RealityDest != "max.ru" {
		t.Errorf("expected RealityPort=8443, RealityDest=max.ru, got port %d, dest %q", status.RealityPort, status.RealityDest)
	}
	if !status.TLSFragment || !status.TLSMin13 || !status.BlockQUIC {
		t.Errorf("expected all AntiDPI=true, got frag=%v, min13=%v, quic=%v", status.TLSFragment, status.TLSMin13, status.BlockQUIC)
	}

	for _, proto := range status.Protocols {
		if !proto.Enabled {
			t.Errorf("expected protocol %q enabled", proto.Key)
		}
		if proto.DisplayName != "" {
			t.Errorf("expected protocol %q display name to be empty, got %q", proto.Key, proto.DisplayName)
		}
		if proto.Fingerprint != "" && proto.Fingerprint != "firefox" {
			t.Errorf("expected fingerprint firefox, got %q", proto.Fingerprint)
		}
	}

	// Verify custom inbounds were cleared on master
	inbounds, err := m.store.Inbounds(model.LocalNodeID)
	if err != nil {
		t.Fatalf("read inbounds: %v", err)
	}
	if len(inbounds) != 0 {
		t.Errorf("expected 0 inbounds after reset, got %d", len(inbounds))
	}
}

func TestResetNodeConnections(t *testing.T) {
	m := testConnManager(t)

	node, err := m.store.CreateNode("edge-1", "edge1.example.com", "coming-soon")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	// Mutate node connections
	if err := m.store.SetNodeProtocols(node.ID, false, true, false); err != nil {
		t.Fatalf("set node protocols: %v", err)
	}
	if err := m.store.SetNodeRealityDest(node.ID, "donor.node.com"); err != nil {
		t.Fatalf("set node reality dest: %v", err)
	}

	// Add custom inbound to node
	_, err = m.CreateInbound(context.Background(), model.Inbound{
		ServerID: node.ID,
		Name:     "NodeInbound",
		Protocol: "vless",
		Port:     15432,
		Opts: model.InboundOpts{
			Transport: "ws",
			Security:  "tls",
			Path:      "/node-ws",
		},
	})

	if err != nil {
		t.Fatalf("create node inbound: %v", err)
	}

	// Perform ResetNodeConnections
	status, err := m.ResetNodeConnections(node.ID)
	if err != nil {
		t.Fatalf("ResetNodeConnections: %v", err)
	}

	for _, proto := range status.Protocols {
		if !proto.Enabled {
			t.Errorf("expected node protocol %q enabled", proto.Key)
		}
	}
	if status.HysteriaPort != 443 || status.RealityPort != 8443 {
		t.Errorf("expected reset ports 443 / 8443, got %d / %d", status.HysteriaPort, status.RealityPort)
	}

	// Verify node inbounds were cleared
	inbounds, err := m.store.Inbounds(node.ID)
	if err != nil {
		t.Fatalf("read node inbounds: %v", err)
	}
	if len(inbounds) != 0 {
		t.Errorf("expected 0 node inbounds after reset, got %d", len(inbounds))
	}
}
