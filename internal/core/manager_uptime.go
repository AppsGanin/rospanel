package core

import (
	"sort"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The public status page's data: a liveness sample per server per watch tick,
// folded into a daily rollup, and the read side that turns it into rows a page can
// render. See migration 0043 for the storage shape.

// SampleUptime records one liveness sample for every server. Called from the node
// watch loop, which already runs a minute apart and already reads the node list.
//
// The master samples "is Xray up", not "is the panel up": the panel being down is
// self-evident (nothing serves the page), while Xray being down is the outage a
// customer would notice and the operator would be asked about.
func (m *Manager) SampleUptime() {
	day := time.Now().In(m.loc()).Format("2006-01-02")
	if err := m.store.RecordUptimeSample(model.LocalNodeID, day, m.sup.Running()); err != nil {
		logErr("uptime: sample failed", "node", model.LocalNodeID, "err", err)
		return // a failing write will fail for every node too; don't repeat it N times
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for i := range nodes {
		n := &nodes[i]
		// A node switched off, or never installed, is not an outage — sampling it
		// would drag the fleet's uptime down for a decision the operator made.
		if !n.Enabled || !n.Joined() {
			continue
		}
		up := n.Online(now) && n.XrayRunning
		if err := m.store.RecordUptimeSample(n.ID, day, up); err != nil {
			logErr("uptime: sample failed", "node", n.ID, "err", err)
		}
	}
}

// PurgeOldUptime drops history past the retention window. Called from the retention
// sweep.
func (m *Manager) PurgeOldUptime() {
	cutoff := time.Now().In(m.loc()).AddDate(0, 0, -model.UptimeRetentionDays).Format("2006-01-02")
	if _, err := m.store.PurgeUptime(cutoff); err != nil {
		logErr("uptime: retention sweep failed", "err", err)
	}
}

// StatusServer is one server as the public page shows it. Deliberately narrow: a
// name, a state and a history. No host, no address, no version — the page answers
// to strangers, and everything beyond "is it working" is reconnaissance.
type StatusServer struct {
	Name    string
	Up      bool
	Days    []StatusDay // oldest first, one per day in the window
	Uptime  float64     // percentage over the window, 0–100
	Samples int         // total samples behind Uptime (0 ⇒ no history yet)
}

// StatusDay is one day's bar on the page.
type StatusDay struct {
	Day     string
	Ratio   float64 // 0–1; only meaningful when Samples > 0
	Samples int
}

// StatusReport is the whole page's data.
type StatusReport struct {
	Servers []StatusServer
	AllUp   bool
	Days    int // width of the window actually rendered
	// At is when the report was assembled, in the OPERATOR's timezone — the same
	// clock every other date in the panel is printed on. The page shows it, and a
	// reader deciding whether an outage is current needs it to mean what they think.
	At time.Time
}

// StatusPageData assembles the status report over the last `days` days.
//
// Servers currently switched off are left out entirely rather than shown as down:
// to a customer reading the page, a server the operator retired is not an
// incident, and listing it as broken invites tickets about a machine that no
// longer exists.
func (m *Manager) StatusPageData(days int) (*StatusReport, error) {
	if days <= 0 || days > model.UptimeRetentionDays {
		days = model.UptimeRetentionDays
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	from := now.In(m.loc()).AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	history, err := m.store.UptimeSince(from)
	if err != nil {
		return nil, err
	}
	byNode := make(map[int64]map[string]model.UptimeDay, len(nodes)+1)
	for _, h := range history {
		if byNode[h.NodeID] == nil {
			byNode[h.NodeID] = map[string]model.UptimeDay{}
		}
		byNode[h.NodeID][h.Day] = h
	}

	// The calendar the bars are laid out on: every day in the window exists as a
	// column, whether or not it was sampled, so a gap reads as a gap.
	calendar := make([]string, 0, days)
	for i := days - 1; i >= 0; i-- {
		calendar = append(calendar, now.In(m.loc()).AddDate(0, 0, -i).Format("2006-01-02"))
	}

	rep := &StatusReport{AllUp: true, Days: days, At: now.In(m.loc())}
	unix := now.Unix()
	add := func(id int64, name string, up bool) {
		s := StatusServer{Name: name, Up: up}
		var upSamples, total int
		for _, day := range calendar {
			d := byNode[id][day]
			s.Days = append(s.Days, StatusDay{Day: day, Samples: d.Total, Ratio: ratio(d.Up, d.Total)})
			upSamples += d.Up
			total += d.Total
		}
		s.Samples = total
		s.Uptime = ratio(upSamples, total) * 100
		if !up {
			rep.AllUp = false
		}
		rep.Servers = append(rep.Servers, s)
	}

	// The master's own label when the operator set one (multi-node installs name
	// their servers), else the generic local-node name.
	masterName := set.MasterLabel
	if masterName == "" {
		masterName = model.LocalNodeName
	}
	add(model.LocalNodeID, masterName, m.sup.Running())
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() {
			continue
		}
		add(n.ID, n.Name, n.Online(unix) && n.XrayRunning)
	}
	return rep, nil
}

func ratio(up, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(up) / float64(total)
}
