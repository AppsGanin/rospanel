package core

import (
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sysstat"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// PollStats reads per-user traffic from Xray, accumulates lifetime totals
// (handling counter resets on Xray restart), and enforces quotas/expiry.
func (m *Manager) PollStats() error {
	stats, err := m.sup.QueryStats(m.sup.APIAddr())
	if err != nil {
		return err
	}
	users, err := m.store.ListUsers()
	if err != nil {
		return err
	}
	today := time.Now().In(m.loc()).Format("2006-01-02") // operator-local calendar day
	now := time.Now().Unix()
	// Collect the whole cycle first, then commit it in one transaction. Written
	// per-user this was three fsyncs per active user on a single connection, which is
	// what put a hard ceiling on how many users the panel could account for at all.
	deltas := make([]store.TrafficDelta, 0, len(users))
	for _, u := range users {
		t, ok := stats[fmt.Sprintf("u%d", u.ID)]
		if !ok {
			continue
		}
		addUp, addDown := t.Up-u.LastUp, t.Down-u.LastDown
		if t.Up < u.LastUp { // Xray restarted → counter reset to 0
			addUp = t.Up
		}
		if t.Down < u.LastDown {
			addDown = t.Down
		}
		if addUp != 0 || addDown != 0 || t.Up != u.LastUp || t.Down != u.LastDown {
			au, ad := nonNeg(addUp), nonNeg(addDown)
			d := store.TrafficDelta{
				UserID: u.ID, NodeID: model.LocalNodeID, Day: today,
				AddUp: au, AddDown: ad,
				// The local poller reads cumulative counters, so it records where it
				// read them for the next cycle to subtract from.
				Baseline: &store.TrafficBaseline{Up: t.Up, Down: t.Down},
			}
			if au > 0 || ad > 0 {
				d.SeenAt = now // online (all protocols)
			}
			deltas = append(deltas, d)
		}
	}
	if err := m.store.ApplyTrafficDeltas(deltas); err != nil {
		logErr("stats: traffic batch failed", "users", len(deltas), "err", err)
	}
	// Re-baseline a reset user's counters to the live Xray value (reusing the stats
	// already fetched above) so the next poll measures the delta from the reset.
	m.applyResets(users, time.Now().Unix(), func(id int64) (int64, int64) {
		t := stats[fmt.Sprintf("u%d", id)]
		return t.Up, t.Down
	})
	return m.enforceAfterTraffic(users)
}

// enforceAfterTraffic runs the post-accounting tail shared by the local poll
// (PollStats) and remote-node traffic ingest (IngestNodeSync): alert on status
// transitions, sync users if someone crossed a limit/expiry, and apply billing
// downgrades. `users` is the pre-enforcement snapshot used for transition alerts.
func (m *Manager) enforceAfterTraffic(users []model.User) error {
	// Alert admins when a user crosses active → expired / out-of-quota / over-device.
	m.notifyStatusTransitions(users)
	// Reconcile if the working set changed since the last applied config — e.g. a
	// user just crossed their data limit (traffic) or expiry (time).
	working, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		return err
	}
	if m.workingChanged(working) {
		slog.Info("working set changed (limit/expiry), syncing users")
		m.TriggerUserSync()
	}
	// Downgrade users whose paid/trial period ended to the free plan (no-op unless
	// billing is enabled with a configured free plan).
	_ = m.EnforceBilling(time.Now().Unix())
	return nil
}

// Summary is the dashboard overview.
type Summary struct {
	Users        int   `json:"users"`
	EnabledUsers int   `json:"enabled_users"`
	TotalUp      int64 `json:"total_up"`
	TotalDown    int64 `json:"total_down"`
	TrafficToday int64 `json:"traffic_today"` // up+down for the current local-time day
	XrayRunning  bool  `json:"xray_running"`
	CertDaysLeft int   `json:"cert_days_left"`
}

// Summary computes the dashboard overview.
func (m *Manager) Summary() (*Summary, error) {
	c, err := m.store.CountUsers(time.Now().Unix())
	if err != nil {
		return nil, err
	}
	s := &Summary{
		XrayRunning:  m.sup.Running(),
		Users:        c.Total,
		EnabledUsers: c.Active, // actually working (enabled, not expired, within quota)
		TotalUp:      c.TotalUp,
		TotalDown:    c.TotalDown,
	}
	today := time.Now().In(m.loc()).Format("2006-01-02") // operator-local calendar day
	if pts, err := m.store.StatsSeries(0, today, today); err == nil {
		for _, p := range pts {
			s.TrafficToday += p.Up + p.Down
		}
	}
	if info, err := tlsutil.ReadCertInfo(m.tls.CertPath); err == nil {
		s.CertDaysLeft = info.DaysLeft
	}
	return s, nil
}

// StartSysstat begins sampling host metrics (CPU/RAM/disk/network) and the live
// VPN throughput for the dashboard. diskPath selects the filesystem reported
// under "disk".
func (m *Manager) StartSysstat(diskPath string) {
	m.sys = sysstat.New(diskPath)
	go m.vpnSpeedLoop()
}

// TrackVPNViewer marks one active dashboard-stream subscriber for the life of the
// returned release func — call `defer mgr.TrackVPNViewer()()`. vpnSpeedLoop only
// samples Xray (forking `api statsquery` every 3s) while at least one viewer is
// connected, so an unattended panel costs nothing extra.
func (m *Manager) TrackVPNViewer() func() {
	m.vpnViewers.Add(1)
	return func() { m.vpnViewers.Add(-1) }
}

// vpnSpeedLoop derives VPN client throughput from Xray's cumulative stats
// counters, sampled every 3s WHILE the dashboard is open (see TrackVPNViewer).
// The lifetime DB totals update on the separate 60s poll.
func (m *Manager) vpnSpeedLoop() {
	apiAddr := m.sup.APIAddr()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for range t.C {
		if m.vpnViewers.Load() == 0 {
			// Nobody watching → skip the xray-forking sample and clear the baseline
			// so a later resume re-bootstraps cleanly (no smeared first reading).
			m.vpnMu.Lock()
			m.lastVPNT = time.Time{}
			m.vpnMu.Unlock()
			continue
		}
		stats, err := m.sup.QueryStats(apiAddr)
		if err != nil {
			continue
		}
		var up, down int64
		for _, tr := range stats {
			up += tr.Up
			down += tr.Down
		}
		now := time.Now()
		m.vpnMu.Lock()
		dt := now.Sub(m.lastVPNT).Seconds()
		switch {
		case m.lastVPNT.IsZero(): // first sample, no baseline yet
		case up < m.lastVPNUp || down < m.lastVPNDown: // counters reset (Xray restart)
			m.vpnUp, m.vpnDown = 0, 0
		case dt > 0:
			m.vpnUp = int64(float64(up-m.lastVPNUp) / dt)
			m.vpnDown = int64(float64(down-m.lastVPNDown) / dt)
		}
		m.lastVPNUp, m.lastVPNDown = up, down
		m.lastVPNT = now
		m.vpnMu.Unlock()
	}
}

// SystemStatus is the full dashboard payload: host resources plus Xray/panel
// state.
type SystemStatus struct {
	sysstat.Stats
	XrayRunning  bool   `json:"xray_running"`
	XrayUptime   int64  `json:"xray_uptime"` // seconds
	XrayVersion  string `json:"xray_version"`
	Goroutines   int    `json:"goroutines"`
	CPUCores     int    `json:"cpu_cores"`
	ProcMem      int64  `json:"proc_mem"` // panel process RSS
	VPNUp        int64  `json:"vpn_up"`   // VPN client throughput (bytes/sec)
	VPNDown      int64  `json:"vpn_down"`
	TotalUp      int64  `json:"total_up"`
	TotalDown    int64  `json:"total_down"`
	Users        int    `json:"users"`
	EnabledUsers int    `json:"enabled_users"`
	TrafficToday int64  `json:"traffic_today"`
	CertDaysLeft int    `json:"cert_days_left"`
}

// SystemStatus assembles the dashboard payload from the host sampler, the Xray
// supervisor and the user summary.
func (m *Manager) SystemStatus() (*SystemStatus, error) {
	sum, err := m.Summary()
	if err != nil {
		return nil, err
	}
	s := &SystemStatus{
		XrayRunning:  sum.XrayRunning,
		XrayUptime:   m.sup.UptimeSeconds(),
		XrayVersion:  m.sup.Version(),
		Goroutines:   runtime.NumGoroutine(),
		CPUCores:     runtime.NumCPU(),
		ProcMem:      sysstat.ProcMem(),
		TotalUp:      sum.TotalUp,
		TotalDown:    sum.TotalDown,
		Users:        sum.Users,
		EnabledUsers: sum.EnabledUsers,
		TrafficToday: sum.TrafficToday,
		CertDaysLeft: sum.CertDaysLeft,
	}
	if m.sys != nil {
		s.Stats = m.sys.Read()
	}
	m.vpnMu.Lock()
	s.VPNUp, s.VPNDown = m.vpnUp, m.vpnDown
	m.vpnMu.Unlock()
	return s, nil
}

// StatsSeries returns per-day traffic between from/to (YYYY-MM-DD); userID 0 = all.
func (m *Manager) StatsSeries(userID int64, from, to string) ([]model.DailyPoint, error) {
	return m.store.StatsSeries(userID, from, to)
}

// NodeTraffic is one server's share of the traffic over a period.
type NodeTraffic struct {
	NodeID int64  `json:"node_id"` // 0 = the panel's own server
	Name   string `json:"name"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

// NodeTrafficBreakdown splits a period's traffic by the server that carried it,
// busiest first. userID 0 covers everyone, matching StatsSeries.
//
// Names are resolved here rather than left to the client: the caller would
// otherwise need the node list too, and an operator without rights to see nodes
// still gets the breakdown this way. A deleted node keeps its traffic rows (they
// carry the numeric id), so it is named rather than dropped — its bytes were real.
func (m *Manager) NodeTrafficBreakdown(userID int64, from, to string) ([]NodeTraffic, error) {
	totals, err := m.store.NodeTrafficTotals(userID, from, to)
	if err != nil {
		return nil, err
	}
	names, _ := m.store.NodeNames() // best-effort: a failure just falls back to ids
	if names == nil {
		names = map[int64]string{}
	}
	names[model.LocalNodeID] = "Этот сервер"
	out := make([]NodeTraffic, 0, len(totals))
	for id, t := range totals {
		if t[0] == 0 && t[1] == 0 {
			continue
		}
		name := names[id]
		if name == "" {
			name = fmt.Sprintf("сервер #%d", id) // purged tombstone: id is all that's left
		}
		out = append(out, NodeTraffic{NodeID: id, Name: name, Up: t[0], Down: t[1]})
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := out[i].Up+out[i].Down, out[j].Up+out[j].Down
		if li != lj {
			return li > lj
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out, nil
}

// StatsByUser returns per-user totals over the period.
func (m *Manager) StatsByUser(from, to string) ([]model.UserTotal, error) {
	return m.store.StatsByUser(from, to)
}

// ResetStats clears the entire per-day traffic history.
func (m *Manager) ResetStats() error {
	return m.store.ResetDailyStats()
}

// PurgeOldTraffic drops per-day traffic history past the retention window. Shares
// the journals' slow timer; safe to call as often as you like. The cutoff is a
// calendar day in the operator's timezone rather than a duration, because that is
// the calendar the rows were written on — deriving it any other way would shift the
// boundary by a day for anyone east or west of UTC.
func (m *Manager) PurgeOldTraffic() {
	cutoff := time.Now().In(m.loc()).
		AddDate(0, 0, -model.TrafficDailyRetentionDays).Format("2006-01-02")
	n, err := m.store.PurgeTrafficDaily(cutoff)
	if err != nil {
		logErr("stats: traffic retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("stats: old traffic history purged",
			"count", n, "older_than_days", model.TrafficDailyRetentionDays)
	}
}
