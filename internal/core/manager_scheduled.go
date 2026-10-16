package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// scheduledEvery is how often the scheduled work is looked at: broadcasts due, and
// the month's summary.
const scheduledEvery = 30 * time.Second

// RunScheduledLoop sends what was set for later: broadcasts whose time has come, and
// on the 1st the admin bot's summary of the month before.
func (m *Manager) RunScheduledLoop(ctx context.Context) {
	t := time.NewTicker(scheduledEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		m.StartDueBroadcasts(now.Unix())
		m.maybeMonthlyReport(now)
	}
}

// The summary goes out on the 1st from monthlyReportHour, operator time — or, the
// panel having been off then, by the 3rd; later than that it is old news.
const (
	monthlyReportHour    = 9
	monthlyReportLastDay = 3
)

// maybeMonthlyReport sends the summary of the month before once it is due, once.
func (m *Manager) maybeMonthlyReport(now time.Time) {
	local := now.In(m.loc())
	if local.Day() > monthlyReportLastDay || (local.Day() == 1 && local.Hour() < monthlyReportHour) {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil || !set.AdminEventEnabled(model.AdminEventMonthly) {
		return
	}
	// Somewhere to deliver it: the admin bot on, with a chat. Without one it waits —
	// an operator who sets the bot up on the 2nd still gets the 1st's summary.
	m.notifyMu.Lock()
	wired := m.adminNotify != nil
	m.notifyMu.Unlock()
	if !wired || !set.TGBotEnabled || strings.TrimSpace(set.TGBotToken) == "" || len(set.TelegramChatIDs()) == 0 {
		return
	}
	thisMonth := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, m.loc())
	prev := thisMonth.AddDate(0, -1, 0)
	key := prev.Format("2006-01")
	if set.MonthlyReportSent == key {
		return
	}
	html, err := m.monthlyReport(prev, thisMonth, set)
	if err != nil {
		logErr("monthly report: gathering failed", "err", err)
		return
	}
	// Claimed before it is sent: a summary lost to a send that fails beats two.
	if ok, err := m.store.SetMonthlyReportSent(key); err != nil || !ok {
		return
	}
	m.notifyAdminEvent(model.AdminEventMonthly, html)
}

// monthlyReport words the summary of [from, to) in the admin bot's language.
func (m *Manager) monthlyReport(from, to time.Time, set *model.Settings) (string, error) {
	r, err := m.store.MonthReport(from.Unix(), to.Unix(), from.Format("2006-01-02"),
		to.AddDate(0, 0, -1).Format("2006-01-02"), time.Now().Unix())
	if err != nil {
		return "", err
	}
	lang := m.botLang()
	month := from.Format("01.2006")
	if names := strings.Split(i18n.T(lang, "notify.monthNames"), ","); len(names) == 12 {
		month = strings.TrimSpace(names[from.Month()-1]) + " " + strconv.Itoa(from.Year())
	}
	lines := []string{i18n.T(lang, "notify.monthlyTitle", escHTML(month))}
	if set.BillingEnabled || r.RevenueRub > 0 {
		lines = append(lines, i18n.T(lang, "notify.monthlyRevenue", groupDigits(r.RevenueRub), r.Payments))
	}
	lines = append(lines,
		i18n.T(lang, "notify.monthlyUsers", r.NewUsers, r.ActiveUsers, r.Users),
		i18n.T(lang, "notify.monthlyTraffic", humanBytes(r.Traffic)))
	if up := m.uptimeLine(r.Uptime, set); up != "" {
		lines = append(lines, i18n.T(lang, "notify.monthlyUptime", up))
	}
	if r.Incidents > 0 || r.Maintenance > 0 {
		lines = append(lines, i18n.T(lang, "notify.monthlyIncidents", r.Incidents, r.Maintenance))
	}
	return strings.Join(lines, "\n"), nil
}

// uptimeLine is "Main 99.98% · NL 99.71%" over the servers sampled in the month,
// named as they are now (one deleted since is left out).
func (m *Manager) uptimeLine(uptime []model.MonthUptime, set *model.Settings) string {
	names := map[int64]string{model.LocalNodeID: masterStatusName(set)}
	if nodes, err := m.store.ListNodes(); err == nil {
		for i := range nodes {
			names[nodes[i].ID] = nodes[i].Name
		}
	}
	var parts []string
	for _, u := range uptime {
		name, ok := names[u.NodeID]
		if !ok || u.Total == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %.2f%%", escHTML(name), float64(u.Up)*100/float64(u.Total)))
	}
	return strings.Join(parts, " · ")
}

// groupDigits writes 12500 as "12 500".
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
