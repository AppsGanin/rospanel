package store

import "testing"

// A node's lanes commit with its report: a resend of a counted report adds nothing.
func TestLaneTrafficWithNodeReport(t *testing.T) {
	t.Parallel()
	st := openNodeStore(t)
	n, _ := st.CreateNode("lanes", "l.example.com", "")
	lanes := []LaneDelta{{NodeID: n.ID, Lane: "naive-in", Day: "2026-10-12", Up: 100, Down: 1000},
		{NodeID: n.ID, Lane: "awg", Day: "2026-10-12", Up: 5, Down: 50}}
	for range 2 { // the second is the node's resend of a report whose answer was lost
		if _, err := st.ApplyNodeReport(n.ID, 4, nil, lanes...); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ApplyLaneTraffic([]LaneDelta{{NodeID: 0, Lane: "vless-in", Day: "2026-10-11", Up: 1, Down: 2},
		{NodeID: 0, Lane: "vless-in", Day: "2026-10-12", Up: 3, Down: 4}}); err != nil {
		t.Fatal(err)
	}
	got := map[[2]any][2]int64{}
	totals, err := st.LaneTrafficTotals("2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range totals {
		got[[2]any{x.NodeID, x.Lane}] = [2]int64{x.Up, x.Down}
	}
	want := map[[2]any][2]int64{
		{n.ID, "naive-in"}: {100, 1000}, {n.ID, "awg"}: {5, 50}, {int64(0), "vless-in"}: {4, 6},
	}
	if len(got) != len(want) {
		t.Fatalf("totals %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%v = %v, want %v", k, got[k], v)
		}
	}
	// The range bounds the days, and the purge and the reset reach the lanes too.
	if only, _ := st.LaneTrafficTotals("2026-10-12", "2026-10-12"); len(only) != 3 {
		t.Fatalf("one day: %v", only)
	}
	if _, err := st.PurgeTrafficDaily("2026-10-12"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.LaneTrafficTotals("2026-10-11", "2026-10-11"); len(left) != 0 {
		t.Fatalf("purge left %v", left)
	}
	if err := st.ResetDailyStats(); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.LaneTrafficTotals("2026-10-01", "2026-10-31"); len(left) != 0 {
		t.Fatalf("reset left %v", left)
	}
}
