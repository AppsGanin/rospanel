package core

import (
	"context"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
)

// autoDeleteMaxDays bounds the grace period an operator can set. A year is already
// far past any plausible "keep them around in case they come back", and the cap
// keeps a typo (3650) from being read as "effectively never" — that's what 0 is for.
const autoDeleteMaxDays = 365

// SetUserAutoDelete configures how many days an expired user is kept before being
// deleted. 0 disables deletion entirely. The admin-audit row for the change is
// written by the HTTP layer (see server/audit.go), like every other setting.
func (m *Manager) SetUserAutoDelete(days int) error {
	if days < 0 || days > autoDeleteMaxDays {
		return invalid("срок хранения истёкших: от 0 (не удалять) до %d дней", autoDeleteMaxDays)
	}
	return m.store.SetUserAutoDeleteDays(days)
}

// PurgeExpiredUsers deletes users whose expiry date is further in the past than the
// configured grace period. Called from the retention sweep; a no-op when the setting
// is 0 (the default), so an operator who never opts in never loses a user.
//
// Deletion is irreversible, so it is deliberately conservative:
//   - it keys off expire_at, not the derived `status` (see store.ExpiredUsersBefore) —
//     a user whose plan was renewed has a future expiry and is never a candidate;
//   - users with no expiry date at all are never touched;
//   - every deletion is written to the user journal (which outlives the user) and
//     pushed as a webhook, so «где мой пользователь?» always has an answer.
func (m *Manager) PurgeExpiredUsers() {
	set, err := m.store.GetSettings()
	if err != nil || set == nil || set.UserAutoDeleteDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -set.UserAutoDeleteDays).Unix()
	doomed, err := m.store.ExpiredUsersBefore(cutoff)
	if err != nil {
		logErr("autodelete: listing expired users failed", "err", err)
		return
	}
	if len(doomed) == 0 {
		return
	}

	ids := make([]int64, 0, len(doomed))
	for _, u := range doomed {
		ids = append(ids, u.ID)
	}
	n, err := m.store.DeleteUsers(ids)
	if err != nil {
		logErr("autodelete: deleting expired users failed", "count", len(ids), "err", err)
		return
	}

	// One sync for the whole batch, not one per user: the deleted users have to leave
	// the running Xray config, but bouncing it N times to do so would be gratuitous.
	m.TriggerUserSync()

	ctx := actor.With(context.Background(), actor.System)
	for _, u := range doomed {
		m.auditNamed(ctx, u.ID, u.Name, model.EventUserDeleted, map[string]any{
			"reason":     "autodelete",
			"expire_at":  u.ExpireAt,
			"after_days": set.UserAutoDeleteDays,
		})
		m.EmitWebhook(model.WebhookUserDeleted, userEventData(u))
	}
	logInfo("autodelete: expired users removed", "count", n, "after_days", set.UserAutoDeleteDays)
}
