package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func snapStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestConfigSnapshots(t *testing.T) {
	st := snapStore(t)

	// Create a manual and an auto snapshot; the list is newest-first with metadata.
	manual, _ := json.Marshal(model.ServerConfigSnapshot{VLESSPort: 443, RealityPrivateKey: "secret-key"})
	if _, err := st.CreateConfigSnapshot("before egress edit", false, string(manual)); err != nil {
		t.Fatalf("create manual: %v", err)
	}
	if _, err := st.CreateConfigSnapshot("", true, `{"vless_port":8443}`); err != nil {
		t.Fatalf("create auto: %v", err)
	}
	snaps, err := st.ListConfigSnapshots()
	if err != nil || len(snaps) != 2 {
		t.Fatalf("list: %v (%d)", err, len(snaps))
	}
	if !snaps[0].Auto {
		t.Error("newest should be the auto snapshot")
	}
	if snaps[1].Label != "before egress edit" || snaps[1].Auto {
		t.Errorf("manual snapshot metadata wrong: %+v", snaps[1])
	}

	// The (encrypted-at-rest) payload round-trips by id, and delete removes it.
	cfg, err := st.ConfigSnapshot(snaps[1].ID)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if cfg.VLESSPort != 443 || cfg.RealityPrivateKey != "secret-key" {
		t.Errorf("payload round-trip wrong: %+v", cfg)
	}
	// The stored blob must be encrypted, not plaintext JSON.
	var raw string
	_ = st.db.QueryRow(`SELECT config_json FROM config_snapshots WHERE id = ?`, snaps[1].ID).Scan(&raw)
	if raw == string(manual) {
		t.Error("config_json is stored as plaintext; it should be encrypted at rest")
	}
	if err := st.DeleteConfigSnapshot(snaps[1].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if left, _ := st.ListConfigSnapshots(); len(left) != 1 {
		t.Errorf("after delete: %d snapshots, want 1", len(left))
	}
}

// A rollback must recreate the server's custom inbounds with their ORIGINAL ids, so a
// group grant (keyed on the opaque token inbound:<id>) keeps resolving and grouped users
// don't silently lose the lane. Inbounds that existed before but aren't in the snapshot
// get their now-dangling grants swept.
func TestRestoreServerConfigKeepsInboundIDsAndGrants(t *testing.T) {
	st := snapStore(t)

	inA, err := st.CreateInbound(model.Inbound{
		ServerID: model.LocalNodeID, Enabled: true, Name: "A", Protocol: "vless", Port: 10001,
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	inB, err := st.CreateInbound(model.Inbound{
		ServerID: model.LocalNodeID, Enabled: true, Name: "B", Protocol: "vless", Port: 10002,
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	grp, err := st.CreateGroup("VIP", []string{
		model.InboundToken(inA.ID), model.InboundToken(inB.ID),
	})
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	// A snapshot taken back when only A existed (B was added afterwards).
	cfg := &model.ServerConfigSnapshot{Inbounds: []model.Inbound{*inA}}
	if err := st.RestoreServerConfig(cfg); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// A is back at its original id — not a fresh autoincrement one.
	got, err := st.GetInbound(inA.ID)
	if err != nil || got.Name != "A" {
		t.Fatalf("inbound A not restored at id %d: %+v (%v)", inA.ID, got, err)
	}
	// B is gone (not in the snapshot).
	left, err := st.Inbounds(model.LocalNodeID)
	if err != nil || len(left) != 1 || left[0].ID != inA.ID {
		t.Fatalf("expected only inbound A(%d) after restore, got %+v (%v)", inA.ID, left, err)
	}

	// A's grant still resolves; B's dangling grant was swept.
	grants, err := st.groupGrants(grp.ID)
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	hasA, hasB := false, false
	for _, g := range grants {
		if g == model.InboundToken(inA.ID) {
			hasA = true
		}
		if g == model.InboundToken(inB.ID) {
			hasB = true
		}
	}
	if !hasA {
		t.Errorf("grant for restored inbound A(%d) was lost: %v", inA.ID, grants)
	}
	if hasB {
		t.Errorf("dangling grant for removed inbound B(%d) was not swept: %v", inB.ID, grants)
	}
}

// The history is capped so an auto-snapshot on every routing change can't grow the DB
// without bound.
func TestConfigSnapshotsCapped(t *testing.T) {
	st := snapStore(t)
	for i := 0; i < maxConfigSnapshots+15; i++ {
		if _, err := st.CreateConfigSnapshot("", true, `{}`); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	snaps, err := st.ListConfigSnapshots()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != maxConfigSnapshots {
		t.Errorf("kept %d snapshots, want the cap of %d", len(snaps), maxConfigSnapshots)
	}
}
