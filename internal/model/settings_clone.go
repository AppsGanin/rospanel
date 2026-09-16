package model

import "slices"

// Clone returns a copy of the settings that shares no memory with them: every slice,
// and every slice inside a slice, is a new one. The store keeps one decoded copy of the
// settings and hands out clones of it, and a caller that appends to a list or edits a
// lane in place must not be editing what the next caller is given.
//
// A reference field added to Settings, or to a type inside it, has to be copied here:
// TestSettingsCloneSharesNothing fills every field it can reach and fails on any that
// still points at the original.
func (s *Settings) Clone() *Settings {
	c := *s
	c.ProxyAccounts = slices.Clone(s.ProxyAccounts)
	c.SubRules = slices.Clone(s.SubRules)
	c.ConnPolicy.Countries = slices.Clone(s.ConnPolicy.Countries)
	c.Routing = s.Routing.clone()
	return &c
}

func (rc RoutingConfig) clone() RoutingConfig {
	rc.BlockIPs = slices.Clone(rc.BlockIPs)
	rc.BlockDomains = slices.Clone(rc.BlockDomains)
	rc.WarpDomains = slices.Clone(rc.WarpDomains)
	rc.WarpIPs = slices.Clone(rc.WarpIPs)
	rc.OperaDomains = slices.Clone(rc.OperaDomains)
	rc.OperaIPs = slices.Clone(rc.OperaIPs)
	rc.DirectDomains = slices.Clone(rc.DirectDomains)
	rc.DirectIPs = slices.Clone(rc.DirectIPs)
	rc.RoutingOrder = slices.Clone(rc.RoutingOrder)
	rc.ProxyURLs = slices.Clone(rc.ProxyURLs)
	rc.ProxyManual = slices.Clone(rc.ProxyManual)
	rc.ProxyDomains = slices.Clone(rc.ProxyDomains)
	rc.ProxyIPs = slices.Clone(rc.ProxyIPs)
	if rc.Lanes != nil {
		lanes := make([]EgressLane, len(rc.Lanes))
		for i, l := range rc.Lanes {
			l.URLs = slices.Clone(l.URLs)
			l.Manual = slices.Clone(l.Manual)
			l.Domains = slices.Clone(l.Domains)
			l.IPs = slices.Clone(l.IPs)
			lanes[i] = l
		}
		rc.Lanes = lanes
	}
	return rc
}
