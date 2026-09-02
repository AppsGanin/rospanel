package core

import (
	"errors"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// User groups gate which connections a user may reach. A user in no group reaches
// everything; a user in one or more groups reaches exactly the union of what those
// groups grant, enforced in config generation (the credential is withheld from a lane
// the user isn't allowed) — see genOptsFor / xray.Generate.
//
// A group change doesn't add or remove users, so the live user-sync delta can't see
// it; every group mutation therefore goes through a full reconcile (one debounced Xray
// restart) and wakes the nodes, exactly like a protocol toggle.

// Groups returns every group with its grants and member count, for the management UI.
func (m *Manager) Groups() ([]model.Group, error) { return m.store.Groups() }

// GroupTarget is one server's grantable connections, for the group editor: its
// built-in lanes and its custom inbounds, each with the token a grant would store.
type GroupTarget struct {
	ServerID   int64             `json:"server_id"`
	ServerName string            `json:"server_name"`
	Lanes      []GroupLaneOpt    `json:"lanes"`
	Inbounds   []GroupInboundOpt `json:"inbounds"`
}

// GroupLaneOpt is one built-in lane as a grantable item.
type GroupLaneOpt struct {
	Lane    string `json:"lane"`  // vless | reality | hysteria2
	Label   string `json:"label"` // display name (custom or default)
	Token   string `json:"token"`
	Enabled bool   `json:"enabled"`
}

// GroupInboundOpt is one custom inbound as a grantable item.
type GroupInboundOpt struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Token   string `json:"token"`
	Enabled bool   `json:"enabled"`
}

// GroupTargets lists every server (master + nodes) with the connections a group can
// grant. Disabled lanes/inbounds are included too, so a grant survives a temporary
// disable and the operator can pre-grant.
func (m *Manager) GroupTargets() ([]GroupTarget, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	custom, err := m.store.AllInbounds()
	if err != nil {
		return nil, err
	}
	target := func(id int64, name string, s *model.Settings) GroupTarget {
		lanes := []GroupLaneOpt{
			{model.LaneVLESS, s.ProtoLabel(model.ProtoVLESS), model.BuiltinToken(id, model.LaneVLESS), s.VLESSEnabled},
			{model.LaneReality, s.ProtoLabel(model.ProtoReality), model.BuiltinToken(id, model.LaneReality), s.RealityEnabled},
			{model.LaneHysteria, s.ProtoLabel(model.ProtoHysteria), model.BuiltinToken(id, model.LaneHysteria), s.HysteriaEnabled},
			{model.LaneAWG, s.ProtoLabel(model.ProtoAWG), model.BuiltinToken(id, model.LaneAWG), s.AWGEnabled},
		}
		t := GroupTarget{ServerID: id, ServerName: name, Lanes: lanes, Inbounds: []GroupInboundOpt{}}
		for _, in := range custom[id] {
			t.Inbounds = append(t.Inbounds, GroupInboundOpt{
				ID: in.ID, Name: in.Name, Token: model.InboundToken(in.ID), Enabled: in.Enabled,
			})
		}
		return t
	}

	// The master's lane labels have no node prefix; NodeLabel is per-request cosmetics.
	masterSet := *set
	masterSet.NodeLabel = ""
	out := []GroupTarget{target(model.LocalNodeID, model.LocalNodeName, &masterSet)}

	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		n := &nodes[i]
		out = append(out, target(n.ID, n.Name, nodeSettings(set, n)))
	}
	return out, nil
}

// sanitizeGrants keeps only well-formed grant tokens (a built-in lane or a custom
// inbound), dropping anything malformed. A token that references a missing inbound or
// server is allowed through — it simply grants access to nothing and is swept when
// that target is deleted.
func sanitizeGrants(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	seen := map[string]bool{}
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		ok := false
		if _, isInbound := model.ParseInboundToken(t); isInbound {
			ok = true
		} else if _, lane, isBuiltin := model.ParseBuiltinToken(t); isBuiltin {
			ok = lane == model.LaneVLESS || lane == model.LaneReality || lane == model.LaneHysteria || lane == model.LaneAWG
		}
		if ok {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// CreateGroup validates and stores a new group.
func (m *Manager) CreateGroup(name string, grants []string) (*model.Group, error) {
	name = strings.TrimSpace(name)
	if err := model.ValidateGroupName(name); err != nil {
		return nil, fromFieldErr(err)
	}
	g, err := m.store.CreateGroup(name, sanitizeGrants(grants))
	if err != nil {
		return nil, groupErr(err)
	}
	m.applyAccessChange()
	return g, nil
}

// UpdateGroup validates and stores an edit (rename + grants).
func (m *Manager) UpdateGroup(id int64, name string, grants []string) error {
	name = strings.TrimSpace(name)
	if err := model.ValidateGroupName(name); err != nil {
		return fromFieldErr(err)
	}
	if err := m.store.UpdateGroup(id, name, sanitizeGrants(grants)); err != nil {
		return groupErr(err)
	}
	m.applyAccessChange()
	return nil
}

// DeleteGroup removes a group; its members lose that grant.
func (m *Manager) DeleteGroup(id int64) error {
	if err := m.store.DeleteGroup(id); err != nil {
		return err
	}
	m.applyAccessChange()
	return nil
}

// SetGroupMembers replaces the users in a group — the group-side twin of
// SetUserGroups, so membership can be edited from either end.
func (m *Manager) SetGroupMembers(groupID int64, userIDs []int64) error {
	g, err := m.store.GetGroup(groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return invalidCode("err.groupNotFound", "группа не найдена")
	}
	if err := m.store.SetGroupMembers(groupID, userIDs); err != nil {
		return err
	}
	m.applyAccessChange()
	return nil
}

// SetUserGroups replaces a user's group membership.
func (m *Manager) SetUserGroups(userID int64, groupIDs []int64) error {
	if _, err := m.store.GetUser(userID); err != nil {
		return err
	}
	if err := m.store.SetUserGroups(userID, groupIDs); err != nil {
		return err
	}
	m.applyAccessChange()
	return nil
}

// GroupsForUser / GroupsForAllUsers expose membership for the user views.
func (m *Manager) GroupsForUser(userID int64) ([]model.GroupRef, error) {
	return m.store.GroupsForUser(userID)
}
func (m *Manager) GroupsForAllUsers() (map[int64][]model.GroupRef, error) {
	return m.store.GroupsForAllUsers()
}

// applyAccessChange reconciles the master and wakes the nodes so a group change takes
// effect. A group edit changes which users belong to which lanes but adds/removes no
// users, so the live user-sync would see nothing — a full reconcile is the change.
func (m *Manager) applyAccessChange() {
	m.TriggerReconcile()
	m.notifyNodes()
}

// groupErr maps the store's name-conflict sentinel to a user-facing message.
func groupErr(err error) error {
	if errors.Is(err, store.ErrGroupNameTaken) {
		return invalidCode("err.groupNameTaken", "группа с таким названием уже есть")
	}
	return err
}
