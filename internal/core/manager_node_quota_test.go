package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// TestNodeSyncAnswersTheQuotaLeft: a node asking about its active users hears what
// each with a quota has left, in its own bytes — a node that counts double gets half —
// counted after the traffic its report carried. Users with no quota, or none left, and
// ids that are nobody, are not in the answer.
func TestNodeSyncAnswersTheQuotaLeft(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	capped, _ := m.store.CreateUser("capped", "uuid-capped", "pw", "tok-capped", 1000, 0, 0)
	spent, _ := m.store.CreateUser("spent", "uuid-spent", "pw", "tok-spent", 100, 0, 0)
	free, _ := m.store.CreateUser("free", "uuid-free", "pw", "tok-free", 0, 0, 0)
	n := servingNode(t, m, "n1", "nl1.example.com")
	n.TrafficCoefficient = 2

	resp, err := m.IngestNodeSync(n, nodeapi.SyncRequest{
		ReportID: 1,
		Traffic: []nodeapi.TrafficDelta{
			{UserID: capped.ID, Up: 100, Down: 100}, // 400 of 1000 at ×2
			{UserID: spent.ID, Up: 50, Down: 50},    // 200 of 100
		},
		QuotaUsers: []int64{capped.ID, spent.ID, free.ID, 999999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.QuotaLeft) != 1 || resp.QuotaLeft[capped.ID] != 300 {
		t.Fatalf("quota left %v, want only user %d with 300 bytes of this node's traffic", resp.QuotaLeft, capped.ID)
	}
}

// TestNodeSyncEnforcesACrossedQuotaAtOnce: a report the node marks as carrying a user
// past their quota is enforced at once, not with the batch the fleet's reports share.
func TestNodeSyncEnforcesACrossedQuotaAtOnce(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	m.reconcileCh = make(chan struct{}, 1)
	capped, _ := m.store.CreateUser("capped", "uuid-capped", "pw", "tok-capped", 1000, 0, 0)
	n := servingNode(t, m, "n1", "nl1.example.com")
	working, err := m.store.WorkingCredentials(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	m.setApplied(working)

	if _, err := m.IngestNodeSync(n, nodeapi.SyncRequest{
		ReportID:     1,
		Traffic:      []nodeapi.TrafficDelta{{UserID: capped.ID, Up: 600, Down: 600}},
		QuotaCrossed: true,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.reconcileCh:
	case <-time.After(trafficEnforceDelay / 2):
		t.Fatal("a report marked as crossing a quota waited for the batch")
	}
	m.wg.Wait()
}
