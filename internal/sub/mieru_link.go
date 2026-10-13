package sub

import (
	"regexp"
	"strings"
)

// throneUA matches Throne ("Throne/1.2.0"), whose subscription parser reads mierus://
// from 1.2.0.
var throneUA = regexp.MustCompile(`(?i)^throne/v?(\d+)\.(\d+)\.(\d+)`)

// MieruLinkFor reports whether a subscription client imports the mierus:// link —
// checked against each client's own parser: Exclave (built in from 0.17.0), Karing
// (from 1.2.23), Throne (from 1.2.0); Shadowrocket, whose parser is closed, imported it
// and connected on a device (it keeps port and profile, takes TCP when protocol is
// absent). The mihomo family reads it only as a proxy-provider, not as a profile, and
// gets the Clash proxy instead (clashMieru); Hiddify and husi are served a Clash or
// sing-box profile by their User-Agent; v2rayN/NG, NekoBox, Happ, INCY, Streisand,
// v2RayTun and the sing-box apps have no mieru. An empty User-Agent is a hand-made
// request, which gets every form.
func MieruLinkFor(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	if m := throneUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 2, 0)
	}
	return ua == "" || strings.HasPrefix(ua, "exclave/") || strings.Contains(ua, "karing") ||
		strings.Contains(ua, "shadowrocket")
}
