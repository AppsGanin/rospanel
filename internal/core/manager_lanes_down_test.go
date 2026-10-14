package core

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

func TestLanesDownHidesAfterAWhileAndShowsAtOnce(t *testing.T) {
	var d lanesDown
	t0 := time.Unix(1_000_000, 0)
	const node = int64(7)
	down := map[int64][]string{model.LocalNodeID: nil, node: {model.LaneAWG, inboundDownKey(42)}}

	d.observe(t0, laneRound{down: down})
	if lanes, ins := d.hiddenOn(node); lanes != nil || ins != nil {
		t.Fatalf("hidden at once: %v %v", lanes, ins)
	}
	d.observe(t0.Add(laneHideAfter-time.Second), laneRound{down: down})
	if lanes, _ := d.hiddenOn(node); lanes != nil {
		t.Fatalf("hidden before laneHideAfter: %v", lanes)
	}
	d.observe(t0.Add(laneHideAfter), laneRound{down: down})
	lanes, ins := d.hiddenOn(node)
	if !lanes[model.LaneAWG] || !ins[42] || len(lanes) != 1 || len(ins) != 1 {
		t.Fatalf("hidden = %v %v, want awg and inbound 42", lanes, ins)
	}
	if l, _ := d.hiddenOn(model.LocalNodeID); l != nil {
		t.Fatalf("master hidden: %v", l)
	}

	// The tunnel is back: shown at the next look; the relay is still down.
	d.observe(t0.Add(laneHideAfter+20*time.Second), laneRound{down: map[int64][]string{node: {inboundDownKey(42)}}})
	lanes, ins = d.hiddenOn(node)
	if lanes[model.LaneAWG] || !ins[42] {
		t.Fatalf("after recovery = %v %v", lanes, ins)
	}

	// Down again: the clock starts over.
	d.observe(t0.Add(laneHideAfter+40*time.Second), laneRound{down: map[int64][]string{node: {model.LaneAWG, inboundDownKey(42)}}})
	if lanes, _ := d.hiddenOn(node); lanes[model.LaneAWG] {
		t.Fatal("hidden again without waiting")
	}

	// The node is no longer looked at (offline, switched off): all of it forgotten.
	d.observe(t0.Add(time.Hour), laneRound{down: map[int64][]string{}})
	if lanes, ins := d.hiddenOn(node); lanes != nil || ins != nil {
		t.Fatalf("kept for a node not reporting: %v %v", lanes, ins)
	}
}

func TestLaneAlertsSayDownOnceAndBackWithDowntime(t *testing.T) {
	var d lanesDown
	t0 := time.Unix(2_000_000, 0)
	const node = int64(3)
	relay := inboundDownKey(9)
	down := map[int64][]string{node: {model.LaneMieru, model.LaneAWG, relay}}
	notes := map[string]laneNote{
		noteKey(node, model.LaneMieru): {server: "NL", lane: "mieru", reason: "listen tcp :2222: address already in use"},
		noteKey(node, relay):           {server: "NL", lane: "TURN (TURN-VK, UDP 3478)"},
		noteKey(node, model.LaneAWG):   {server: "NL", lane: "AmneziaWG"},
	}

	if got := laneAlerts(d.observe(t0, laneRound{down: down, notes: notes}), i18n.EN, t0); len(got) != 0 {
		t.Fatalf("alerted at once: %v", got)
	}
	at := t0.Add(laneHideAfter)
	got := laneAlerts(d.observe(at, laneRound{down: down, notes: notes}), i18n.EN, at)
	if len(got) != 3 {
		t.Fatalf("down alerts = %q", got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"mieru is down", "address already in use", "TURN (TURN-VK, UDP 3478) is down", "AmneziaWG is down", "NL"} {
		if !strings.Contains(joined, want) {
			t.Errorf("down alerts miss %q:\n%s", want, joined)
		}
	}
	if again := laneAlerts(d.observe(at.Add(20*time.Second), laneRound{down: down, notes: notes}), i18n.EN, at); len(again) != 0 {
		t.Fatalf("said twice: %v", again)
	}

	// mieru comes back: no note this round, the one from when it went down names it.
	back := at.Add(5 * time.Minute)
	got = laneAlerts(d.observe(back, laneRound{down: map[int64][]string{node: {model.LaneAWG, relay}}}), i18n.EN, back)
	if len(got) != 1 || !strings.Contains(got[0], "mieru is back") || !strings.Contains(got[0], "NL") || !strings.Contains(got[0], "7 min") {
		t.Fatalf("back alert = %q", got)
	}
}

// A lane switched off or deleted while down is not "back", and an offline node keeps
// its state until it reports again.
func TestLaneAlertsNoFalseBackAndOfflineKept(t *testing.T) {
	var d lanesDown
	t0 := time.Unix(3_000_000, 0)
	const a, b = int64(4), int64(5)
	notes := map[string]laneNote{noteKey(a, model.LaneMieru): {server: "A", lane: "mieru"}, noteKey(b, model.LaneMieru): {server: "B", lane: "mieru"}}
	both := map[int64][]string{a: {model.LaneMieru}, b: {model.LaneMieru}}
	d.observe(t0, laneRound{down: both, notes: notes})
	d.observe(t0.Add(laneHideAfter), laneRound{down: both, notes: notes})

	// a: mieru switched off. b: offline, kept.
	at := t0.Add(laneHideAfter + time.Minute)
	off := func(server int64, key string) bool { return server != a }
	got := laneAlerts(d.observe(at, laneRound{down: map[int64][]string{a: nil}, keep: map[int64]bool{b: true}, wanted: off}), i18n.EN, at)
	if len(got) != 0 {
		t.Fatalf("alerts for a lane switched off / a node offline: %q", got)
	}
	if lanes, _ := d.hiddenOn(b); !lanes[model.LaneMieru] {
		t.Fatal("offline node's state was dropped")
	}
	// b reports again with mieru up: back, with the whole downtime.
	back := at.Add(10 * time.Minute)
	got = laneAlerts(d.observe(back, laneRound{down: map[int64][]string{a: nil, b: nil}, wanted: off}), i18n.EN, back)
	if len(got) != 1 || !strings.Contains(got[0], "mieru is back") || !strings.Contains(got[0], "B") {
		t.Fatalf("back after offline = %q", got)
	}
}
