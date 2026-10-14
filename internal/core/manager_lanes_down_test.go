package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestLanesDownHidesAfterAWhileAndShowsAtOnce(t *testing.T) {
	var d lanesDown
	t0 := time.Unix(1_000_000, 0)
	const node = int64(7)
	down := map[int64][]string{model.LocalNodeID: nil, node: {model.LaneAWG, inboundDownKey(42)}}

	d.observe(t0, down)
	if lanes, ins := d.hiddenOn(node); lanes != nil || ins != nil {
		t.Fatalf("hidden at once: %v %v", lanes, ins)
	}
	d.observe(t0.Add(laneHideAfter-time.Second), down)
	if lanes, _ := d.hiddenOn(node); lanes != nil {
		t.Fatalf("hidden before laneHideAfter: %v", lanes)
	}
	d.observe(t0.Add(laneHideAfter), down)
	lanes, ins := d.hiddenOn(node)
	if !lanes[model.LaneAWG] || !ins[42] || len(lanes) != 1 || len(ins) != 1 {
		t.Fatalf("hidden = %v %v, want awg and inbound 42", lanes, ins)
	}
	if l, _ := d.hiddenOn(model.LocalNodeID); l != nil {
		t.Fatalf("master hidden: %v", l)
	}

	// The tunnel is back: shown at the next look; the relay is still down.
	d.observe(t0.Add(laneHideAfter+20*time.Second), map[int64][]string{node: {inboundDownKey(42)}})
	lanes, ins = d.hiddenOn(node)
	if lanes[model.LaneAWG] || !ins[42] {
		t.Fatalf("after recovery = %v %v", lanes, ins)
	}

	// Down again: the clock starts over.
	d.observe(t0.Add(laneHideAfter+40*time.Second), map[int64][]string{node: {model.LaneAWG, inboundDownKey(42)}})
	if lanes, _ := d.hiddenOn(node); lanes[model.LaneAWG] {
		t.Fatal("hidden again without waiting")
	}

	// The node is no longer looked at (offline, switched off): all of it forgotten.
	d.observe(t0.Add(time.Hour), map[int64][]string{})
	if lanes, ins := d.hiddenOn(node); lanes != nil || ins != nil {
		t.Fatalf("kept for a node not reporting: %v %v", lanes, ins)
	}
}
