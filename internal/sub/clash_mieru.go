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
	awg    bool // AmneziaWG 3.1 (ClashAWG)
	mieru  bool // mieru (clashMieru)
	anytls bool // AnyTLS (clashAnyTLS)
	// vlessEnc: VLESS Encryption in Xray 25.8.31's form (clashVLESSEnc); without it a
	// custom inbound that has it is left out.
	vlessEnc bool
	// pin: the client checks a pinned certificate's fingerprint (clashPinCert); without
	// it a custom inbound that pins one (its SNI anyone's name) is left out.
	pin bool
}

func clashCapsFor(userAgent string) clashCaps {
	return clashCaps{awg: ClashAWG(userAgent), mieru: clashMieru(userAgent), anytls: clashAnyTLS(userAgent),
		vlessEnc: clashVLESSEnc(userAgent), pin: clashPinCert(userAgent)}
}

// clashPinCert reports whether a Clash-format client honours a proxy's fingerprint:
// every mihomo core does. Hiddify and NekoBox read the profile into sing-box, which
// checks the name instead — a borrowed SNI then never verifies.
func clashPinCert(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	return !strings.Contains(ua, "hiddify") && !strings.HasPrefix(ua, "nekobox")
}

// clashVLESSEnc reports whether a Clash client's core speaks VLESS Encryption as Xray
// 25.8.31 writes it: mihomo v1.19.14 (v1.19.13 had the form before it) — by the core's own
// version where the client names it, or by a client bundling a core past the AmneziaWG
// 3.1 gate (1.19.30). Client versions in between bundle unknown cores, so they are not
// risked: a core without it connects without the handshake, to a server that refuses.
func clashVLESSEnc(userAgent string) bool {
	if m := clashCoreUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 14)
	}
	if m := clashMiUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 14)
	}
	if m := flClashXCoreUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 14)
	}
	return ClashAWG(userAgent)
}

// clashAnyTLS reports whether a Clash client's core has the anytls proxy type (mihomo
// v1.19.3) — checked against each client's core at its release tags: by the core's own
// version where the client names it, or by the first client version bundling a newer
// core — ClashMetaForAndroid 2.11.7, FlClash 0.8.77, Clash Verge Rev 2.2.2, Mihomo Party
// 1.7.2, NekoBox 1.3.8 (its Clash import reads the type), Stash per platform (stashAt);
// every Koala Clash, FlClashX and ClashFest.
func clashAnyTLS(userAgent string) bool {
	if m := clashCoreUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 3)
	}
	if m := clashMiUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 3)
	}
	if m := cmfaUA.FindStringSubmatch(userAgent); m != nil {
		return clashFestUA(m) || versionAtLeast(m[1:], 2, 11, 7)
	}
	if m := flClashUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 8, 77)
	}
	if m := vergeUA.FindStringSubmatch(userAgent); m != nil {
		return koalaAsVerge(m) || versionAtLeast(m[1:], 2, 2, 2)
	}
	if m := partyUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 7, 2)
	}
	if m := koalaUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 2, 0)
	}
	if m := nekoBoxUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 3, 8)
	}
	if m := stashUA.FindStringSubmatch(userAgent); m != nil {
		return stashAt(m, []int{3, 3}, []int{4, 1})
	}
	return strings.HasPrefix(strings.ToLower(userAgent), "koala-clash/") || flClashXUA.MatchString(userAgent) ||
		ClashAWG(userAgent)
}

// nekoBoxUA matches NekoBox for Android ("NekoBox/Android/1.3.8 (Prefer ClashMeta Format)"),
// which asks for the Clash profile.
var nekoBoxUA = regexp.MustCompile(`(?i)^nekobox/android/v?(\d+)\.(\d+)\.(\d+)`)

// stashUA matches Stash ("Stash/3.3.1 …"). Its versions name a platform: 3.0–3.2 may be
// macOS, 3.3–3.x is iOS/tvOS, 4.x is macOS.
var stashUA = regexp.MustCompile(`(?i)^stash/v?(\d+)\.(\d+)(?:\.(\d+))?`)

// stashAt reports whether a Stash version has a feature that came in iOS at mobile and
// in macOS at mac (major, minor each).
func stashAt(m []string, mobile, mac []int) bool {
	v := []string{m[1], m[2], m[3]}
	if v[2] == "" {
		v[2] = "0"
	}
	if versionAtLeast(v, 4, 0, 0) {
		return versionAtLeast(v, mac[0], mac[1], 0)
	}
	return versionAtLeast(v, mobile[0], mobile[1], 0)
}

// clashFestUA reports whether a ClashMetaForAndroid User-Agent is ClashFest's before 1.2.0,
// which sent its own version (0.x, 1.x) under that name, with a recent mihomo alpha.
func clashFestUA(m []string) bool { return m[1] == "0" || m[1] == "1" }

// clashAnyTLSLine is a user's AnyTLS lane on one server as a mihomo proxy. udp: mihomo
// sends a proxy's UDP outside the tunnel unless it says it carries UDP (it does, as
// sing-box's UDP over TCP).
func clashAnyTLSLine(name string, u model.User, s *model.Settings) string {
	return fmt.Sprintf(
		"  - {name: %q, type: anytls, server: %q, port: %d, password: %q, sni: %q, client-fingerprint: %s, udp: true}",
		name, s.Host, s.VLESSPort, u.UUID, s.SNI, s.VLESSFP())
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
// FlClash 0.8.72, Clash Verge Rev 2.0.3 (mihomo 1.19.1), Mihomo Party 1.7.3 (1.19.5),
// Stash iOS 3.6 / macOS 4.3, every Koala Clash, FlClashX and ClashFest. NekoBox skips the
// type silently, so it is not offered there. A client whose version proves nothing about its core
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
		return clashFestUA(m) || versionAtLeast(m[1:], 2, 11, 3)
	}
	if m := flClashUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 8, 72)
	}
	if m := vergeUA.FindStringSubmatch(userAgent); m != nil {
		return koalaAsVerge(m) || versionAtLeast(m[1:], 2, 0, 3)
	}
	if m := stashUA.FindStringSubmatch(userAgent); m != nil {
		return stashAt(m, []int{3, 6}, []int{4, 3})
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

// koalaAsVerge reports whether a "clash-verge/v0.x" User-Agent is Koala Clash 0.2.x,
// which sent that name (with a mihomo 1.19.11 or newer), rather than the original
// Clash Verge 0.0.x and its old cores.
func koalaAsVerge(m []string) bool { return m[1] == "0" && versionAtLeast(m[1:], 0, 2, 0) }
