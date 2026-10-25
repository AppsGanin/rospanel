package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sysstat"
)

// Alert rules: thresholds the operator sets on a server's figures. The node watch
// loop evaluates them once a minute against what each server last reported. A rule
// fires when its condition has held for the rule's duration, and clears the first
// time it does not; both are told to the admin chats and sent as webhooks, and every
// firing is a row in alert_events.
//
// The open rows are also the memory: after a restart the engine reads them back, so
// a condition that is still true is not announced a second time.
const (
	alertMaxRules   = 100
	alertNameMax    = 80
	alertMaxForSecs = 86400
	alertMaxValue   = 1e9
	// alertRefireCooldown keeps a figure hovering around its line from firing and
	// clearing every minute: after a rule clears on a server it waits this long
	// before it may fire there again.
	alertRefireCooldown = 10 * time.Minute
	alertHistoryMax     = 200
)

type alertKey struct{ rule, node int64 }

// alertState is one (rule, server) pair: since when its condition has held, the open
// firing if there is one, and when it last cleared.
type alertState struct {
	since        time.Time
	eventID      int64
	firedAt      int64
	value        float64 // the figure when it fired
	peak         float64
	lastResolved time.Time
}

type alertEngine struct {
	mu       sync.Mutex
	restored bool
	states   map[alertKey]*alertState
}

// alertSample is one server's figures at a sweep. A metric missing from vals is
// unknown — an agent too old to report it — and is never read as zero.
type alertSample struct {
	name string
	host string
	vals map[string]float64
}

// alertNotice is one message the sweep owes: a rule fired or cleared on a server.
type alertNotice struct {
	fired bool
	ev    model.AlertEvent
	host  string
}

// AlertRules lists the rules with how many firings each has open.
func (m *Manager) AlertRules() ([]model.AlertRule, error) {
	rules, err := m.store.ListAlertRules()
	if rules == nil {
		rules = []model.AlertRule{}
	}
	return rules, err
}

// AlertEvents pages the firing history, newest first.
func (m *Manager) AlertEvents(limit int, beforeID int64) ([]model.AlertEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > alertHistoryMax {
		limit = alertHistoryMax
	}
	ev, err := m.store.ListAlertEvents(limit, beforeID)
	if ev == nil {
		ev = []model.AlertEvent{}
	}
	return ev, err
}

func alertPercentMetric(metric string) bool { return model.AlertMetricUnit(metric) == "%" }

func (m *Manager) validateAlertRule(in *model.AlertRuleInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > alertNameMax {
		return invalidCode("err.alertName", "название — от 1 до {{max}} символов", map[string]any{"max": alertNameMax})
	}
	if model.AlertMetricUnit(in.Metric) == "" {
		return invalidCode("err.alertMetric", "неизвестная метрика")
	}
	if in.Op != model.AlertOpAbove && in.Op != model.AlertOpBelow {
		return invalidCode("err.alertOp", "условие — больше или меньше")
	}
	max := alertMaxValue
	if alertPercentMetric(in.Metric) {
		max = 100
	}
	if !model.AlertFinite(in.Threshold) || in.Threshold < 0 || in.Threshold > max {
		return invalidCode("err.alertThreshold", "порог — число от 0 до {{max}}", map[string]any{"max": int64(max)})
	}
	if in.ForSecs < 0 || in.ForSecs > alertMaxForSecs {
		return invalidCode("err.alertFor", "длительность — от 0 до суток")
	}
	if in.NodeID != model.AlertEveryServer && in.NodeID != model.LocalNodeID {
		n, err := m.store.GetNode(in.NodeID)
		if err != nil {
			return err
		}
		if n == nil {
			return invalidCode("err.alertServer", "такого сервера нет")
		}
	}
	return nil
}

// SaveAlertRule creates the rule (id 0) or replaces rule id's settings. A change to
// what the rule measures closes its open firings: they describe the old rule, and the
// next sweep opens a fresh one if the new condition holds.
func (m *Manager) SaveAlertRule(_ context.Context, id int64, in model.AlertRuleInput) (*model.AlertRule, error) {
	if err := m.validateAlertRule(&in); err != nil {
		return nil, err
	}
	if id == 0 {
		n, err := m.store.CountAlertRules()
		if err != nil {
			return nil, err
		}
		if n >= alertMaxRules {
			return nil, invalidCode("err.alertTooMany", "правил не больше {{max}}", map[string]any{"max": alertMaxRules})
		}
		return m.store.CreateAlertRule(in)
	}
	old, err := m.store.GetAlertRule(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	if err := m.store.UpdateAlertRule(id, in); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	if !in.Enabled || in.Metric != old.Metric || in.Op != old.Op || in.Threshold != old.Threshold ||
		in.NodeID != old.NodeID || in.ForSecs != old.ForSecs {
		if err := m.store.ResolveAlertEventsOfRule(id, m.now().Unix()); err != nil {
			logErr("alerts: closing firings of a changed rule failed", "rule", id, "err", err)
		}
		m.alerts.forgetRule(id)
	}
	return &model.AlertRule{ID: id, AlertRuleInput: in, CreatedAt: old.CreatedAt}, nil
}

// DeleteAlertRule removes a rule; its history stays.
func (m *Manager) DeleteAlertRule(_ context.Context, id int64) error {
	if err := m.store.DeleteAlertRule(id, m.now().Unix()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	m.alerts.forgetRule(id)
	return nil
}

func (e *alertEngine) forgetRule(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.states {
		if k.rule == id {
			delete(e.states, k)
		}
	}
}

// PurgeOldAlertEvents drops finished firings past the retention window.
func (m *Manager) PurgeOldAlertEvents() {
	cutoff := time.Now().AddDate(0, 0, -model.AlertEventRetentionDays).Unix()
	if n, err := m.store.PurgeAlertEvents(cutoff); err != nil {
		logErr("alerts: retention sweep failed", "err", err)
	} else if n > 0 {
		logInfo("alerts: old firings purged", "count", n)
	}
}

// alertSamples collects each live server's figures. Offline servers are left out: what
// they last reported is stale, and the unreachable alert is the one that fits.
func (m *Manager) alertSamples(nodes []model.Node, local *sysstat.Stats, now time.Time, withOnline bool) (map[int64]alertSample, map[int64]struct{}) {
	var online map[int64]int
	if withOnline {
		online = m.OnlineByServer()
	}
	samples := make(map[int64]alertSample, len(nodes)+1)
	known := map[int64]struct{}{model.LocalNodeID: {}}

	if local != nil {
		s := alertSample{name: model.LocalNodeName, vals: map[string]float64{
			model.AlertMetricCPU:     local.CPUPercent,
			model.AlertMetricNetUp:   mbps(local.NetUp),
			model.AlertMetricNetDown: mbps(local.NetDown),
		}}
		if withOnline {
			s.vals[model.AlertMetricOnlineUs] = float64(online[model.LocalNodeID])
		}
		if local.MemTotal > 0 {
			s.vals[model.AlertMetricMem] = pct(local.MemUsed, local.MemTotal)
		}
		if local.DiskTotal > 0 {
			s.vals[model.AlertMetricDisk] = pct(local.DiskUsed, local.DiskTotal)
		}
		samples[model.LocalNodeID] = s
	}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() {
			continue
		}
		known[n.ID] = struct{}{}
		if !n.Online(now.Unix()) {
			continue
		}
		s := alertSample{name: n.Name, host: n.Host, vals: map[string]float64{}}
		if withOnline {
			s.vals[model.AlertMetricOnlineUs] = float64(online[n.ID])
		}
		if h, ok := m.NodeHostStats(n.ID); ok {
			// An agent older than the CPU/network fields sends zeros for all three.
			if h.CPUPercent > 0 || h.NetUp > 0 || h.NetDown > 0 {
				s.vals[model.AlertMetricCPU] = h.CPUPercent
				s.vals[model.AlertMetricNetUp] = mbps(h.NetUp)
				s.vals[model.AlertMetricNetDown] = mbps(h.NetDown)
			}
			if h.MemTotal > 0 {
				s.vals[model.AlertMetricMem] = pct(h.MemUsed, h.MemTotal)
			}
			if h.DiskTotal > 0 {
				s.vals[model.AlertMetricDisk] = pct(h.DiskUsed, h.DiskTotal)
			}
		}
		samples[n.ID] = s
	}
	return samples, known
}

func mbps(bytesPerSec int64) float64 { return float64(bytesPerSec) * 8 / 1e6 }

func pct(used, total int64) float64 { return float64(used) * 100 / float64(total) }

// sweepAlertRules runs one evaluation and tells whoever should hear about it.
func (m *Manager) sweepAlertRules(nodes []model.Node, local *sysstat.Stats, now time.Time) {
	rules, err := m.store.ListAlertRules()
	if err != nil {
		logErr("alerts: cannot list rules", "err", err)
		return
	}
	var online, anyOn bool
	for _, r := range rules {
		anyOn = anyOn || r.Enabled
		online = online || (r.Enabled && r.Metric == model.AlertMetricOnlineUs)
	}
	// Figures are read only for rules that exist: counting who is online refreshes a
	// cache other readers rely on, and a panel with no rules should not pay for it.
	samples, known := map[int64]alertSample{}, map[int64]struct{}{}
	if anyOn {
		samples, known = m.alertSamples(nodes, local, now, online)
	}
	for _, n := range m.evalAlertRules(rules, samples, known, now) {
		m.sendAlertNotice(n)
	}
}

// evalAlertRules advances every (rule, server) pair one step and returns the notices
// that step produced. Sending is left to the caller.
func (m *Manager) evalAlertRules(rules []model.AlertRule, samples map[int64]alertSample,
	known map[int64]struct{}, now time.Time) []alertNotice {
	e := &m.alerts
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.states == nil {
		e.states = map[alertKey]*alertState{}
	}
	if !e.restored {
		open, err := m.store.OpenAlertEvents()
		if err != nil {
			logErr("alerts: reading the open firings failed", "err", err)
			return nil
		}
		for _, ev := range open {
			e.states[alertKey{ev.RuleID, ev.NodeID}] = &alertState{
				eventID: ev.ID, firedAt: ev.FiredAt, value: ev.Value, peak: ev.Peak}
		}
		e.restored = true
	}

	var out []alertNotice
	live := map[alertKey]struct{}{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		for id, s := range samples {
			if r.NodeID != model.AlertEveryServer && r.NodeID != id {
				continue
			}
			k := alertKey{r.ID, id}
			live[k] = struct{}{}
			st := e.states[k]
			if st == nil {
				st = &alertState{}
				e.states[k] = st
			}
			v, ok := s.vals[r.Metric]
			if !ok {
				st.since = time.Time{}
				continue
			}
			if n := m.alertStep(r, s, id, v, st, now); n != nil {
				out = append(out, *n)
			}
		}
		// A server that is known but silent keeps its state: the rule neither fires
		// nor clears on figures that are not being reported.
		for id := range known {
			if r.NodeID != model.AlertEveryServer && r.NodeID != id {
				continue
			}
			if _, has := samples[id]; has {
				continue
			}
			k := alertKey{r.ID, id}
			if st := e.states[k]; st != nil {
				st.since = time.Time{}
				live[k] = struct{}{}
			}
		}
	}
	// Whatever is left was a rule switched off or removed, or a server taken away:
	// close its firing without a word.
	at := now.Unix()
	for k, st := range e.states {
		if _, ok := live[k]; ok {
			continue
		}
		if st.eventID != 0 {
			if err := m.store.ResolveAlertEvent(st.eventID, at); err != nil {
				logErr("alerts: closing an orphaned firing failed", "event", st.eventID, "err", err)
			}
		}
		delete(e.states, k)
	}
	return out
}

// alertStep moves one pair: fires it, extends or clears an open firing, or counts
// time toward firing. Called with the engine locked.
func (m *Manager) alertStep(r model.AlertRule, s alertSample, node int64, v float64,
	st *alertState, now time.Time) *alertNotice {
	if !model.AlertHolds(r.Op, v, r.Threshold) {
		st.since = time.Time{}
		if st.eventID == 0 {
			return nil
		}
		ev := model.AlertEvent{ID: st.eventID, RuleID: r.ID, RuleName: r.Name, NodeID: node, NodeName: s.name,
			Metric: r.Metric, Op: r.Op, Threshold: r.Threshold, Value: st.value, Peak: st.peak,
			FiredAt: st.firedAt, ResolvedAt: now.Unix()}
		if err := m.store.ResolveAlertEvent(st.eventID, ev.ResolvedAt); err != nil {
			logErr("alerts: closing a firing failed", "event", st.eventID, "err", err)
			return nil // still open in the store: try again next sweep
		}
		st.eventID, st.lastResolved = 0, now
		return &alertNotice{ev: ev, host: s.host}
	}
	if st.eventID != 0 {
		worse := (r.Op == model.AlertOpAbove && v > st.peak) || (r.Op == model.AlertOpBelow && v < st.peak)
		if worse {
			st.peak = v
			if err := m.store.SetAlertEventPeak(st.eventID, v); err != nil {
				logErr("alerts: recording the peak failed", "event", st.eventID, "err", err)
			}
		}
		return nil
	}
	if st.since.IsZero() {
		st.since = now
	}
	if now.Sub(st.since) < time.Duration(r.ForSecs)*time.Second {
		return nil
	}
	if !st.lastResolved.IsZero() && now.Sub(st.lastResolved) < alertRefireCooldown {
		return nil
	}
	ev := model.AlertEvent{RuleID: r.ID, RuleName: r.Name, NodeID: node, NodeName: s.name,
		Metric: r.Metric, Op: r.Op, Threshold: r.Threshold, Value: v, Peak: v, FiredAt: now.Unix()}
	id, err := m.store.AddAlertEvent(ev)
	if err != nil {
		logErr("alerts: recording a firing failed", "rule", r.ID, "err", err)
		return nil
	}
	ev.ID = id
	st.eventID, st.firedAt, st.value, st.peak = id, ev.FiredAt, v, v
	return &alertNotice{fired: true, ev: ev, host: s.host}
}

// alertMetricLabel is the metric's name in the admin bot's language.
func alertMetricLabel(metric string, lang i18n.Lang) string {
	switch metric {
	case model.AlertMetricCPU:
		return i18n.T(lang, "notify.alertMetric.cpu_percent")
	case model.AlertMetricMem:
		return i18n.T(lang, "notify.alertMetric.mem_percent")
	case model.AlertMetricDisk:
		return i18n.T(lang, "notify.alertMetric.disk_percent")
	case model.AlertMetricNetUp:
		return i18n.T(lang, "notify.alertMetric.net_up_mbps")
	case model.AlertMetricNetDown:
		return i18n.T(lang, "notify.alertMetric.net_down_mbps")
	case model.AlertMetricOnlineUs:
		return i18n.T(lang, "notify.alertMetric.online_users")
	}
	return metric
}

// formatAlertValue writes a figure with its unit, the way the message and the panel
// read it.
func formatAlertValue(metric string, v float64, lang i18n.Lang) string {
	switch model.AlertMetricUnit(metric) {
	case "%":
		return fmt.Sprintf("%.0f%%", v)
	case "Mbit/s":
		return fmt.Sprintf("%.1f %s", v, i18n.T(lang, "notify.alertUnitMbps"))
	}
	return fmt.Sprintf("%.0f", v)
}

func (m *Manager) sendAlertNotice(n alertNotice) {
	lang := m.botLang()
	ev := n.ev
	node := &model.Node{ID: ev.NodeID, Name: ev.NodeName, Host: n.host}
	metric := alertMetricLabel(ev.Metric, lang)
	word := i18n.T(lang, "notify.alertAbove")
	if ev.Op == model.AlertOpBelow {
		word = i18n.T(lang, "notify.alertBelow")
	}
	cond := fmt.Sprintf("%s %s", word, formatAlertValue(ev.Metric, ev.Threshold, lang))
	data := nodeEventData(node, map[string]any{
		"rule_id": ev.RuleID, "rule_name": ev.RuleName, "metric": ev.Metric, "op": ev.Op,
		"threshold": ev.Threshold, "value": ev.Value, "fired_at": ev.FiredAt,
	})
	if n.fired {
		html := i18n.T(lang, "notify.alertFired", escHTML(ev.RuleName), nodeLabel(node, lang),
			metric, formatAlertValue(ev.Metric, ev.Value, lang), cond)
		m.notifyAdminEvent(model.AdminEventAlerts, html)
		m.EmitWebhook(model.WebhookAlertFired, data)
		return
	}
	lasted := time.Duration(ev.ResolvedAt-ev.FiredAt) * time.Second
	html := i18n.T(lang, "notify.alertResolved", escHTML(ev.RuleName), nodeLabel(node, lang),
		metric, formatAlertValue(ev.Metric, ev.Peak, lang), fmtDowntime(lasted, lang))
	m.notifyAdminEvent(model.AdminEventAlerts, html)
	data["peak"] = ev.Peak
	data["resolved_at"] = ev.ResolvedAt
	data["duration_seconds"] = ev.ResolvedAt - ev.FiredAt
	m.EmitWebhook(model.WebhookAlertResolved, data)
}
