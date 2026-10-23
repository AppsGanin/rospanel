package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// External subscriptions: servers that belong to somebody else, read from their
// subscription and handed on to this panel's users as extra entries — a partner's
// fleet, a second provider used as a spare, an old panel during a migration. The
// panel owns none of them: no credential of ours is on them, no traffic of theirs
// is counted here. What it owns is the list — which of them a user gets, decided by
// the same access groups that gate its own lanes.
//
// A subscription can also be relayed: its servers are then handed out as entries of one
// of our own VLESS lanes on a server the operator picks, and that server carries the
// traffic on to them. The user's connection is ours again — counted, capped and cut
// off like any other — and the foreign credential never leaves the panel. Which
// external server a connection is for rides in two bytes of the user's UUID (see
// ExtRoute).
//
// The same sources (a URL, a happ:// link, a pasted list) are also accepted as the
// upstreams of an egress lane, where the servers carry traffic OUT of this server
// rather than being handed to users; that path is internal/proxypool and needs no
// row here.

// ExtSyncInterval is how often an enabled subscription is re-read.
const ExtSyncInterval = time.Hour

// ExtSubscription is one source and what the last reading of it found.
type ExtSubscription struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Source is an http(s) URL, fetched on every sync, or the payload itself
	// (a happ://crypt… link, a base64 blob, a list of links) decoded in place.
	Source string `json:"source"`
	// Identity is what this panel presents when it fetches that source. Every field
	// is an override of a default, so an untouched subscription behaves exactly as it
	// did before the fields existed.
	Identity    ExtIdentity `json:"identity"`
	Enabled     bool        `json:"enabled"`
	LastFetchAt int64       `json:"last_fetch_at"`
	LastOKAt    int64       `json:"last_ok_at"`
	LastError   string      `json:"last_error,omitempty"`
	ServerCount int         `json:"server_count"`
	CreatedAt   int64       `json:"created_at"`
	// Relays are the lanes of our servers the servers are relayed through (migration
	// 0135): each is one more entry per server in a user's subscription. None hands
	// them out as they are.
	Relays []ExtRelayVia `json:"relays"`
	// Routing is how relayed traffic leaves the relay server (migration 0135); unused
	// while the subscription is handed out as it is.
	Routing ExtRouting `json:"routing"`
}

// ExtRouting is how a relayed subscription's traffic leaves the relay server: Rules
// send what they match elsewhere, the rest goes to Rest, and the partner is dialled
// through Dial. The zero value is all of it to the partner, dialled directly.
//
// A target is ExtToPartner, "direct", "block", "warp", "opera" or the id of one of
// the relay server's egress lanes (a cascade lane included). One that cannot be built
// there (WARP with no account, a lane with no upstream) behaves as it does in the
// server's own routing: direct, or dropped under its StrictEgress.
type ExtRouting struct {
	Rules []ExtRule `json:"rules"`
	Rest  string    `json:"rest,omitempty"` // "" = ExtToPartner
	// Dial is what the relay server reaches the partner through: "" directly, "warp",
	// "opera" (TCP only) or a lane id.
	Dial string `json:"dial,omitempty"`
}

// ExtRule sends the traffic matching Domains or IPs (the forms the server's routing
// takes: domain, geosite:, geoip:, CIDR, iplist:) to Target.
type ExtRule struct {
	Domains []string `json:"domains"`
	IPs     []string `json:"ips"`
	Target  string   `json:"target"`
}

// ExtToPartner is the target that is the external server itself.
const ExtToPartner = "partner"

// Bounds on what an operator writes into a subscription's routing.
const (
	ExtRulesMax       = 32
	ExtRuleEntriesMax = 500
)

// RestTarget is where traffic no rule matched goes.
func (r ExtRouting) RestTarget() string {
	if r.Rest == "" {
		return ExtToPartner
	}
	return r.Rest
}

// IsZero reports whether the routing is the default one.
func (r ExtRouting) IsZero() bool {
	return len(r.Rules) == 0 && r.RestTarget() == ExtToPartner && r.Dial == ""
}

// Targets lists every target the routing names, Dial included.
func (r ExtRouting) Targets() []string {
	out := []string{r.RestTarget()}
	for _, x := range r.Rules {
		out = append(out, x.Target)
	}
	if r.Dial != "" {
		out = append(out, r.Dial)
	}
	return out
}

// ExtRelayVia is one lane of one of our servers a subscription is relayed through.
type ExtRelayVia struct {
	ServerID int64  `json:"server_id"`
	Lane     string `json:"lane"` // LaneVLESS or LaneReality
}

// RelayLanesOn lists the lanes relays puts on server serverID.
func RelayLanesOn(relays []ExtRelayVia, serverID int64) []string {
	var out []string
	for _, v := range relays {
		if v.ServerID == serverID {
			out = append(out, v.Lane)
		}
	}
	return out
}

// RelayLanes are the lanes a subscription can be relayed through: the built-in VLESS
// ones, where the route bytes of the UUID reach routing (see ExtRoute).
var RelayLanes = []string{LaneVLESS, LaneReality}

// ExtIdentity is the device this panel claims to be when it reads somebody else's
// subscription. Panels increasingly refuse a caller that does not identify a device —
// ours does — so the fetch has to answer that question, and the answer has to be
// stable across syncs or every refresh burns another device slot upstream.
//
// Every field is OPTIONAL and every empty one falls back to a default the panel
// derives (see extsub.Headers). The fields exist for the case the defaults are not
// what the other side expects: an id the operator was given, an app name a panel
// whitelists, a client string that decides which format it serves. All of them, rather
// than a chosen few, because a partially editable identity is an arbitrary hole to
// explain later.
type ExtIdentity struct {
	HWID        string `json:"hwid"`
	DeviceOS    string `json:"device_os"`
	OSVersion   string `json:"os_version"`
	DeviceModel string `json:"device_model"`
	UserAgent   string `json:"user_agent"`
}

// ExtServer is one server a subscription listed. Key is the server's identity
// across syncs (see extsub.Endpoint.Key), which is what lets the operator's on/off
// choice survive a re-read; Link is handed to users as is and is encrypted at
// rest, since it carries somebody else's credential.
type ExtServer struct {
	ID       int64  `json:"id"`
	SubID    int64  `json:"sub_id"`
	Key      string `json:"-"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Link     string `json:"-"`
	Enabled  bool   `json:"enabled"`
	SeenAt   int64  `json:"seen_at"`
	// SourceName is the name the source gives; Name is what users see — the operator's
	// own (migration 0136) when one is set, else the same.
	SourceName string `json:"source_name"`
	CustomName string `json:"custom_name"`
	// Relayed through, and how, copied from the server's subscription.
	Relays  []ExtRelayVia `json:"-"`
	Routing ExtRouting    `json:"-"`
}

// Relayed reports whether the server is relayed rather than handed out as it is.
func (e ExtServer) Relayed() bool { return len(e.Relays) > 0 }

// ExtWireGuard is the protocol of a partner's WireGuard server. Its config is one
// device: it is never handed to users, and only one of our servers dials it.
const ExtWireGuard = "wireguard"

// RelayVias is the lanes the server is relayed through, as they are put to use: a
// WireGuard server rides only the first of our servers its subscription names — two of
// ours dialling one config would take its peer from each other.
func (e ExtServer) RelayVias() []ExtRelayVia {
	if e.Protocol != ExtWireGuard || len(e.Relays) == 0 {
		return e.Relays
	}
	first := e.Relays[0].ServerID
	for _, v := range e.Relays {
		first = min(first, v.ServerID)
	}
	var out []ExtRelayVia
	for _, v := range e.Relays {
		if v.ServerID == first {
			out = append(out, v)
		}
	}
	return out
}

// ExtServerNameMax bounds an operator's name for an external server.
const ExtServerNameMax = 64

// ExtRoute is the VLESS route an external server is reached by through a relay: the
// 7th and 8th bytes of the user's UUID, which Xray leaves out when it checks the UUID
// and hands to routing as vlessRoute. A random UUID (version 4) always carries
// 0x4000–0x4FFF there, so that range is skipped and no user's own UUID names a
// relayed server. False past the last route.
func ExtRoute(id int64) (uint16, bool) {
	switch {
	case id <= 0:
		return 0, false
	case id < 0x4000:
		return uint16(id), true
	case id+0x1000 <= 0xFFFF:
		return uint16(id + 0x1000), true
	}
	return 0, false
}

// UUIDRoute is the route a UUID carries as it is: its 7th and 8th bytes. False when
// uuid is not in the canonical 8-4-4-4-12 hex form.
func UUIDRoute(uuid string) (uint16, bool) {
	if !canonicalUUID(uuid) {
		return 0, false
	}
	v, err := strconv.ParseUint(uuid[14:18], 16, 16)
	return uint16(v), err == nil
}

// RouteUUID is uuid with route in its 7th and 8th bytes: the UUID a user's client
// sends to reach a relayed server. False when uuid is not canonical.
func RouteUUID(uuid string, route uint16) (string, bool) {
	if !canonicalUUID(uuid) {
		return "", false
	}
	return strings.ToLower(uuid[:14]) + fmt.Sprintf("%04x", route) + strings.ToLower(uuid[18:]), true
}

func canonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// ExtToken is a group-grant token for an external server.
func ExtToken(serverID int64) string { return fmt.Sprintf("ext:%d", serverID) }

// ParseExtToken returns the external server id a token refers to, or ok=false.
func ParseExtToken(token string) (int64, bool) {
	rest, ok := strings.CutPrefix(token, "ext:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil
}

// AllowsExt reports whether the user may be handed an external server.
func (a Access) AllowsExt(serverID int64) bool {
	return a.All || a.Tokens[ExtToken(serverID)]
}

// MaxExtSubscriptionName bounds the label; it is a heading in the UI, nothing more.
const MaxExtSubscriptionName = 64

// CleanExtSubscriptionName trims and bounds a subscription's name; empty is
// allowed (the UI falls back to the source's host).
func CleanExtSubscriptionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > MaxExtSubscriptionName {
		return "", fieldErr("err.extNameLong", "название слишком длинное")
	}
	if strings.ContainsAny(name, "\n\r\t") {
		return "", fieldErr("err.extNameCharset", "название не может содержать переводы строк")
	}
	return name, nil
}
