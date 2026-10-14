package model

import "net/netip"

// PrivateEgressCIDRs are destination ranges a tunnelled client is never allowed to
// reach: loopback, RFC1918 private space, link-local (covers the 169.254.169.254
// cloud-metadata endpoint), CGNAT, and their IPv6 equivalents. Explicit CIDRs (not
// geoip:private) so the rule needs no geo database to be present and can never
// silently no-op if geoip.dat is missing at boot.
var PrivateEgressCIDRs = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24", // IETF protocol assignments
	"192.168.0.0/16",
	"198.18.0.0/15", // benchmarking range — routers on it answer as themselves
	"224.0.0.0/4",   // multicast: not a destination a tunnelled client has business with
	"240.0.0.0/4",   // reserved
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	// NB: do NOT add "::ffff:0:0/96" here. Xray parses an IPv4-mapped literal back to
	// a 4-byte address and then rejects any prefix over 32, so the whole routing config
	// fails to build — and since a panel restart stops Xray BEFORE the new config is
	// validated, that is a full outage, not a no-op. Xray already normalises v4-in-v6
	// to IPv4, so the IPv4 rows above cover the mapped form. TestPrivateEgressCIDRsAreValid
	// pins this.
}

var privateEgressPrefixes = func() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(PrivateEgressCIDRs))
	for _, c := range PrivateEgressCIDRs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}()

// IsPrivateEgress reports whether a tunnelled client may not reach addr: it falls in
// PrivateEgressCIDRs. An IPv4-mapped address counts as its IPv4 self.
func IsPrivateEgress(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range privateEgressPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
