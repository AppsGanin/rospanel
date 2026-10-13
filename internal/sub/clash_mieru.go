package sub

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// clashCaps are the lanes a Clash client's core is known to read beyond the ones every
// mihomo has: a profile carries a lane only for a core that will not reject it.
type clashCaps struct {
	awg   bool // AmneziaWG 3.1 (ClashAWG)
	mieru bool // mieru (clashMieru)
}

func clashCapsFor(userAgent string) clashCaps {
	return clashCaps{awg: ClashAWG(userAgent), mieru: clashMieru(userAgent)}
}

// clashMiUA matches ClashMi ("ClashMeta/1.19.32; mihomo/1.19.32") by its core token:
// "ClashMeta/<x>" alone is no core version — SlothClash falls back to
// "ClashMeta/2.10.1.Meta-Alpha", a CMFA version.
var clashMiUA = regexp.MustCompile(`(?i)^clashmeta/[^;]*;\s*mihomo/v?(\d+)\.(\d+)\.(\d+)`)

// cmfaUA matches ClashMetaForAndroid ("ClashMetaForAndroid/2.11.25").
var cmfaUA = regexp.MustCompile(`(?i)^clashmetaforandroid/v?(\d+)\.(\d+)\.(\d+)`)

// vergeUA matches Clash Verge Rev ("clash-verge/v2.4.5"), which names its own version:
// each release bundles the mihomo stable and alpha of its build day, and offers no older
// core, so the GUI's version bounds the core's.
var vergeUA = regexp.MustCompile(`(?i)^clash-verge/v(\d+)\.(\d+)\.(\d+)`)

// koalaUA matches Koala Clash ("koala-clash/v0.2.10", "koala-clash/1.5.0", and in
// 1.0.2–1.0.4 its core's version): every release bundles the mihomo of its build day,
// the first (0.2.0, July 2025) already 1.19.11. Tags below 0.2.0 are clash-verge's.
var koalaUA = regexp.MustCompile(`(?i)^koala-clash/v?(\d+)\.(\d+)\.(\d+)`)

// flClashXUA matches FlClashX ("FlClashX/v0.2.1 Platform/…", "FlClash X/v0.4.1 core/v1.19.28 …"),
// whose embedded core had mieru from its first release.
var flClashXUA = regexp.MustCompile(`(?i)^flclash ?x/`)

// partyUA matches Mihomo Party / Clash Party ("mihomo.party/v1.7.3 (clash.meta)"),
// likewise; before 1.7.3 it sent a bare "clash.meta", which proves nothing.
var partyUA = regexp.MustCompile(`(?i)^mihomo\.party/v(\d+)\.(\d+)\.(\d+)`)

// clashMieru reports whether a Clash client's core has the mieru proxy type — checked
// against each client's sources and release notes: mihomo from v1.19.0 (Sparkle 1.6.11+,
// SlothClash and ClashFest send its version too), ClashMetaForAndroid from 2.11.3,
// FlClash 0.8.99, Clash Verge Rev 2.0.3 (mihomo 1.19.1), Mihomo Party 1.7.3 (1.19.5),
// every Koala Clash and FlClashX. A client whose version proves nothing about its core
// (Hiddify, Clash Nyanpasu — whose core is the user's pick —, "clash.meta/alpha-<sha>",
// a bare "clash.meta") is not risked: a type the core does not know fails the profile.
func clashMieru(userAgent string) bool {
	if m := clashCoreUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 0)
	}
	if m := clashMiUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 0)
	}
	if m := cmfaUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 2, 11, 3)
	}
	if m := flClashUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 8, 99)
	}
	if m := vergeUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 2, 0, 3)
	}
	if m := partyUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 7, 3)
	}
	if m := koalaUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 2, 0)
	}
	if strings.HasPrefix(strings.ToLower(userAgent), "koala-clash/") {
		return true // "koala-clash/alpha-<sha>": a Koala build, so a 1.19+ core
	}
	return flClashXUA.MatchString(userAgent)
}

// clashMieruLine is a user's mieru lane on one server as a mihomo proxy. udp: mihomo
// sends a proxy's UDP outside the tunnel unless it says it carries UDP (see
// clashProxies), and mieru does.
func clashMieruLine(name string, u model.User, s *model.Settings) string {
	return fmt.Sprintf(
		"  - {name: %q, type: mieru, server: %q, port: %d, transport: %s, username: %q, password: %q, multiplexing: MULTIPLEXING_LOW, udp: true}",
		name, s.Host, s.MieruPort, model.MieruTransportOr(s.MieruTransport), model.UserEmail(u.ID), u.UUID)
}
