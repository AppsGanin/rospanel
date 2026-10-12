package sub

import (
	"encoding/base64"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// AmneziaWG in the link list. INCY's mobile apps read a server's whole .conf from an
// amneziawg:// line — url-safe base64 of the file, the name after # — with the
// AmneziaWG 3.1 engine behind it (header protection, content padding, random trailers,
// timer bands: docs.incy.cc, subscription format). Its desktop build ignores the scheme.
// No other link-list client reads 3.1, so no other gets the line.

// incyUA matches INCY's subscription requests: "INCY/<version>/<platform>".
var incyUA = regexp.MustCompile(`(?i)^incy/v?(\d+)\.(\d+)(?:\.(\d+))?/(\S+)`)

// AWGLinkFor reports whether a link-list client reads AmneziaWG 3.1 from an
// amneziawg:// line: INCY from the releases after its 3.1 engine was documented
// (2026-08-24) — iOS 2.6.2, Android 3.7.0.
func AWGLinkFor(userAgent string) bool {
	m := incyUA.FindStringSubmatch(userAgent)
	if m == nil {
		return false
	}
	v := []string{m[1], m[2], m[3]}
	if v[2] == "" {
		v[2] = "0"
	}
	if strings.Contains(strings.ToLower(m[4]), "ios") {
		return versionAtLeast(v, 2, 6, 2)
	}
	return versionAtLeast(v, 3, 7, 0)
}

// AWGIncyLink is a user's tunnel on one server as an amneziawg:// line: the same .conf
// the subscription page hands out, url-safe base64'd. "" for a user with no tunnel
// identity yet or a server whose parameters are unreadable.
func AWGIncyLink(u model.User, s *model.Settings) string {
	conf := AWGClientConf(u, s)
	if conf == "" {
		return ""
	}
	return "amneziawg://" + base64.RawURLEncoding.EncodeToString([]byte(conf)) + "#" +
		url.PathEscape(link.LabelFor(model.ProtoAWG, u, s))
}

// AWGClientConf is a user's AmneziaWG config file for one server, or "" when there is
// none to give: the user has no tunnel identity yet, or the parameters are unreadable.
func AWGClientConf(u model.User, s *model.Settings) string {
	// The host is written into the file as is; a line break in it would add lines of
	// its own (an awg-quick PreUp runs as root).
	if s.Host != "" && !model.ValidServerHost(s.Host) {
		return ""
	}
	p, err := awg.FromModel(s.AWGParams)
	if err != nil {
		return ""
	}
	addr, ok := awg.ClientAddr(u.AWGSlot)
	if !ok || u.WGPrivateKey == "" {
		return ""
	}
	return awg.ClientConfig{
		PrivateKey:      u.WGPrivateKey,
		Address:         addr,
		DNS:             awg.TunnelDNS(s),
		Params:          p,
		ServerPublicKey: s.AWGPublicKey,
		Endpoint:        net.JoinHostPort(s.Host, strconv.Itoa(s.AWGPort)),
	}.Render()
}
