package server

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// A held poll is answered with what its users have left when the answer goes — a
// limit changed during the hold included. Answered with nothing, it left the node
// watching nobody from the first held answer on.
func TestHeldNodePollAnswersTheQuotaLeftNow(t *testing.T) {
	rt, st := rolesTestRouter(t)
	u, err := st.CreateUser("capped", "uuid-capped", "pw", "tok-capped", 1000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, token := joinedNode(t, rt, "berlin")
	if err := st.SetNodeProtocols(id, true, false, false); err != nil {
		t.Fatal(err)
	}
	node, err := rt.mgr.GetNode(id)
	if err != nil || node == nil {
		t.Fatalf("get node: %v", err)
	}
	desired, err := rt.mgr.NodeDesiredState(node)
	if err != nil {
		t.Fatalf("desired state: %v", err)
	}

	held := make(chan nodeapi.SyncResponse, 1)
	go func() {
		held <- decodeSync(t, syncRequest(rt, token, nodeapi.SyncRequest{ConfigHash: desired.Hash, QuotaUsers: []int64{u.ID}}))
	}()
	select {
	case <-held:
		t.Fatal("a poll with nothing new was answered at once")
	case <-time.After(300 * time.Millisecond):
	}

	if err := st.SetUserQuota(u.ID, 600, 0); err != nil {
		t.Fatal(err)
	}
	if err := rt.mgr.SetNodeEnabled(id, true); err != nil { // wakes the held poll
		t.Fatal(err)
	}
	select {
	case resp := <-held:
		if resp.QuotaLeft[u.ID] != 600 {
			t.Fatalf("held answer carried quota %v, want user %d with the 600 set during the hold", resp.QuotaLeft, u.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wake did not reach the held poll")
	}
}

// A counted report marked as carrying someone past their quota is answered at once,
// so the traffic waiting behind it is not held; unmarked, the same report is held.
// The reports carry no traffic: one that does runs an enforcement pass, and here its
// reconcile wakes the poll by itself.
func TestCrossedNodeReportIsAnsweredAtOnce(t *testing.T) {
	rt, st := rolesTestRouter(t)
	id, token := joinedNode(t, rt, "berlin")
	if err := st.SetNodeProtocols(id, true, false, false); err != nil {
		t.Fatal(err)
	}
	node, err := rt.mgr.GetNode(id)
	if err != nil || node == nil {
		t.Fatalf("get node: %v", err)
	}
	desired, err := rt.mgr.NodeDesiredState(node)
	if err != nil {
		t.Fatalf("desired state: %v", err)
	}
	report := func(rid int64, crossed bool) nodeapi.SyncRequest {
		return nodeapi.SyncRequest{ConfigHash: desired.Hash, ReportID: rid, QuotaCrossed: crossed}
	}

	done := make(chan nodeapi.SyncResponse, 1)
	go func() { done <- decodeSync(t, syncRequest(rt, token, report(1, true))) }()
	select {
	case resp := <-done:
		if resp.AckReport != 1 {
			t.Fatalf("ack %d, want 1", resp.AckReport)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a report carrying a crossed quota was held")
	}

	held := make(chan nodeapi.SyncResponse, 1)
	go func() { held <- decodeSync(t, syncRequest(rt, token, report(2, false))) }()
	select {
	case <-held:
		t.Fatal("an ordinary report was answered at once")
	case <-time.After(300 * time.Millisecond):
	}
	if err := rt.mgr.SetNodeEnabled(id, true); err != nil { // let the held poll go
		t.Fatal(err)
	}
	<-held
}
