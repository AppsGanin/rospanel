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
	"github.com/AppsGanin/rospanel/internal/xray"
)

// StatsTickEvery is how often TickStats runs: how late, at most, the master notices a
// user of its own Xray running past their quota. The traffic itself is still written
// once per StatsFlushEvery.
const (
	StatsTickEvery  = 10 * time.Second
	StatsFlushEvery = 60 * time.Second
)

// quotaWatch is what TickStats measures between two flushes: for every user with
// quota left, the Xray counters at the last flush (keyed by stats name, "u<id>") and
// how many bytes they had left then.
type quotaWatch struct {
	at    time.Time // the last flush attempt, written or not
	users map[string]quotaLeft
}

type quotaLeft struct {
	up, down int64 // Xray counters at the last flush
	left     int64 // bytes of quota left at the last flush
}

// crossed reports whether a user has used up, since the last flush, the quota they
// had left then. Only the local Xray's bytes count here — a user's other servers
// report through their own path — so it can only be late, never early.
func (w quotaWatch) crossed(stats map[string]xray.Traffic) bool {
	for name, q := range w.users {
		t, ok := stats[name]
		if !ok {
			continue
		}
		up, down := t.Up-q.up, t.Down-q.down
		if t.Up < q.up { // Xray restarted: the counter began again from 0
			up = t.Up
		}
		if t.Down < q.down {
			down = t.Down
		}
		if up+down >= q.left {
			return true
		}
	}
	return false
}

// queryLocalStats reads the local Xray's per-user counters through its gRPC API; a
// flush that cannot get them that way tries the CLI as well.
func (m *Manager) queryLocalStats(flush bool) (map[string]xray.Traffic, error) {
	if m.statsSource != nil {
		return m.statsSource()
	}
	stats, err := m.sup.QueryStatsLive(m.sup.APIAddr())
	if err != nil && flush {
		logWarn("stats: reading the counters over the API failed, using the CLI", "err", err)
		return m.sup.QueryStats(m.sup.APIAddr())
	}
	return stats, err
}

// TickStats runs every StatsTickEvery. It writes the traffic once per StatsFlushEvery,
// and in between reads the counters only while someone has a quota to watch — and
// flushes at once when one of them has used it up, so the user leaves the config
// within a tick instead of up to a minute later. The writes stay one batch a minute
// unless someone crosses.
//
// Only the flush's own failure is returned: a watch read failing between flushes is
// the next flush's to report, not something to log six times a minute.
func (m *Manager) TickStats() error {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	due := time.Since(m.quota.at) >= StatsFlushEvery
	if !due && m.quotaStale.Swap(false) {
		if err := m.rewatchQuota(); err != nil {
			logErr("stats: re-reading quotas failed", "err", err)
		}
	}
	if !due && len(m.quota.users) == 0 {
		return nil
	}
	if due {
		// Counted as tried whatever happens: with Xray down, the next try is a minute
		// away rather than every tick.
		m.quota.at = time.Now()
	}
	stats, err := m.queryLocalStats(due)
	if err != nil {
		if due {
			return err
		}
		return nil
	}
	if !due && !m.quota.crossed(stats) {
		return nil
	}
	return m.pollStatsWith(stats)
}

// rewatchQuota re-reads the watch from the users as they are now, without writing
// anything: a quota set, raised or reset since the last flush is measured against its
// new value from the next tick on, rather than from the next flush. The counters each
// user was last flushed at are the database's baselines, so the watch stays measured
// from the flush it was built on. statsMu is held.
func (m *Manager) rewatchQuota() error {
	bases, err := m.store.QuotaBaselines()
	if err != nil {
		return err
	}
	users := make(map[string]quotaLeft, len(bases))
	for id, b := range bases {
		users[fmt.Sprintf("u%d", id)] = quotaLeft{up: b.Up, down: b.Down, left: b.DataLimit - b.Used}
	}
	m.quota.users = users
	return nil
}

// PollStats reads per-user traffic from Xray, accumulates lifetime totals
// (handling counter resets on Xray restart), and enforces quotas/expiry.
func (m *Manager) PollStats() error {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.quota.at = time.Now()
	stats, err := m.queryLocalStats(true)
	if err != nil {
		return err
	}
	return m.pollStatsWith(stats)
}

// pollStatsWith is PollStats over counters already read. statsMu is held.
func (m *Manager) pollStatsWith(stats map[string]xray.Traffic) error {
	// Only the counters and the quota: what the poll subtracts from and what the watch
	// between polls measures against. Whole users were read here, every minute, to use
	// three columns of them.
	bases, err := m.store.TrafficBaselines()
	if err != nil {
		return err
	}
	today := time.Now().In(m.loc()).Format("2006-01-02") // operator-local calendar day
	now := time.Now().Unix()
	// Collect the whole cycle first, then commit it in one transaction. Written
	// per-user this was three fsyncs per active user on a single connection, which is
	// what put a hard ceiling on how many users the panel could account for at all.
	deltas := make([]store.TrafficDelta, 0, len(stats))
	watch := quotaWatch{at: m.quota.at, users: map[string]quotaLeft{}}
	for id, base := range bases {
		name := fmt.Sprintf("u%d", id)
		t, ok := stats[name]
		if !ok {
			// No counter: nothing used through this Xray since it started, so the watch
			// measures from zero.
			if left := base.DataLimit - base.Used; base.DataLimit > 0 && left > 0 {
				watch.users[name] = quotaLeft{left: left}
			}
			continue
		}
		lastUp, lastDown := base.Up, base.Down
		addUp, addDown := t.Up-lastUp, t.Down-lastDown
		if t.Up < lastUp { // Xray restarted → counter reset to 0
			addUp = t.Up
		}
		if t.Down < lastDown {
			addDown = t.Down
		}
		au, ad := nonNeg(addUp), nonNeg(addDown)
		if left := base.DataLimit - base.Used - au - ad; base.DataLimit > 0 && left > 0 {
			watch.users[name] = quotaLeft{up: t.Up, down: t.Down, left: left}
		}
		if addUp != 0 || addDown != 0 || t.Up != lastUp || t.Down != lastDown {
			d := store.TrafficDelta{
				UserID: id, NodeID: model.LocalNodeID, Day: today,
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
		// The watch keeps what it had; the next flush tries again.
		logErr("stats: traffic batch failed", "users", len(deltas), "err", err)
	} else {
		// A user whose quota a reset below restores is watched against what they had
		// left before it; the worst that does is flush once early.
		m.quota = watch
		m.quotaStale.Store(false)
	}
	// Re-baseline a reset user's counters to the live Xray value (reusing the stats
	// already fetched above) so the next poll measures the delta from the reset. Only
	// users with a reset period can be due one.
	if ids, err := m.store.ResetCandidates(); err != nil {
		logErr("stats: reading reset candidates failed", "err", err)
	} else if due, err := m.store.UserStatesByID(ids); err != nil {
		logErr("stats: reading reset candidates failed", "err", err)
	} else {
		m.applyResets(due, time.Now().Unix(), func(id int64) (int64, int64) {
			t := stats[fmt.Sprintf("u%d", id)]
			return t.Up, t.Down
		})
	}
	return m.enforceTraffic()
}

// trafficEnforceDelay is how long a node's traffic report waits for the enforcement
// pass it triggers, so the reports of the whole fleet within that window share one.
var trafficEnforceDelay = 10 * time.Second

// enforceTraffic runs one enforcement pass over the users as they are now, after the
// traffic that prompted it has landed. One pass at a time, and the users are read
// inside the lock: two passes over the same stale snapshot would both see a user's
// status change and both alert about it.
func (m *Manager) enforceTraffic() error {
	m.enforceMu.Lock()
	defer m.enforceMu.Unlock()
	users, err := m.enforcementUsers()
	if err != nil {
		return err
	}
	return m.enforceAfterTraffic(users)
}

// now is the time the enforcement pass judges users at: the real one, unless a test
// fixed it (Manager.clock) so every read in it sees one moment.
func (m *Manager) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// enforcementUsers reads the users the enforcement pass may act on (see
// store.EnforcementCandidates) — usually a handful of the whole list.
func (m *Manager) enforcementUsers() ([]model.User, error) {
	// The widest horizon a setting allows when the settings cannot be read: a superset
	// is harmless, and the expiry warnings are skipped without settings anyway.
	horizon := int64(30 * 86400)
	if set, err := m.store.GetSettings(); err == nil {
		horizon = int64(set.ExpiringDays()) * 86400
	}
	ids, err := m.store.EnforcementCandidates(m.now().Unix(), horizon)
	if err != nil {
		return nil, err
	}
	return m.store.UserStatesByID(ids)
}

// enforceTrafficSoon schedules an enforcement pass for node traffic, unless one is
// already waiting to run.
//
// Every node report used to read the whole user list for its own pass — with 20,000
// users on a 1-vCPU server that read alone was ~0.4s, per node, every ~20 seconds.
// Reports landing within trafficEnforceDelay now share one pass. The cost is that a
// user who ran out of traffic on a node is cut up to that much later; node traffic
// already arrives up to a poll late.
func (m *Manager) enforceTrafficSoon() {
	if !m.enforcePending.CompareAndSwap(false, true) {
		return
	}
	m.runAsync(func() {
		if !m.wait(trafficEnforceDelay) {
			m.enforcePending.Store(false)
			return
		}
		// Cleared before the pass, so a report that lands during it schedules the
		// next one rather than being folded into a read that may predate it.
		m.enforcePending.Store(false)
		if err := m.enforceTraffic(); err != nil {
			logErr("node traffic: enforcement pass failed", "err", err)
		}
	})
}

// flushBeforeRestart writes the traffic the running Xray has counted before a deliberate
// restart resets its counters. Skipped while a flush is under way, which reads the same
// counters: waiting for it could hold the restart behind a sync that waits on the
// restart. Only the in-process API is asked — the process is about to go, and the CLI
// fallback is a fork per call.
func (m *Manager) flushBeforeRestart() {
	if !m.statsMu.TryLock() {
		return
	}
	defer m.statsMu.Unlock()
	if m.statsSource == nil && m.sup == nil {
		return
	}
	stats, err := m.queryLocalStats(false)
	if err != nil {
		logWarn("stats: cannot read the counters before a restart", "err", err)
		return
	}
	m.quota.at = time.Now()
	if err := m.pollStatsWith(stats); err != nil {
		logWarn("stats: flushing before a restart failed", "err", err)
	}
}

// enforceTrafficNow runs an enforcement pass right away, outside the batch node reports
// share: for a report that carries someone past their quota.
func (m *Manager) enforceTrafficNow() {
	m.runAsync(func() {
		if err := m.enforceTraffic(); err != nil {
			logErr("node traffic: enforcement pass failed", "err", err)
		}
	})
}

// enforceAfterTraffic runs the post-accounting tail shared by the local poll
// (PollStats) and remote-node traffic ingest (IngestNodeSync), through
// enforceTraffic: alert on status transitions, sync users if someone crossed a
// limit/expiry, and apply billing downgrades. `users` is the snapshot the alerts are
// judged on, read after the traffic landed.
func (m *Manager) enforceAfterTraffic(users []model.User) error {
	// Alert admins when a user crosses active → expired / out-of-quota / over-device.
	m.notifyStatusTransitions(users)
	// Give back what the blocklist measures took, once their time is up.
	m.LiftAbuseMeasures(time.Now().Unix())
	// Reconcile if the working set changed since the last applied config — e.g. a
	// user just crossed their data limit (traffic) or expiry (time).
	working, err := m.store.WorkingUserIDs(time.Now().Unix())
	if err != nil {
		return err
	}
	if m.workingIDsChanged(working) {
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
	Users        int `json:"users"`
	EnabledUsers int `json:"enabled_users"`
	OnlineUsers  int `json:"online_users"` // connected within model.DeviceOnlineWindow
	// TotalUp/TotalDown sum the users' QUOTA counters (used_up/used_down), which is
	// what a data limit is measured against — so they fall to zero when traffic is
	// reset or a plan rolls the period over. They are not lifetime totals and will
	// not match traffic_daily over the same span; that history is StatsSeries.
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
		OnlineUsers:  c.Online, // carrying traffic right now, anywhere in the fleet
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

// summaryTTL is how long SystemStatus reuses the user counts.
//
// The dashboard feed asks for the system status every two seconds while anyone has
// it open, and the counts behind it are a pass over every user joined with every
// address seen in the last two minutes: with 50,000 users that was a tenth of a
// 1-vCPU panel, spent to repaint numbers that move by the minute — "online" is itself
// a two-minute window. The host metrics and the Xray state on the same payload stay
// fresh; only the counts wait.
const summaryTTL = 15 * time.Second

// recentSummary is Summary, reused for summaryTTL: shared, so callers must not change
// it. Callers asking at once share one computation. Summary itself — the API's
// summary endpoint — always counts afresh.
func (m *Manager) recentSummary() (*Summary, error) {
	m.summaryMu.Lock()
	defer m.summaryMu.Unlock()
	if m.summaryCache == nil || time.Since(m.summaryAt) >= summaryTTL {
		s, err := m.Summary()
		if err != nil {
			return nil, err
		}
		m.summaryCache, m.summaryAt = s, time.Now()
	}
	return m.summaryCache, nil
}

// StartSysstat begins sampling host metrics (CPU/RAM/disk/network) and the live
// VPN throughput for the dashboard. diskPath selects the filesystem reported
// under "disk".
func (m *Manager) StartSysstat(diskPath string) {
	m.sys = sysstat.New(diskPath)
	m.runAsync(m.vpnSpeedLoop)
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
	for {
		select {
		case <-t.C:
		case <-m.done:
			return
		}
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
	OnlineUsers  int    `json:"online_users"`
	TrafficToday int64  `json:"traffic_today"`
	CertDaysLeft int    `json:"cert_days_left"`
}

// SystemStatus assembles the dashboard payload from the host sampler, the Xray
// supervisor and the user summary.
func (m *Manager) SystemStatus() (*SystemStatus, error) {
	sum, err := m.recentSummary()
	if err != nil {
		return nil, err
	}
	s := &SystemStatus{
		XrayRunning:  m.sup.Running(), // live: a crash shows on the next tick, not in 15s
		XrayUptime:   m.sup.UptimeSeconds(),
		XrayVersion:  m.sup.Version(),
		Goroutines:   runtime.NumGoroutine(),
		CPUCores:     runtime.NumCPU(),
		ProcMem:      sysstat.ProcMem(),
		TotalUp:      sum.TotalUp,
		TotalDown:    sum.TotalDown,
		Users:        sum.Users,
		EnabledUsers: sum.EnabledUsers,
		OnlineUsers:  sum.OnlineUsers,
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
	pts, err := m.store.StatsSeries(userID, from, to)
	if err != nil {
		return nil, err
	}
	return fillDays(pts, from, to), nil
}

// NodeDailyTraffic is one server's traffic on one day.
type NodeDailyTraffic struct {
	Day    string `json:"day"`     // YYYY-MM-DD, operator-local
	NodeID int64  `json:"node_id"` // 0 = the panel's own server
	Name   string `json:"name"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

// NodeTrafficSeries is the per-day, per-server breakdown: what NodeTrafficBreakdown
// totals and StatsSeries splits by day, in one answer.
//
// It exists because the two together could not produce it: the totals endpoint has
// no day dimension and the series endpoint has no server dimension, so plotting one
// line per server meant one call per day. Every server that carried anything in the
// window gets a full row per day, zeros included — a line with holes in it is not
// the same shape as a line with zeros, and every moving average reads it wrong.
func (m *Manager) NodeTrafficSeries(userID int64, from, to string) ([]NodeDailyTraffic, error) {
	rows, err := m.store.NodeTrafficSeries(userID, from, to)
	if err != nil {
		return nil, err
	}
	names, _ := m.store.NodeNames() // best-effort: a failure just falls back to ids
	if names == nil {
		names = map[int64]string{}
	}
	names[model.LocalNodeID] = model.LocalNodeName

	// Group by server, then fill each server's own line across the window.
	perNode := map[int64][]model.DailyPoint{}
	for _, r := range rows {
		perNode[r.NodeID] = append(perNode[r.NodeID], model.DailyPoint{Day: r.Day, Up: r.Up, Down: r.Down})
	}
	ids := make([]int64, 0, len(perNode))
	for id := range perNode {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]NodeDailyTraffic, 0, len(rows))
	for _, id := range ids {
		name := names[id]
		if name == "" {
			name = fmt.Sprintf("server #%d", id) // purged tombstone: id is all that's left
		}
		for _, p := range fillDays(perNode[id], from, to) {
			out = append(out, NodeDailyTraffic{
				Day: p.Day, NodeID: id, Name: name, Up: p.Up, Down: p.Down,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out, nil
}

// fillDays returns one point per calendar day in [from, to], carrying zeros for the
// days that hold no rows.
//
// A day with no traffic writes no row — the poller only records deltas — so the
// stored series is not a time series at all, it is a list of days that happened to
// be busy. Handing that to anything that averages over a window silently overstates
// the quiet periods, and reading a gap as "the panel was down" is the other way to
// be wrong about it. The dates are parsed rather than incremented as strings so
// month and year boundaries are the calendar's problem, not this function's.
func fillDays(pts []model.DailyPoint, from, to string) []model.DailyPoint {
	start, err := time.Parse(dayFormat, from)
	if err != nil {
		return pts // an unparseable window is the caller's to reject; don't invent days
	}
	end, err := time.Parse(dayFormat, to)
	if err != nil || end.Before(start) {
		return pts
	}
	if end.Sub(start) > maxSeriesSpan {
		return pts // a decade of zeros helps nobody; hand back what was stored
	}
	have := make(map[string]model.DailyPoint, len(pts))
	for _, p := range pts {
		have[p.Day] = p
	}
	out := make([]model.DailyPoint, 0, int(end.Sub(start)/(24*time.Hour))+1)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format(dayFormat)
		if p, ok := have[day]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, model.DailyPoint{Day: day})
	}
	return out
}

const (
	dayFormat = "2006-01-02"
	// maxSeriesSpan bounds what zero-filling will materialise: five years of daily
	// points is already an odd thing to ask a panel for, and beyond it the padding
	// would dwarf the data.
	maxSeriesSpan = 5 * 366 * 24 * time.Hour
)

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
	names[model.LocalNodeID] = model.LocalNodeName
	out := make([]NodeTraffic, 0, len(totals))
	for id, t := range totals {
		if t[0] == 0 && t[1] == 0 {
			continue
		}
		name := names[id]
		if name == "" {
			name = fmt.Sprintf("server #%d", id) // purged tombstone: id is all that's left
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
