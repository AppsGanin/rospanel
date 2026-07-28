package core

import (
	"strings"

	"github.com/AppsGanin/rospanel/internal/cron"
)

// maxBackupKeep bounds the retention count. Archives are full copies of the data
// dir, so an operator who types a large number here would quietly fill the disk —
// which is exactly the failure a backup is supposed to protect them from.
const maxBackupKeep = 90

// SaveLocalBackup validates and persists the scheduled local-backup config: a
// 5-field cron expression in the operator timezone (empty = scheduling off) and how
// many archives to retain.
//
// The cron is validated here rather than at fire time on purpose. The scheduler can
// only log an unparseable expression and skip, which looks exactly like "no backups
// were due" — so a typo would silently mean no backups at all until the day someone
// needed one.
func (m *Manager) SaveLocalBackup(expr string, keep int) error {
	expr = strings.TrimSpace(expr)
	if expr != "" {
		if _, err := cron.Parse(expr); err != nil {
			return invalidCode("err.badCron", "неверное расписание (cron): {{err}}", map[string]any{"err": err})
		}
	}
	if keep < 0 || keep > maxBackupKeep {
		return invalidCode("err.backupKeepRange", "число хранимых копий должно быть от 0 до {{max}} (0 — хранить все)", map[string]any{"max": maxBackupKeep})
	}
	return m.store.SetLocalBackup(expr, keep)
}
