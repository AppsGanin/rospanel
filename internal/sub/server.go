package sub

import "github.com/Shu1t3/rospanel-shu1t3/internal/model"

// Server is one server as a subscription sees it: its effective settings (host, SNI,
// ports, REALITY material, node label), its enabled custom inbounds, and the
// requesting user's access to this server's lanes.
//
// Settings and inbounds travel together because they answer the same question from
// different tables — the built-in lanes live in the settings row, the custom ones in
// their own table keyed by server. Access rides along so a subscription only ever
// lists the lanes the user is actually allowed (their credential is on the server for
// exactly those); it's the same gate as config generation, applied to what the client
// is told.
type Server struct {
	Set    *model.Settings
	Custom []model.Inbound
	Access model.Access
}

// allowsBuiltin / allowsInbound apply the user's access for THIS server.
func (s Server) allowsBuiltin(lane string) bool {
	return s.Access.AllowsBuiltin(s.Set.ServerID, lane)
}
func (s Server) allowsInbound(id int64) bool { return s.Access.AllowsInbound(id) }

// Servers pairs each settings value with its server's custom inbounds (looked up by
// Settings.ServerID) and the requesting user's access, applied to every server.
func Servers(sets []*model.Settings, custom map[int64][]model.Inbound, access model.Access) []Server {
	out := make([]Server, 0, len(sets))
	for _, set := range sets {
		isRented := set != nil && set.IsRented
		out = append(out, Server{Set: set, Custom: filterInbounds(custom[set.ServerID], isRented), Access: access})
	}
	return out
}

// One wraps a single server with no custom inbounds and unrestricted access — the
// shape every legacy single-server helper and test needs.
func One(set *model.Settings) []Server {
	return []Server{{Set: set, Access: model.UnrestrictedAccess()}}
}

// filterInbounds filters out inbounds the operator has switched off, and isolates
// tenant inbounds from owner subscriptions so credentials never mix.
func filterInbounds(list []model.Inbound, isRentedServer bool) []model.Inbound {
	out := make([]model.Inbound, 0, len(list))
	for _, in := range list {
		if !in.Enabled {
			continue
		}
		// On an owner server, do not leak tenant-owned inbounds into owner subscriptions.
		if !isRentedServer && in.IsRental() {
			continue
		}
		out = append(out, in)
	}
	return out
}
