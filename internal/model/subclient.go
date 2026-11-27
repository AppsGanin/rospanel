package model

import (
	"strings"
	"unicode/utf8"
)

// SubClientInfo is what a subscription fetch says about the app that made it.
type SubClientInfo struct {
	App      string // display name ("Happ", "v2rayNG", …), never empty
	Version  string
	Platform string // "iOS", "Android", "Windows", "macOS", "Linux", "tvOS", or ""
}

// Platform names as the panel shows them.
const (
	PlatformIOS     = "iOS"
	PlatformAndroid = "Android"
	PlatformWindows = "Windows"
	PlatformMacOS   = "macOS"
	PlatformLinux   = "Linux"
	PlatformTVOS    = "tvOS"
)

// AppUnknown names a fetch that sent no User-Agent at all.
const AppUnknown = "unknown"

// knownClient maps a lower-case User-Agent fragment to the app's name, and to the
// platform the app only exists on ("" when it runs on several). Order matters: the
// first match wins, so an app whose User-Agent carries another app's name as a
// compatibility hint (FlClash says "clash-verge", Hiddify says "ClashMeta") comes
// before the name it borrows.
type knownClient struct {
	key, name, platform string
}

var knownClients = []knownClient{
	{"happ", "Happ", ""},
	{"v2raytun", "v2RayTun", ""},
	{"v2rayng", "v2rayNG", PlatformAndroid},
	{"v2rayn", "v2rayN", ""},
	{"hiddify", "Hiddify", ""},
	{"flclashx", "FlClashX", ""},
	{"flclash", "FlClash", ""},
	{"clash-verge", "Clash Verge", ""},
	{"clashmetaforandroid", "Clash Meta for Android", PlatformAndroid},
	{"clash.meta", "Clash Meta", ""},
	{"clashx", "ClashX", PlatformMacOS},
	{"mihomo", "mihomo", ""},
	{"stash", "Stash", ""},
	{"shadowrocket", "Shadowrocket", PlatformIOS},
	{"streisand", "Streisand", PlatformIOS},
	{"foxray", "FoxRay", ""},
	{"v2box", "V2Box", ""},
	{"karing", "Karing", ""},
	{"nekobox", "NekoBox", ""},
	{"nekoray", "NekoRay", ""},
	{"throne", "Throne", ""},
	{"exclave", "Exclave", PlatformAndroid},
	{"husi", "husi", PlatformAndroid},
	{"incy", "INCY", ""},
	{"nyamebox", "NyameBox", ""},
	{"wings", "WINGS", ""},
	{"amnezia", "AmneziaVPN", ""},
	{"quantumult", "Quantumult X", PlatformIOS},
	{"loon", "Loon", PlatformIOS},
	{"surge", "Surge", ""},
	{"sfa/", "sing-box", PlatformAndroid},
	{"sfi/", "sing-box", PlatformIOS},
	{"sfm/", "sing-box", PlatformMacOS},
	{"sft/", "sing-box", PlatformTVOS},
	{"sing-box", "sing-box", ""},
	{"clash", "Clash", ""},
	{"xray", "Xray", ""},
	{"curl/", "curl", ""},
	{"wget/", "Wget", ""},
	{"mozilla/", "Browser", ""},
}

// ParseSubClient names the app behind a subscription fetch from its User-Agent and
// the x-device-os header (which Happ and v2RayTun send and which beats any guess
// from the User-Agent).
func ParseSubClient(userAgent, deviceOS string) SubClientInfo {
	ua := strings.TrimSpace(userAgent)
	low := strings.ToLower(ua)
	var info SubClientInfo
	for _, k := range knownClients {
		// The match is found in ua itself, not in its lower-cased copy: lower-casing
		// can change a string's byte length (U+023A is two bytes, its lower case
		// U+2C65 three), and a byte index from one string slices the other nowhere.
		if i := indexFold(ua, k.key); i >= 0 {
			info.App, info.Platform = k.name, k.platform
			if k.name != "Browser" {
				info.Version = versionAfter(ua[i:])
			}
			break
		}
	}
	if info.App == "" {
		info.App, info.Version = productToken(ua)
	}
	if p := platformOf(strings.ToLower(deviceOS)); p != "" {
		info.Platform = p
	} else if info.Platform == "" {
		info.Platform = platformOf(low)
	}
	return info
}

// indexFold is strings.Index for an ASCII needle, ignoring case, returning a byte
// index into s itself: the needle is ASCII, so a match can only start on an ASCII
// byte and a slice beginning mid-rune can never equal one.
func indexFold(s, needle string) int {
	if needle == "" {
		return 0
	}
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i] < utf8.RuneSelf && strings.EqualFold(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// versionAfter reads the version that follows the product name at the start of s:
// "Happ/3.7.0/ios" → "3.7.0", "sing-box 1.11.4" → "1.11.4", "clash-verge/v2.0.3" →
// "2.0.3". Up to two path segments are skipped to find one that starts with a digit
// ("NekoBox/Android/1.3.4").
func versionAfter(s string) string {
	end := strings.IndexAny(s, " ;()")
	head := s
	if end >= 0 {
		head = s[:end]
	}
	parts := strings.Split(head, "/")
	for _, p := range parts[1:min(len(parts), 4)] {
		if v := cleanVersion(p); v != "" {
			return v
		}
	}
	// "sing-box 1.11.4": the version is the next word.
	if end >= 0 && s[end] == ' ' {
		next := strings.Fields(s[end:])
		if len(next) > 0 {
			return cleanVersion(next[0])
		}
	}
	return ""
}

// cleanVersion keeps a version-looking token: digits and dots (with a leading "v"
// dropped), cut at the first other character, at most 24 characters.
func cleanVersion(p string) string {
	p = strings.TrimPrefix(strings.TrimPrefix(p, "v"), "V")
	if p == "" || p[0] < '0' || p[0] > '9' {
		return ""
	}
	n := 0
	for n < len(p) && n < 24 && (p[n] == '.' || (p[n] >= '0' && p[n] <= '9')) {
		n++
	}
	return strings.TrimRight(p[:n], ".")
}

// productToken names an app the list does not know by its User-Agent's first product
// token ("MyClient/1.2 …" → "MyClient", "1.2"): letters, digits and - _ . only, at most
// 32 characters, so whatever the client wrote stays short and plain.
func productToken(ua string) (name, version string) {
	if ua == "" {
		return AppUnknown, ""
	}
	head := ua
	if i := strings.IndexAny(head, " ;("); i >= 0 {
		head = head[:i]
	}
	name, rest, _ := strings.Cut(head, "/")
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return -1
	}, name)
	if len(clean) > 32 {
		clean = clean[:32]
	}
	if clean == "" {
		return AppUnknown, ""
	}
	return clean, cleanVersion(strings.SplitN(rest, "/", 2)[0])
}

// platformOf finds a platform name in a lower-case string.
func platformOf(s string) string {
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "android"):
		return PlatformAndroid
	case strings.Contains(s, "tvos") || strings.Contains(s, "appletv"):
		return PlatformTVOS
	case strings.Contains(s, "ios") || strings.Contains(s, "iphone") || strings.Contains(s, "ipad") ||
		strings.Contains(s, "cfnetwork"):
		return PlatformIOS
	case strings.Contains(s, "windows") || strings.Contains(s, "win64") || strings.Contains(s, "win32"):
		return PlatformWindows
	case strings.Contains(s, "macos") || strings.Contains(s, "mac os") || strings.Contains(s, "macintosh") ||
		strings.Contains(s, "darwin"):
		return PlatformMacOS
	case strings.Contains(s, "linux"):
		return PlatformLinux
	}
	return ""
}

// SubClientKey is the row a fetch belongs to: the install id when the client sends
// one, its app and platform when it does not.
func SubClientKey(hwid string, info SubClientInfo) string {
	if hwid != "" {
		return "h:" + hwid
	}
	return "a:" + info.App + "|" + info.Platform
}
