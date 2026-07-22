// Package abuse matches connection destinations against IP-reputation blocklists,
// so the operator learns that an account is reaching known-bad infrastructure
// without the panel having to keep everyone's browsing history.
//
// Only matches are ever recorded. That is the whole point: matches are rare, so they
// cost almost nothing to store and are worth keeping for weeks, whereas the traffic
// they are drawn from is high-cardinality and is never persisted at all.
//
// # Why IP only
//
// This package once matched domains too, against threat/piracy/gambling feeds. That
// was dropped because a domain can only be matched when the destination reaches the
// panel AS a domain, and on real traffic it usually does not: clients resolve DNS off
// the tunnel and encrypt the TLS SNI (ECH), so the access log records a bare IP.
// Measured on a live server it was 23 IPs to every 4 domains, and those 4 were
// connectivity checks. Three domain feeds cost ~12 MB of memory and a daily download
// to match almost nothing, so the destination is now matched as an address or not at
// all.
package abuse

import (
	"net/netip"
	"sync"
)

// Category is the kind of trouble a destination represents. These are the buckets
// the UI groups by and the operator reasons about.
//
// The retired domain categories are still listed: rows written before the switch to
// IP-only carry them, and Title must keep rendering those rows correctly.
type Category string

const (
	// CatCustom is the operator's own list. Checked first so a local entry wins.
	CatCustom Category = "custom"
	// CatBadIP is the IP-reputation feed (attack/malware infrastructure) — the only
	// downloaded list, and the one that sees the bare-IP traffic everything else misses.
	CatBadIP Category = "badip"

	// Retired domain categories, kept so historical rows still render.
	CatMalware  Category = "malware"
	CatPiracy   Category = "piracy"
	CatGambling Category = "gambling"
)

// Title is the Russian label shown in the panel.
func (c Category) Title() string {
	switch c {
	case CatCustom:
		return "Свой список"
	case CatBadIP:
		return "Вредоносный IP"
	case CatMalware:
		return "Вредоносное ПО"
	case CatPiracy:
		return "Пиратство"
	case CatGambling:
		return "Азартные игры"
	}
	return string(c)
}

// matchOrder is the order categories are tested in: the operator's own list first,
// so a local entry decides, then the downloaded feed.
var matchOrder = []Category{CatCustom, CatBadIP}

// Matcher answers "is this destination on a list, and which one". Safe for
// concurrent use; reads take a read lock so the access-log path is not serialised
// against itself, and a feed reload swaps whole sets under the write lock.
//
// A nil *Matcher is valid and matches nothing, so a panel that has never downloaded
// a feed behaves as if the feature is off rather than crashing the access-log reader.
type Matcher struct {
	mu      sync.RWMutex
	ipLists map[Category]*ipList
}

// New returns an empty matcher that matches nothing until a list is set.
func New() *Matcher { return &Matcher{ipLists: map[Category]*ipList{}} }

// Clear removes a category's set, so a disabled category matches nothing until it is
// loaded again.
func (m *Matcher) Clear(cat Category) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.ipLists, cat)
	m.mu.Unlock()
}

// Counts reports how many entries each loaded category holds, for the settings UI.
func (m *Matcher) Counts() map[Category]int {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[Category]int, len(m.ipLists))
	for cat, l := range m.ipLists {
		out[cat] = l.entries
	}
	return out
}

// Match reports the category a destination falls under, if any.
//
// Anything that is not an IP address — a hostname the panel did happen to see — is
// simply not matched: there are no domain lists to test it against.
func (m *Matcher) Match(dest string) (Category, bool) {
	if m == nil || dest == "" {
		return "", false
	}
	addr, err := netip.ParseAddr(dest)
	if err != nil {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.matchIP(addr)
}
