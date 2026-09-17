package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// A node that speaks nodeapi.DeltaRev is sent its config without users and writes them
// back in itself (see nodeapi/split.go). SplitUsers is the panel's half and RenderUsers
// the node's; between them the config the node runs must be the config the generator
// built, entry for entry, so SplitUsers checks every entry it takes out against the one
// RenderUsers would put back, and gives up on a config where any differ.

// SplitConfig is a generated config taken apart.
type SplitConfig struct {
	// Skeleton is the config with every user list in Slots empty.
	Skeleton *Config
	Slots    []nodeapi.UserSlot
	// Placed lists, for every user in the config, the slots they are in, ascending.
	Placed map[int64][]int
}

// SplitUsers takes the users out of a config Generate built from these custom inbounds
// and users. ok is false when the config has a user list it cannot account for — an
// inbound it does not know, an entry that is not a user, an entry RenderUsers would not
// write the same way — and then the config has to be sent whole.
func SplitUsers(cfg *Config, custom []model.Inbound, users []model.User) (*SplitConfig, bool) {
	byTag := make(map[string]*model.Inbound, len(custom))
	for i := range custom {
		byTag[custom[i].Tag()] = &custom[i]
	}
	byID := make(map[int64]*model.User, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}
	out := &SplitConfig{Placed: map[int64][]int{}}
	skel := *cfg
	skel.Inbounds = make([]Inbound, len(cfg.Inbounds))
	copy(skel.Inbounds, cfg.Inbounds)
	for i, in := range cfg.Inbounds {
		if liveUserKey[in.Protocol] == "" {
			continue
		}
		slot, ok := slotOf(in, byTag)
		if !ok {
			return nil, false
		}
		index := len(out.Slots)
		entries, empty, ok := usersOf(in.Settings)
		if !ok {
			return nil, false
		}
		// Nobody on a Shadowsocks inbound: its one entry is the locked one.
		if slot.Kind == "shadowsocks" && len(entries) == 1 {
			if b, err := json.Marshal(entries[0]); err == nil && bytes.Equal(b, slot.Locked) {
				entries = nil
			}
		}
		for _, e := range entries {
			id, ok := model.UserIDOfEmail(emailOf(e))
			if !ok {
				return nil, false
			}
			u := byID[id]
			if u == nil || !reflect.DeepEqual(e, slotEntry(slot, u.ID, userSlotCreds(slot, u))) {
				return nil, false
			}
			placed := out.Placed[id]
			if n := len(placed); n > 0 && placed[n-1] == index {
				return nil, false // listed twice in one inbound
			}
			out.Placed[id] = append(placed, index)
		}
		slot.Inbound = i
		out.Slots = append(out.Slots, slot)
		skel.Inbounds[i].Settings = empty
	}
	out.Skeleton = &skel
	return out, true
}

// slotOf says how an inbound's user entries are written: the built-in lanes by their
// tags, a custom inbound by its protocol and options.
func slotOf(in Inbound, custom map[string]*model.Inbound) (nodeapi.UserSlot, bool) {
	var s nodeapi.UserSlot
	switch in.Tag {
	case TagVLESS:
		s = nodeapi.UserSlot{Kind: "vless", Flow: VisionFlow}
	case TagReality:
		s = nodeapi.UserSlot{Kind: "vless"}
	case TagHysteria:
		s = nodeapi.UserSlot{Kind: "hysteria"}
	default:
		c := custom[in.Tag]
		if c == nil {
			return s, false
		}
		switch c.Protocol {
		case model.InbVLESS:
			s = nodeapi.UserSlot{Kind: "vless", Flow: c.Opts.Flow}
		case model.InbTrojan:
			s = nodeapi.UserSlot{Kind: "trojan"}
		case model.InbHysteria:
			s = nodeapi.UserSlot{Kind: "hysteria"}
		case model.InbWireGuard:
			s = nodeapi.UserSlot{Kind: "wireguard"}
		case model.InbShadowsocks:
			locked, err := json.Marshal(ShadowsocksClient{
				Password: model.LockedShadowKey(c.Opts.ShadowKey, c.Opts.Method),
				Email:    "ss-locked-" + c.Tag(),
			})
			if err != nil {
				return s, false
			}
			s = nodeapi.UserSlot{Kind: "shadowsocks", Method: c.Opts.Method, Locked: locked}
		default:
			return s, false
		}
	}
	return s, s.Kind == in.Protocol
}

// usersOf returns an inbound's user entries and its settings with them emptied.
func usersOf(settings any) (entries []any, empty any, ok bool) {
	switch s := settings.(type) {
	case VLESSInboundSettings:
		for _, c := range s.Clients {
			entries = append(entries, c)
		}
		s.Clients = []VLESSClient{}
		return entries, s, true
	case TrojanInboundSettings:
		for _, c := range s.Clients {
			entries = append(entries, c)
		}
		s.Clients = []TrojanClient{}
		return entries, s, true
	case HysteriaInboundSettings:
		for _, c := range s.Users {
			entries = append(entries, c)
		}
		s.Users = []HysteriaClient{}
		return entries, s, true
	case ShadowsocksInboundSettings:
		for _, c := range s.Users {
			entries = append(entries, c)
		}
		s.Users = []ShadowsocksClient{}
		return entries, s, true
	case WireGuardInboundSettings:
		for _, c := range s.Peers {
			entries = append(entries, c)
		}
		s.Peers = []WireGuardInboundPeer{}
		return entries, s, true
	}
	return nil, nil, false
}

func emailOf(entry any) string {
	switch e := entry.(type) {
	case VLESSClient:
		return e.Email
	case TrojanClient:
		return e.Email
	case HysteriaClient:
		return e.Email
	case ShadowsocksClient:
		return e.Email
	case WireGuardInboundPeer:
		return e.Email
	}
	return ""
}

// slotCreds are the credentials a user's entry in a slot is written from.
type slotCreds struct {
	uuid, password string
	// wgKey and wgAddr are the user's tunnel public key and address, for a wireguard
	// slot.
	wgKey, wgAddr string
}

// userSlotCreds takes a slot's credentials from a user. The tunnel pair is derived only
// for a slot that uses it: the public key costs a curve multiplication.
func userSlotCreds(s nodeapi.UserSlot, u *model.User) slotCreds {
	c := slotCreds{uuid: u.UUID, password: u.Password}
	if s.Kind == "wireguard" {
		c.wgKey, c.wgAddr, _ = TunnelIdentity(u)
	}
	return c
}

// TunnelIdentity is a user's WireGuard public key and tunnel address, as a WireGuard
// inbound's peer holds them. ok is false for a user with none yet or an unreadable key.
func TunnelIdentity(u *model.User) (key, addr string, ok bool) {
	peer, ok := wireGuardPeer(u.ID, u.WGPrivateKey, u.AWGSlot)
	if !ok {
		return "", "", false
	}
	a, _ := awg.ClientAddr(u.AWGSlot)
	return peer.PublicKey, a.String(), true
}

// slotEntry is one user's entry in a slot's list, as the generator writes it.
func slotEntry(s nodeapi.UserSlot, id int64, c slotCreds) any {
	email := model.UserEmail(id)
	switch s.Kind {
	case "vless":
		return VLESSClient{ID: c.uuid, Flow: s.Flow, Email: email}
	case "trojan":
		return TrojanClient{Password: c.password, Email: email}
	case "hysteria":
		return HysteriaClient{Auth: c.password, Email: email}
	case "shadowsocks":
		return ShadowsocksClient{Password: model.UserShadowKey(c.uuid, s.Method), Email: email}
	case "wireguard":
		return WireGuardInboundPeer{PublicKey: c.wgKey, AllowedIPs: []string{c.wgAddr + "/32"}, Email: email}
	}
	return nil
}

// SlotNeed says which of a user's credentials a slot's entry is written from.
type SlotNeed struct {
	UUID, Password bool
	Tunnel         bool // the WireGuard public key and address
}

// SlotNeeds reports which of a user's credentials a slot's entry is written from.
func SlotNeeds(s nodeapi.UserSlot) SlotNeed {
	switch s.Kind {
	case "vless", "shadowsocks":
		return SlotNeed{UUID: true}
	case "trojan", "hysteria":
		return SlotNeed{Password: true}
	case "wireguard":
		return SlotNeed{Tunnel: true}
	}
	return SlotNeed{}
}

// RenderUsers writes rows back into a skeleton config: every user into each list their
// row names, in the order of the rows, which must be ascending by id — the order the
// generator lists users in. A Shadowsocks list nobody is in gets its locked entry.
func RenderUsers(skeleton []byte, slots []nodeapi.UserSlot, rows []nodeapi.UserRow) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(skeleton))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("skeleton: %w", err)
	}
	inbounds, _ := cfg["inbounds"].([]any)
	lists := make([][]any, len(slots))
	for i := range rows {
		r := &rows[i]
		if i > 0 && rows[i-1].ID >= r.ID {
			return nil, fmt.Errorf("rows out of order at user %d", r.ID)
		}
		for _, si := range r.Slots {
			if si < 0 || si >= len(slots) {
				return nil, fmt.Errorf("user %d: no slot %d", r.ID, si)
			}
			lists[si] = append(lists[si], slotEntry(slots[si], r.ID, slotCreds{
				uuid: r.UUID, password: r.Password, wgKey: r.WGKey, wgAddr: r.WGAddr,
			}))
		}
	}
	for si, s := range slots {
		if s.Inbound < 0 || s.Inbound >= len(inbounds) {
			return nil, fmt.Errorf("slot %d: no inbound %d", si, s.Inbound)
		}
		in, _ := inbounds[s.Inbound].(map[string]any)
		if in == nil || in["protocol"] != s.Kind || liveUserKey[s.Kind] == "" {
			return nil, fmt.Errorf("slot %d: inbound %d is not %s", si, s.Inbound, s.Kind)
		}
		settings, _ := in["settings"].(map[string]any)
		if settings == nil {
			return nil, fmt.Errorf("slot %d: inbound %d has no settings", si, s.Inbound)
		}
		list := lists[si]
		if list == nil {
			list = []any{}
		}
		if s.Kind == "shadowsocks" && len(list) == 0 {
			if len(s.Locked) == 0 {
				return nil, fmt.Errorf("slot %d: a shadowsocks list with nobody in it has no locked entry", si)
			}
			list = []any{s.Locked}
		}
		settings[liveUserKey[s.Kind]] = list
	}
	return json.Marshal(cfg)
}
