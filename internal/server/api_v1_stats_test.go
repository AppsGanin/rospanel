package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/core"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// statsFixture seeds two servers' traffic on two days, with a quiet day between.
func statsFixture(t *testing.T) (h http.Handler, base, key string, days [3]string) {
	t.Helper()
	h, mgr, st := nodeAPITestServer(t)
	user, err := mgr.CreateUser(t.Context(), "stats-user", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	node, err := mgr.CreateNode("stats-node", "stats.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	now := time.Now().In(mgr.Location())
	for i := range days {
		days[i] = now.AddDate(0, 0, i-2).Format("2006-01-02")
	}
	// days[0]: both servers carried traffic. days[1]: nobody did. days[2] (today):
	// only the node did.
	for _, seed := range []struct {
		node int64
		day  string
		up   int64
		down int64
	}{
		{model.LocalNodeID, days[0], 10, 100},
		{node.ID, days[0], 20, 200},
		{node.ID, days[2], 30, 300},
	} {
		if err := st.AddDailyTrafficNode(user.ID, seed.node, seed.day, seed.up, seed.down); err != nil {
			t.Fatalf("seed %s node %d: %v", seed.day, seed.node, err)
		}
	}
	base, key = apiFixture(t, h, st)
	return h, base, key, days
}

// Omitting the window used to answer with an empty array: the raw strings went
// straight into `day BETWEEN ” AND ”`. The panel's own dashboard defaulted to the
// last 30 days all along — the external surface just never called that code.
func TestAPIStatsDefaultsToTheLastThirtyDays(t *testing.T) {
	h, base, key, days := statsFixture(t)

	for _, path := range []string{"/v1/stats/series", "/v1/stats/nodes", "/v1/stats/nodes/series"} {
		rec := apiGet(t, h, base+path, key)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", path, rec.Code, rec.Body.String())
		}
		var out struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		if len(out.Data) == 0 {
			t.Errorf("%s with no from/to answered empty", path)
		}
	}

	// And the daily series covers every day of that window, not only the busy ones.
	var series struct {
		Data []model.DailyPoint `json:"data"`
	}
	rec := apiGet(t, h, base+"/v1/stats/series", key)
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Data) != 30 {
		t.Errorf("default window carries %d points, want 30", len(series.Data))
	}
	quiet := false
	for _, p := range series.Data {
		if p.Day == days[1] {
			quiet = true
			if p.Up != 0 || p.Down != 0 {
				t.Errorf("the quiet day carries traffic: %+v", p)
			}
		}
	}
	if !quiet {
		t.Errorf("the day with no traffic is missing from the series entirely")
	}
}

// The per-server daily split: what used to take one call per day.
func TestAPIStatsNodeSeriesSplitsByDayAndServer(t *testing.T) {
	h, base, key, days := statsFixture(t)

	rec := apiGet(t, h, base+"/v1/stats/nodes/series?from="+days[0]+"&to="+days[2], key)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data []core.NodeDailyTraffic `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Two servers carried something in the window, three days wide, every cell
	// present — a line with holes is not the same shape as a line with zeros.
	if len(out.Data) != 6 {
		t.Fatalf("got %d rows, want 6 (2 servers × 3 days): %+v", len(out.Data), out.Data)
	}
	cell := map[string]core.NodeDailyTraffic{}
	for _, r := range out.Data {
		cell[r.Day+"/"+r.Name] = r
	}
	if got := cell[days[0]+"/"+model.LocalNodeName]; got.Down != 100 {
		t.Errorf("master on the busy day = %+v, want down=100", got)
	}
	if got := cell[days[0]+"/stats-node"]; got.Down != 200 {
		t.Errorf("node on the busy day = %+v, want down=200", got)
	}
	if got, ok := cell[days[1]+"/stats-node"]; !ok || got.Up != 0 || got.Down != 0 {
		t.Errorf("node on the quiet day = %+v (present=%v), want an explicit zero", got, ok)
	}
	if got := cell[days[2]+"/stats-node"]; got.Down != 300 {
		t.Errorf("node today = %+v, want down=300", got)
	}
	// The totals endpoint must agree with the series summed over the same window.
	var totals struct {
		Data []core.NodeTraffic `json:"data"`
	}
	rec = apiGet(t, h, base+"/v1/stats/nodes?from="+days[0]+"&to="+days[2], key)
	if err := json.Unmarshal(rec.Body.Bytes(), &totals); err != nil {
		t.Fatalf("decode totals: %v", err)
	}
	summed := map[int64]int64{}
	for _, r := range out.Data {
		summed[r.NodeID] += r.Down
	}
	for _, tot := range totals.Data {
		if summed[tot.NodeID] != tot.Down {
			t.Errorf("server %d: series sums to %d, totals say %d",
				tot.NodeID, summed[tot.NodeID], tot.Down)
		}
	}
}

// A malformed window is answered rather than quietly treated as "everything".
func TestAPIStatsRejectsABadWindow(t *testing.T) {
	h, base, key, days := statsFixture(t)
	for _, q := range []string{"?from=yesterday", "?to=2026-13-40", "?from=" + days[2] + "&to=" + days[0]} {
		if rec := apiGet(t, h, base+"/v1/stats/series"+q, key); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", q, rec.Code, rec.Body.String())
		}
	}
}
