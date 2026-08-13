package xray

import (
	"os"
	"path/filepath"
	"testing"
)

// An unchanged config must not be re-applied: on a node that means an Xray restart,
// and every live connection on it dropped, for a state change that never touched
// the proxy — a per-user speed cap, say.
func TestApplyRawIfChangedSkipsIdenticalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	// No binary: the supervisor writes and validates nothing, which is exactly the
	// shape these assertions need (they are about the write, not about Xray).
	sup := NewSupervisor("", cfgPath, dir)

	cfg := []byte(`{"inbounds":[]}`)
	changed, err := sup.ApplyRawIfChanged(cfg)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if !changed {
		t.Fatal("first apply reported no change")
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != string(cfg) {
		t.Fatalf("config on disk = %q", got)
	}

	// Xray isn't running here (no binary), so the shortcut must NOT engage — a
	// stopped process with a matching config still needs starting.
	if changed, err := sup.ApplyRawIfChanged(cfg); err != nil || !changed {
		t.Errorf("apply with Xray down: changed=%v err=%v, want changed", changed, err)
	}

	other := []byte(`{"inbounds":[{"port":443}]}`)
	if changed, err := sup.ApplyRawIfChanged(other); err != nil || !changed {
		t.Errorf("changed config: changed=%v err=%v, want changed", changed, err)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != string(other) {
		t.Errorf("config on disk = %q, want the new one", got)
	}
}
