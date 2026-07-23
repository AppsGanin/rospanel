package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// joinedNode registers a node and completes its join, returning its id and the
// bearer token its agent would then sync with.
func joinedNode(t *testing.T, rt *Router, name string) (int64, string) {
	t.Helper()
	n, err := rt.mgr.CreateNode(name, "de.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	node, token, err := rt.mgr.Store().ConsumeJoinToken(n.RawJoinToken)
	if err != nil || node == nil {
		t.Fatalf("join node: %v", err)
	}
	return node.ID, token
}

// syncRequest posts one sync as the node and returns the recorder. Blocking: the
// panel may hold it.
func syncRequest(rt *Router, token string, body nodeapi.SyncRequest) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/v1/sync", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	rt.handleNodeSync(rec, r)
	return rec
}

func decodeSync(t *testing.T, rec *httptest.ResponseRecorder) nodeapi.SyncResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var resp nodeapi.SyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("sync body: %v (%s)", err, rec.Body.String())
	}
	return resp
}

// Switching a node back on has to reach it at once. It only can if the panel HOLDS
// the poll of a node that already knows it is switched off — a revoked node is
// waiting on nothing else, so an unheld poll leaves the panel's wake with nothing
// to wake and the node discovers the news on its own slow schedule. On the live
// pair that was 62 seconds of the panel showing a node as enabled while it served
// nobody and phones failed to connect.
//
// The first revocation still has to be immediate: a node that has NOT heard yet
// must be told now, not in 45 seconds' time.
func TestRevokedNodePollIsHeldOnlyOnceItKnows(t *testing.T) {
	rt, _ := rolesTestRouter(t)
	id, token := joinedNode(t, rt, "berlin")
	if err := rt.mgr.SetNodeEnabled(id, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// It hasn't heard yet → answered straight away.
	done := make(chan nodeapi.SyncResponse, 1)
	go func() { done <- decodeSync(t, syncRequest(rt, token, nodeapi.SyncRequest{})) }()
	select {
	case resp := <-done:
		if !resp.Revoked {
			t.Fatal("a disabled node was not told it is revoked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first revocation was held — the node keeps serving until it is told")
	}

	// Now it knows. This poll must park instead of being answered on the spot.
	held := make(chan nodeapi.SyncResponse, 1)
	go func() { held <- decodeSync(t, syncRequest(rt, token, nodeapi.SyncRequest{Revoked: true})) }()
	select {
	case <-held:
		t.Fatal("the poll of an already-revoked node returned immediately — " +
			"re-enabling it would not reach it until its next poll")
	case <-time.After(300 * time.Millisecond):
	}

	// Switching it back on wakes that parked poll, and the answer is no longer a
	// revocation — which is what makes the agent resume.
	if err := rt.mgr.SetNodeEnabled(id, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	select {
	case resp := <-held:
		if resp.Revoked {
			t.Error("the node was told it is still revoked after being switched back on")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("switching the node on did not wake its held poll")
	}
}

// The wake only reaches a node that is parked on a poll. Re-enable one BETWEEN
// polls — which is most of the time, since a suspended node sleeps between them —
// and the wake fires at nobody; the request that follows is the only chance to tell
// it. Holding that request is what made the node come back "sometimes in six
// seconds, sometimes in fifty": the fast case was the wake landing by luck.
//
// So a node still reporting itself revoked while the panel has it enabled must be
// answered at once, with no wake involved anywhere in this test.
func TestReEnabledNodeIsAnsweredWithoutWaitingForAWake(t *testing.T) {
	rt, st := rolesTestRouter(t)
	// An enabled node's sync runs the full ingest, which reaches config generation —
	// and that needs the WS path first-run bootstrap normally seeds.
	if err := st.SetWSPath("/ws"); err != nil {
		t.Fatalf("seed ws path: %v", err)
	}
	id, token := joinedNode(t, rt, "berlin")

	// The node reports the config it already has. Without this it would report an
	// empty hash, the panel would answer "here is your config" — and the request
	// would return promptly for a reason that has nothing to do with what is being
	// tested. A node coming back from a revocation has the current config already.
	node, err := rt.mgr.GetNode(id)
	if err != nil || node == nil {
		t.Fatalf("get node: %v", err)
	}
	desired, err := rt.mgr.NodeDesiredState(node)
	if err != nil {
		t.Fatalf("desired state: %v", err)
	}

	// Off, then on again — both while the node is not polling at all, so every wake
	// these fire is lost, exactly as when an operator flips the switch twice.
	if err := rt.mgr.SetNodeEnabled(id, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := rt.mgr.SetNodeEnabled(id, true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// The node syncs still believing it is switched off. This must not park.
	req := nodeapi.SyncRequest{Revoked: true, ConfigHash: desired.Hash}
	done := make(chan nodeapi.SyncResponse, 1)
	go func() { done <- decodeSync(t, syncRequest(rt, token, req)) }()
	select {
	case resp := <-done:
		if resp.Revoked {
			t.Fatal("an enabled node was told it is revoked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a node that still thinks it is switched off was held — it stays down, " +
			"and the panel shows it as enabled the whole time")
	}
}
