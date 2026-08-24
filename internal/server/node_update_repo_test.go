package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/updater"
)

func TestNodeSyncReturnsUpdateAndRepo(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)

	node, err := mgr.CreateNode("n-upd", "n-upd.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	set, _ := st.GetSettings()
	base := "/" + set.NodeAPIPath + "/" + nodeapi.PathPrefix

	// Join to get token
	rec := postJSON(t, h, base+"/join", "", nodeapi.JoinRequest{
		JoinToken: node.RawJoinToken, NodeVersion: "2.10.0",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("join code = %d", rec.Code)
	}
	var jr nodeapi.JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &jr); err != nil {
		t.Fatalf("join decode: %v", err)
	}

	// Request node update from the manager
	if err := mgr.RequestNodeUpdate(node.ID); err != nil {
		t.Fatalf("request node update: %v", err)
	}

	// Next sync should return Update=true and UpdateRepo=updater.Repo
	rec = postJSON(t, h, base+"/sync", jr.Token, nodeapi.SyncRequest{ConfigHash: "hash"})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync code = %d", rec.Code)
	}
	var sr nodeapi.SyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("sync decode: %v", err)
	}

	if !sr.Update {
		t.Errorf("sr.Update = false, want true")
	}
	if sr.UpdateRepo != updater.Repo {
		t.Errorf("sr.UpdateRepo = %q, want %q", sr.UpdateRepo, updater.Repo)
	}
}

func TestNodeInstallCommandHonoursUpdateRepo(t *testing.T) {
	h, _, _ := nodeAPITestServer(t)
	rt := h.(*Router)
	req := httptest.NewRequest(http.MethodGet, "https://panel.example.com/nodes", nil)

	// Default: should contain updater.Repo
	cmdDefault := rt.nodeInstallCommand(req, "sec", "tok123")
	if !strings.Contains(cmdDefault, updater.Repo) {
		t.Errorf("cmd %q does not contain default repo %q", cmdDefault, updater.Repo)
	}

	// With ROSPANEL_REPO override
	const customRepo = "custom-fork/rospanel-custom"
	orig := os.Getenv("ROSPANEL_REPO")
	defer os.Setenv("ROSPANEL_REPO", orig)

	_ = os.Setenv("ROSPANEL_REPO", customRepo)
	cmdCustom := rt.nodeInstallCommand(req, "sec", "tok123")
	if !strings.Contains(cmdCustom, customRepo) {
		t.Errorf("cmd %q does not contain custom repo %q", cmdCustom, customRepo)
	}
}
