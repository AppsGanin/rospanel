package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/google/uuid"
)

// mutateUser runs a single-user store write, logging the outcome and triggering
// a live user-set sync on success. It collapses the identical guard/log/sync tail
// shared by the user CRUD methods. `done` is the past-tense success line (e.g.
// "user 7 deleted"); failures log it with the error appended.
func (m *Manager) mutateUser(done string, fn func() error) error {
	if err := fn(); err != nil {
		logErr("mutation failed", "op", done, "err", err)
		return err
	}
	logInfo(done)
	m.TriggerUserSync()
	return nil
}

// ListUsers returns all users, newest first (read-only; used by the Telegram bot).
func (m *Manager) ListUsers() ([]model.User, error) { return m.store.ListUsers() }

// CreateUser creates a user (one credential set for all protocols) with optional
// data limit (bytes, 0=unlimited) and expiry (unix, 0=never), then reconciles.
func (m *Manager) CreateUser(ctx context.Context, name string, dataLimit, expireAt int64) (*model.User, error) {
	u, err := m.createUser(name, dataLimit, expireAt)
	if err != nil {
		return nil, err
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventUserCreated, map[string]any{
		"data_limit": dataLimit, "expire_at": expireAt,
	})
	return u, nil
}

// createUser is CreateUser without the audit row. Self-registration builds on it
// (via CreateRegisteredUser) so a signup records a single "user.registered" event
// rather than that plus a "user.created" — one action, one row.
func (m *Manager) createUser(name string, dataLimit, expireAt int64) (*model.User, error) {
	name, err := cleanUserName(name)
	if err != nil {
		return nil, err
	}
	if err := validateUserLimits(dataLimit, expireAt, 0); err != nil {
		return nil, err
	}
	password, err := auth.RandomPassword()
	if err != nil {
		return nil, err
	}
	subToken, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	u, err := m.store.CreateUser(name, uuid.NewString(), password, subToken, dataLimit, expireAt, 0)
	if err != nil {
		logErr("user create failed", "name", name, "err", err)
		return nil, err
	}
	logInfo("user created", "id", u.ID, "name", name, "limit", dataLimit, "expire", expireAt)
	m.TriggerUserSync()
	m.EmitWebhook(model.WebhookUserCreated, userEventData(*u))
	return u, nil
}

// SetUserEnabled enables/disables a user (manual on/off) and reconciles so the
// proxy config drops or re-adds them.
func (m *Manager) SetUserEnabled(ctx context.Context, id int64, enabled bool) error {
	// A toggle to the state the user is already in is a no-op — don't file an audit
	// row claiming a change that didn't happen (a double-clicked button, a stale UI).
	prev, err := m.store.GetUser(id)
	if err == nil && prev.Enabled == enabled {
		return nil
	}
	err = m.mutateUser(fmt.Sprintf("user %d enabled=%v", id, enabled),
		func() error { return m.store.SetUserEnabled(id, enabled) })
	if err == nil {
		m.audit(ctx, id, enabledAction(enabled), nil)
	}
	return err
}

// enabledAction maps the on/off flag to its audit action key.
func enabledAction(enabled bool) string {
	if enabled {
		return model.EventUserEnabled
	}
	return model.EventUserDisabled
}

// RenameUser updates a user's display name. The name is cosmetic (Xray keys
// users by "u<id>"), so no config reload is needed.
func (m *Manager) RenameUser(ctx context.Context, id int64, name string) error {
	name, err := cleanUserName(name)
	if err != nil {
		return err
	}
	prev := ""
	if u, err := m.store.GetUser(id); err == nil {
		prev = u.Name
	}
	if err := m.store.SetUserName(id, name); err != nil {
		return err
	}
	m.audit(ctx, id, model.EventUserRenamed, map[string]any{"from": prev, "to": name})
	return nil
}

// DeleteUser removes a user and reconciles.
func (m *Manager) DeleteUser(ctx context.Context, id int64) error {
	// Capture the user before deletion so the webhook payload and the audit row carry
	// its details (best-effort: a missing row still emits the id).
	u, _ := m.store.GetUser(id)
	err := m.mutateUser(fmt.Sprintf("user %d deleted", id),
		func() error { return m.store.DeleteUser(id) })
	if err == nil {
		data := map[string]any{"id": id}
		name := ""
		if u != nil {
			data = userEventData(*u)
			name = u.Name
		}
		// auditNamed, not audit: the user row is gone, so the name can't be looked up.
		m.auditNamed(ctx, id, name, model.EventUserDeleted, nil)
		m.EmitWebhook(model.WebhookUserDeleted, data)
	}
	return err
}

// ResetTraffic zeroes a user's usage and re-enables them. The raw counters are
// re-baselined to the live Xray value so the next stats poll doesn't re-add the
// user's whole lifetime total back (see store.ResetTraffic).
func (m *Manager) ResetTraffic(ctx context.Context, id int64) error {
	up, down := m.liveCounter(id)
	// Record what was wiped — after the reset the used totals read 0, so the audit row
	// is the only place the discarded usage survives.
	var used int64
	if u, err := m.store.GetUser(id); err == nil {
		used = u.UsedUp + u.UsedDown
	}
	err := m.mutateUser(fmt.Sprintf("user %d traffic counters reset", id),
		func() error { return m.store.ResetTraffic(id, up, down) })
	if err == nil {
		m.audit(ctx, id, model.EventTrafficReset, map[string]any{"used_before": used})
	}
	return err
}

// liveCounter returns the user's current cumulative Xray uplink/downlink, or
// (0,0) if Xray isn't reporting it. Used to re-baseline last_up/last_down on a
// quota reset so the next PollStats delta starts from now.
func (m *Manager) liveCounter(id int64) (up, down int64) {
	stats, err := m.sup.QueryStats(m.sup.APIAddr())
	if err != nil {
		return 0, 0
	}
	t := stats[fmt.Sprintf("u%d", id)]
	return t.Up, t.Down
}

// SetUserLimits updates a user's quota/expiry/device cap and re-enables them.
func (m *Manager) SetUserLimits(ctx context.Context, id, dataLimit, expireAt int64, deviceLimit int) error {
	if err := validateUserLimits(dataLimit, expireAt, deviceLimit); err != nil {
		return err
	}
	// store.SetUserLimits recomputes status from the new limit/expiry/devices.
	err := m.mutateUser(fmt.Sprintf("user %d limits updated: limit=%d expire=%d devices=%d", id, dataLimit, expireAt, deviceLimit),
		func() error { return m.store.SetUserLimits(id, dataLimit, expireAt, deviceLimit) })
	if err == nil {
		m.audit(ctx, id, model.EventUserLimits, map[string]any{
			"data_limit": dataLimit, "expire_at": expireAt, "device_limit": deviceLimit,
		})
	}
	return err
}

// BulkUserAction applies one action to many users with a SINGLE config sync at the
// end (instead of one per user), returning how many users were actually affected.
// Actions: "enable", "disable", "delete", "reset" (traffic), "extend" (push expiry
// by `days`). "extend" only touches users that already have an expiry — unlimited
// users are left alone (extending "never" is meaningless) and not counted.
func (m *Manager) BulkUserAction(ctx context.Context, ids []int64, action string, days int) (int, error) {
	if len(ids) == 0 {
		return 0, invalid("не выбрано ни одного пользователя")
	}
	// Snapshot the users up front. The audit rows for a bulk DELETE can't look their
	// names up afterwards; an id that isn't in the snapshot doesn't exist (so this
	// doubles as the "which ids are real" filter); and the prior state is what tells a
	// real toggle from a no-op.
	before := m.snapshotUsers(ids)
	var affected int64
	var err error
	switch action {
	case "enable", "disable":
		enable := action == "enable"
		affected, err = m.store.SetUsersEnabled(ids, enable)
		if err == nil {
			// SQLite counts MATCHED rows, so `affected` includes users already in the
			// target state. Audit only the ones this actually flipped.
			m.auditBulk(ctx, changedEnabled(before, enable), enabledAction(enable), nil)
		}
	case "delete":
		affected, err = m.store.DeleteUsers(ids)
		if err == nil {
			m.auditBulk(ctx, names(before), model.EventUserDeleted, nil)
		}
	case "reset":
		reset := m.bulkResetTraffic(ids)
		affected = int64(len(reset))
		m.auditBulk(ctx, pick(names(before), reset), model.EventTrafficReset, nil)
	case "extend":
		if days <= 0 {
			return 0, invalid("укажите число дней для продления")
		}
		if days > maxExtendDays {
			return 0, invalid("слишком большой срок продления (макс. %d дней)", maxExtendDays)
		}
		extended := m.bulkExtendExpiry(ids, days)
		affected = int64(len(extended))
		for id, expire := range extended {
			u := before[id]
			// Carry the untouched limits too: the row renders as a full "limits changed"
			// statement, and omitting them would make it claim the user has none.
			m.auditNamed(ctx, id, u.Name, model.EventUserLimits, map[string]any{
				"data_limit": u.DataLimit, "device_limit": u.DeviceLimit,
				"expire_at": expire, "extended_days": days, "bulk": true,
			})
		}
	default:
		return 0, invalid("неизвестное действие %q", action)
	}
	if err != nil {
		logErr("bulk user action failed", "action", action, "count", len(ids), "err", err)
		return 0, err
	}
	logInfo("bulk user action", "action", action, "selected", len(ids), "affected", affected)
	m.TriggerUserSync()
	return int(affected), nil
}

// snapshotUsers reads each id that still exists. Ids with no row are simply absent.
func (m *Manager) snapshotUsers(ids []int64) map[int64]model.User {
	out := make(map[int64]model.User, len(ids))
	for _, id := range ids {
		if u, err := m.store.GetUser(id); err == nil {
			out[id] = *u
		}
	}
	return out
}

// names reduces a user snapshot to id→name, the shape the audit rows need.
func names(users map[int64]model.User) map[int64]string {
	out := make(map[int64]string, len(users))
	for id, u := range users {
		out[id] = u.Name
	}
	return out
}

// changedEnabled narrows a snapshot to the users whose enabled flag the action
// actually flips — the ones already in the target state changed nothing.
func changedEnabled(users map[int64]model.User, enable bool) map[int64]string {
	out := map[int64]string{}
	for id, u := range users {
		if u.Enabled != enable {
			out[id] = u.Name
		}
	}
	return out
}

// pick narrows a name snapshot to the given ids.
func pick(names map[int64]string, ids []int64) map[int64]string {
	out := make(map[int64]string, len(ids))
	for _, id := range ids {
		if n, ok := names[id]; ok {
			out[id] = n
		}
	}
	return out
}

// auditBulk writes one audit row per user in names, flagged as part of a bulk action
// so the journal can tell a hand-picked change from a mass one.
func (m *Manager) auditBulk(ctx context.Context, names map[int64]string, action string, details map[string]any) {
	for id, name := range names {
		d := map[string]any{"bulk": true}
		for k, v := range details {
			d[k] = v
		}
		m.auditNamed(ctx, id, name, action, d)
	}
}

// bulkResetTraffic zeroes usage for many users, re-baselining each one's raw
// counters to the live Xray value fetched once up front (see store.ResetTraffic).
// It returns the ids it actually reset.
func (m *Manager) bulkResetTraffic(ids []int64) []int64 {
	stats, _ := m.sup.QueryStats(m.sup.APIAddr()) // nil map on error → (0,0) baselines
	var done []int64
	for _, id := range ids {
		t := stats[fmt.Sprintf("u%d", id)]
		if err := m.store.ResetTraffic(id, t.Up, t.Down); err == nil {
			done = append(done, id)
		}
	}
	return done
}

// bulkExtendExpiry pushes each selected user's expiry out by `days`, anchored at the
// later of now and their current expiry so stacking adds time rather than resetting
// it. Users with no expiry (0 = never) are skipped. It returns each extended user's
// new expiry.
func (m *Manager) bulkExtendExpiry(ids []int64, days int) map[int64]int64 {
	now := time.Now().Unix()
	add := int64(days) * 86400
	out := map[int64]int64{}
	for _, id := range ids {
		u, err := m.store.GetUser(id)
		if err != nil || u.ExpireAt == 0 {
			continue
		}
		base := now
		if u.ExpireAt > now {
			base = u.ExpireAt
		}
		expire := base + add
		if err := m.store.SetUserLimits(id, u.DataLimit, expire, u.DeviceLimit); err == nil {
			out[id] = expire
		}
	}
	return out
}

// RotateSubToken issues a new subscription URL token for a user. Protocol
// credentials stay the same — only the public /<sub_path>/<token> link changes,
// so the old URL stops working immediately. Triggers a user sync so any
// token-derived state is refreshed.
func (m *Manager) RotateSubToken(ctx context.Context, id int64) (*model.User, error) {
	token, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	if err := m.store.SetSubToken(id, token); err != nil {
		logErr("sub token rotate failed", "id", id, "err", err)
		return nil, err
	}
	u, err := m.store.GetUser(id)
	if err != nil {
		return nil, err
	}
	logInfo("sub token rotated", "id", id)
	m.TriggerUserSync()
	m.audit(ctx, id, model.EventSubRotated, nil)
	return u, nil
}

// UnlinkUserTelegram detaches a VPN user's Telegram chat.
func (m *Manager) UnlinkUserTelegram(ctx context.Context, id int64) error {
	if err := m.store.ClearUserTelegramChat(id); err != nil {
		return err
	}
	m.audit(ctx, id, model.EventTelegramUnlink, nil)
	return nil
}

// AuditTelegramLinked records that a user bound their Telegram account (the bind
// itself happens in the user bot, which owns the one-time code).
func (m *Manager) AuditTelegramLinked(ctx context.Context, id int64, username string) {
	m.audit(ctx, id, model.EventTelegramLinked, map[string]any{"username": username})
}

// GenerateUserTgLinkCode issues a fresh one-time code for binding a VPN user to
// the public Telegram bot. It replaces the old sub-token deep link, so a leaked
// link expires (TelegramLinkCodeTTL) and can't be reused after binding.
func (m *Manager) GenerateUserTgLinkCode(userID int64) (string, error) {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return "", err
	}
	if u.TgChatID != 0 {
		return "", invalid("Telegram уже привязан к этому пользователю")
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b[:])
	if err := m.store.SetUserTgLinkCode(userID, code); err != nil {
		return "", err
	}
	return code, nil
}

// Connections returns a user's recent source IPs.
func (m *Manager) Connections(id int64) ([]model.Connection, error) {
	return m.store.RecentConnections(id, 20)
}

// SetResetPeriod sets a user's automatic quota-reset period (none|daily|weekly|
// monthly|yearly), anchoring the cycle at now.
func (m *Manager) SetResetPeriod(ctx context.Context, id int64, period string) error {
	switch period {
	case "none", "daily", "weekly", "monthly", "yearly":
	default:
		return invalid("неверный период сброса %q", period)
	}
	if err := m.store.SetResetPeriod(id, period, time.Now().Unix()); err != nil {
		return err
	}
	m.audit(ctx, id, model.EventResetPeriod, map[string]any{"period": period})
	return nil
}

// applyResets zeroes the quota of users whose reset period has rolled over (new
// day/week/month/year). Re-enables them and reconciles if any were reset. This runs
// off the background poller, so the audit rows it writes are attributed to the system.
func (m *Manager) applyResets(users []model.User, now int64, counter func(int64) (int64, int64)) {
	ctx := context.Background()
	reset := 0
	loc := m.loc()
	for _, u := range users {
		if resetDue(u.ResetPeriod, u.LastResetAt, now, loc) {
			up, down := counter(u.ID)
			if err := m.store.ResetUserQuota(u.ID, now, up, down); err == nil {
				reset++
				m.auditNamed(ctx, u.ID, u.Name, model.EventQuotaReset, map[string]any{
					"period": u.ResetPeriod, "used_before": u.UsedUp + u.UsedDown,
				})
			}
		}
	}
	if reset > 0 {
		logInfo("quota reset", "count", reset)
		m.TriggerUserSync() // re-enabled users must re-enter the config
	}
}

// resetDue reports whether the calendar period changed since the last reset.
func resetDue(period string, lastReset, now int64, loc *time.Location) bool {
	if period == "" || period == "none" || lastReset == 0 {
		return false
	}
	// Rolling N-day cycle ("days:N"), used by free plans to refill the quota
	// every срок действия: due once N days have elapsed since the last reset,
	// regardless of calendar boundaries.
	if spec, ok := strings.CutPrefix(period, "days:"); ok {
		n, err := strconv.Atoi(spec)
		if err != nil || n <= 0 {
			return false
		}
		return now-lastReset >= int64(n)*86400
	}
	// Compare in the operator timezone so the period rolls over at local midnight.
	last := time.Unix(lastReset, 0).In(loc)
	n := time.Unix(now, 0).In(loc)
	switch period {
	case "daily":
		return n.Year() != last.Year() || n.YearDay() != last.YearDay()
	case "weekly":
		ly, lw := last.ISOWeek()
		ny, nw := n.ISOWeek()
		return ny != ly || nw != lw
	case "monthly":
		return n.Year() != last.Year() || n.Month() != last.Month()
	case "yearly":
		return n.Year() != last.Year()
	}
	return false
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
