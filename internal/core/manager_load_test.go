package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The report has one point per step across the whole window, gaps as nulls, and
// outages clipped to the window with an ongoing one counted up to now.
func TestServerLoadReport(t *testing.T) {
	t.Parallel()
	m, st := uptimeManager(t)
	now := time.Now().Unix()
	if err := st.RecordLoadTick(now-120, []model.LoadSample{{NodeID: 0, Up: true, HasStats: true, CPU: 12.34, Mem: 50, Disk: 30, RX: 1000, TX: 500, Online: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordLoadTick(now-60, []model.LoadSample{{NodeID: 0}}); err != nil {
		t.Fatal(err)
	}
	rep, err := m.ServerLoad(0, "bogus")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Range != "24h" || rep.Step != 300 || len(rep.Points) != 288 {
		t.Fatalf("range %s step %d points %d, want 24h/300/288", rep.Range, rep.Step, len(rep.Points))
	}
	if rep.Points[0].Up != nil || rep.Points[0].CPU != nil {
		t.Errorf("an unsampled step = %+v, want nulls", rep.Points[0])
	}
	var sampled int
	for _, p := range rep.Points {
		if p.Up != nil {
			sampled++
		}
	}
	if sampled == 0 || rep.Samples != 2 || rep.Uptime != 50 {
		t.Errorf("sampled steps %d, samples %d, uptime %.1f; want some, 2, 50", sampled, rep.Samples, rep.Uptime)
	}
	// Traffic is the minute rates times a minute; the down sample adds nothing.
	if rep.RXBytes != 60_000 || rep.TXBytes != 30_000 || rep.OnlineMax != 4 {
		t.Errorf("rx %d tx %d online max %d, want 60000/30000/4", rep.RXBytes, rep.TXBytes, rep.OnlineMax)
	}
	var disk bool
	for _, p := range rep.Points {
		if p.Disk != nil && *p.Disk == 30 && p.RX != nil && *p.RX == 1000 && p.Online != nil {
			disk = true
		}
	}
	if !disk {
		t.Error("no point carries the disk, network and online figures")
	}
	if len(rep.Outages) != 1 || rep.Outages[0].End != 0 || rep.DownSeconds < 60 {
		t.Errorf("outages %+v down %d, want one ongoing ≥ 60 s", rep.Outages, rep.DownSeconds)
	}
	for _, r := range []string{"7d", "30d"} {
		rep, err := m.ServerLoad(0, r)
		if err != nil || len(rep.Points) < 300 || len(rep.Points) > 400 {
			t.Errorf("%s: %v, %d points", r, err, len(rep.Points))
		}
	}
	if _, err := m.ServerLoad(999, "24h"); err == nil {
		t.Error("an unknown server got a report")
	}
}
