package sub

import (
	"net/url"

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
		switch e.RelayLane {
		case model.LaneVLESS:
			if !s.Set.VLESSEnabled || !s.allowsBuiltin(model.LaneVLESS) {
				continue
			}
		case model.LaneReality:
			if !s.Set.RealityEnabled || s.Set.RealityPublicKey == "" || !s.allowsBuiltin(model.LaneReality) {
				continue
			}
		default:
			continue
		}
		route, ok := model.ExtRoute(e.ID)
		if !ok {
			continue
		}
		// A UUID that already carries the route cannot reach the relay apart from the
		// lane itself; the routing leaves such a user out as well.
		if own, ok := model.UUIDRoute(u.UUID); !ok || own == route {
			continue
		}
		id, ok := model.RouteUUID(u.UUID, route)
		if !ok {
			continue
		}
		ru := u
		ru.UUID = id
		out = append(out, relayEntry{name: e.Name, lane: e.RelayLane, user: ru})
	}
	return out
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
