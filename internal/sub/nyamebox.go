package sub

import (
	"net/url"
	"regexp"
	"strings"
)

// nyameBoxUA matches NyameBox ("nekobox/5.11.28.3 (Prefer ClashMeta Format)"), the
// desktop fork of nekoray. NekoBox for Android ("NekoBox/Android/1.4.2") does not match.
var nyameBoxUA = regexp.MustCompile(`(?i)^nekobox/v?(\d+)\.(\d+)\.(\d+)`)

// NyameBoxLinkList reports whether NyameBox is served the link list rather than the Clash
// profile its User-Agent asks for: its Clash import has no TrustTunnel or mieru, while its
// link parser reads tt:// (its own URL form, link.TrustTunnelURL), naive+https:// with uot
// and anytls://. 5.11.28 is the first release whose parser maps tt:// to TrustTunnel.
func NyameBoxLinkList(userAgent string) bool {
	m := nyameBoxUA.FindStringSubmatch(userAgent)
	return m != nil && versionAtLeast(m[1:], 5, 11, 28)
}

// nyameBoxLinks rewrites a link list for NyameBox's parser, which before 5.11.28.7 reads
// no certificate pin (pcs) and no port range in the address: the server's pinned
// self-signed certificate becomes insecure=1, what the Clash profile gave it as
// skip-cert-verify (a pin under a borrowed SNI is left out, dropPinnedFor), and
// a hysteria2:// line carries its hop range as mport (fm, which it ignores, is dropped).
func nyameBoxLinks(links []string) []string {
	out := make([]string, len(links))
	for i, l := range links {
		out[i] = nyameBoxLink(l)
	}
	return out
}

func nyameBoxLink(l string) string {
	hy2 := strings.HasPrefix(l, "hysteria2://")
	if !hy2 && !strings.HasPrefix(l, "vless://") && !strings.HasPrefix(l, "trojan://") {
		return l
	}
	u, err := url.Parse(l)
	if err != nil {
		return l
	}
	q := u.Query()
	changed := false
	if hy2 {
		if fm := q.Get("fm"); fm != "" {
			q.Del("fm")
			if ports := fmHopPorts(fm); ports != "" {
				q.Set("mport", ports)
			}
			changed = true
		}
	}
	if q.Get("pcs") != "" || q.Get("pinSHA256") != "" {
		q.Set("insecure", "1")
		changed = true
	}
	if !changed {
		return l
	}
	u.RawQuery = q.Encode()
	return u.String()
}
