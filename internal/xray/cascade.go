package xray

import (
	"maps"
	"net/netip"
	"slices"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Cascades: a server (the entry) sends its users' traffic out through another of our
// servers (the exit). On the entry it is an egress lane like any proxy pool, its
// upstreams links to the exits (Options.Cascade). On the exit the entry is one more
// client of a built-in lane, under a service credential (Options.Bridges) that only
// the entry's own addresses may use.

// Bridge is one cascade entry as its exit sees it.
type Bridge struct {
	Entry    int64  // the entry server
	Email    string // model.BridgeEmail
	UUID     string // VLESS lanes
	Password string // Hysteria2
	Lane     string // model.BridgeReality / BridgeVision / BridgeHysteria
	// Sources are the entry's addresses; the credential from any other is dropped.
	// None known drops it from everywhere — a bridge that cannot be pinned to its
	// entry is a credential anyone holding it could use.
	Sources []string
}

// addBridgeClients puts each bridge on the lane it reaches this server over — only a
// lane that is on: a lane switched off takes nobody, a bridge included.
func addBridgeClients(set *model.Settings, bridges []Bridge, vision []VLESSClient, hy2 []HysteriaClient, reality []VLESSClient) ([]VLESSClient, []HysteriaClient, []VLESSClient) {
	for _, b := range bridges {
		switch b.Lane {
		case model.BridgeVision:
			if set.VLESSEnabled {
				vision = append(vision, VLESSClient{ID: b.UUID, Flow: VisionFlow, Email: b.Email})
			}
		case model.BridgeHysteria:
			if set.HysteriaEnabled {
				hy2 = append(hy2, HysteriaClient{Auth: b.Password, Email: b.Email})
			}
		default:
			if set.RealityEnabled {
				reality = append(reality, VLESSClient{ID: b.UUID, Email: b.Email})
			}
		}
	}
	return vision, hy2, reality
}

// withCascade makes the cascade lanes' upstreams our servers and nothing else: a pool
// resolved for a lane before it became a cascade must not carry its traffic, and with
// no exit left the lane has no upstream at all (unusable, so dropped).
func withCascade(proxies, cascade map[string][]model.ProxyEndpoint, lanes []model.EgressLane) map[string][]model.ProxyEndpoint {
	if len(cascade) == 0 && !slices.ContainsFunc(lanes, model.EgressLane.IsCascade) {
		return proxies
	}
	out := maps.Clone(proxies)
	if out == nil {
		out = map[string][]model.ProxyEndpoint{}
	}
	for _, l := range lanes {
		if l.IsCascade() {
			delete(out, l.ID)
		}
	}
	maps.Copy(out, cascade)
	return out
}

// bridgeSourceRules drops each bridge's credential from every address but its entry's.
// Xray has no "not these addresses" for a plain CIDR, so the rule names the rest of the
// address space instead: a few dozen prefixes per entry address.
func bridgeSourceRules(bridges []Bridge) []RouteRule {
	var out []RouteRule
	seen := map[string]bool{}
	for _, b := range bridges {
		// One rule per entry, whatever lanes it comes over: the user is the same, and
		// two rules under one tag break the live rule replacement (hysteria_live.go).
		if seen[b.Email] {
			continue
		}
		seen[b.Email] = true
		var addrs []netip.Addr
		for _, s := range b.Sources {
			if a, err := netip.ParseAddr(s); err == nil {
				addrs = append(addrs, a.Unmap())
			}
		}
		r := RouteRule{Type: "field", User: []string{b.Email}, OutboundTag: "block", RuleTag: "bridge-src-" + b.Email}
		if len(addrs) > 0 {
			r.Source = complementCIDRs(addrs)
		}
		out = append(out, r)
	}
	return out
}

// complementCIDRs is every address but addrs, as prefixes, IPv4 and IPv6.
func complementCIDRs(addrs []netip.Addr) []string {
	var out []string
	for _, root := range []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")} {
		out = appendComplement(out, root, addrs)
	}
	return out
}

func appendComplement(out []string, p netip.Prefix, addrs []netip.Addr) []string {
	inside := slices.ContainsFunc(addrs, p.Contains)
	if !inside {
		return append(out, p.String())
	}
	if p.Bits() == p.Addr().BitLen() {
		return out // the address itself
	}
	lo := netip.PrefixFrom(p.Addr(), p.Bits()+1)
	hi := netip.PrefixFrom(upperHalf(p), p.Bits()+1)
	return appendComplement(appendComplement(out, lo, addrs), hi, addrs)
}

// upperHalf is the first address of p's upper half.
func upperHalf(p netip.Prefix) netip.Addr {
	b := p.Addr().AsSlice()
	bit := p.Bits() // the bit that tells the halves apart
	b[bit/8] |= 0x80 >> (bit % 8)
	a, _ := netip.AddrFromSlice(b)
	return a
}
