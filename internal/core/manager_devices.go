package core

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Device binding. A client that follows the subscription-header convention sends a
// stable install id in x-hwid; the panel binds it to the user on first fetch and
// refuses the fetch once the user's device cap is full. See migration 0041 for why
// this exists next to the IP-based count rather than replacing it.

// deviceRefusalQuiet is how long the same (user, device) refusal stays silent after
// it has been reported once. A refused client keeps retrying on its own update
// timer — without this, one person who installed the app on a fourth phone would
// write an audit row and ping the operator every few minutes, forever.
const deviceRefusalQuiet = 6 * time.Hour

// deviceNotice remembers which refusals have already been reported. It is bounded by
// the number of distinct (user, hwid) pairs that hit a full cap, and swept on the
// same timer that expires them.
type deviceNotice struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newDeviceNotice() *deviceNotice { return &deviceNotice{seen: map[string]time.Time{}} }

// should reports whether this refusal is worth reporting, and marks it reported.
func (n *deviceNotice) should(key string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if last, ok := n.seen[key]; ok && now.Sub(last) < deviceRefusalQuiet {
		return false
	}
	for k, t := range n.seen { // opportunistic sweep: the map is only ever walked here
		if now.Sub(t) >= deviceRefusalQuiet {
			delete(n.seen, k)
		}
	}
	n.seen[key] = now
	return true
}

// DeviceVerdict is the answer to "may this client have the subscription?".
type DeviceVerdict struct {
	Allow bool
	// Cap and Count describe the roster at the moment of the decision, for the
	// message the refused client is shown. Both 0 when the feature is off.
	Cap   int
	Count int
}

// AdmitDevice decides whether one subscription fetch is served, binding the client
// to the user when it identifies itself and there is room.
//
// With the feature off, every fetch is served exactly as before. With it on, a
// client that sends no id is refused unless the operator turned HWIDRequire off:
// serving the silent ones leaves a cap that anyone can dodge by switching to a
// client that says nothing, which is why requiring is the default — at the cost of
// clients that send no id (v2rayN, Clash, curl) no longer working.
//
// The subscription PAGE is unaffected either way: a person opening their account in
// a browser is not an install asking for credentials.
func (m *Manager) AdmitDevice(ctx context.Context, u model.User, set *model.Settings, d model.Device) DeviceVerdict {
	if !set.HWIDEnabled {
		return DeviceVerdict{Allow: true}
	}
	if d.HWID == "" {
		return DeviceVerdict{Allow: !set.HWIDRequire}
	}
	capacity := set.DeviceCap(u)
	adm, err := m.store.RegisterDevice(u.ID, d, capacity)
	if err != nil {
		// A storage failure must not lock a paying user out of their subscription:
		// the cap is a policy, and losing the ability to enforce it for one fetch is
		// the lesser failure. Logged so it doesn't pass unnoticed.
		logErr("devices: register failed", "user", u.ID, "err", err)
		return DeviceVerdict{Allow: true}
	}
	switch {
	case adm.New:
		m.audit(ctx, u.ID, model.EventDeviceBound, map[string]any{
			"hwid": d.HWID, "os": d.OS, "model": d.Model,
			"devices": adm.Count, "device_limit": capacity,
		})
	case !adm.Allowed:
		m.reportDeviceRefusal(ctx, u, set, d, capacity, adm.Count)
	}
	return DeviceVerdict{Allow: adm.Allowed, Cap: capacity, Count: adm.Count}
}

// reportDeviceRefusal writes the audit row and pings the operator and the user, at
// most once per deviceRefusalQuiet for the same device.
func (m *Manager) reportDeviceRefusal(
	ctx context.Context, u model.User, set *model.Settings, d model.Device, capacity, count int,
) {
	if !m.devNotice.should(deviceKey(u.ID, d.HWID), time.Now()) {
		return
	}
	m.audit(ctx, u.ID, model.EventDeviceRefused, map[string]any{
		"hwid": d.HWID, "os": d.OS, "model": d.Model,
		"devices": count, "device_limit": capacity,
	})
	// Reuse the device-limit notification categories rather than inventing a pair the
	// operator would have to discover and switch on: to them this is the same event
	// they already asked to hear about — someone has more devices than they may.
	m.notifyAdminEvent(model.AdminEventDeviceLimited, fmt.Sprintf(
		i18n.T(m.botLang(), "notify.adminDeviceRefused"),
		escHTML(u.Name), escHTML(deviceLabel(d)), count, capacity))
	m.notifyUserEvent(set, u, model.UserNotifyDeviceLimited, fmt.Sprintf(
		i18n.T(m.userLang(u.TgChatID), "notify.userDeviceRefused"), count, capacity))
	m.EmitWebhook(model.WebhookUserDeviceLimit, userEventData(u))
}

// UserDevices lists the devices bound to a user (the user card's Devices list).
func (m *Manager) UserDevices(userID int64) ([]model.Device, error) {
	return m.store.ListDevices(userID)
}

// UnbindDevice releases one device slot. Reports whether anything was bound under
// that id, so the caller can answer 404 rather than pretend.
func (m *Manager) UnbindDevice(ctx context.Context, userID int64, hwid string) (bool, error) {
	ok, err := m.store.DeleteDevice(userID, hwid)
	if err != nil || !ok {
		return ok, err
	}
	m.audit(ctx, userID, model.EventDeviceUnbound, map[string]any{"hwid": hwid})
	return true, nil
}

// UnbindAllDevices releases every device of a user — the "they replaced their
// phone / lost the lot" button.
func (m *Manager) UnbindAllDevices(ctx context.Context, userID int64) (int64, error) {
	n, err := m.store.DeleteDevices(userID)
	if err != nil || n == 0 {
		return n, err
	}
	m.audit(ctx, userID, model.EventDeviceUnbound, map[string]any{"devices": n})
	return n, nil
}

// PurgeIdleDevices forgets devices that have not fetched for HWIDTTLDays, returning
// their slots. Called from the retention sweep; a no-op only when the TTL is 0
// (never forget).
//
// It runs even while device binding is switched OFF. The rows are inert then, but
// they are not gone — and an operator who turns the feature off for half a year and
// back on would otherwise find every user instantly at their cap, held there by
// devices nobody owns any more.
func (m *Manager) PurgeIdleDevices() {
	set, err := m.store.GetSettings()
	if err != nil || set.HWIDTTLDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -set.HWIDTTLDays).Unix()
	n, err := m.store.PurgeDevices(cutoff)
	if err != nil {
		logErr("devices: retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("devices: forgot idle devices", "count", n, "ttl_days", set.HWIDTTLDays)
	}
}

// deviceKey identifies one (user, device) pair for the refusal quiet period.
func deviceKey(userID int64, hwid string) string {
	return strconv.FormatInt(userID, 10) + "\x00" + hwid
}

// deviceLabel renders a device for a human: the model and OS when the client sent
// them, the raw id when it sent nothing else.
func deviceLabel(d model.Device) string {
	switch {
	case d.Model != "" && d.OS != "":
		return d.Model + " (" + d.OS + ")"
	case d.Model != "":
		return d.Model
	case d.OS != "":
		return d.OS
	default:
		return d.HWID
	}
}
