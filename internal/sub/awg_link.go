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

// AWGThroneLink is a user's tunnel on one server in the link form Throne imports (its
// wireguard.cpp ParseFromLink, from 1.3.0, which reads the whole 3.1 set):
// wg://host:port?private_key=…&local_address=…&public_key=…&enable_amnezia=true&jc=…#label.
// The keys are base64 and written as they are — Qt reads "+", "/" and a value's "="
// literally. I1/I2 are left out: the imitation packets are the client's own, the server
// does not check them, and their "<…>" chains are not something to trust a URL decoder
// with. "" when the user has no tunnel identity yet or the parameters are unreadable.
func AWGThroneLink(u model.User, s *model.Settings) string {
	if s.Host != "" && !model.ValidServerHost(s.Host) {
		return ""
	}
	p, err := awg.FromModel(s.AWGParams)
	if err != nil || p.IsZero() {
		return ""
	}
	addr, ok := awg.ClientAddr(u.AWGSlot)
	if !ok || u.WGPrivateKey == "" {
		return ""
	}
	q := []string{
		"private_key=" + u.WGPrivateKey,
		"local_address=" + addr.String() + "/32",
		"public_key=" + s.AWGPublicKey,
		"persistent_keepalive_interval=" + strconv.Itoa(awg.Keepalive),
		"enable_amnezia=true",
		"jc=" + strconv.Itoa(p.Jc), "jmin=" + strconv.Itoa(p.Jmin), "jmax=" + strconv.Itoa(p.Jmax),
		"s1=" + strconv.Itoa(p.S1), "s2=" + strconv.Itoa(p.S2),
	}
	if p.S3 > 0 {
		q = append(q, "s3="+strconv.Itoa(p.S3))
	}
	if p.S4 > 0 {
		q = append(q, "s4="+strconv.Itoa(p.S4))
	}
	q = append(q, "h1="+p.H1.String(), "h2="+p.H2.String(), "h3="+p.H3.String(), "h4="+p.H4.String())
	if p.HeaderKey != "" {
		q = append(q, "header_protection_key="+p.HeaderKey)
	}
	if !p.Padding.IsZero() {
		q = append(q, "content_padding_addition="+p.Padding.String())
	}
	if p.Trailers {
		q = append(q, "random_trailers=true")
	}
	for _, t := range []struct {
		key string
		val awg.Range
	}{
		{"rekey_after_time", p.RekeyAfter},
		{"rekey_timeout", p.RekeyTimeout},
		{"reject_after_time", p.RejectAfter},
		{"keepalive_timeout", p.Keepalive},
		{"max_handshake_attempts", p.Handshakes},
	} {
		if !t.val.IsZero() {
			q = append(q, t.key+"="+t.val.String())
		}
	}
	return "wg://" + net.JoinHostPort(s.Host, strconv.Itoa(s.AWGPort)) + "?" + strings.Join(q, "&") + "#" +
		url.PathEscape(link.LabelFor(model.ProtoAWG, u, s))
}

// awgLinkFor is the AmneziaWG link form a subscription client imports, or nil: INCY
// (AWGLinkFor) its amneziawg://, Throne from 1.3.0 (random trailers among the rest) its
// wg://. The others with an AmneziaWG of their own speak 1.5 or 2.0 — Hiddify, Karing,
// NyameBox, v2RayTun — and cannot finish a handshake with a 3.1 server.
func awgLinkFor(userAgent string) func(model.User, *model.Settings) string {
	if AWGLinkFor(userAgent) {
		return AWGIncyLink
	}
	if m := throneUA.FindStringSubmatch(userAgent); m != nil && versionAtLeast(m[1:], 1, 3, 0) {
		return AWGThroneLink
	}
	return nil
}
