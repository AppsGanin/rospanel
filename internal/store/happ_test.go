package store

import (
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/happ"
)

func openHappStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "happ.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHappSubscriptionAndNodes(t *testing.T) {
	st := openHappStore(t)

	// 1. Create subscription
	subID, err := st.CreateHappSubscription("MySub", "https://example.com/sub")
	if err != nil {
		t.Fatalf("CreateHappSubscription failed: %v", err)
	}
	if subID <= 0 {
		t.Fatalf("expected positive subID, got %d", subID)
	}

	// 2. Get subscription
	sub, err := st.GetHappSubscription(subID)
	if err != nil || sub == nil {
		t.Fatalf("GetHappSubscription failed: %v", err)
	}
	if sub.Name != "MySub" || sub.URL != "https://example.com/sub" || !sub.Enabled || sub.UpdateIntervalMin != 59 {
		t.Fatalf("unexpected sub fields: %+v", sub)
	}

	// 3. List enabled subscription IDs
	ids, err := st.ListEnabledHappSubscriptionIDs()
	if err != nil || len(ids) != 1 || ids[0] != subID {
		t.Fatalf("ListEnabledHappSubscriptionIDs failed: %v, ids=%v", err, ids)
	}

	// 4. Upsert nodes
	nodes := []happ.Node{
		{
			IdentityKey: happ.IdentityKeyFor(subID, "vless", "nl.example.com", 443, "uuid1"),
			Name:        "NL-1",
			Protocol:    "vless",
			Host:        "nl.example.com",
			Port:        443,
			URI:         "vless://uuid1@nl.example.com:443#NL-1",
		},
		{
			IdentityKey: happ.IdentityKeyFor(subID, "trojan", "fi.example.com", 443, "pass1"),
			Name:        "FI-1",
			Protocol:    "trojan",
			Host:        "fi.example.com",
			Port:        443,
			URI:         "trojan://pass1@fi.example.com:443#FI-1",
		},
	}
	added, updated, err := st.UpsertHappNodesFull(subID, nodes)
	if err != nil {
		t.Fatalf("UpsertHappNodesFull failed: %v", err)
	}
	if added != 2 || updated != 0 {
		t.Fatalf("expected added=2 updated=0, got added=%d updated=%d", added, updated)
	}

	// 5. Update fetch status
	if err := st.UpdateHappSubscriptionFetch(subID, 2, ""); err != nil {
		t.Fatalf("UpdateHappSubscriptionFetch failed: %v", err)
	}

	// 6. List nodes
	list, err := st.ListHappNodes(subID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListHappNodes failed: %v, len=%d", err, len(list))
	}

	// 7. Toggle enabled
	nodeID := list[0].ID
	if err := st.SetHappNodeEnabled(nodeID, false); err != nil {
		t.Fatalf("SetHappNodeEnabled failed: %v", err)
	}

	// 8. List enabled nodes (should now be 1)
	enabledNodes, err := st.ListEnabledHappNodes()
	if err != nil || len(enabledNodes) != 1 {
		t.Fatalf("ListEnabledHappNodes failed: %v, len=%d", err, len(enabledNodes))
	}
	if enabledNodes[0].ID == nodeID {
		t.Fatalf("disabled node still returned in ListEnabledHappNodes")
	}

	// 9. Delete single node
	if err := st.DeleteHappNode(nodeID); err != nil {
		t.Fatalf("DeleteHappNode failed: %v", err)
	}
	allNodes, err := st.ListAllHappNodes()
	if err != nil || len(allNodes) != 1 {
		t.Fatalf("ListAllHappNodes after delete failed: %v, len=%d", err, len(allNodes))
	}

	// 10. Delete subscription (cascade delete remaining nodes)
	if err := st.DeleteHappSubscription(subID); err != nil {
		t.Fatalf("DeleteHappSubscription failed: %v", err)
	}
	allNodesAfterSubDelete, err := st.ListAllHappNodes()
	if err != nil || len(allNodesAfterSubDelete) != 0 {
		t.Fatalf("expected 0 nodes after subscription cascade delete, got %d", len(allNodesAfterSubDelete))
	}
}
