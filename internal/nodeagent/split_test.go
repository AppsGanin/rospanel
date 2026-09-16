package nodeagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/nodestate"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// splitAgent is an agent holding a small state in parts: users 2 and 4 on one VLESS lane.
func splitAgent(t *testing.T) (*Agent, *nodestate.Parts) {
	t.Helper()
	dir := t.TempDir()
	skel, _ := json.Marshal(nodeapi.Skeleton{
		Config: json.RawMessage(`{"inbounds":[{"tag":"vless-in","protocol":"vless","settings":{"clients":[],"decryption":"none"}}]}`),
		Slots:  []nodeapi.UserSlot{{Inbound: 0, Kind: "vless", Flow: "xtls-rprx-vision"}},
		Meta:   nodeapi.NodeMeta{Host: "n.example.com"},
	})
	h := &nodestate.Held{Tag: "t1", Skeleton: skel, Blocked: json.RawMessage(`{"ips":["198.51.100.1"],"ttl_hours":24}`)}
	for _, id := range []int64{2, 4} {
		b, _ := json.Marshal(nodeapi.UserRow{ID: id, UUID: "uuid", Slots: []int{0}, Speed: int(id) * 100})
		h.Rows = append(h.Rows, b)
	}
	h.Hash = nodeapi.ContentHash(h.Skeleton, h.Rows, h.Blocked)
	p, err := nodestate.Decode(h)
	if err != nil {
		t.Fatal(err)
	}
	st, err := nodestate.Assemble(p)
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		dataDir:      dir,
		sup:          xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		certPath:     filepath.Join(dir, "cert.pem"),
		state:        &persistState{},
		pending:      map[int64]*nodeapi.TrafficDelta{},
		inflight:     map[int64]*nodeapi.TrafficDelta{},
		lastCounters: map[string]xray.Traffic{},
	}
	a.parts = p
	a.setSplit(h, st)
	return a, p
}

// A state held in parts is written as its parts — not a second copy of the users — and
// comes back from disk as the same config, the same meta and the same hash, even from a
// file something has reformatted.
func TestSplitStateSurvivesARestart(t *testing.T) {
	a, p := splitAgent(t)
	raw, err := os.ReadFile(statePath(a.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("last_config")) {
		t.Error("the assembled config was written beside its parts")
	}
	check := func(label string) {
		t.Helper()
		b := &Agent{state: loadState(a.dataDir)}
		b.restoreSplit()
		if b.parts == nil || b.state.LastConfig == nil {
			t.Fatalf("%s: nothing restored", label)
		}
		held := b.parts.Held
		if held.Hash != p.Held.Hash || held.Tag != "t1" ||
			nodeapi.ContentHash(held.Skeleton, held.Rows, held.Blocked) != p.Held.Hash {
			t.Errorf("%s: restored hash %s, tag %q", label, held.Hash, held.Tag)
		}
		want, _ := nodestate.Assemble(p)
		if !bytes.Equal(b.state.LastConfig.XrayConfig, want.XrayConfig) || b.state.LastConfig.Hash != p.Held.Hash ||
			b.state.LastConfig.Meta.SpeedLimits["u4"] != 400 || len(b.state.LastConfig.Meta.BlockedIPs) != 1 {
			t.Errorf("%s: restored %+v", label, b.state.LastConfig.Meta)
		}
	}
	check("as written")

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "    "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(a.dataDir), pretty.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	check("reformatted")

	// A newer agent binary asks for everything again, parts included.
	var st persistState
	_ = json.Unmarshal(raw, &st)
	st.AgentVersion = "0.0.1-older"
	old, _ := json.Marshal(st)
	if err := os.WriteFile(statePath(a.dataDir), old, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadState(a.dataDir); got.Split == nil || got.Split.Hash != "" || got.Split.Tag != "" || len(got.Split.Rows) != 2 {
		t.Errorf("after an upgrade: %+v", got.Split)
	}
}

// A sync says which revision of parts the agent speaks, and names and hashes the state
// it holds.
func TestSyncRequestNamesTheSplitState(t *testing.T) {
	a, p := splitAgent(t)
	req := a.buildSyncRequest()
	if req.DeltaRev != nodeapi.DeltaRev || req.StateTag != "t1" || req.ConfigHash != p.Held.Hash {
		t.Errorf("request: rev %d, tag %q, hash %q", req.DeltaRev, req.StateTag, req.ConfigHash)
	}
	a.setLastConfig(&nodeapi.NodeState{Hash: "whole"})
	if req := a.buildSyncRequest(); req.StateTag != "" || req.ConfigHash != "whole" || a.parts != nil {
		t.Errorf("after a whole config: tag %q, hash %q", req.StateTag, req.ConfigHash)
	}
}

// A change the node cannot take is not backed off over: the node forgets the name of
// what it holds, so the panel compares content and sends it whole. A change that only
// renames is taken without touching Xray, and one that changes users is applied and
// kept.
func TestSplitChangesTheNodeCannotTake(t *testing.T) {
	a, _ := splitAgent(t)
	if err := a.applyDelta(&nodeapi.StateDelta{From: "t0", Tag: "t2"}); err != nil {
		t.Fatalf("a change from another state was an error: %v", err)
	}
	if a.splitTag() != "" || a.parts.Held.Tag != "" || loadState(a.dataDir).Split.Tag != "" {
		t.Errorf("the name was kept: %q / %q", a.splitTag(), a.parts.Held.Tag)
	}

	b, p := splitAgent(t)
	if err := b.applyDelta(&nodeapi.StateDelta{From: "t1", Tag: "t9"}); err != nil {
		t.Fatal(err)
	}
	if b.splitTag() != "t9" || b.parts.Held.Hash != p.Held.Hash {
		t.Errorf("a rename: tag %q", b.splitTag())
	}

	// A change taken: the node holds the new rows under the new name, the config on disk
	// carries them, and so does what a restart would load.
	c, _ := splitAgent(t)
	row, _ := json.Marshal(nodeapi.UserRow{ID: 9, UUID: "uuid-9", Slots: []int{0}})
	if err := c.applyDelta(&nodeapi.StateDelta{From: "t1", Tag: "t2", Upsert: []json.RawMessage{row}, Remove: []int64{2}}); err != nil {
		t.Fatal(err)
	}
	if c.splitTag() != "t2" || len(c.parts.Rows) != 2 || c.parts.Rows[1].ID != 9 {
		t.Errorf("after the change: tag %q, rows %+v", c.splitTag(), c.parts.Rows)
	}
	onDisk, err := os.ReadFile(filepath.Join(c.dataDir, "config.json"))
	if err != nil || !bytes.Contains(onDisk, []byte(`"uuid-9"`)) || bytes.Contains(onDisk, []byte(`"u2"`)) {
		t.Errorf("the config on disk: %s (%v)", onDisk, err)
	}
	if st := loadState(c.dataDir); st.Split == nil || st.Split.Tag != "t2" || st.Split.Hash != c.parts.Held.Hash || len(st.Split.Rows) != 2 {
		t.Errorf("persisted: %+v", st.Split)
	}

	d := &Agent{state: &persistState{}, dataDir: t.TempDir()}
	if err := d.applyDelta(&nodeapi.StateDelta{From: "", Tag: "t2"}); err != nil || d.parts != nil {
		t.Errorf("a change with nothing held: %v", err)
	}
}
