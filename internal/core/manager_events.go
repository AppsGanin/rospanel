package core

import (
	"context"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The audit trail. Every mutating Manager method calls audit() (or auditNamed(),
// when the user row is about to disappear) to record what happened, who did it, and
// enough detail to make the row meaningful on its own — the panel shows this in the
// user's «Журнал» modal and on the global journal page.
//
// Auditing is best-effort: a failed write is logged and swallowed. Losing an audit
// row must never fail the operation that produced it.

// audit records an event, resolving the user's current name for the row.
func (m *Manager) audit(ctx context.Context, userID int64, action string, details map[string]any) {
	name := ""
	if u, err := m.store.GetUser(userID); err == nil {
		name = u.Name
	}
	m.auditNamed(ctx, userID, name, action, details)
}

// auditNamed records an event with an explicitly supplied user name — for events
// where the name can't be looked up after the fact (a deletion) or is already known.
func (m *Manager) auditNamed(ctx context.Context, userID int64, userName, action string, details map[string]any) {
	a := actor.From(ctx)
	err := m.store.AddUserEvent(model.UserEvent{
		UserID:    userID,
		UserName:  userName,
		Action:    action,
		ActorKind: a.Kind,
		ActorName: a.Name,
		Details:   detailsOrNil(details),
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		logErr("audit: write failed", "user", userID, "action", action, "err", err)
	}
}

// detailsOrNil normalizes an empty map to nil so the row stores "" rather than "{}".
func detailsOrNil(d map[string]any) any {
	if len(d) == 0 {
		return nil
	}
	return d
}

// UserEvents returns one user's audit trail, newest first (the «Журнал» modal).
func (m *Manager) UserEvents(userID int64, limit int, beforeID int64) ([]model.UserEvent, error) {
	return m.store.ListUserEvents(userID, EventPageLimit(limit), beforeID)
}

// Events returns the global audit trail, newest first (the journal page).
func (m *Manager) Events(f store.UserEventFilter) ([]model.UserEvent, error) {
	f.Limit = EventPageLimit(f.Limit)
	return m.store.ListEvents(f)
}

// eventPageMax bounds a page so a caller can't ask for the whole table at once.
const eventPageMax = 200

// EventPageLimit is the effective page size for a requested limit. The HTTP layer
// applies it too, so its "was this page full?" cursor check compares against the
// same number the store was actually asked for.
func EventPageLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > eventPageMax {
		return eventPageMax
	}
	return n
}

// PurgeOldEvents drops audit rows past the retention window. Called on a slow timer
// from the service loop; safe to call as often as you like.
func (m *Manager) PurgeOldEvents() {
	cutoff := time.Now().AddDate(0, 0, -model.UserEventRetentionDays).Unix()
	n, err := m.store.PurgeUserEvents(cutoff)
	if err != nil {
		logErr("audit: retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("audit: old events purged", "count", n, "older_than_days", model.UserEventRetentionDays)
	}
}

// PurgeOldConnections drops connection rows whose IP hasn't been seen inside the
// retention window. Shares the audit sweep's slow timer; safe to call repeatedly.
func (m *Manager) PurgeOldConnections() {
	cutoff := time.Now().AddDate(0, 0, -model.ConnectionRetentionDays).Unix()
	n, err := m.store.PurgeConnections(cutoff)
	if err != nil {
		logErr("connections: retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("connections: stale rows purged", "count", n, "older_than_days", model.ConnectionRetentionDays)
	}
}
