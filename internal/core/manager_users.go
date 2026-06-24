package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	return m.store.SetUserName(id, name)
}

// DeleteUser removes a user and reconciles.
func (m *Manager) DeleteUser(id int64) error {
	return m.mutateUser(fmt.Sprintf("user %d deleted", id),
		func() error { return m.store.DeleteUser(id) })
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
	// store.SetUserLimits recomputes status from the new limit/expiry/devices.
	return m.mutateUser(fmt.Sprintf("user %d limits updated: limit=%d expire=%d devices=%d", id, dataLimit, expireAt, deviceLimit),
		func() error { return m.store.SetUserLimits(id, dataLimit, expireAt, deviceLimit) })
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
