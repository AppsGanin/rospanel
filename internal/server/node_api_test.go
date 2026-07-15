package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// nodeAPITestServer builds a full router (via server.New, so the decoy + node-API
// path callback are wired) over a fresh store with a seeded WS path.
func nodeAPITestServer(t *testing.T) (http.Handler, *core.Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetWSPath("/ws"); err != nil {
		t.Fatalf("ws path: %v", err)
	}
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	mgr := core.New(st, sup, xray.Options{PanelDest: "127.0.0.1:8080"}, core.TLSPaths{}, dir)
	h, err := New(mgr, "secretpath", "nginx", dir)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	return h, mgr, st
}

func postJSON(t *testing.T, h http.Handler, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNodeJoinAndSync(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)

	node, err := mgr.CreateNode("n1", "nl1.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	set, _ := st.GetSettings()
	base := "/" + set.NodeAPIPath + "/" + nodeapi.PathPrefix

	// Join with the one-time token → permanent token.
	rec := postJSON(t, h, base+"/join", "", nodeapi.JoinRequest{
		JoinToken: node.RawJoinToken, NodeVersion: "1.0.0",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("join status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var jr nodeapi.JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &jr); err != nil {
		t.Fatalf("join decode: %v", err)
	}
	if jr.Token == "" || jr.NodeID != node.ID {
		t.Fatalf("join response = %+v", jr)
	}

	// First sync: the node has nothing applied (config_hash ""), so it must get a
	// change immediately (no long-poll hold).
	rec = postJSON(t, h, base+"/sync", jr.Token, nodeapi.SyncRequest{ConfigHash: ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d", rec.Code)
	}
	var sr nodeapi.SyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("sync decode: %v (body=%s)", err, rec.Body.String())
	}
	if !sr.Changed || sr.State == nil || sr.State.Hash == "" {
		t.Fatalf("expected a config change, got %+v", sr)
	}
	if len(sr.State.XrayConfig) == 0 {
		t.Fatal("expected an xray config in the pushed state")
	}

	// A second sync reporting the just-applied hash gets no change... but that would
	// block for the hold. Instead verify idempotency of status: last_seen advanced.
	updated, _ := st.GetNode(node.ID)
	if updated.LastSeen == 0 {
		t.Fatal("sync did not record last_seen")
	}
}

func TestNodeSyncRevokedAfterDisable(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	node, _ := mgr.CreateNode("n1", "nl1.example.com")
	set, _ := st.GetSettings()
	base := "/" + set.NodeAPIPath + "/" + nodeapi.PathPrefix

	rec := postJSON(t, h, base+"/join", "", nodeapi.JoinRequest{JoinToken: node.RawJoinToken})
	var jr nodeapi.JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &jr); err != nil {
		t.Fatalf("join decode: %v (code %d)", err, rec.Code)
	}

	// Disable the node → next sync must say revoked (immediately, no hold).
	if err := mgr.SetNodeEnabled(node.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec = postJSON(t, h, base+"/sync", jr.Token, nodeapi.SyncRequest{ConfigHash: "whatever"})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d", rec.Code)
	}
	var sr nodeapi.SyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("sync decode: %v", err)
	}
	if !sr.Revoked {
		t.Fatalf("disabled node should be told revoked, got %+v", sr)
	}
}

func TestNodeSyncRevokedAfterDelete(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	node, _ := mgr.CreateNode("n1", "nl1.example.com")
	set, _ := st.GetSettings()
	base := "/" + set.NodeAPIPath + "/" + nodeapi.PathPrefix

	rec := postJSON(t, h, base+"/join", "", nodeapi.JoinRequest{JoinToken: node.RawJoinToken})
	var jr nodeapi.JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &jr); err != nil {
		t.Fatalf("join decode: %v (code %d)", err, rec.Code)
	}

	// Deleting a node must revoke it on the next sync — NOT leave it silently
	// serving. The token survives as a tombstone precisely so this revoke lands.
	if err := mgr.DeleteNode(node.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	rec = postJSON(t, h, base+"/sync", jr.Token, nodeapi.SyncRequest{ConfigHash: "whatever"})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d", rec.Code)
	}
	var sr nodeapi.SyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("sync decode: %v (body=%s)", err, rec.Body.String())
	}
	if !sr.Revoked {
		t.Fatalf("deleted node must be told revoked, got %+v", sr)
	}
	// The node is gone from the operator's list.
	views, _ := mgr.NodeViews()
	for _, v := range views {
		if v.ID == node.ID {
			t.Fatal("deleted node still appears in NodeViews")
		}
	}
}

func TestInstallCommandInsecureForSelfSignedPanel(t *testing.T) {
	h, _, _ := nodeAPITestServer(t)
	// The test panel has no CA cert (TLSPaths{} → HasValidCert false), so the install
	// command must carry --insecure — otherwise a node can't verify the panel's TLS
	// on join and never connects.
	rt := h.(*Router)
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Host = "144.31.159.81"
	cmd := rt.nodeInstallCommand(req, "seg", "rpn_tok")
	if !strings.HasSuffix(cmd, "--insecure") {
		t.Fatalf("self-signed panel install command must end with --insecure, got: %s", cmd)
	}
}

func TestIsBroadcastableHost(t *testing.T) {
	for _, c := range []struct {
		host string
		ok   bool
	}{
		{"panel.example.com", true},
		{"vpn.example.co.uk", true},
		{"", false},
		{"localhost", false},
		{"127.0.0.1", false},
		{"203.0.113.7", false},            // IPv4 — cert may lack an IP SAN
		{"2001:db8::1", false},            // IPv6 literal
		{"panel.example.com:8443", false}, // carries a port
		{"panel", false},                  // no TLD
	} {
		if got := isBroadcastableHost(c.host); got != c.ok {
			t.Errorf("isBroadcastableHost(%q) = %v, want %v", c.host, got, c.ok)
		}
	}
}

func TestNodeJoinBadTokenIsDecoy(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	if _, err := mgr.CreateNode("n1", "nl1.example.com"); err != nil {
		t.Fatalf("create: %v", err)
	}
	set, _ := st.GetSettings()
	base := "/" + set.NodeAPIPath + "/" + nodeapi.PathPrefix

	// An unknown join token must not reveal the API — it falls through to the decoy
	// (an HTML page), never a JSON join response.
	rec := postJSON(t, h, base+"/join", "", nodeapi.JoinRequest{JoinToken: "rpn_bogus"})
	var jr nodeapi.JoinResponse
	if json.Unmarshal(rec.Body.Bytes(), &jr) == nil && jr.Token != "" {
		t.Fatal("bogus join token returned a real token")
	}

	// A sync with no valid bearer token likewise falls through to the decoy.
	rec = postJSON(t, h, base+"/sync", "rpn_bogus", nodeapi.SyncRequest{})
	var sr nodeapi.SyncResponse
	if json.Unmarshal(rec.Body.Bytes(), &sr) == nil && (sr.Changed || sr.Revoked) {
		t.Fatal("bogus bearer token got a real sync response")
	}
}
