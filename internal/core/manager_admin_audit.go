package core

import (
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// The admin trail — see model/admin_audit.go for what it holds and why it isn't the
// user journal. Writes come from the HTTP layer (one middleware over the panel
// routes), not from here: the thing worth recording is "an admin called this route
// and it succeeded", which only the router knows.
//
// Like the user journal, auditing is best-effort: a failed write is logged and
// swallowed. Losing an audit row must never fail the operation that produced it.

// AddAdminAudit records one event.
func (m *Manager) AddAdminAudit(ev model.AdminAudit) {
	if ev.CreatedAt == 0 {
		ev.CreatedAt = time.Now().Unix()
	}
	if err := m.store.AddAdminAudit(ev); err != nil {
		logErr("admin audit: write failed", "action", ev.Action, "err", err)
	}
}

// AdminAudit returns the admin trail, newest first.
func (m *Manager) AdminAudit(f store.AdminAuditFilter) ([]model.AdminAudit, error) {
	f.Limit = EventPageLimit(f.Limit)
	return m.store.ListAdminAudit(f)
}

// adminAuditExportCap bounds a single CSV export. The trail is already retention-
// swept, but a hard cap keeps one export from streaming an unbounded scan.
const adminAuditExportCap = 100000

// ExportAdminAudit streams the filtered trail (newest first) to fn, honouring every
// filter except the page cursor — an export is the whole matching set, capped. The
// caller supplies the same store.AdminAuditFilter used for the list, minus BeforeID.
func (m *Manager) ExportAdminAudit(f store.AdminAuditFilter, fn func(model.AdminAudit) error) error {
	f.BeforeID = 0
	if f.Limit <= 0 || f.Limit > adminAuditExportCap {
		f.Limit = adminAuditExportCap
	}
	return m.store.StreamAdminAudit(f, fn)
}

// PurgeOldAdminAudit drops trail rows past the retention window. Called from the
// same slow timer as the user journal's sweep.
func (m *Manager) PurgeOldAdminAudit() {
	cutoff := time.Now().AddDate(0, 0, -model.AdminAuditRetentionDays).Unix()
	n, err := m.store.PurgeAdminAudit(cutoff)
	if err != nil {
		logErr("admin audit: retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("admin audit: old events purged",
			"count", n, "older_than_days", model.AdminAuditRetentionDays)
	}
}
