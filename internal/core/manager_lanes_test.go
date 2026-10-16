package core

import (
	"math"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// A lane is shown by its protocol, a custom inbound by the operator's own name.
func TestLaneName(t *testing.T) {
	inbounds := map[int64]string{7: "Germany WS"}
	for lane, want := range map[string]string{
		xray.TagVLESS: model.ProtoVLESS, xray.TagReality: model.ProtoReality,
		xray.TagHysteria: model.ProtoHysteria, xray.TagNaive: model.ProtoNaive,
		xray.LaneAWG: model.ProtoAWG, "custom-7": "Germany WS", "custom-9": "custom-9",
	} {
		if got := laneName(lane, inbounds); got != want {
			t.Errorf("%s: %q, want %q", lane, got, want)
		}
	}
}

// A node's report is untrusted: only the lanes that server can have are kept, once
// each, within laneReportMax — no made-up lanes, no other server's inbounds, no sums
// pushed toward overflow.
func TestNodeLanesTakeOnlyTheNodesOwn(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	syncedNode(t, m.store, "edge", "26.10.10")
	syncedNode(t, m.store, "other", "26.10.10")
	nodes, _ := m.store.ListNodes()
	own, other := nodes[0].ID, nodes[1].ID
	mk := func(server int64, port int) string {
		in, err := m.store.CreateInbound(model.Inbound{ServerID: server, Name: "ws", Protocol: model.InbVLESS,
			Port: port, Enabled: true, Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecNone, Path: "/x"}})
		if err != nil {
			t.Fatal(err)
		}
		return in.Tag()
	}
	ownTag, otherTag := mk(own, 2053), mk(other, 2054)

	got, err := m.nodeLanes(own, "2026-10-12", []nodeapi.LaneDelta{
		{Lane: xray.TagVLESS, Up: 1, Down: 2},
		{Lane: xray.TagVLESS, Up: 100, Down: 100}, // a second entry for the same lane
		{Lane: xray.LaneAWG, Up: 3, Down: 4},
		{Lane: ownTag, Up: 5, Down: 6},
		{Lane: otherTag, Up: 7, Down: 8},        // another server's inbound
		{Lane: "custom-999999", Up: 9, Down: 9}, // no such inbound
		{Lane: "custom-junk", Up: 9, Down: 9},   // not even an id
		{Lane: "api", Up: 9, Down: 9},           // not a lane
		{Lane: xray.TagNaive, Up: math.MaxInt64, Down: -5},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int64{
		xray.TagVLESS: {1, 2}, xray.LaneAWG: {3, 4}, ownTag: {5, 6}, xray.TagNaive: {laneReportMax, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("kept %+v, want %v", got, want)
	}
	for _, l := range got {
		if w, ok := want[l.Lane]; !ok || l.NodeID != own || [2]int64{l.Up, l.Down} != w {
			t.Errorf("lane %+v, want %v", l, w)
		}
	}
}

// What a failed write kept rides the next one: the read that took it reset Xray's
// counters, so nothing else would bring it back.
func TestLocalLanesCarryWhatAWriteRefused(t *testing.T) {
	m := nodeTestManager(t)
	m.lanesUnsaved = map[string]xray.Traffic{xray.TagVLESS: {Up: 10, Down: 20}, xray.TagNaive: {Up: 1, Down: 2}}
	m.bookLocalLanes("2026-10-12", map[string]xray.Traffic{xray.TagVLESS: {Up: 5, Down: 5}})
	if m.lanesUnsaved != nil {
		t.Fatalf("kept %v after a write that went through", m.lanesUnsaved)
	}
	totals, err := m.store.LaneTrafficTotals("2026-10-12", "2026-10-12")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int64{}
	for _, l := range totals {
		got[l.Lane] = [2]int64{l.Up, l.Down}
	}
	if got[xray.TagVLESS] != [2]int64{15, 25} || got[xray.TagNaive] != [2]int64{1, 2} {
		t.Fatalf("totals %v, want vless 15/25 and naive 1/2", got)
	}
}
