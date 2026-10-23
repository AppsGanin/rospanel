package core

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Relayed external subscriptions (model.ExtSubscription.RelayLane): their servers reach
// users as entries of one of our VLESS lanes, and the server that lane is on carries the
// traffic on to them. The routing that does it is in the relay server's Xray config
// (see xray/relay.go), so unlike a subscription handed out as it is, a change to a
// relayed one reaches Xray.

// SetExtSubscriptionRelays relays a subscription's servers through lanes of our
// servers — each (server, lane) one more entry per server a user gets — or, with none,
// hands them out as they are. routing, when given, is checked against the new servers
// and stored with them: one save, both or neither. A relay through a server that is
// gone is dropped.
func (m *Manager) SetExtSubscriptionRelays(id int64, relays []model.ExtRelayVia, routing *model.ExtRouting) (*model.ExtSubscription, error) {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, invalidCode("err.extNotFound", "подписка не найдена")
	}
	clean := []model.ExtRelayVia{}
	for _, v := range relays {
		if slices.Contains(clean, v) {
			continue
		}
		if v.ServerID != model.LocalNodeID {
			if n, err := m.store.GetNode(v.ServerID); err != nil {
				return nil, err
			} else if n == nil {
				continue
			}
		}
		if err := m.checkRelayLane(v.Lane, v.ServerID); err != nil {
			return nil, err
		}
		clean = append(clean, v)
	}
	slices.SortFunc(clean, func(a, b model.ExtRelayVia) int {
		if a.ServerID != b.ServerID {
			return int(a.ServerID - b.ServerID)
		}
		return strings.Compare(a.Lane, b.Lane)
	})
	var cleanRouting model.ExtRouting
	if routing != nil && len(clean) > 0 {
		next := *sub
		next.Relays = clean
		if cleanRouting, err = m.cleanExtRouting(&next, *routing); err != nil {
			return nil, err
		}
	}
	if slices.Equal(clean, sub.Relays) && (routing == nil || len(clean) == 0) {
		return sub, nil
	}
	if err := m.store.SetExtSubscriptionRelays(id, clean); err != nil {
		return nil, err
	}
	if routing != nil && len(clean) > 0 {
		if err := m.store.SetExtSubscriptionRouting(id, cleanRouting); err != nil {
			return nil, err
		}
	}
	logInfo("extsub: relays set", "id", id, "relays", len(clean))
	m.extRelayChanged(true)
	return m.store.ExtSubscription(id)
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
		if len(s.Relays) > 0 {
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
		if lanes := model.RelayLanesOn(e.RelayVias(), serverID); len(lanes) > 0 {
			out = append(out, xray.Relay{ExtID: e.ID, Lanes: lanes, Link: e.Link, Routing: e.Routing})
		}
	}
	return out, nil
}

// SetExtSubscriptionRouting sets how a relayed subscription's traffic leaves the relay
// server (model.ExtRouting): what its rules send elsewhere, where the rest goes, and
// what the partner is dialled through. A lane named must be one of the relay server's.
func (m *Manager) SetExtSubscriptionRouting(id int64, r model.ExtRouting) (*model.ExtSubscription, error) {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, invalidCode("err.extNotFound", "подписка не найдена")
	}
	if r, err = m.cleanExtRouting(sub, r); err != nil {
		return nil, err
	}
	if err := m.store.SetExtSubscriptionRouting(id, r); err != nil {
		return nil, err
	}
	logInfo("extsub: routing set", "id", id, "rules", len(r.Rules), "rest", r.RestTarget(), "dial", r.Dial)
	m.extRelayChanged(len(sub.Relays) > 0)
	return m.store.ExtSubscription(id)
}

// cleanExtRouting trims what the operator wrote and refuses what cannot be built.
func (m *Manager) cleanExtRouting(sub *model.ExtSubscription, r model.ExtRouting) (model.ExtRouting, error) {
	// A lane named must be on every server the subscription is relayed through: the
	// routing is the subscription's, the same on each of them.
	lanes, err := m.relayCommonLanes(sub.Relays)
	if err != nil {
		return r, err
	}
	target := func(t string) error {
		switch t {
		case model.ExtToPartner, "direct", "block", "warp", "opera":
			return nil
		}
		if !lanes[t] {
			return invalidCode("err.extRouteTarget", "такого направления нет на сервере ретрансляции")
		}
		return nil
	}
	if len(r.Rules) > model.ExtRulesMax {
		return r, invalidCode("err.extRulesMax", "не больше {{max}} правил", map[string]any{"max": model.ExtRulesMax})
	}
	out := model.ExtRouting{Rules: []model.ExtRule{}, Rest: strings.TrimSpace(r.Rest), Dial: strings.TrimSpace(r.Dial)}
	for _, x := range r.Rules {
		rule := model.ExtRule{Domains: cleanEntries(x.Domains), IPs: cleanEntries(x.IPs), Target: strings.TrimSpace(x.Target)}
		if len(rule.Domains)+len(rule.IPs) == 0 {
			continue // an empty row in the editor
		}
		if len(rule.Domains) > model.ExtRuleEntriesMax || len(rule.IPs) > model.ExtRuleEntriesMax {
			return r, invalidCode("err.extRuleEntriesMax", "в правиле не больше {{max}} адресов", map[string]any{"max": model.ExtRuleEntriesMax})
		}
		for _, e := range append(slices.Clone(rule.Domains), rule.IPs...) {
			if len(e) > 256 || strings.ContainsAny(e, " \t\"") {
				return r, invalidCode("err.extRuleEntry", "неверная запись: {{value}}", map[string]any{"value": e})
			}
		}
		for _, ip := range rule.IPs {
			if !validRouteIP(ip) {
				return r, invalidCode("err.extRuleEntry", "неверная запись: {{value}}", map[string]any{"value": ip})
			}
		}
		if err := target(rule.Target); err != nil {
			return r, err
		}
		out.Rules = append(out.Rules, rule)
	}
	if out.Rest == model.ExtToPartner {
		out.Rest = ""
	}
	if err := target(out.RestTarget()); err != nil {
		return r, err
	}
	switch out.Dial {
	case "", "warp", "opera":
	case model.ExtToPartner, "direct":
		out.Dial = ""
	case "block":
		return r, invalidCode("err.extRouteTarget", "такого направления нет на сервере ретрансляции")
	default:
		if !lanes[out.Dial] {
			return r, invalidCode("err.extRouteTarget", "такого направления нет на сервере ретрансляции")
		}
	}
	if out.Dial == "opera" {
		servers, err := m.store.ExtServers()
		if err != nil {
			return r, err
		}
		for _, e := range servers {
			if e.SubID == sub.ID && (e.Protocol == "hysteria2" || e.Protocol == "wireguard") {
				return r, invalidCode("err.extDialOperaUDP", "Opera не передаёт UDP: серверы этой подписки на {{proto}} через неё не подключить",
					map[string]any{"proto": e.Protocol})
			}
		}
	}
	return out, nil
}

// cleanEntries trims a list and drops its empty entries.
func cleanEntries(in []string) []string {
	out := []string{}
	for _, e := range in {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// validRouteIP accepts what an IP list of the routing takes: an address, a CIDR, a
// geoip: list or an iplist: group.
func validRouteIP(e string) bool {
	if rest, ok := strings.CutPrefix(e, "geoip:"); ok {
		return rest != ""
	}
	if rest, ok := strings.CutPrefix(e, "iplist:"); ok {
		return rest != ""
	}
	if _, err := netip.ParsePrefix(e); err == nil {
		return true
	}
	_, err := netip.ParseAddr(e)
	return err == nil
}

// relayCommonLanes is the egress lanes every server in relays has.
func (m *Manager) relayCommonLanes(relays []model.ExtRelayVia) (map[string]bool, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	var common map[string]bool
	seen := map[int64]bool{}
	for _, v := range relays {
		if seen[v.ServerID] {
			continue
		}
		seen[v.ServerID] = true
		s := set
		if v.ServerID != model.LocalNodeID {
			n, err := m.store.GetNode(v.ServerID)
			if err != nil {
				return nil, err
			}
			if n == nil {
				continue
			}
			s = nodeSettings(set, n)
		}
		has := map[string]bool{}
		for _, l := range s.Routing.Lanes {
			if common == nil || common[l.ID] {
				has[l.ID] = true
			}
		}
		common = has
	}
	if common == nil {
		common = map[string]bool{}
	}
	return common, nil
}
