package sub

import (
	"net/url"
	"strings"

	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// relayEntry is a relayed external server as one user reaches it: an entry of lane on
// this server, under the external server's name, with the user's UUID carrying the
// server's route (model.ExtRoute; the routing is xray/relay.go).
type relayEntry struct {
	name string
	lane string
	user model.User
}

// relayEntries lists the relayed servers this user reaches through this server: the
// ones switched on and granted to them, on a lane that runs here and is theirs.
func (s Server) relayEntries(u model.User) []relayEntry {
	var out []relayEntry
	for _, e := range s.Relays {
		if !e.Enabled || !s.allowsExt(e.ID) {
			continue
		}
		for _, lane := range model.RelayLanesOn(e.RelayVias(), s.Set.ServerID) {
			if r, ok := s.relayEntry(e, lane, u); ok {
				out = append(out, r)
			}
		}
	}
	return append(out, s.exitEntries(u)...)
}

// exitEntries are this server's own ways out handed to the user as connections
// (model.ExitConfig): one entry per lane picked, the way out's route in their UUID —
// while that way out runs here and their groups grant it.
func (s Server) exitEntries(u model.User) []relayEntry {
	var out []relayEntry
	for _, kind := range model.ExitKinds {
		c, ok := model.ExitFor(s.Set.ExitConfigs, kind)
		if !ok || !s.exitRuns(kind) || !s.allowsBuiltin(kind) {
			continue
		}
		route, _ := model.ExitRoute(kind)
		if own, ok := model.UUIDRoute(u.UUID); !ok || own == route {
			continue
		}
		id, ok := model.RouteUUID(u.UUID, route)
		if !ok {
			continue
		}
		ru := u
		ru.UUID = id
		name := c.Name
		if name == "" {
			name = exitLabel(kind)
			if label := strings.TrimSpace(s.Set.NodeLabel); label != "" {
				name = label + " · " + name
			}
		}
		for _, lane := range c.Lanes {
			if !s.laneRuns(lane) {
				continue
			}
			n := name
			if len(c.Lanes) > 1 {
				n += " · " + laneShort(lane)
			}
			out = append(out, relayEntry{name: n, lane: lane, user: ru})
		}
	}
	return out
}

// exitRuns reports whether a way out is built on this server.
func (s Server) exitRuns(kind string) bool {
	switch kind {
	case model.ExitWARP:
		return s.Set.WarpEnabled && s.Set.WarpRegistered()
	case model.ExitOpera:
		return s.Set.OperaEnabled
	}
	return false
}

// laneRuns reports whether a relay lane runs on this server and is the user's.
func (s Server) laneRuns(lane string) bool {
	switch lane {
	case model.LaneVLESS:
		return s.Set.VLESSEnabled && s.allowsBuiltin(model.LaneVLESS)
	case model.LaneReality:
		return s.Set.RealityEnabled && s.Set.RealityPublicKey != "" && s.allowsBuiltin(model.LaneReality)
	}
	return false
}

func exitLabel(kind string) string {
	if kind == model.ExitOpera {
		return "Opera"
	}
	return "WARP"
}

func laneShort(lane string) string {
	if lane == model.LaneReality {
		return "REALITY"
	}
	return "TCP-TLS"
}

// relayEntry is e reached through lane on this server, when the lane runs here and is
// the user's.
func (s Server) relayEntry(e model.ExtServer, lane string, u model.User) (relayEntry, bool) {
	{
		switch lane {
		case model.LaneVLESS:
			if !s.Set.VLESSEnabled || !s.allowsBuiltin(model.LaneVLESS) {
				return relayEntry{}, false
			}
		case model.LaneReality:
			if !s.Set.RealityEnabled || s.Set.RealityPublicKey == "" || !s.allowsBuiltin(model.LaneReality) {
				return relayEntry{}, false
			}
		default:
			return relayEntry{}, false
		}
		route, ok := model.ExtRoute(e.ID)
		if !ok {
			return relayEntry{}, false
		}
		// A UUID that already carries the route cannot reach the relay apart from the
		// lane itself; the routing leaves such a user out as well.
		if own, ok := model.UUIDRoute(u.UUID); !ok || own == route {
			return relayEntry{}, false
		}
		id, ok := model.RouteUUID(u.UUID, route)
		if !ok {
			return relayEntry{}, false
		}
		ru := u
		ru.UUID = id
		name := e.Name
		if len(e.RelayVias()) > 1 {
			// Through more than one lane of ours: the lane's own label (with this
			// server's in a multi-server subscription) tells the entries apart, in every
			// format alike.
			proto := model.ProtoVLESS
			if lane == model.LaneReality {
				proto = model.ProtoReality
			}
			name += " · " + s.Set.ProtoLabel(proto)
		}
		return relayEntry{name: name, lane: lane, user: ru}, true
	}
}

// link is the entry as a share link: the lane's link for the routed UUID, named after
// the external server.
func (r relayEntry) link(set *model.Settings) string {
	raw := link.VLESS(r.user, set)
	if r.lane == model.LaneReality {
		raw = link.Reality(r.user, set)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Fragment = r.name
	return parsed.String()
}
