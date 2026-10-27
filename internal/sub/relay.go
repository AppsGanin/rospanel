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
	return out
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
		// Formats that need unique names number the repeats (uniqueLabel).
		return relayEntry{name: s.relayName(e, lane, u), lane: lane, user: ru}, true
	}
}

// Placeholders for {name} and {proto} while the rest of a template renders.
const (
	extHoldName  = "\uE000"
	extHoldProto = "\uE001"
)

// relayName is what a relayed entry is called: the provider's server alone, or the
// subscription's name template (model.ExtRouting.Name) rendered for this user, this
// server of ours and this lane.
func (s Server) relayName(e model.ExtServer, lane string, u model.User) string {
	tmpl := e.Routing.Name
	if tmpl == "" {
		return e.Name
	}
	server := s.Set.NodeLabel
	if server == "" {
		server = s.Set.MasterLabel
	}
	proto := "TCP-TLS"
	if lane == model.LaneReality {
		proto = "REALITY"
	}
	// One pass over the template: {name} and {proto} stand in as private-use runes
	// while the connection-name variables render, so neither a provider's name nor a
	// user's has its own text read as a variable.
	held := strings.NewReplacer(model.ExtNameVarName, extHoldName, model.ExtNameVarProto, extHoldProto).Replace(tmpl)
	name := model.RenderName(held, model.NameVars{Server: server, User: &u, Loc: s.Set.Location()})
	name = strings.NewReplacer(extHoldName, e.Name, extHoldProto, proto).Replace(name)
	if name = strings.Join(strings.Fields(name), " "); name == "" {
		return e.Name
	}
	return name
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
