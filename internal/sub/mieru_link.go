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
	for _, c := range []struct {
		re   *regexp.Regexp
		want []int
	}{
		{throneUA, []int{1, 2, 0}},
		{exclaveUA, []int{0, 17, 0}},
		{karingUA, []int{1, 2, 23}},
	} {
		if m := c.re.FindStringSubmatch(userAgent); m != nil {
			return versionAtLeast(m[1:], c.want...)
		}
	}
	return ua == "" || strings.Contains(ua, "shadowrocket")
}

var (
	v2raynUA  = regexp.MustCompile(`(?i)^v2rayn/v?(\d+)\.(\d+)\.(\d+)`)
	exclaveUA = regexp.MustCompile(`(?i)^exclave/v?(\d+)\.(\d+)\.(\d+)`)
	// karingUA matches Karing ("Karing/1.2.23.2601 android/…"): major.minor.patch.build.
	karingUA = regexp.MustCompile(`(?i)^karing/v?(\d+)\.(\d+)\.(\d+)`)
)

// AnyTLSLinkFor reports whether a subscription client imports the anytls:// link —
// checked against each client's own parser: v2rayN from 7.14.0, Throne from 1.0.1,
// Exclave from 0.13.11, Karing, Shadowrocket (from 2.2.64; its User-Agent carries a build
// number, not the version, so any), Loon (from 3.3.9, its base64 subscription reading
// anytls://); NekoBox when it asks for links (?format=). The mihomo
// family and the sing-box apps get the Clash proxy or the sing-box outbound instead.
// An empty User-Agent is a hand-made request, which gets every form.
func AnyTLSLinkFor(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	for _, c := range []struct {
		re   *regexp.Regexp
		want []int
	}{
		{v2raynUA, []int{7, 14, 0}},
		{throneUA, []int{1, 0, 1}},
		{exclaveUA, []int{0, 13, 11}},
	} {
		if m := c.re.FindStringSubmatch(userAgent); m != nil {
			return versionAtLeast(m[1:], c.want...)
		}
	}
	return ua == "" || strings.Contains(ua, "karing") || strings.Contains(ua, "shadowrocket") ||
		strings.HasPrefix(ua, "loon") || strings.HasPrefix(ua, "nekobox/")
}

// VLESSEncLinkFor reports whether a link-list client passes a vless:// link's encryption
// (VLESS Encryption, in the form of Xray 25.8.31) on to a core that speaks it — checked
// against each client's parser: v2rayN from 7.14.1, v2rayNG from 1.10.21, Exclave from
// 0.16.2, Throne from 1.1.0, husi from 0.14.2, Shadowrocket (2.2.78; its User-Agent has
// no version). The sing-box apps and NekoBox have no VLESS Encryption; Happ, Streisand,
// INCY and v2RayTun document none in links. An empty User-Agent is a hand-made request.
func VLESSEncLinkFor(userAgent string) bool {
	for _, c := range []struct {
		re   *regexp.Regexp
		want []int
	}{
		{v2raynUA, []int{7, 14, 1}},
		{v2rayngUA, []int{1, 10, 21}},
		{exclaveUA, []int{0, 16, 2}},
		{throneUA, []int{1, 1, 0}},
		{husiUA, []int{0, 14, 2}},
	} {
		if m := c.re.FindStringSubmatch(userAgent); m != nil {
			return versionAtLeast(m[1:], c.want...)
		}
	}
	ua := strings.ToLower(userAgent)
	return ua == "" || strings.Contains(ua, "shadowrocket")
}

// v2rayngUA matches v2rayNG ("v2rayNG/1.10.21").
var v2rayngUA = regexp.MustCompile(`(?i)^v2rayng/v?(\d+)\.(\d+)\.(\d+)`)
