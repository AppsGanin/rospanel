package server

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// A node draining a traffic backlog sends it in chunks and says so. Each counted chunk
// is answered on the spot so the next one follows straight away; without the flag, or
// with nothing in the report, the poll is held as usual — otherwise the flag alone
// would turn a node into a tight loop against the panel.
func TestTrafficBacklogChunkIsAnsweredAtOnce(t *testing.T) {
	t.Parallel()
	rt, _ := rolesTestRouter(t)
	id, token := joinedNode(t, rt, "berlin")
	node, err := rt.mgr.GetNode(id)
	if err != nil || node == nil {
		t.Fatalf("get node: %v", err)
	}
	desired, err := rt.mgr.NodeDesiredState(node)
	if err != nil {
		t.Fatalf("desired state: %v", err)
	}
	// Traffic for an id with no user: counted as nothing, acked all the same. A real
	// user would do, but creating one schedules a user sync that wakes every node a
	// moment later, and that wake would release a wrongly held poll just in time to
	// look answered at once.
	const uid = 424242
	chunk := nodeapi.SyncRequest{
		ConfigHash: desired.Hash, ReportID: 1, TrafficMore: true,
		Traffic: []nodeapi.TrafficDelta{{UserID: uid, Up: 100, Down: 200}},
	}
	done := make(chan nodeapi.SyncResponse, 1)
	go func() { done <- decodeSync(t, syncRequest(rt, token, chunk)) }()
	select {
	case resp := <-done:
		if resp.AckReport != 1 {
			t.Fatalf("chunk acked as %d, want 1", resp.AckReport)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a chunk of a traffic backlog was held — the rest waits a whole hold per chunk")
	}

	// A chunk of connection samples with more waiting is answered at once as well.
	samples := nodeapi.SyncRequest{ConfigHash: desired.Hash, ConnsMore: true,
		Conns: []nodeapi.ConnSample{{Email: "u424242", IP: "198.51.100.7"}}}
	quick := make(chan struct{}, 1)
	go func() { syncRequest(rt, token, samples); quick <- struct{}{} }()
	select {
	case <-quick:
	case <-time.After(2 * time.Second):
		t.Fatal("a chunk of connection samples was held — the rest waits a whole hold per chunk")
	}

	for _, c := range []struct {
		name string
		req  nodeapi.SyncRequest
	}{
		{"the last chunk", nodeapi.SyncRequest{ConfigHash: desired.Hash, ReportID: 2,
			Traffic: []nodeapi.TrafficDelta{{UserID: uid, Up: 1, Down: 1}}}},
		{"the flag with no traffic", nodeapi.SyncRequest{ConfigHash: desired.Hash, TrafficMore: true}},
		{"the samples flag with no samples", nodeapi.SyncRequest{ConfigHash: desired.Hash, ConnsMore: true}},
		{"the last chunk of samples", nodeapi.SyncRequest{ConfigHash: desired.Hash,
			Conns: []nodeapi.ConnSample{{Email: "u424242", IP: "198.51.100.9"}}}},
	} {
		held := make(chan struct{}, 1)
		go func() { syncRequest(rt, token, c.req); held <- struct{}{} }()
		select {
		case <-held:
			t.Fatalf("%s returned immediately instead of being held", c.name)
		case <-time.After(300 * time.Millisecond):
		}
		if err := rt.mgr.SetNodeEnabled(id, true); err != nil { // wakes the parked poll
			t.Fatal(err)
		}
		select {
		case <-held:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the wake did not release the held poll", c.name)
		}
	}
}

// An agent from before chunking may already hold a batch over the old 1 MB cap, and it
// resends that same batch forever. The panel has to take it, or the node never syncs
// again — not even to receive the update that would chunk it.
func TestOversizedTrafficBatchFromAnOlderAgentIsAccepted(t *testing.T) {
	t.Parallel()
	rt, _ := rolesTestRouter(t)
	_, token := joinedNode(t, rt, "berlin")
	req := nodeapi.SyncRequest{ReportID: 1}
	for i := int64(1); i <= 30000; i++ {
		req.Traffic = append(req.Traffic, nodeapi.TrafficDelta{UserID: 100000 + i, Up: 12_345_678, Down: 123_456_789})
	}
	rec := syncRequest(rt, token, req)
	if rec.Code != 200 {
		t.Fatalf("a %d-row batch was refused with %d", len(req.Traffic), rec.Code)
	}
	if resp := decodeSync(t, rec); resp.AckReport != 1 {
		t.Fatalf("oversized batch acked as %d, want 1", resp.AckReport)
	}
}
