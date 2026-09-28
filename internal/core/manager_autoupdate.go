package core

import (
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/updater"
)

// Auto-update: the schedule the autoupdate service keeps, and what it asks of the
// servers. The service itself (release check, download, swap, restart) lives in the
// autoupdate package, beside the panel's data directory it backs up.

// SaveAutoUpdate stores the auto-update schedule — a 5-field cron in the panel's
// timezone, "" = off — and whether the servers follow the panel. The cron is checked
// here: at fire time a typo could only be skipped, which reads as "nothing was due"
// until someone wonders why the panel never updated.
func (m *Manager) SaveAutoUpdate(expr string, nodes bool) error {
	expr = strings.TrimSpace(expr)
	if expr != "" {
		sched, err := cron.Parse(expr)
		if err != nil {
			return invalidCode("err.badCron", "неверное расписание (cron): {{err}}", map[string]any{"err": err})
		}
		// Every attempt asks GitHub, which serves 60 unauthenticated requests an hour
		// per address — and nothing is released that often.
		if firesPerDay(sched) > 24 {
			return invalidCode("err.autoUpdateTooOften", "автообновление — не чаще раза в час")
		}
	}
	return m.store.SetAutoUpdate(expr, nodes)
}

// firesPerDay counts the minutes of an ordinary day a schedule matches.
func firesPerDay(s *cron.Schedule) int {
	day := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC) // a Wednesday
	n := 0
	for i := range 24 * 60 {
		if s.Match(day.Add(time.Duration(i) * time.Minute)) {
			n++
		}
	}
	return n
}

// RequestOutdatedNodesUpdate tells every server online now whose agent runs a version
// older than current to update itself — the servers that missed the panel's own
// update (offline then, or their download failed). One long gone would only be asked
// again every day for nothing. Returns how many were asked.
func (m *Manager) RequestOutdatedNodesUpdate(current string) (int, error) {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return 0, err
	}
	online := time.Now().Unix() - model.NodeOnlineWindow
	var ids []int64
	for i := range nodes {
		n := &nodes[i]
		if n.Enabled && n.LastSeen > online && n.NodeVersion != "" && updater.IsNewer(current, n.NodeVersion) {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	m.nodeUpdateMu.Lock()
	n, err := m.store.SetNodeCommands(ids, nodeCmdUpdate, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		return 0, err
	}
	m.notifyNodes()
	return n, nil
}

// NotifyUpdate tells the admin bot what an auto-update did, in the bot's language.
// Text arguments are escaped: an error can carry a URL whose & would make Telegram
// refuse the one message that has to arrive.
func (m *Manager) NotifyUpdate(key string, args ...any) {
	for i, a := range args {
		if s, ok := a.(string); ok {
			args[i] = escHTML(s)
		}
	}
	m.notifyAdminEvent(model.AdminEventUpdate, i18n.T(m.botLang(), key, args...))
}
