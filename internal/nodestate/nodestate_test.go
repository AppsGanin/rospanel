package nodestate

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

func row(t *testing.T, r nodeapi.UserRow) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// held is a small state: one VLESS slot, users 2, 4 and 6.
func held(t *testing.T) *Parts {
	t.Helper()
	skel, _ := json.Marshal(nodeapi.Skeleton{
		Config: json.RawMessage(`{"inbounds":[{"tag":"vless-in","protocol":"vless","settings":{"clients":[],"decryption":"none"}}]}`),
		Slots:  []nodeapi.UserSlot{{Inbound: 0, Kind: "vless", Flow: "xtls-rprx-vision"}},
	})
	h := &Held{Tag: "t1", Skeleton: skel, Blocked: json.RawMessage(`{"ips":["198.51.100.1"],"ttl_hours":24}`)}
	for _, id := range []int64{2, 4, 6} {
		h.Rows = append(h.Rows, row(t, nodeapi.UserRow{ID: id, UUID: "uuid", Slots: []int{0}}))
	}
	h.Hash = nodeapi.ContentHash(h.Skeleton, h.Rows, h.Blocked)
	p, err := Decode(h)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func ids(p *Parts) []int64 {
	var out []int64
	for _, r := range p.Rows {
		out = append(out, r.ID)
	}
	return out
}

// A change replaces, adds and removes rows by user, keeps the rest as they were, and
// hashes to what the same rows sent whole would.
func TestApplyMergesRowsByUser(t *testing.T) {
	p := held(t)
	d := &nodeapi.StateDelta{
		From: "t1", Tag: "t2",
		Upsert: []json.RawMessage{
			row(t, nodeapi.UserRow{ID: 1, UUID: "new", Slots: []int{0}}),
			row(t, nodeapi.UserRow{ID: 4, UUID: "changed", Slots: []int{0}, Speed: 512}),
			row(t, nodeapi.UserRow{ID: 7, UUID: "last", Slots: []int{0}}),
		},
		Remove: []int64{6, 99}, // 99 was never held: nothing to remove
	}
	next, err := Apply(p, d)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(next); !slices.Equal(got, []int64{1, 2, 4, 7}) {
		t.Fatalf("rows %v", got)
	}
	if next.Rows[2].UUID != "changed" || next.Rows[2].Speed != 512 || next.Rows[1].UUID != "uuid" {
		t.Errorf("rows %+v", next.Rows)
	}
	if next.Held.Tag != "t2" {
		t.Errorf("tag %q", next.Held.Tag)
	}
	if want := nodeapi.ContentHash(next.Held.Skeleton, next.Held.Rows, next.Held.Blocked); next.Held.Hash != want {
		t.Error("the hash is not the hash of the rows held")
	}
	whole := held(t).Held
	whole.Rows = []json.RawMessage{d.Upsert[0], p.Held.Rows[0], d.Upsert[1], d.Upsert[2]}
	if next.Held.Hash != nodeapi.ContentHash(whole.Skeleton, whole.Rows, whole.Blocked) {
		t.Error("the change does not hash to the same rows sent whole")
	}
	// The held state is untouched: a change that fails later leaves nothing half-done.
	if got := ids(p); !slices.Equal(got, []int64{2, 4, 6}) || p.Held.Tag != "t1" {
		t.Errorf("the held state changed: %v %q", got, p.Held.Tag)
	}
	if cfg, err := Assemble(next); err != nil || !strings.Contains(string(cfg.XrayConfig), `"id":"changed"`) {
		t.Errorf("assembled: %s %v", cfg.XrayConfig, err)
	}
}

// Blocked addresses: kept when a change carries none, replaced — emptied too — when it does.
func TestApplyBlockedAddresses(t *testing.T) {
	p := held(t)
	kept, err := Apply(p, &nodeapi.StateDelta{From: "t1", Tag: "t2"})
	if err != nil || !slices.Equal(kept.Blocked.IPs, []string{"198.51.100.1"}) || kept.Blocked.TTLHours != 24 {
		t.Errorf("kept: %+v %v", kept.Blocked, err)
	}
	lifted, err := Apply(p, &nodeapi.StateDelta{From: "t1", Tag: "t2", Blocked: json.RawMessage(`{}`)})
	if err != nil || len(lifted.Blocked.IPs) != 0 || lifted.Blocked.TTLHours != 0 {
		t.Errorf("lifted: %+v %v", lifted.Blocked, err)
	}
	st, _ := Assemble(lifted)
	if len(st.Meta.BlockedIPs) != 0 {
		t.Errorf("assembled blocks after lifting: %v", st.Meta.BlockedIPs)
	}
}

// A change that cannot be taken is refused whole.
func TestApplyRefusesMalformedChanges(t *testing.T) {
	p := held(t)
	for name, d := range map[string]*nodeapi.StateDelta{
		"from another state": {From: "t0", Tag: "t2"},
		"rows out of order": {From: "t1", Tag: "t2", Upsert: []json.RawMessage{
			row(t, nodeapi.UserRow{ID: 5}), row(t, nodeapi.UserRow{ID: 3}),
		}},
		"a row twice": {From: "t1", Tag: "t2", Upsert: []json.RawMessage{
			row(t, nodeapi.UserRow{ID: 5}), row(t, nodeapi.UserRow{ID: 5}),
		}},
		"changed and removed":   {From: "t1", Tag: "t2", Upsert: []json.RawMessage{row(t, nodeapi.UserRow{ID: 4})}, Remove: []int64{4}},
		"a row that is not one": {From: "t1", Tag: "t2", Upsert: []json.RawMessage{json.RawMessage(`"x"`)}},
		"blocked that is not":   {From: "t1", Tag: "t2", Blocked: json.RawMessage(`[1]`)},
	} {
		if _, err := Apply(p, d); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

// A state is held only if it hashes as the panel said, and its rows are in order.
func TestFromSplitAndDecodeRefuse(t *testing.T) {
	p := held(t)
	s := &nodeapi.SplitState{Tag: "t", Hash: p.Held.Hash, Skeleton: p.Held.Skeleton, Rows: p.Held.Rows, Blocked: p.Held.Blocked}
	if _, err := FromSplit(s); err != nil {
		t.Fatal(err)
	}
	s.Hash = "0" + s.Hash[1:]
	if _, err := FromSplit(s); err == nil {
		t.Error("a state whose hash differs was held")
	}
	h := *p.Held
	h.Rows = []json.RawMessage{h.Rows[1], h.Rows[0]}
	if _, err := Decode(&h); err == nil {
		t.Error("rows out of order were decoded")
	}
}

// Addresses banned by hand travel in Blocked beside the policy's, and are replaced with it.
func TestBannedAddresses(t *testing.T) {
	p := held(t)
	next, err := Apply(p, &nodeapi.StateDelta{From: "t1", Tag: "t2",
		Blocked: json.RawMessage(`{"ips":["198.51.100.1"],"ttl_hours":24,"banned":["203.0.113.7"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Assemble(next)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Meta.BannedIPs, []string{"203.0.113.7"}) || !slices.Equal(st.Meta.BlockedIPs, []string{"198.51.100.1"}) {
		t.Errorf("assembled: banned %v, blocked %v", st.Meta.BannedIPs, st.Meta.BlockedIPs)
	}
	lifted, _ := Apply(next, &nodeapi.StateDelta{From: "t2", Tag: "t3", Blocked: json.RawMessage(`{}`)})
	if st, _ := Assemble(lifted); len(st.Meta.BannedIPs) != 0 {
		t.Errorf("bans after lifting: %v", st.Meta.BannedIPs)
	}
}
