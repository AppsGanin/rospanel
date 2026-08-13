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

// DeviceCap is the number of devices this user may bind: their own limit when they
// have one, otherwise the panel-wide fallback. 0 means unlimited.
//
// It reuses users.device_limit deliberately — an operator sets "three devices" once
// and both counters honour it, rather than the account carrying two limits that can
// disagree.
func (s *Settings) DeviceCap(u User) int {
	if u.DeviceLimit > 0 {
		return u.DeviceLimit
	}
	return s.HWIDFallbackLimit
}
