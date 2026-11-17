package core

import (
	"math"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Load history behind a server card's charts: CPU, RAM and whether the server was
// up, sampled on the node watch tick next to the status page's uptime. See
// migration 0146 for the storage shape.

// loadStaleGap is how long the panel may be gone before an outage it left open is
// ended at the server's last row rather than carried on. An update restarts the
// panel within a minute or two; an outage across it is still one outage.
const loadStaleGap = 10 * 60

// loadSampleSeconds is how often sampleLoad runs (the node watch tick): one sample
// stands for that long when the minute rates are summed into traffic.
const loadSampleSeconds = int64(nodeWatchInterval / time.Second)

// sampleLoad records one tick for every watched server: the master, and each node
// switched on and installed (the same set SampleUptime samples, with the same
// meaning of "up").
func (m *Manager) sampleLoad(nodes []model.Node, now time.Time) {
	unix := now.Unix()
	if !m.loadBooted {
		m.loadBooted = true
		if err := m.store.CloseStaleOutages(unix, loadStaleGap); err != nil {
			logErr("load: closing stale outages failed", "err", err)
		}
	}
	online := m.OnlineByServer()
	samples := make([]model.LoadSample, 0, len(nodes)+1)
	local := model.LoadSample{NodeID: model.LocalNodeID, Up: m.sup.Running(), Online: online[model.LocalNodeID]}
	if m.sys != nil {
		st := m.sys.Read()
		local.HasStats = st.MemTotal > 0
		local.CPU, local.Mem = st.CPUAvg, pctOf(st.MemUsed, st.MemTotal)
		local.Disk = pctOf(st.DiskUsed, st.DiskTotal)
		local.RX, local.TX = float64(st.NetDownAvg), float64(st.NetUpAvg)
	}
	samples = append(samples, local)
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() {
			continue
		}
		live := n.Online(unix)
		s := model.LoadSample{NodeID: n.ID, Up: live && n.XrayRunning, Online: online[n.ID]}
		// Host stats are the last ones reported: from a node gone quiet they are
		// stale, and a flat line of them would hide that it went quiet.
		if h, ok := m.NodeHostStats(n.ID); ok && live && h.MemTotal > 0 {
			cpu := h.CPUAvg
			if cpu == 0 {
				cpu = h.CPUPercent // an agent older than the average
			}
			rx, tx := h.NetDownAvg, h.NetUpAvg
			if rx == 0 && tx == 0 {
				rx, tx = h.NetDown, h.NetUp // likewise
			}
			s.HasStats, s.CPU, s.Mem = true, cpu, pctOf(h.MemUsed, h.MemTotal)
			s.Disk, s.RX, s.TX = pctOf(h.DiskUsed, h.DiskTotal), float64(rx), float64(tx)
		}
		samples = append(samples, s)
	}
	if err := m.store.RecordLoadTick(unix, samples); err != nil {
		logErr("load: sample failed", "err", err)
	}
}

func pctOf(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

// PurgeOldLoad drops load history past its window. Called from the retention sweep.
func (m *Manager) PurgeOldLoad() {
	now := time.Now()
	load := now.AddDate(0, 0, -model.LoadRetentionDays).Unix()
	outages := now.AddDate(0, 0, -model.UptimeRetentionDays).Unix()
	if err := m.store.PurgeLoad(load, outages); err != nil {
		logErr("load: retention sweep failed", "err", err)
	}
}

// loadRanges are the windows the charts offer and the width of one point in each:
// about three hundred points whichever is picked.
var loadRanges = map[string]struct{ span, step time.Duration }{
	"24h": {24 * time.Hour, 5 * time.Minute},
	"7d":  {7 * 24 * time.Hour, 30 * time.Minute},
	"30d": {30 * 24 * time.Hour, 2 * time.Hour},
}

// LoadReport is one server's charts over a window.
type LoadReport struct {
	Range string `json:"range"`
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Step  int64  `json:"step"`
	// Points has one entry per step in the window, oldest first. Up is the share of
	// samples that found the server up (null with no samples: the panel was not
	// watching); CPU and Mem are null when the server reported nothing.
	Points []LoadChartPoint `json:"points"`
	// Outages are those overlapping the window, clipped to it; End is 0 while one
	// lasts.
	Outages []LoadOutage `json:"outages"`
	// Uptime is the percentage of samples in the window that found the server up,
	// Samples how many there were (0 ⇒ no history yet).
	Uptime  float64 `json:"uptime"`
	Samples int     `json:"samples"`
	// DownSeconds is the outages' total length inside the window.
	DownSeconds int64 `json:"down_seconds"`
	// RXBytes and TXBytes are the traffic in and out over the window, from the
	// minute averages (each sample stands for its minute); OnlineMax is the most
	// users seen at once.
	RXBytes   int64 `json:"rx_bytes"`
	TXBytes   int64 `json:"tx_bytes"`
	OnlineMax int   `json:"online_max"`
}

// LoadChartPoint is one step of the charts.
type LoadChartPoint struct {
	T      int64    `json:"t"`
	CPU    *float64 `json:"cpu"`
	CPUMax *float64 `json:"cpu_max"`
	Mem    *float64 `json:"mem"`
	MemMax *float64 `json:"mem_max"`
	Up     *float64 `json:"up"`
	// Disk is percent in use; RX and TX bytes per second; Online users. Null on
	// steps that did not measure them.
	Disk      *float64 `json:"disk"`
	DiskMax   *float64 `json:"disk_max"`
	RX        *float64 `json:"rx"`
	RXMax     *float64 `json:"rx_max"`
	TX        *float64 `json:"tx"`
	TXMax     *float64 `json:"tx_max"`
	Online    *float64 `json:"online"`
	OnlineMax *int     `json:"online_max"`
}

// LoadOutage is an outage as the charts draw it.
type LoadOutage struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// ServerLoad returns server id's load history over rangeKey ("24h", "7d", "30d";
// anything else is 24h).
func (m *Manager) ServerLoad(id int64, rangeKey string) (*LoadReport, error) {
	if id != model.LocalNodeID {
		n, err := m.store.GetNode(id)
		if err != nil {
			return nil, err
		}
		if n == nil {
			return nil, invalidCode("err.nodeNotFound", "нода не найдена")
		}
	}
	r, ok := loadRanges[rangeKey]
	if !ok {
		rangeKey, r = "24h", loadRanges["24h"]
	}
	now := time.Now().Unix()
	step := int64(r.step / time.Second)
	// The window ends at the step holding now and covers span before it.
	last := now - now%step
	from := last - int64(r.span/time.Second) + step
	rows, err := m.store.LoadSeries(id, from, step)
	if err != nil {
		return nil, err
	}
	outages, err := m.store.OutagesSince(id, from)
	if err != nil {
		return nil, err
	}
	rep := &LoadReport{Range: rangeKey, From: from, To: now, Step: step}
	byTS := make(map[int64]model.LoadPoint, len(rows))
	var up, total int
	var rx, tx float64
	for _, p := range rows {
		byTS[p.TS] = p
		up += p.Up
		total += p.Total
		rx += p.RXSum
		tx += p.TXSum
		if p.Ext > 0 {
			rep.OnlineMax = max(rep.OnlineMax, p.OnlineMax)
		}
	}
	rep.RXBytes, rep.TXBytes = int64(rx*float64(loadSampleSeconds)), int64(tx*float64(loadSampleSeconds))
	rep.Samples = total
	rep.Uptime = ratio(up, total) * 100
	rep.Points = make([]LoadChartPoint, 0, (last-from)/step+1)
	for t := from; t <= last; t += step {
		pt := LoadChartPoint{T: t}
		if p, ok := byTS[t]; ok {
			if p.Total > 0 {
				pt.Up = ptr(ratio(p.Up, p.Total))
			}
			if p.Stats > 0 {
				pt.CPU, pt.CPUMax = ptr(round1(p.CPU)), ptr(round1(p.CPUMax))
				pt.Mem, pt.MemMax = ptr(round1(p.Mem)), ptr(round1(p.MemMax))
			}
			if p.ExtStats > 0 {
				pt.Disk, pt.DiskMax = ptr(round1(p.Disk)), ptr(round1(p.DiskMax))
				pt.RX, pt.RXMax = ptr(math.Round(p.RX)), ptr(p.RXMax)
				pt.TX, pt.TXMax = ptr(math.Round(p.TX)), ptr(p.TXMax)
			}
			if p.Ext > 0 {
				pt.Online, pt.OnlineMax = ptr(round1(p.Online)), ptr(p.OnlineMax)
			}
		}
		rep.Points = append(rep.Points, pt)
	}
	rep.Outages = make([]LoadOutage, 0, len(outages))
	for _, o := range outages {
		start, end := max(o.StartedAt, from), o.EndedAt
		upTo := end
		if end == 0 {
			upTo = now
		}
		if upTo < start {
			continue
		}
		rep.DownSeconds += upTo - start
		rep.Outages = append(rep.Outages, LoadOutage{Start: start, End: end})
	}
	return rep, nil
}

func ptr[T any](v T) *T { return &v }

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
