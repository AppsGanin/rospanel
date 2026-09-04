package sub

import (
	"fmt"
	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/model"
)

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
	// External are servers that are not ours, handed on beside this server's own
	// lanes (model.ExtServer). They are a panel-level list, so exactly one entry
	// carries them for the whole subscription — the master where it survived the
	// ordering, otherwise the first server that did. Nothing about the carrier
	// reaches the output: an external server renders from its own fields alone.
	External []model.ExtServer
}

// allowsBuiltin / allowsInbound apply the user's access for THIS server.
func (s Server) allowsBuiltin(lane string) bool {
	return s.Access.AllowsBuiltin(s.Set.ServerID, lane)
}
func (s Server) allowsInbound(id int64) bool { return s.Access.AllowsInbound(id) }
func (s Server) allowsExt(id int64) bool     { return s.Access.AllowsExt(id) }

// externalEndpoints is the external servers the user may have, in the shape the
// format converters read.
func (s Server) externalEndpoints() []extsub.Endpoint {
	var out []extsub.Endpoint
	for _, e := range s.External {
		if e.Enabled && s.allowsExt(e.ID) {
			out = append(out, extsub.Endpoint{Protocol: e.Protocol, Host: e.Host, Port: e.Port, Name: e.Name, Link: e.Link})
		}
	}
	return out
}

// Servers pairs each settings value with its server's custom inbounds (looked up by
// Settings.ServerID) and the requesting user's access, applied to every server.
func Servers(sets []*model.Settings, custom map[int64][]model.Inbound, access model.Access) []Server {
	out := make([]Server, 0, len(sets))
	for _, set := range sets {
		out = append(out, Server{Set: set, Custom: enabledOnly(custom[set.ServerID]), Access: access})
	}
	return out
}

// One wraps a single server with no custom inbounds and unrestricted access — the
// shape every legacy single-server helper and test needs.
func One(set *model.Settings) []Server {
	return []Server{{Set: set, Access: model.UnrestrictedAccess()}}
}

// enabledOnly filters out inbounds the operator has switched off, so a parked
// configuration never reaches a client.
func enabledOnly(list []model.Inbound) []model.Inbound {
	out := make([]model.Inbound, 0, len(list))
	for _, in := range list {
		if in.Enabled {
			out = append(out, in)
		}
	}
	return out
}

// uniqueLabel returns name, or name with a discriminator appended, so that no two
// entries in one profile carry the same one.
//
// It exists because the uniqueness the panel enforces when a name is SAVED is
// uniqueness of the stored text, and a name may carry variables (model.RenderName)
// that resolve per user at render time. Two different names can resolve to the same
// string — "{left}" and "{total}" are both ∞ for an account with no quota, "{used}"
// and "{left}" meet for one user at the moment they have spent half of theirs — and a
// duplicate Clash node name or sing-box tag makes a client reject the WHOLE profile,
// costing that user every server rather than one.
//
// The same shape NodeLinkSettings already uses for colliding server labels: keep the
// first, discriminate the rest, never drop anything.
func uniqueLabel(seen map[string]int, name string) string {
	seen[name]++
	if n := seen[name]; n > 1 {
		return fmt.Sprintf("%s #%d", name, n)
	}
	return name
}
