package geo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RefPrefix marks a routing entry that names an iplist group rather than a
// literal matcher: "iplist:<source>/<group>", e.g. "iplist:global/youtube" or
// "iplist:russia/vk". The same ref means different things by the field it sits
// in — in a domain list it expands to the group's domains, in an IP list to its
// CIDRs — mirroring how geosite:/geoip: already divide.
const RefPrefix = "iplist:"

// GroupRules is one iplist group's merged rule data: every domain and every
// CIDR belonging to any site in the group.
type GroupRules struct {
	Domains []string `json:"domains"`
	IPs     []string `json:"ips"` // cidr4 + cidr6
}

// GroupSet maps "<source>/<group>" to its rules.
type GroupSet map[string]GroupRules

// ipListSite is the slice of an iplist JSON entry we care about. The service
// also reports dns/timeout/ip4/ip6/external; ip4/ip6 are the raw per-host
// addresses that cidr4/cidr6 already aggregate (youtube: 257 IPs → 14 CIDRs),
// so we take the aggregated form and ignore the rest.
type ipListSite struct {
	Group   string   `json:"group"`
	Domains []string `json:"domains"`
	CIDR4   []string `json:"cidr4"`
	CIDR6   []string `json:"cidr6"`
}

// ParseRef splits an "iplist:<source>/<group>" entry into its lookup key
// ("<source>/<group>"). ok is false for anything that is not such a ref, so
// callers can pass unrecognised entries through untouched.
func ParseRef(entry string) (key string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(entry), RefPrefix)
	if !found {
		return "", false
	}
	src, group, split := strings.Cut(rest, "/")
	if !split || src == "" || group == "" {
		return "", false
	}
	if _, known := ipListFiles[src]; !known {
		return "", false
	}
	return src + "/" + group, true
}

// Groups parses the iplist databases in dir into their groups. A source whose
// file is missing or unreadable is skipped rather than failing the whole set, so
// one dead service does not take the other's groups down with it; an error is
// returned only when no source yielded anything.
func Groups(dir string) (GroupSet, error) {
	out := GroupSet{}
	for src, file := range ipListFiles {
		sites, err := parseIPList(filepath.Join(dir, file))
		if err != nil {
			continue
		}
		merge(out, src, sites)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no iplist databases found in %s", dir)
	}
	return out, nil
}

// GroupNames returns the "<source>/<group>" keys, sorted — the options the
// routing UI offers.
func (g GroupSet) GroupNames() []string {
	out := make([]string, 0, len(g))
	for k := range g {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseIPList(path string) (map[string]ipListSite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sites map[string]ipListSite
	if err := json.Unmarshal(data, &sites); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sites, nil
}

// merge folds every site of one source into its group's rules. Entries are
// deduplicated and sorted so an unchanged upstream always compiles to a
// byte-identical Xray config and cannot churn a reload.
func merge(out GroupSet, src string, sites map[string]ipListSite) {
	domains := map[string]map[string]struct{}{}
	ips := map[string]map[string]struct{}{}
	for _, s := range sites {
		group := strings.ToLower(strings.TrimSpace(s.Group))
		if group == "" {
			continue
		}
		key := src + "/" + group
		if domains[key] == nil {
			domains[key], ips[key] = map[string]struct{}{}, map[string]struct{}{}
		}
		addAll(domains[key], s.Domains)
		addAll(ips[key], s.CIDR4)
		addAll(ips[key], s.CIDR6)
	}
	for key := range domains {
		out[key] = GroupRules{Domains: sorted(domains[key]), IPs: sorted(ips[key])}
	}
}

func addAll(set map[string]struct{}, entries []string) {
	for _, e := range entries {
		if e = strings.TrimSpace(e); e != "" {
			set[e] = struct{}{}
		}
	}
}

func sorted(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
