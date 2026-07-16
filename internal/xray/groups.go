package xray

import (
	"log"

	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
)

// expandGroups returns a copy of rc with every "iplist:<source>/<group>" entry
// replaced by the group's actual rules: its domains in the domain fields, its
// CIDRs in the IP fields.
//
// This runs once, before any rule is compiled, because a ref that survived to
// normDomains would be emitted to Xray verbatim — normDomains passes through
// anything containing ":" on the assumption it is a geosite:/ext: matcher, so a
// leaked "iplist:global/ai" would reach Xray as a literal domain matcher.
//
// A ref naming a group that the databases do not have (an unknown group, or a
// list not downloaded yet) expands to nothing. Dropping it is the safe failure:
// the lane simply does not claim that traffic and it falls through to the next
// one, whereas emitting the ref verbatim would have Xray reject the whole config.
func expandGroups(rc model.RoutingConfig, groups geo.GroupSet) model.RoutingConfig {
	domains := func(entries []string) []string {
		return expandRefs(entries, groups, func(g geo.GroupRules) []string { return g.Domains })
	}
	ips := func(entries []string) []string {
		return expandRefs(entries, groups, func(g geo.GroupRules) []string { return g.IPs })
	}

	rc.BlockDomains, rc.BlockIPs = domains(rc.BlockDomains), ips(rc.BlockIPs)
	rc.WarpDomains, rc.WarpIPs = domains(rc.WarpDomains), ips(rc.WarpIPs)
	rc.OperaDomains, rc.OperaIPs = domains(rc.OperaDomains), ips(rc.OperaIPs)
	rc.DirectDomains, rc.DirectIPs = domains(rc.DirectDomains), ips(rc.DirectIPs)

	// Copy the lanes before rewriting them: rc is passed by value, but its Lanes
	// slice still aliases the caller's backing array.
	if len(rc.Lanes) > 0 {
		lanes := make([]model.EgressLane, len(rc.Lanes))
		copy(lanes, rc.Lanes)
		for i := range lanes {
			lanes[i].Domains, lanes[i].IPs = domains(lanes[i].Domains), ips(lanes[i].IPs)
		}
		rc.Lanes = lanes
	}
	return rc
}

// expandRefs replaces the iplist refs in entries with the rules pick() takes from
// each group, leaving every other entry in place and in order.
func expandRefs(entries []string, groups geo.GroupSet, pick func(geo.GroupRules) []string) []string {
	// Nothing to do for the common case of a list with no refs — avoids
	// reallocating every rule list on every reconcile.
	hasRef := false
	for _, e := range entries {
		if _, ok := geo.ParseRef(e); ok {
			hasRef = true
			break
		}
	}
	if !hasRef {
		return entries
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		key, ok := geo.ParseRef(e)
		if !ok {
			out = append(out, e)
			continue
		}
		g, known := groups[key]
		if !known {
			log.Printf("xray: routing references unknown iplist group %q — rule skipped", key)
			continue
		}
		out = append(out, pick(g)...)
	}
	return out
}
