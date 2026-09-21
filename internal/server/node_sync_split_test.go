package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/nodestate"
)

// An agent that speaks parts is sent its state in parts over the wire — compressed when
// it accepts that, with the state gate let go once encoded — and a change after a hold
// as only the change.
func TestSplitStateOverTheWire(t *testing.T) {
	rt, st := rolesTestRouter(t)
	id, token := joinedNode(t, rt, "berlin")
	if _, err := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.BlockIP(model.BlockedIP{IP: "198.51.100.7", Reason: "asn", At: now.Unix(), Until: now.Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(nodeapi.SyncRequest{DeltaRev: nodeapi.DeltaRev})
	r := httptest.NewRequest("POST", "/v1/sync", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	rt.handleNodeSync(rec, r)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("a state in parts was not compressed for a node accepting gzip")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var first nodeapi.SyncResponse
	if err := json.NewDecoder(zr).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if !first.Changed || first.Split == nil || first.State != nil || first.Delta != nil {
		t.Fatalf("first sync: changed %v, split %v, state %v, delta %v", first.Changed, first.Split != nil, first.State != nil, first.Delta != nil)
	}
	held, err := nodestate.FromSplit(first.Split)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := nodestate.Decode(held)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts.Blocked.IPs) != 1 {
		t.Fatalf("blocked addresses sent: %v", parts.Blocked.IPs)
	}

	// The gate was let go once the response was encoded.
	n, _ := st.GetNode(id)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	push, done, err := rt.mgr.NodeSyncPush(ctx, n, core.NodeHas{DeltaRev: nodeapi.DeltaRev, ConfigHash: "not-this"})
	if err != nil || push.Split == nil {
		t.Fatalf("the gate was not free after the push: %v", err)
	}
	done()

	// Held with the state it has; lifting the block wakes it with only the change.
	answer := make(chan nodeapi.SyncResponse, 1)
	go func() {
		answer <- decodeSync(t, syncRequest(rt, token, nodeapi.SyncRequest{
			DeltaRev: nodeapi.DeltaRev, StateTag: parts.Held.Tag, ConfigHash: parts.Held.Hash,
		}))
	}()
	time.Sleep(300 * time.Millisecond) // parked
	if gone, err := rt.mgr.UnblockIP("198.51.100.7"); err != nil || !gone {
		t.Fatalf("unblock: %v %v", gone, err)
	}
	select {
	case resp := <-answer:
		if !resp.Changed || resp.Delta == nil || resp.Split != nil || resp.State != nil {
			t.Fatalf("the woken poll: changed %v, delta %v, split %v", resp.Changed, resp.Delta != nil, resp.Split != nil)
		}
		next, err := nodestate.Apply(parts, resp.Delta)
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Blocked.IPs) != 0 || len(resp.Delta.Upsert) != 0 || len(resp.Delta.Remove) != 0 {
			t.Errorf("the change: blocks %v, rows %d, removals %d", next.Blocked.IPs, len(resp.Delta.Upsert), len(resp.Delta.Remove))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the held poll was never answered")
	}
}
