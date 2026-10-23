package extsub

import (
	"encoding/base64"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// WireGuard servers, as a partner hands them out: a wireguard:// (or wg://) link, or a
// .conf file pasted as it is. A WireGuard config is one device — its private key is one
// peer on the partner's server — so the panel never hands one to users: it is only ever
// dialled by our own server, as an Xray wireguard outbound, for the users relayed through
// it. AmneziaWG configs are not taken: Xray's wireguard outbound cannot speak them.
//
// The link form is the one v2rayN, Hiddify and Throne exchange:
//
//	wireguard://<private key>@host:port?publickey=…&address=10.0.0.2/32,fd00::2/128
//	    &presharedkey=…&mtu=1420&reserved=1,2,3#name

// amneziaParams are the query keys that make a wg:// link an AmneziaWG one.
var amneziaParams = []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"}

// parseWireGuard reads a wireguard:// or wg:// link; an AmneziaWG one reports false.
func parseWireGuard(raw string) (Endpoint, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.User.Username() == "" {
		return Endpoint{}, false
	}
	q := wgQuery(u)
	if !validWGKey(u.User.Username()) || !validWGKey(q.Get("publickey")) || wgAmnezia(q) {
		return Endpoint{}, false
	}
	host, port, ok := hostPort(u.Host)
	if !ok {
		return Endpoint{}, false
	}
	return Endpoint{
		Protocol: "wireguard", Host: host, Port: port,
		Name: wgLabel(u.Fragment, host), Link: raw,
	}, true
}

// wgAmnezia reports whether a link's query carries AmneziaWG obfuscation.
func wgAmnezia(q url.Values) bool {
	if v := strings.ToLower(q.Get("enable_amnezia")); v == "true" || v == "1" {
		return true
	}
	for _, k := range amneziaParams {
		if v := q.Get(k); v != "" && v != "0" {
			return true
		}
	}
	return false
}

// validWGKey reports whether s is a WireGuard key: 32 bytes in standard base64.
func validWGKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	return err == nil && len(b) == 32
}

// IsWireGuardConf reports whether a pasted text is one or more WireGuard .conf files.
func IsWireGuardConf(s string) bool {
	return strings.Contains(strings.ToLower(s), "[interface]")
}

// WireGuardConfLinks turns pasted .conf files (each an [Interface] with its [Peer])
// into wireguard:// links. A config naming AmneziaWG parameters, or missing a key or an
// endpoint, is left out.
func WireGuardConfLinks(s string) []string {
	var out []string
	var cur *wgConf
	var section, pending string // pending: a name written before the config it labels
	flush := func() {
		if cur != nil {
			if l, ok := cur.link(); ok {
				out = append(out, l)
			}
		}
	}
	for _, raw := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			// "# Name = Germany" is how several providers label a config: inside its
			// [Interface], or above it — and after a [Peer], it is the next one's.
			if k, v, ok := strings.Cut(strings.TrimLeft(line, "#; "), "="); ok && strings.EqualFold(strings.TrimSpace(k), "name") {
				if cur == nil || section == "peer" {
					pending = strings.TrimSpace(v)
				} else {
					cur.name = strings.TrimSpace(v)
				}
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			if section == "interface" {
				flush()
				cur = &wgConf{name: pending}
				pending = ""
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		cur.set(section, strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v))
	}
	flush()
	return out
}

// wgConf is one .conf as read so far.
type wgConf struct {
	name, private, address, mtu string
	public, preshared, endpoint string
	amnezia                     bool
	reserved                    string
}

func (c *wgConf) set(section, k, v string) {
	switch section {
	case "interface":
		switch k {
		case "privatekey":
			c.private = v
		case "address":
			c.address = v
		case "mtu":
			c.mtu = v
		default:
			for _, p := range amneziaParams {
				if k == p && v != "" && v != "0" {
					c.amnezia = true
				}
			}
		}
	case "peer":
		switch k {
		case "publickey":
			c.public = v
		case "presharedkey":
			c.preshared = v
		case "endpoint":
			c.endpoint = v
		case "reserved":
			c.reserved = v
		}
	}
}

// link renders the config as a wireguard:// link.
func (c *wgConf) link() (string, bool) {
	if c.amnezia || !validWGKey(c.private) || !validWGKey(c.public) {
		return "", false
	}
	host, port, ok := hostPort(c.endpoint)
	if !ok {
		return "", false
	}
	q := url.Values{}
	q.Set("publickey", c.public)
	if c.preshared != "" {
		q.Set("presharedkey", c.preshared)
	}
	if addrs := wgList(c.address); len(addrs) > 0 {
		q.Set("address", strings.Join(addrs, ","))
	}
	if c.mtu != "" {
		q.Set("mtu", c.mtu)
	}
	if r := wgList(c.reserved); len(r) > 0 {
		q.Set("reserved", strings.Join(r, ","))
	}
	u := url.URL{
		Scheme: "wireguard", User: url.User(c.private),
		Host: net.JoinHostPort(host, strconv.Itoa(port)), RawQuery: q.Encode(), Fragment: c.name,
	}
	return u.String(), true
}

// wgList splits a comma-separated .conf value, trimming and dropping empty entries.
func wgList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wireguardOutbound is the Xray wireguard outbound a link dials: userspace
// (noKernelTun — a kernel TUN leaks an ip rule per restart, see warpOutbound), every
// address routed through the tunnel.
func wireguardOutbound(l *url.URL, tag, host string, port int) (map[string]any, bool) {
	q := wgQuery(l)
	if wgAmnezia(q) || !validWGKey(l.User.Username()) || !validWGKey(q.Get("publickey")) {
		return nil, false
	}
	var addrs []string
	for _, a := range wgList(q.Get("address")) {
		if p, err := netip.ParsePrefix(a); err == nil {
			addrs = append(addrs, p.String())
		} else if ip, err := netip.ParseAddr(a); err == nil {
			addrs = append(addrs, netip.PrefixFrom(ip, ip.BitLen()).String())
		}
	}
	if len(addrs) == 0 {
		return nil, false // nothing to give the tunnel: the partner would drop every packet
	}
	peer := map[string]any{
		"publicKey":  q.Get("publickey"),
		"endpoint":   net.JoinHostPort(host, strconv.Itoa(port)),
		"allowedIPs": []string{"0.0.0.0/0", "::/0"},
	}
	if psk := q.Get("presharedkey"); psk != "" {
		if !validWGKey(psk) {
			return nil, false
		}
		peer["preSharedKey"] = psk
	}
	settings := map[string]any{
		"secretKey":   l.User.Username(),
		"address":     addrs,
		"peers":       []map[string]any{peer},
		"noKernelTun": true,
	}
	if mtu, err := strconv.Atoi(q.Get("mtu")); err == nil && mtu >= 1280 && mtu <= 1500 {
		settings["mtu"] = mtu
	}
	if r := wgList(q.Get("reserved")); len(r) == 3 {
		var res []int
		for _, p := range r {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 255 {
				return nil, false
			}
			res = append(res, n)
		}
		settings["reserved"] = res
	}
	return map[string]any{"tag": tag, "protocol": "wireguard", "settings": settings}, true
}

// wgLabel is a WireGuard server's name: the link's own, else "WireGuard <host>".
func wgLabel(fragment, host string) string {
	if s, err := url.PathUnescape(fragment); err == nil {
		fragment = s
	}
	if fragment = strings.TrimSpace(fragment); fragment != "" {
		return fragment
	}
	return "WireGuard " + host
}

// wgQuery is a link's query with a literal "+" kept: keys are base64, and an app that
// does not escape them writes one, which form decoding would turn into a space.
func wgQuery(u *url.URL) url.Values {
	q, err := url.ParseQuery(strings.ReplaceAll(u.RawQuery, "+", "%2B"))
	if err != nil {
		return u.Query()
	}
	return q
}
