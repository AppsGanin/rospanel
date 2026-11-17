package store

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Samples fold into five-minute rows (averages stay exact through a coarser step),
// and the outage log opens on the first down sample and closes on the next up one.
func TestRecordLoadTickFoldsAndTracksOutages(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	base := int64(1_800_000_000) // a multiple of 300
	tick := func(at int64, samples ...model.LoadSample) {
		t.Helper()
		if err := s.RecordLoadTick(at, samples); err != nil {
			t.Fatalf("RecordLoadTick: %v", err)
		}
	}
	up := func(id int64, cpu, mem float64) model.LoadSample {
		return model.LoadSample{NodeID: id, Up: true, HasStats: true, CPU: cpu, Mem: mem}
	}
	down := func(id int64) model.LoadSample { return model.LoadSample{NodeID: id} }

	tick(base, up(0, 10, 40), up(7, 50, 20))
	tick(base+60, up(0, 30, 60), down(7))
	tick(base+120, up(0, 20, 50), down(7))
	tick(base+360, up(0, 40, 50), up(7, 10, 10)) // next bucket; 7 back up

	rows, err := s.LoadSeries(0, base, model.LoadBucket)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].TS != base || rows[0].Total != 3 || rows[0].CPU != 20 || rows[0].CPUMax != 30 || rows[0].Mem != 50 {
		t.Fatalf("master rows = %+v", rows)
	}
	// A coarser step averages over samples, not over rows.
	rows, _ = s.LoadSeries(0, base, 3600)
	if len(rows) != 1 || rows[0].CPU != 25 || rows[0].Total != 4 {
		t.Fatalf("hourly master = %+v", rows)
	}
	// Down samples count against uptime but carry no CPU.
	rows, _ = s.LoadSeries(7, base, model.LoadBucket)
	if rows[0].Up != 1 || rows[0].Total != 3 || rows[0].Stats != 1 || rows[0].CPU != 50 {
		t.Fatalf("node row = %+v", rows[0])
	}

	out, err := s.OutagesSince(7, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].StartedAt != base+60 || out[0].EndedAt != base+360 {
		t.Fatalf("outages = %+v, want one from +60 to +360", out)
	}
}

// A server no longer sampled (switched off, deleted) has no ongoing outage.
func TestRecordLoadTickClosesUnwatched(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	base := int64(1_800_000_000)
	_ = s.RecordLoadTick(base, []model.LoadSample{{NodeID: 0, Up: true}, {NodeID: 3}})
	_ = s.RecordLoadTick(base+60, []model.LoadSample{{NodeID: 0, Up: true}})
	out, _ := s.OutagesSince(3, 0)
	if len(out) != 1 || out[0].EndedAt != base+60 {
		t.Fatalf("outages = %+v, want closed at the tick that stopped watching it", out)
	}
}

// After a long gap the open outage ends at the server's last row; after a short
// one (an update) it carries on.
func TestCloseStaleOutages(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	base := int64(1_800_000_000)
	_ = s.RecordLoadTick(base, []model.LoadSample{{NodeID: 0, Up: true}, {NodeID: 4}})

	if err := s.CloseStaleOutages(base+300, 600); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.OutagesSince(4, 0); out[0].EndedAt != 0 {
		t.Fatalf("short gap closed the outage: %+v", out)
	}
	if err := s.CloseStaleOutages(base+7200, 600); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.OutagesSince(4, 0); out[0].EndedAt != base+model.LoadBucket {
		t.Fatalf("long gap: %+v, want ended at the last row's end", out)
	}
}

func TestPurgeLoad(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	base := int64(1_800_000_000)
	_ = s.RecordLoadTick(base, []model.LoadSample{{NodeID: 1}})
	_ = s.RecordLoadTick(base+60, []model.LoadSample{{NodeID: 1, Up: true}})
	_ = s.RecordLoadTick(base+600, []model.LoadSample{{NodeID: 1}}) // ongoing
	if err := s.PurgeLoad(base+600, base+600); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.LoadSeries(1, 0, model.LoadBucket)
	out, _ := s.OutagesSince(1, 0)
	if len(rows) != 1 || len(out) != 1 || out[0].EndedAt != 0 {
		t.Fatalf("after purge rows=%+v outages=%+v, want only the newest of each", rows, out)
	}
}

// Disk and network average over the samples that carried stats, online over every
// sample; a row from before migration 0148 (ext = 0) reads as unmeasured.
func TestLoadSeriesExtraFigures(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	base := int64(1_800_000_000)
	_ = s.RecordLoadTick(base, []model.LoadSample{{NodeID: 0, Up: true, HasStats: true, Disk: 40, RX: 1000, TX: 300, Online: 5}})
	_ = s.RecordLoadTick(base+60, []model.LoadSample{{NodeID: 0, Online: 3}}) // down: no stats
	_ = s.RecordLoadTick(base+120, []model.LoadSample{{NodeID: 0, Up: true, HasStats: true, Disk: 42, RX: 3000, TX: 100, Online: 1}})
	rows, err := s.LoadSeries(0, base, model.LoadBucket)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.Ext != 3 || r.ExtStats != 2 || r.Disk != 41 || r.DiskMax != 42 || r.RX != 2000 || r.RXMax != 3000 ||
		r.TX != 200 || r.RXSum != 4000 || r.Online != 3 || r.OnlineMax != 5 {
		t.Fatalf("row = %+v", r)
	}

	// An old row: no ext, so nothing derived from the new columns.
	if _, err := s.db.Exec(`INSERT INTO server_load (node_id, ts, cpu_sum, stats, up, total) VALUES (9, ?, 10, 1, 1, 1)`, base); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.LoadSeries(9, base, model.LoadBucket)
	if rows[0].Ext != 0 || rows[0].ExtStats != 0 || rows[0].Online != 0 || rows[0].CPU != 10 {
		t.Fatalf("old row = %+v", rows[0])
	}
}
