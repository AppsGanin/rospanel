package model

import (
	"strings"
	"unicode"
)

// Device is one client install bound to a user, identified by the id it sends in
// the x-hwid subscription header. See migration 0041 for why the panel counts these
// alongside the IP-based device count rather than instead of it.
type Device struct {
	HWID      string `json:"hwid"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Model     string `json:"model"`
	App       string `json:"app"` // User-Agent the client identified itself with
	IP        string `json:"ip"`  // source address of its last subscription fetch
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

// Subscription headers carrying device identity. The convention comes from Happ and
// is followed by v2RayTun and others; only x-hwid is required, the rest are there to
// make the row readable to a human deciding which device to unbind.
const (
	HeaderHWID        = "x-hwid"
	HeaderDeviceOS    = "x-device-os"
	HeaderOSVersion   = "x-ver-os"
	HeaderDeviceModel = "x-device-model"
)

// Field length caps. These strings are attacker-controlled — anyone holding a
// subscription token picks them — and they end up in the database and in the panel
// UI, so they are bounded on the way in rather than trusted. The HWID cap is
// generous next to the UUIDs and install ids clients actually send.
const (
	maxHWIDLen        = 128
	maxDeviceFieldLen = 64
	maxDeviceAppLen   = 128
)

// CleanHWID normalises a client-supplied hardware id: control characters removed,
// surrounding space trimmed, length capped. Returns "" for anything left empty,
// which callers treat as "this client sent no HWID".
func CleanHWID(s string) string { return cleanDeviceField(s, maxHWIDLen) }

// CleanDeviceField normalises a descriptive device header (OS, version, model).
func CleanDeviceField(s string) string { return cleanDeviceField(s, maxDeviceFieldLen) }

// CleanDeviceApp normalises the User-Agent a client fetched with.
func CleanDeviceApp(s string) string { return cleanDeviceField(s, maxDeviceAppLen) }

func cleanDeviceField(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		// Control characters (and the replacement rune a bad UTF-8 byte decodes to)
		// are dropped outright: a header is a single line of text, and a newline in
		// one is either a broken client or someone probing what the panel echoes back.
		if r == unicode.ReplacementChar || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > max {
		// Cut on a rune boundary so the stored value stays valid UTF-8.
		for max > 0 && !utf8Start(s[max]) {
			max--
		}
		s = s[:max]
	}
	return s
}

// utf8Start reports whether b begins a UTF-8 sequence (i.e. is not a continuation
// byte).
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// Device-count modes. The value decides whether source addresses enforce DeviceLimit.
const (
	// DeviceCountAuto counts distinct source addresses seen inside DeviceOnlineWindow.
	// The default, and the only thing that limits how many places one credential is
	// used at once.
	DeviceCountAuto = "auto"
	// DeviceCountHWID stops counting addresses entirely, leaving the HWID roster as the
	// only limit. This gives up concurrency enforcement — see CountsIPAsDevice — and is
	// an operator's explicit choice.
	DeviceCountHWID = "hwid"
	// DeviceCountBoth is accepted for the rows and API clients that already store it.
	// It once meant "count addresses without forgiving a handover"; the forgiving half
	// was removed (see CountsIPAsDevice), so it now behaves exactly as "auto" and is no
	// longer offered in the UI.
	DeviceCountBoth = "both"
)

// DeviceCountModeOr is the stored mode, or "auto" for a row that predates the column.
func (s *Settings) DeviceCountModeOr() string {
	switch s.DeviceCountMode {
	case DeviceCountHWID, DeviceCountBoth:
		return s.DeviceCountMode
	default:
		return DeviceCountAuto
	}
}

// CountsIPAsDevice reports whether distinct source addresses enforce DeviceLimit.
//
// Only "hwid" switches them off, and that is deliberately NOT the default. HWID caps who
// may FETCH a subscription; addresses cap how many places a credential is USED at once.
// They are not interchangeable: a share link copied by hand to another device never
// touches the subscription endpoint, so the HWID roster cannot see it, while the address
// count could. Turning the address count off by default would have quietly removed the
// only thing standing between one paid account and any number of simultaneous users.
//
// The residual false positive from issue #66 is left standing and documented rather than
// papered over: a phone changing network briefly shows two addresses, and the abandoned
// one keeps a fresh last_seen until it leaves DeviceOnlineWindow, so a user on the exact
// number of devices they are allowed can be cut for up to two minutes.
//
// Forgiving the stale address instead was tried (a "handover grace" measured against the
// user's newest sighting) and removed. It could not work: cutting the user is what stops
// their sightings, so the reference it measured against froze at the moment of the cut
// and the abandoned address stayed inside the grace for the full window — the reported
// outage was unchanged. Meanwhile an address stopped counting after thirty quiet seconds,
// and access-log lines are written per newly accepted connection, so five devices taking
// forty-second turns counted as one. An operator who wants the false positive gone should
// choose "hwid" and accept what it gives up, which is a decision only they can make.
func (s *Settings) CountsIPAsDevice() bool {
	return s.DeviceCountModeOr() != DeviceCountHWID
}

// MaxDevicesPerUser is the DEFAULT cap, applied when no operator limit is set. The roster is written from an unauthenticated subscription
// fetch carrying a client-supplied hardware id, so "no limit" cannot mean "unbounded" —
// one token would otherwise insert a row per request forever.
const MaxDevicesPerUser = 50

// DeviceCap is the number of devices this user may bind: their own limit when they have
// one, the panel-wide fallback when that is set, and MaxDevicesPerUser when neither is.
//
// It reuses users.device_limit deliberately — an operator sets "three devices" once and
// both counters honour it, rather than the account carrying two limits that can disagree.
//
// It never returns 0. The enforced number and the displayed number have to be the SAME
// number: while this returned 0 for "unlimited", the store still refused the 51st device
// and the refused user was told "51 / 0", with the operator's roster and the API both
// reporting the limit as unlimited.
func (s *Settings) DeviceCap(u User) int {
	cap := s.HWIDFallbackLimit
	if u.DeviceLimit > 0 {
		cap = u.DeviceLimit
	}
	// Only "no number given" is replaced by the ceiling. An operator who deliberately
	// allows 100 devices gets 100 — capping their explicit choice would be a silent
	// policy change, and it is the UNSET case that was unbounded, not the set one.
	if cap <= 0 {
		return MaxDevicesPerUser
	}
	return cap
}
