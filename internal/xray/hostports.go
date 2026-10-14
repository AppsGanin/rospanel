package xray

import (
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/hostfw"
)

// listener is the part of an inbound that says where it takes connections from.
type listener struct {
	tag, listen, protocol string
	from, to              int
	network               string // the protocol's own setting (shadowsocks, dokodemo-door)
	socksUDP              bool
	stream                string // streamSettings.network
}

// PublicListeners is every port an inbound of the config takes from the outside, by
// transport — what the host firewall must let in. Loopback and unix-socket listeners
// are left out: nothing reaches them from the network; so are the inbounds tagged
// skip (the built-in Hysteria2 listener stays up with no users while the lane is off).
func PublicListeners(cfg *Config, skip ...string) []hostfw.Port {
	var ls []listener
	for _, in := range cfg.Inbounds {
		l := listener{tag: in.Tag, listen: in.Listen, protocol: in.Protocol, from: in.Port, to: in.Port}
		switch s := in.Settings.(type) {
		case ShadowsocksInboundSettings:
			l.network = s.Network
		case SocksInboundSettings:
			l.socksUDP = s.UDP
		}
		if in.StreamSettings != nil {
			l.stream = in.StreamSettings.Network
		}
		ls = append(ls, l)
	}
	return publicPorts(ls, skip)
}

// PublicListenersRaw is PublicListeners for a config as JSON, the form a node holds.
func PublicListenersRaw(data []byte, skip ...string) []hostfw.Port {
	cfg, err := decodeRaw(data)
	if err != nil {
		return nil
	}
	var ls []listener
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		in, tag := asInbound(raw)
		if in == nil {
			continue
		}
		from, to, ok := portRange(in["port"])
		if !ok {
			continue
		}
		l := listener{tag: tag, from: from, to: to}
		l.listen, _ = in["listen"].(string)
		l.protocol, _ = in["protocol"].(string)
		if s, _ := in["settings"].(map[string]any); s != nil {
			l.network, _ = s["network"].(string)
			l.socksUDP, _ = s["udp"].(bool)
		}
		if ss, _ := in["streamSettings"].(map[string]any); ss != nil {
			l.stream, _ = ss["network"].(string)
		}
		ls = append(ls, l)
	}
	return publicPorts(ls, skip)
}

func publicPorts(ls []listener, skip []string) []hostfw.Port {
	var out []hostfw.Port
	for _, l := range ls {
		if l.from <= 0 || !publicListen(l.listen) || slices.Contains(skip, l.tag) {
			continue
		}
		for _, proto := range l.transports() {
			out = append(out, hostfw.Port{Proto: proto, From: l.from, To: l.to})
		}
	}
	return out
}

// publicListen reports whether a listen address is reachable from the network: unset,
// a wildcard or any non-loopback address — not "@abstract" or a socket path.
func publicListen(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "@") || strings.HasPrefix(s, "/") {
		return false
	}
	ip := net.ParseIP(s)
	return ip == nil || !ip.IsLoopback()
}

// portRange reads an inbound's port: a number, "443" or "1000-2000".
func portRange(v any) (int, int, bool) {
	if p, ok := inboundPort(map[string]any{"port": v}); ok {
		return p, p, p > 0
	}
	s, _ := v.(string)
	a, b, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	from, err1 := strconv.Atoi(strings.TrimSpace(a))
	to, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || from < 1 || to < from {
		return 0, 0, false
	}
	return from, to, true
}

// transports is "tcp", "udp" or both, as the inbound listens.
func (l listener) transports() []string {
	switch l.protocol {
	case "hysteria", ProtoWireGuard:
		return []string{"udp"}
	case "shadowsocks", "dokodemo-door", "tunnel":
		return networkList(l.network) // Xray's default for these is TCP alone
	case "socks":
		if l.socksUDP {
			return []string{"tcp", "udp"}
		}
	}
	if l.stream == "kcp" || l.stream == "mkcp" {
		return []string{"udp"}
	}
	return []string{"tcp"}
}

func networkList(n string) []string {
	var out []string
	for _, f := range strings.Split(n, ",") {
		if f = strings.TrimSpace(f); f == "tcp" || f == "udp" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return []string{"tcp"}
	}
	return out
}
