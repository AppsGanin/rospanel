package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Only the servers behind the panel's version are sent to update — not one already on
// it, ahead of it, never seen, or reporting no version.
func TestRequestOutdatedNodesUpdate(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	now := time.Now().Unix()
	versions := map[string]string{"old": "4.0.0", "same": "4.1.0", "ahead": "4.2.0", "blank": ""}
	ids := map[string]int64{}
	for name, v := range versions {
		n, err := m.CreateNode(name, name+".example.com")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = n.ID
		if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{LastSeen: now, NodeVersion: v}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.CreateNode("never", "never.example.com"); err != nil { // never synced
		t.Fatal(err)
	}
	n, err := m.RequestOutdatedNodesUpdate("4.1.0")
	if err != nil || n != 1 {
		t.Fatalf("asked %d, %v; want only the old one", n, err)
	}
	if c, _ := m.store.NodeCommand(ids["old"], nodeCmdUpdate); c == nil {
		t.Fatal("the lagging server got no update command")
	}
	for _, name := range []string{"same", "ahead", "blank"} {
		if c, _ := m.store.NodeCommand(ids[name], nodeCmdUpdate); c != nil {
			t.Fatalf("%s was sent to update", name)
		}
	}
	if err := m.SaveAutoUpdate("not a cron", true); !isCode(err, "err.badCron") {
		t.Fatalf("a bad schedule was saved: %v", err)
	}
	if err := m.SaveAutoUpdate("* * * * *", true); !isCode(err, "err.autoUpdateTooOften") {
		t.Fatalf("an every-minute schedule was saved: %v", err)
	}
	for _, ok := range []string{"0 * * * *", "0 4 * * *", "30 3 * * 1"} {
		if err := m.SaveAutoUpdate(ok, true); err != nil {
			t.Fatalf("%q refused: %v", ok, err)
		}
	}
}
