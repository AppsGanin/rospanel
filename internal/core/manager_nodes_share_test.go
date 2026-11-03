package core

import (
	"errors"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A node save applies the shared-port rule only while the node's REALITY lane is on.
// A disabled lane is not generated at all, so switching it off on the shared port has
// to land: the leftovers it keeps — port and donor list — are carried, not refused.
func TestNodeSharedPortConflictOnlyAppliesWhileRealityIsOn(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	n, err := m.store.CreateNode("edge", "edge.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: "1.1.0", XrayVersion: "26.10.10", XrayRunning: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeRealityDest(n.ID, "edge.example.com"); err != nil {
		t.Fatal(err)
	}

	u := DefaultConnections()
	u.RealityPort, u.RealityDest = 443, "edge.example.com"

	var ve *ValidationError
	u.Protocols["reality"] = true
	if err := m.ApplyNodeConnections(n.ID, u); !errors.As(err, &ve) || ve.Code != "err.realityOwnNameOn443" {
		t.Fatalf("node REALITY on the shared port masking as its own name: %v", err)
	}

	u.Protocols["reality"] = false
	if err := m.ApplyNodeConnections(n.ID, u); err != nil {
		t.Fatalf("switching the node lane off refused: %v", err)
	}
}
