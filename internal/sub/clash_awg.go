package sub

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

// AmneziaWG in a Clash profile. mihomo reads the whole AmneziaWG 3.1 parameter set from
// v1.19.30 on — `amnezia-wg-option` with `version: 3`, header protection, content
// padding, random trailers, timer bands — and an older core reads only the 1.5 half, so
// it cannot finish a handshake with a 3.1 server. A Clash profile carries the tunnel only
// for a client whose core is known to be new enough (ClashAWG); the rest keep the config
// file from the subscription page.

// clashCoreUA matches mihomo itself fetching a subscription — "clash.meta/v1.19.32" by
// default, "mihomo/1.19.32" where a client says so.
var clashCoreUA = regexp.MustCompile(`(?i)^(?:clash\.meta|mihomo)/v?(\d+)\.(\d+)\.(\d+)`)

// flClashUA matches FlClash ("FlClash/v0.8.99 clash-verge Platform/android"), whose
// v0.8.99 bundles a mihomo with the 3.1 options.
var flClashUA = regexp.MustCompile(`(?i)^flclash/v?(\d+)\.(\d+)\.(\d+)`)

// ClashAWG reports whether a Clash client's core reads AmneziaWG 3.1.
func ClashAWG(userAgent string) bool {
	if m := clashCoreUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 1, 19, 30)
	}
	if m := flClashUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 8, 99)
	}
	return false
}

func versionAtLeast(v []string, want ...int) bool {
	for i, w := range want {
		n, _ := strconv.Atoi(v[i])
		if n != w {
			return n > w
		}
	}
	return true
}

// clashAWGLine is a user's AmneziaWG tunnel on one server as a mihomo wireguard proxy,
// the same tunnel the config file describes. false for a user with no tunnel identity
// yet or a server whose parameters are unreadable.
func clashAWGLine(name string, u model.User, s *model.Settings) (string, bool) {
	p, err := awg.FromModel(s.AWGParams)
	if err != nil || p.IsZero() {
		return "", false
	}
	addr, ok := awg.ClientAddr(u.AWGSlot)
	if !ok || u.WGPrivateKey == "" {
		return "", false
	}
	var dns []string
	for _, d := range strings.Split(awg.TunnelDNS(s), ",") {
		if d = strings.TrimSpace(d); d != "" {
			dns = append(dns, strconv.Quote(d))
		}
	}
	return fmt.Sprintf(
		"  - {name: %q, type: wireguard, server: %q, port: %d, ip: %q, private-key: %q, public-key: %q, allowed-ips: [\"0.0.0.0/0\", \"::/0\"], udp: true, mtu: %d, persistent-keepalive: %d, remote-dns-resolve: true, dns: [%s], amnezia-wg-option: {%s}}",
		name, s.Host, s.AWGPort, addr.String(), u.WGPrivateKey, s.AWGPublicKey,
		awg.DefaultMTU, awg.Keepalive, strings.Join(dns, ", "), clashAWGOption(p)), true
}

// clashAWGOption is the amnezia-wg-option mapping: version 3 when anything of 3.1 is
// set (mihomo otherwise runs its 1.5 engine, which is what a 1.5 server needs).
func clashAWGOption(p awg.Params) string {
	var f []string
	add := func(k string, v any) { f = append(f, fmt.Sprintf("%s: %v", k, v)) }
	str := func(k, v string) {
		if v != "" {
			f = append(f, fmt.Sprintf("%s: %q", k, v))
		}
	}
	if p.NeedsAgent31() {
		add("version", 3)
	}
	add("jc", p.Jc)
	add("jmin", p.Jmin)
	add("jmax", p.Jmax)
	add("s1", p.S1)
	add("s2", p.S2)
	if p.S3 > 0 {
		add("s3", p.S3)
	}
	if p.S4 > 0 {
		add("s4", p.S4)
	}
	str("h1", p.H1.String())
	str("h2", p.H2.String())
	str("h3", p.H3.String())
	str("h4", p.H4.String())
	str("i1", p.I1)
	str("i2", p.I2)
	str("header-protection-key", p.HeaderKey)
	str("content-padding-addition", p.Padding.String())
	str("rekey-after-time", p.RekeyAfter.String())
	str("rekey-timeout", p.RekeyTimeout.String())
	str("reject-after-time", p.RejectAfter.String())
	str("keepalive-timeout", p.Keepalive.String())
	str("max-handshake-attempts", p.Handshakes.String())
	if p.Trailers {
		add("random-trailers", true)
	}
	return strings.Join(f, ", ")
}
