package core

import (
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
func (m *Manager) CreateUser(name string, dataLimit, expireAt int64) (*model.User, error) {
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
func (m *Manager) SetUserEnabled(id int64, enabled bool) error {
	return m.mutateUser(fmt.Sprintf("user %d enabled=%v", id, enabled),
		func() error { return m.store.SetUserEnabled(id, enabled) })
}

// RenameUser updates a user's display name. The name is cosmetic (Xray keys
// users by "u<id>"), so no config reload is needed.
func (m *Manager) RenameUser(id int64, name string) error {
	name, err := cleanUserName(name)
	if err != nil {
		return err
	}
	return m.store.SetUserName(id, name)
}

// DeleteUser removes a user and reconciles.
func (m *Manager) DeleteUser(id int64) error {
	// Capture the user before deletion so the webhook payload carries its details
	// (best-effort: a missing row still emits the id).
	u, _ := m.store.GetUser(id)
	err := m.mutateUser(fmt.Sprintf("user %d deleted", id),
		func() error { return m.store.DeleteUser(id) })
	if err == nil {
		data := map[string]any{"id": id}
		if u != nil {
			data = userEventData(*u)
		}
		m.EmitWebhook(model.WebhookUserDeleted, data)
	}
	return err
}

// ResetTraffic zeroes a user's usage and re-enables them. The raw counters are
// re-baselined to the live Xray value so the next stats poll doesn't re-add the
// user's whole lifetime total back (see store.ResetTraffic).
func (m *Manager) ResetTraffic(id int64) error {
	up, down := m.liveCounter(id)
	return m.mutateUser(fmt.Sprintf("user %d traffic counters reset", id),
		func() error { return m.store.ResetTraffic(id, up, down) })
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
func (m *Manager) SetUserLimits(id, dataLimit, expireAt int64, deviceLimit int) error {
	if err := validateUserLimits(dataLimit, expireAt, deviceLimit); err != nil {
		return err
	}
	// store.SetUserLimits recomputes status from the new limit/expiry/devices.
	return m.mutateUser(fmt.Sprintf("user %d limits updated: limit=%d expire=%d devices=%d", id, dataLimit, expireAt, deviceLimit),
		func() error { return m.store.SetUserLimits(id, dataLimit, expireAt, deviceLimit) })
}

// BulkUserAction applies one action to many users with a SINGLE config sync at the
// end (instead of one per user), returning how many users were actually affected.
// Actions: "enable", "disable", "delete", "reset" (traffic), "extend" (push expiry
// by `days`). "extend" only touches users that already have an expiry — unlimited
// users are left alone (extending "never" is meaningless) and not counted.
func (m *Manager) BulkUserAction(ids []int64, action string, days int) (int, error) {
	if len(ids) == 0 {
		return 0, invalid("не выбрано ни одного пользователя")
	}
	var affected int64
	var err error
	switch action {
	case "enable":
		affected, err = m.store.SetUsersEnabled(ids, true)
	case "disable":
		affected, err = m.store.SetUsersEnabled(ids, false)
	case "delete":
		affected, err = m.store.DeleteUsers(ids)
	case "reset":
		affected = m.bulkResetTraffic(ids)
	case "extend":
		if days <= 0 {
			return 0, invalid("укажите число дней для продления")
		}
		if days > maxExtendDays {
			return 0, invalid("слишком большой срок продления (макс. %d дней)", maxExtendDays)
		}
		affected = m.bulkExtendExpiry(ids, days)
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

// bulkResetTraffic zeroes usage for many users, re-baselining each one's raw
// counters to the live Xray value fetched once up front (see store.ResetTraffic).
func (m *Manager) bulkResetTraffic(ids []int64) int64 {
	stats, _ := m.sup.QueryStats(m.sup.APIAddr()) // nil map on error → (0,0) baselines
	var n int64
	for _, id := range ids {
		t := stats[fmt.Sprintf("u%d", id)]
		if err := m.store.ResetTraffic(id, t.Up, t.Down); err == nil {
			n++
		}
	}
	return n
}

// bulkExtendExpiry pushes each selected user's expiry out by `days`, anchored at the
// later of now and their current expiry so stacking adds time rather than resetting
// it. Users with no expiry (0 = never) are skipped.
func (m *Manager) bulkExtendExpiry(ids []int64, days int) int64 {
	now := time.Now().Unix()
	add := int64(days) * 86400
	var n int64
	for _, id := range ids {
		u, err := m.store.GetUser(id)
		if err != nil || u.ExpireAt == 0 {
			continue
		}
		base := now
		if u.ExpireAt > now {
			base = u.ExpireAt
		}
		if err := m.store.SetUserLimits(id, u.DataLimit, base+add, u.DeviceLimit); err == nil {
			n++
		}
	}
	return n
}

// RotateSubToken issues a new subscription URL token for a user. Protocol
// credentials stay the same — only the public /<sub_path>/<token> link changes,
// so the old URL stops working immediately. Triggers a user sync so any
// token-derived state is refreshed.
func (m *Manager) RotateSubToken(id int64) (*model.User, error) {
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
	return u, nil
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
func (m *Manager) SetResetPeriod(id int64, period string) error {
	switch period {
	case "none", "daily", "weekly", "monthly", "yearly":
	default:
		return invalid("неверный период сброса %q", period)
	}
	return m.store.SetResetPeriod(id, period, time.Now().Unix())
}

// applyResets zeroes the quota of users whose reset period has rolled over (new
// day/week/month/year). Re-enables them and reconciles if any were reset.
func (m *Manager) applyResets(users []model.User, now int64, counter func(int64) (int64, int64)) {
	reset := 0
	loc := m.loc()
	for _, u := range users {
		if resetDue(u.ResetPeriod, u.LastResetAt, now, loc) {
			up, down := counter(u.ID)
			if err := m.store.ResetUserQuota(u.ID, now, up, down); err == nil {
				reset++
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
