package core

import (
	"context"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/shaper"
)

// Per-user speed caps. The cap itself is enforced by the kernel (see
// internal/shaper); what lives here is the question "who is capped, and where are
// they connected from right now", which only the panel can answer.

// shaperInterval is how often the desired shaping is recomputed. A user's address
// set changes when they move network — the tap notices within its own flush window,
// and a further half minute before the cap follows them is not something anyone
// perceives. Applying is skipped entirely when nothing changed, so the cost of a
// tick is one query.
const shaperInterval = 30 * time.Second

// shaperWindow is how far back a source address still counts as "where this user
// is". Wider than the device-online window (which answers a different question:
// how many places at once) because a cap must not lapse the moment a client goes
// briefly idle — a paused download is still the same user on the same address.
const shaperWindow = 10 * time.Minute

// shaperLoop keeps the kernel's view of who is capped in step with the database.
func (m *Manager) shaperLoop() {
	t := time.NewTicker(shaperInterval)
	defer t.Stop()
	for {
		m.ApplyShaping()
		<-t.C
	}
}

// ApplyShaping recomputes and installs the local server's speed caps. Safe to call
// at any time; a no-op when nothing changed since the last pass.
//
// The address list it works from is the panel's, which also holds the sightings
// nodes report. Addresses that only ever reach a node therefore get a class here
// too — a filter matching traffic that never arrives, costing a rule and nothing
// else. Filtering them out would need per-server sightings, which is a schema
// change to avoid a few unused entries in a table the kernel walks in microseconds.
//
// A manager built without a shaper installs nothing. Only the package's own tests
// build one that way (New always sets it), and a test must not reach for `tc` on
// the machine it runs on — nor crash for lack of one, which is what a nil applier
// does on Linux, where Apply gets past its non-Linux early return.
func (m *Manager) ApplyShaping() {
	if m.shaper == nil {
		return
	}
	targets, err := m.store.ShapedUsers(time.Now().Add(-shaperWindow).Unix())
	if err != nil {
		logErr("shaper: cannot read capped users", "err", err)
		return
	}
	rules := make([]shaper.Rule, 0, len(targets))
	for id, t := range targets {
		rules = append(rules, shaper.Rule{UserID: id, Kbps: t.Kbps, IPs: t.IPs})
	}
	m.shaper.Apply(shaper.State{WAN: m.wanIface(), Rules: rules})
}

// wanIface is the interface the shaper acts on, resolved once and remembered: it is
// a property of the machine, and re-running `ip route` every 30 seconds to be told
// the same thing is waste.
func (m *Manager) wanIface() string {
	m.wanMu.Lock()
	defer m.wanMu.Unlock()
	if m.wan == "" {
		m.wan = shaper.DefaultWAN()
	}
	return m.wan
}

// ResetShaping removes every cap this panel installed. Called on shutdown: the
// kernel keeps a qdisc tree until reboot, so leaving one behind would throttle
// users on behalf of a panel that is no longer running.
func (m *Manager) ResetShaping() {
	if m.shaper == nil {
		return
	}
	m.shaper.Reset()
}

// SetUserSpeedLimit changes one user's cap (kbit/s; 0 = unlimited) and puts it in
// force immediately rather than at the next tick — an operator who has just typed a
// number expects to be able to test it.
func (m *Manager) SetUserSpeedLimit(ctx context.Context, id int64, kbps int) error {
	if kbps < 0 {
		return invalidCode("err.badValue", "скорость не может быть отрицательной")
	}
	u, err := m.store.GetUser(id)
	if err != nil {
		return err
	}
	if err := m.store.SetUserSpeedLimit(id, kbps); err != nil {
		return err
	}
	m.audit(ctx, id, model.EventSpeedLimit, map[string]any{"speed_limit": kbps, "was": u.SpeedLimit})
	// A speed set by hand replaces the panel's throttle rather than layering on it.
	m.overruleAbuseMeasure(ctx, u, model.AbuseActionThrottle)
	go m.ApplyShaping()
	// Nodes shape their own traffic from the limits in their sync payload, so the
	// change has to reach them too.
	m.TriggerUserSync()
	return nil
}

// SpeedLimits is every capped user's limit keyed by their Xray email ("uN") — the
// form a node consumes, since a node knows users by that tag and nothing else.
func (m *Manager) SpeedLimits() map[string]int {
	capped, err := m.store.CappedUsers(time.Now().Unix())
	if err != nil || len(capped) == 0 {
		return nil
	}
	out := make(map[string]int, len(capped))
	for id, kbps := range capped {
		out[model.UserEmail(id)] = kbps
	}
	return out
}
