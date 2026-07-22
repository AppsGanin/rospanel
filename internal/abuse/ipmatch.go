package abuse

import (
	"bufio"
	"io"
	"net/netip"
	"sort"
	"strings"
)

// This is the matcher's storage. Destinations are matched as addresses only (see the
// package doc for why domains were dropped).
//
// Ranges rather than a hash set: feeds list CIDRs, and one /16 is 65k addresses no
// set should hold. Sorted, merged, non-overlapping ranges answer membership with one
// binary search and hold a few thousand entries for a feed of millions of addresses.

// ipRange is an inclusive [lo, hi] address range.
type ipRange struct {
	lo, hi netip.Addr
}

// ipList is one category's IP set.
type ipList struct {
	ranges []ipRange
	// entries is how many lines the feed had, for the settings UI (not len(ranges),
	// which is post-merge).
	entries int
}

// has reports whether a falls in any range. Ranges are sorted by lo and merged, so
// the only candidate is the last range whose lo <= a.
func (l *ipList) has(a netip.Addr) bool {
	i := sort.Search(len(l.ranges), func(i int) bool { return l.ranges[i].lo.Compare(a) > 0 })
	if i == 0 {
		return false
	}
	return a.Compare(l.ranges[i-1].hi) <= 0
}

// SetIP replaces one category's IP set from CIDRs and bare IPs.
func (m *Matcher) SetIP(cat Category, entries []string) {
	ranges := make([]ipRange, 0, len(entries))
	for _, e := range entries {
		if r, ok := parseIPEntry(e); ok {
			ranges = append(ranges, r)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].lo.Compare(ranges[j].lo) < 0 })
	l := &ipList{ranges: mergeRanges(ranges), entries: len(entries)}
	m.mu.Lock()
	if m.ipLists == nil {
		m.ipLists = map[Category]*ipList{}
	}
	m.ipLists[cat] = l
	m.mu.Unlock()
}

// matchIP checks an address against the loaded IP lists, in category order. Caller
// holds the read lock.
func (m *Matcher) matchIP(a netip.Addr) (Category, bool) {
	a = a.Unmap()
	for _, cat := range matchOrder {
		if l := m.ipLists[cat]; l != nil && l.has(a) {
			return cat, true
		}
	}
	return "", false
}

// parseIPEntry turns "1.2.3.4" or "1.2.3.0/24" into a range. v4 addresses are
// unmapped so they sort and compare as v4, never as 4-in-6.
func parseIPEntry(s string) (ipRange, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return ipRange{}, false
		}
		p = p.Masked()
		return ipRange{lo: p.Addr().Unmap(), hi: lastAddr(p).Unmap()}, true
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return ipRange{}, false
	}
	a = a.Unmap()
	return ipRange{lo: a, hi: a}, true
}

// lastAddr returns the highest address in a masked prefix by setting every host bit.
func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Addr()
	b := a.As16()
	bits := p.Bits()
	if a.Is4() {
		bits += 96 // a v4 prefix's bits sit in the low 32 of the 16-byte form
	}
	for i := bits; i < 128; i++ {
		b[i/8] |= 1 << (7 - uint(i%8))
	}
	hi := netip.AddrFrom16(b)
	if a.Is4() {
		return hi.Unmap()
	}
	return hi
}

// mergeRanges collapses overlapping ranges in an lo-sorted slice, so `has` can
// assume at most one range covers any address.
func mergeRanges(rs []ipRange) []ipRange {
	if len(rs) < 2 {
		return rs
	}
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.lo.Compare(last.hi) <= 0 { // overlap
			if r.hi.Compare(last.hi) > 0 {
				last.hi = r.hi
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// ParseCustom reads the operator's own list: one IP or CIDR per line, '#' comments.
// Lines that are not addresses (a hostname, a typo) are skipped rather than
// rejected — the field is free text and a bad line must not void the good ones.
func ParseCustom(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if i := strings.IndexAny(line, " \t;#"); i > 0 {
			line = line[:i]
		}
		if _, ok := parseIPEntry(line); ok {
			out = append(out, line)
		}
	}
	return out
}

// ParseIPList reads an IP blocklist: one CIDR or IP per line, with '#'/';' comments
// and optional trailing comments after the address (as Spamhaus DROP and FireHOL
// netsets carry).
func ParseIPList(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if i := strings.IndexAny(line, " \t;#"); i > 0 {
			line = line[:i]
		}
		if _, ok := parseIPEntry(line); ok {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}
