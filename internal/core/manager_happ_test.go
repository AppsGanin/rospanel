package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/happ"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
	"github.com/Shu1t3/rospanel-shu1t3/internal/xray"
)

func happTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "happ_mgr.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{
		store:       st,
		opts:        xray.Options{PanelDest: "127.0.0.1:8080"},
		tz:          time.Local,
		applied:     map[int64]struct{}{},
		reconcileCh: make(chan struct{}, 1),
	}
}

func TestManagerHappLifecycle(t *testing.T) {
	m := happTestManager(t)

	// 1. Create a subscription directly in store
	subID, err := m.store.CreateHappSubscription("TestSub", "https://example.com/sub")
	if err != nil {
		t.Fatalf("CreateHappSubscription: %v", err)
	}

	// 2. Add some nodes
	nodes := []happ.Node{
		{
			IdentityKey: happ.IdentityKeyFor(subID, "vless", "nl.example.com", 443, "uuid-1"),
			Name:        "NL-VLESS",
			Protocol:    "vless",
			Host:        "nl.example.com",
			Port:        443,
			URI:         "vless://uuid-1@nl.example.com:443?type=tcp&security=tls#NL-VLESS",
		},
		{
			IdentityKey: happ.IdentityKeyFor(subID, "trojan", "fi.example.com", 443, "pass-1"),
			Name:        "FI-Trojan",
			Protocol:    "trojan",
			Host:        "fi.example.com",
			Port:        443,
			URI:         "trojan://pass-1@fi.example.com:443#FI-Trojan",
		},
	}
	_, _, err = m.store.UpsertHappNodesFull(subID, nodes)
	if err != nil {
		t.Fatalf("UpsertHappNodesFull: %v", err)
	}

	// 3. ListHappSubscriptions
	subs, err := m.ListHappSubscriptions()
	if err != nil || len(subs) != 1 {
		t.Fatalf("ListHappSubscriptions failed: %v, len=%d", err, len(subs))
	}

	// 4. ListAllHappNodes
	allNodes, err := m.ListAllHappNodes()
	if err != nil || len(allNodes) != 2 {
		t.Fatalf("ListAllHappNodes failed: %v, len=%d", err, len(allNodes))
	}

	// 5. HappOutbounds - both enabled by default
	outbounds, err := m.HappOutbounds()
	if err != nil || len(outbounds) != 2 {
		t.Fatalf("HappOutbounds failed: %v, len=%d", err, len(outbounds))
	}

	// 6. Disable one node
	nodeToDisable := allNodes[0].ID
	if err := m.SetHappNodeEnabled(nodeToDisable, false); err != nil {
		t.Fatalf("SetHappNodeEnabled: %v", err)
	}

	// Outbounds count should now be 1
	outboundsAfterDisable, err := m.HappOutbounds()
	if err != nil || len(outboundsAfterDisable) != 1 {
		t.Fatalf("HappOutbounds after disable: %v, len=%d", err, len(outboundsAfterDisable))
	}

	// 7. Delete node
	if err := m.DeleteHappNode(allNodes[1].ID); err != nil {
		t.Fatalf("DeleteHappNode: %v", err)
	}
	remainingNodes, err := m.ListAllHappNodes()
	if err != nil || len(remainingNodes) != 1 {
		t.Fatalf("ListAllHappNodes after delete: len=%d", len(remainingNodes))
	}

	// 8. Delete subscription
	if err := m.DeleteHappSubscription(subID); err != nil {
		t.Fatalf("DeleteHappSubscription: %v", err)
	}
	finalNodes, err := m.ListAllHappNodes()
	if err != nil || len(finalNodes) != 0 {
		t.Fatalf("expected 0 nodes after sub delete: len=%d", len(finalNodes))
	}
}
