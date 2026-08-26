package nodeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/nodeapi"
	"github.com/Shu1t3/rospanel-shu1t3/internal/version"
)

// An upgraded agent must re-fetch the whole desired state.
//
// The state file is written by re-marshalling through the agent's own structs, so a
// field the panel added after this node's binary was built is dropped on the way to
// disk — while the hash the node keeps reporting is the panel's, computed over the
// complete state. Panel and node then agree forever that the node is current, over a
// config the node never had. That is not hypothetical: it is how a fleet ended up
// with a panel pushing per-user speed caps and a node that applied none of them,
// silently, with no error anywhere.
func TestLoadStateForcesResyncAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	write := func(s persistState) {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	cfg := &nodeapi.NodeState{Hash: "abc123", XrayConfig: json.RawMessage(`{"inbounds":[]}`)}

	// Written by an older binary (no version recorded at all).
	write(persistState{LastConfig: cfg, LastReportID: 7})
	got := loadState(dir)
	if got.LastConfig == nil || got.LastConfig.Hash != "" {
		t.Errorf("hash = %q after an upgrade, want it cleared so the panel re-pushes",
			got.LastConfig.Hash)
	}
	if got.LastConfig.XrayConfig == nil {
		t.Error("the config itself was dropped — Xray must keep serving across an upgrade")
	}
	if got.LastReportID != 7 {
		t.Errorf("report id = %d, want it preserved (the panel's watermark is forward-only)", got.LastReportID)
	}

	// Written by this same binary: nothing to re-fetch, so the hash stands and the
	// node does not pull a full config on every restart.
	write(persistState{LastConfig: cfg, AgentVersion: version.Version})
	if got := loadState(dir); got.LastConfig.Hash != "abc123" {
		t.Errorf("hash = %q on an unchanged version, want it kept", got.LastConfig.Hash)
	}
}
