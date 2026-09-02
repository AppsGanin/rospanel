package sub

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func placed(id int64, cc string, weight, capacity int, hide bool) Server {
	set := testSet("h" + string(rune('0'+id)))
	set.ServerID = id
	set.ServerPlacement = model.Placement{Country: cc, Weight: weight, Capacity: capacity, HideWhenFull: hide}
	return Server{Set: set, Access: model.UnrestrictedAccess()}
}

func ids(servers []Server) []int64 {
	out := make([]int64, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Set.ServerID)
	}
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOrderModes(t *testing.T) {
	// master (0) in NL, nodes: 1 DE weighty, 2 NL, 3 unknown country.
	servers := []Server{
		placed(0, "NL", 0, 100, false),
		placed(1, "DE", 5, 100, false),
		placed(2, "NL", 0, 10, false),
		placed(3, "", 0, 0, false),
	}
	online := map[int64]int{0: 50, 1: 90, 2: 9, 3: 3}

	cases := []struct {
		mode, cc string
		want     []int64
	}{
		// weight first, then the list.
		{model.OrderManual, "NL", []int64{1, 0, 2, 3}},
		// NL servers first (in manual order between them), then the rest.
		{model.OrderNearest, "NL", []int64{0, 2, 1, 3}},
		// an unknown client country makes nearest degrade to manual.
		{model.OrderNearest, "", []int64{1, 0, 2, 3}},
		// load: 0 → 0.5, 1 → 0.9, 2 → 0.9, 3 → 3 (no capacity: raw count); ties by weight.
		{model.OrderLoad, "NL", []int64{0, 1, 2, 3}},
		// nearest, then load within: NL {0: 0.5, 2: 0.9}, then {1: 0.9, 3: 3}.
		{model.OrderNearestLoad, "NL", []int64{0, 2, 1, 3}},
		{model.OrderNearestLoad, "DE", []int64{1, 0, 2, 3}},
		// unknown mode = manual
		{"bogus", "NL", []int64{1, 0, 2, 3}},
	}
	for _, c := range cases {
		got := ids(Order(servers, c.mode, c.cc, online))
		if !equal(got, c.want) {
			t.Errorf("%s / %q: got %v, want %v", c.mode, c.cc, got, c.want)
		}
	}
}

func TestOrderHidesFullServersButNeverAll(t *testing.T) {
	servers := []Server{
		placed(0, "NL", 0, 10, true),
		placed(1, "NL", 0, 10, true),
		placed(2, "NL", 0, 0, true), // no capacity: never "full"
	}
	got := ids(Order(servers, model.OrderManual, "", map[int64]int{0: 10, 1: 3}))
	if !equal(got, []int64{1, 2}) {
		t.Errorf("full server should drop: %v", got)
	}
	// Every server full: keep them all rather than hand out nothing.
	two := servers[:2]
	got = ids(Order(two, model.OrderLoad, "", map[int64]int{0: 12, 1: 10}))
	if !equal(got, []int64{1, 0}) {
		t.Errorf("all full should keep everything, least loaded first: %v", got)
	}
	// Without hide-when-full a full server merely sorts last under load.
	open := []Server{placed(0, "", 0, 10, false), placed(1, "", 0, 10, false)}
	got = ids(Order(open, model.OrderLoad, "", map[int64]int{0: 10, 1: 2}))
	if !equal(got, []int64{1, 0}) {
		t.Errorf("load order: %v", got)
	}
	// The input slice is not reordered in place.
	if servers[0].Set.ServerID != 0 || servers[1].Set.ServerID != 1 {
		t.Error("Order mutated its input")
	}
}
