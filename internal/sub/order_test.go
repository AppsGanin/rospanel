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
		got := ids(Order(servers, c.mode, c.cc, online, nil))
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
	got := ids(Order(servers, model.OrderManual, "", map[int64]int{0: 10, 1: 3}, nil))
	if !equal(got, []int64{1, 2}) {
		t.Errorf("full server should drop: %v", got)
	}
	// Every server full: keep them all rather than hand out nothing.
	two := servers[:2]
	got = ids(Order(two, model.OrderLoad, "", map[int64]int{0: 12, 1: 10}, nil))
	if !equal(got, []int64{1, 0}) {
		t.Errorf("all full should keep everything, least loaded first: %v", got)
	}
	// Without hide-when-full a full server merely sorts last under load.
	open := []Server{placed(0, "", 0, 10, false), placed(1, "", 0, 10, false)}
	got = ids(Order(open, model.OrderLoad, "", map[int64]int{0: 10, 1: 2}, nil))
	if !equal(got, []int64{1, 0}) {
		t.Errorf("load order: %v", got)
	}
	// The input slice is not reordered in place.
	if servers[0].Set.ServerID != 0 || servers[1].Set.ServerID != 1 {
		t.Error("Order mutated its input")
	}
}

// Random mode exists so the fleet, not the first server, takes the clients that
// connect to whatever comes first. So over many fetches every server has to lead,
// and every fetch has to carry the whole list.
func TestOrderRandomLetsEveryServerLead(t *testing.T) {
	servers := []Server{
		placed(0, "NL", 9, 0, false), // weight would pin it first under manual
		placed(1, "DE", 0, 0, false),
		placed(2, "NL", 0, 0, false),
		placed(3, "", 0, 0, false),
	}
	led := map[int64]int{}
	for range 400 {
		got := Order(servers, model.OrderRandom, "NL", nil, nil)
		if len(got) != len(servers) {
			t.Fatalf("a random fetch dropped servers: %v", ids(got))
		}
		seen := map[int64]bool{}
		for _, s := range got {
			seen[s.Set.ServerID] = true
		}
		if len(seen) != len(servers) {
			t.Fatalf("a random fetch repeated a server: %v", ids(got))
		}
		led[got[0].Set.ServerID]++
	}
	for _, s := range servers {
		if led[s.Set.ServerID] == 0 {
			t.Errorf("server %d never came first in 400 random fetches: %v", s.Set.ServerID, led)
		}
	}
}

// Shuffling is only the order. What hides a server — full with hide-when-full, or
// over its traffic cap with hide-when-over — hides it here too, and the rescue that
// never hands out an empty list still applies.
func TestOrderRandomKeepsTheHidingRules(t *testing.T) {
	prev := shuffle
	t.Cleanup(func() { shuffle = prev })
	shuffle = func(n int, swap func(i, j int)) { // reverse: a known, non-identity order
		for i := 0; i < n/2; i++ {
			swap(i, n-1-i)
		}
	}

	over := placed(2, "", 0, 0, false)
	over.Set.ServerPlacement.HideWhenOver = true
	servers := []Server{placed(0, "", 0, 10, true), placed(1, "", 0, 10, true), over, placed(3, "", 0, 0, false)}
	got := ids(Order(servers, model.OrderRandom, "", map[int64]int{0: 10}, map[int64]bool{2: true}))
	if !equal(got, []int64{3, 1}) {
		t.Errorf("random with a full and an over-cap server: got %v, want [3 1]", got)
	}

	full := []Server{placed(0, "", 0, 1, true), placed(1, "", 0, 1, true)}
	got = ids(Order(full, model.OrderRandom, "", map[int64]int{0: 1, 1: 1}, nil))
	if !equal(got, []int64{1, 0}) {
		t.Errorf("random with every server full: got %v, want both kept [1 0]", got)
	}
}
