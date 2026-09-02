package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Built-in lane keys, as used in a group grant token and in the protocol toggles.
// These are the three lanes the panel manages itself, distinct from custom inbounds.
const (
	LaneVLESS    = "vless"
	LaneReality  = "reality"
	LaneHysteria = "hysteria2"
	LaneAWG      = "awg" // AmneziaWG tunnel (internal/awg)
)

// BuiltinLaneKeys lists the built-in lane keys in display order (distinct from
// BuiltinLanes(), which is the egress-lane set — an unrelated concept).
var BuiltinLaneKeys = []string{LaneVLESS, LaneReality, LaneHysteria, LaneAWG}

// Group is a named set of connections a user is allowed to use. Membership is
// many-to-many; a user in no group may use everything (see Access).
type Group struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	// Grants are the tokens this group unlocks (see BuiltinToken / InboundToken). Only
	// populated by reads that ask for it.
	Grants []string `json:"grants,omitempty"`
	// Members is the count of users in the group, for the management list.
	Members int `json:"members"`
	// MemberIDs are the users in the group, for the editor's member picker. Populated
	// by the group reads (list + get); the count above stays for a cheap display.
	MemberIDs []int64 `json:"member_ids"`
}

// GroupRef is the minimal group identity shown on a user (list + detail).
type GroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// BuiltinToken is a group-grant token for a built-in lane on one server (serverID 0 =
// master). lane is one of the Lane* constants.
func BuiltinToken(serverID int64, lane string) string {
	return fmt.Sprintf("builtin:%d:%s", serverID, lane)
}

// InboundToken is a group-grant token for a custom inbound.
func InboundToken(inboundID int64) string {
	return fmt.Sprintf("inbound:%d", inboundID)
}

// ParseInboundToken returns the inbound id a token refers to, or ok=false when it is
// not an inbound token. Used to sweep grants when an inbound is deleted.
func ParseInboundToken(token string) (int64, bool) {
	rest, ok := strings.CutPrefix(token, "inbound:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil
}

// ParseBuiltinToken returns the (serverID, lane) a built-in token refers to, or
// ok=false. Used to sweep grants when a node is deleted.
func ParseBuiltinToken(token string) (serverID int64, lane string, ok bool) {
	rest, has := strings.CutPrefix(token, "builtin:")
	if !has {
		return 0, "", false
	}
	sid, lane, found := strings.Cut(rest, ":")
	if !found {
		return 0, "", false
	}
	id, err := strconv.ParseInt(sid, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return id, lane, true
}

// Access is a resolved answer to "what may this user connect to". It is deliberately
// a value, computed once per user and consulted many times during config generation
// and link building, so the "no groups ⇒ everything" rule lives in exactly one place.
type Access struct {
	// All ⇒ unrestricted (the user is in no group). Tokens is then irrelevant.
	All bool
	// Tokens is the union of the user's groups' grants when All is false.
	Tokens map[string]bool
}

// UnrestrictedAccess is the access of a user in no group — everything is allowed.
func UnrestrictedAccess() Access { return Access{All: true} }

// AllowsBuiltin reports whether the user may use a built-in lane on a server.
func (a Access) AllowsBuiltin(serverID int64, lane string) bool {
	return a.All || a.Tokens[BuiltinToken(serverID, lane)]
}

// AllowsInbound reports whether the user may use a custom inbound.
func (a Access) AllowsInbound(inboundID int64) bool {
	return a.All || a.Tokens[InboundToken(inboundID)]
}

// AccessOf returns a user's access from a userID→Access map, defaulting a MISSING
// entry to unrestricted. Missing means "not computed / feature effectively off", and
// the safe failure there is to grant access rather than silently lock a user out of
// every lane. A genuinely restricted user always has an explicit entry.
func AccessOf(m map[int64]Access, userID int64) Access {
	if m == nil {
		return UnrestrictedAccess()
	}
	if a, ok := m[userID]; ok {
		return a
	}
	return UnrestrictedAccess()
}

// groupNameRe validates a group name: letters/digits/space and a few safe marks, like
// the connection names, so a name embedded in JSON/YAML or shown as a chip is safe.
var groupNameRe = regexp.MustCompile(`^[\p{L}\p{N} _.()\-]+$`)

// ValidateGroupName checks a group name is present, short, and safe.
func ValidateGroupName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fieldErr("err.groupNameRequired", "укажи название группы")
	}
	if len([]rune(name)) > 32 {
		return fieldErr("err.groupNameTooLong", "название группы не длиннее 32 символов")
	}
	if !groupNameRe.MatchString(name) {
		return fieldErr("err.groupNameChars", "недопустимое название группы (буквы, цифры, пробел, . _ - ( ))")
	}
	return nil
}
