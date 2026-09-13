package model

import (
	"net/netip"
	"sort"
	"strings"
)

// Trusted networks are the addresses the panel never drops at the firewall on its
// own: not for failing the SOCKS/HTTP password, not for scanning the panel's path,
// not for connecting from a country the source policy refuses. They exist for the
// office or the monitoring box behind one shared address, where one stale password
// or one misbehaving script would otherwise ban everybody behind it for an hour.
//
// What they do NOT exempt from is anything that is not a ban: the sign-in throttle,
// the device limit, quotas. A trusted network is a place, not a credential.

// MaxTrustedNets bounds the list. An operator names a few offices and servers; a
// list in the hundreds is a mistake, and the check runs on the connection path.
const MaxTrustedNets = 256

// Narrowest-allowed bounds: a network wider than these is not an office, it is a
// good part of the internet, and trusting it would quietly switch the bans off.
const (
	trustedMinBits4 = 8
	trustedMinBits6 = 32
)

// NormalizeTrustedNets validates the operator's entries and returns them in the
// form they are stored and shown: each a prefix with its host bits cleared (a bare
// address becomes /32 or /128), deduplicated, sorted. Blank entries are dropped.
func NormalizeTrustedNets(entries []string) ([]string, error) {
	seen := map[netip.Prefix]struct{}{}
	nets := make([]netip.Prefix, 0, len(entries))
	for _, raw := range entries {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		p, ok := parseTrustedNet(s)
		if !ok {
			return nil, fieldErr("err.trustedNetInvalid", "«{{value}}»: IP-адрес или подсеть",
				map[string]any{"value": s})
		}
		if (p.Addr().Is4() && p.Bits() < trustedMinBits4) || (p.Addr().Is6() && p.Bits() < trustedMinBits6) {
			return nil, fieldErr("err.trustedNetTooWide", "«{{value}}»: слишком широкая подсеть (IPv4 от /8, IPv6 от /32)",
				map[string]any{"value": s})
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		nets = append(nets, p)
	}
	if len(nets) > MaxTrustedNets {
		return nil, fieldErr("err.trustedNetTooMany", "доверенных адресов не больше {{max}}",
			map[string]any{"max": MaxTrustedNets})
	}
	sort.Slice(nets, func(i, j int) bool {
		if c := nets[i].Addr().Compare(nets[j].Addr()); c != 0 {
			return c < 0
		}
		return nets[i].Bits() < nets[j].Bits()
	})
	out := make([]string, len(nets))
	for i, p := range nets {
		out[i] = p.String()
	}
	return out, nil
}

// parseTrustedNet reads one entry as an address or a prefix. An IPv4-mapped IPv6
// address is read as the IPv4 address it carries, which is how the firewall and the
// access log see it.
func parseTrustedNet(s string) (netip.Prefix, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, false
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), true
}

// TrustedNets is the parsed list, ready to answer for one address.
type TrustedNets []netip.Prefix

// ParseTrustedNets reads a stored list. It was normalized on the way in, so an
// entry that does not parse can only be a damaged row; it is skipped rather than
// allowed to break the check.
func ParseTrustedNets(list []string) TrustedNets {
	out := make(TrustedNets, 0, len(list))
	for _, s := range list {
		if p, ok := parseTrustedNet(strings.TrimSpace(s)); ok {
			out = append(out, p)
		}
	}
	return out
}

// Contains reports whether ip falls inside any trusted network. An address that
// does not parse is not trusted.
func (t TrustedNets) Contains(ip string) bool {
	if len(t) == 0 {
		return false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range t {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
