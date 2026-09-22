package core

import (
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Relayed external subscriptions (model.ExtSubscription.RelayLane): their servers reach
// users as entries of one of our VLESS lanes, and the server that lane is on carries the
// traffic on to them. The routing that does it is in the relay server's Xray config
// (see xray/relay.go), so unlike a subscription handed out as it is, a change to a
// relayed one reaches Xray.

// SetExtSubscriptionRelay relays a subscription's servers through a lane of one of our
// servers, or — with an empty lane — hands them out as they are.
func (m *Manager) SetExtSubscriptionRelay(id int64, lane string, serverID int64) error {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return err
	}
	if sub == nil {
		return invalidCode("err.extNotFound", "подписка не найдена")
	}
	if lane == "" {
		serverID = model.LocalNodeID
	} else if err := m.checkRelayLane(lane, serverID); err != nil {
		return err
	}
	if sub.RelayLane == lane && sub.RelayServerID == serverID {
		return nil
	}
	if err := m.store.SetExtSubscriptionRelay(id, lane, serverID); err != nil {
		return err
	}
	logInfo("extsub: relay set", "id", id, "lane", lane, "server", serverID)
	m.extRelayChanged(true)
	return nil
}

// checkRelayLane refuses a lane that is not a relay lane, a server that does not exist,
// and a lane that server does not run: the servers would silently reach nobody.
func (m *Manager) checkRelayLane(lane string, serverID int64) error {
	switch lane {
	case model.LaneVLESS, model.LaneReality:
	default:
		return invalidCode("err.extRelayLane", "ретранслировать можно только через VLESS TCP-TLS или VLESS REALITY")
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	if serverID != model.LocalNodeID {
		n, err := m.store.GetNode(serverID)
		if err != nil {
			return err
		}
		if n == nil {
			return invalidCode("err.nodeNotFound", "нода не найдена")
		}
		set = nodeSettings(set, n)
	}
	on := set.VLESSEnabled
	if lane == model.LaneReality {
		on = set.RealityEnabled && set.RealityPublicKey != ""
	}
	if !on {
		return invalidCode("err.extRelayLaneOff", "эта линия выключена на выбранном сервере")
	}
	return nil
}

// extRelayChanged puts a change to relayed servers into Xray: the master reloads
// (nothing happens when its config comes out the same) and the nodes re-read their
// state. relayed says whether the change touches a relayed subscription at all.
func (m *Manager) extRelayChanged(relayed bool) {
	if !relayed {
		return
	}
	m.TriggerReconcile()
	m.notifyNodes()
}

// anyExtRelay reports whether any subscription is relayed: a change to a server
// whose subscription is unknown at the call touches Xray only then.
func (m *Manager) anyExtRelay() bool {
	subs, err := m.store.ExtSubscriptions()
	if err != nil {
		return true // cannot tell: a reload that changes nothing costs little
	}
	for _, s := range subs {
		if s.RelayLane != "" {
			return true
		}
	}
	return false
}

// relaysFor lists the external servers server serverID carries traffic on to: switched
// on, from a source switched on, relayed through it.
func (m *Manager) relaysFor(serverID int64) ([]xray.Relay, error) {
	servers, err := m.store.EnabledExtServers()
	if err != nil {
		return nil, err
	}
	var out []xray.Relay
	for _, e := range servers {
		if e.RelayLane != "" && e.RelayServerID == serverID {
			out = append(out, xray.Relay{ExtID: e.ID, Lane: e.RelayLane, Link: e.Link})
		}
	}
	return out, nil
}
