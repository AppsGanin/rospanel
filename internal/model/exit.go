package model

import (
	"encoding/json"
	"slices"
)

// A server's own ways out — WARP and Opera — handed to users as connections of their
// own (migration 0137): a VLESS entry on one of the server's lanes, its route in the
// user's UUID like a relayed external server's (see ExtRoute), whose traffic all leaves
// through that way out. Who gets one is the access groups' to say, as for a lane:
// BuiltinToken(server, kind).

// Exit kinds; each is also a pseudo-lane of the access groups.
const (
	ExitWARP  = "warp"
	ExitOpera = "opera"
)

// ExitKinds lists the kinds, in the order the UI shows them.
var ExitKinds = []string{ExitWARP, ExitOpera}

// ExitConfig is one way out handed to users: through which lanes, under which name
// ("" = "<server> · WARP").
type ExitConfig struct {
	Kind  string   `json:"kind"`
	Lanes []string `json:"lanes"`
	Name  string   `json:"name,omitempty"`
}

// ExitNameMax bounds an exit config's name.
const ExitNameMax = 64

// ExitRoute is the VLESS route an exit config is reached by. They sit above every
// route an external server can take (ExtRoute stops short of them), and are the same
// on every server: a server's exits are its own.
func ExitRoute(kind string) (uint16, bool) {
	switch kind {
	case ExitWARP:
		return 0xFFF1, true
	case ExitOpera:
		return 0xFFF2, true
	}
	return 0, false
}

// ExitFor returns the config of kind in list, if it is handed out at all.
func ExitFor(list []ExitConfig, kind string) (ExitConfig, bool) {
	for _, c := range list {
		if c.Kind == kind && len(c.Lanes) > 0 {
			return c, true
		}
	}
	return ExitConfig{}, false
}

// ExitConfigsOf reads a stored list; empty or unreadable is none.
func ExitConfigsOf(raw string) []ExitConfig {
	out := []ExitConfig{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return slices.DeleteFunc(out, func(c ExitConfig) bool { _, ok := ExitRoute(c.Kind); return !ok })
}

// ExitConfigsJSON is the stored form of a list.
func ExitConfigsJSON(list []ExitConfig) string {
	if len(list) == 0 {
		return ""
	}
	b, _ := json.Marshal(list)
	return string(b)
}
