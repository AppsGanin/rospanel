package sub

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSingBoxLegacy is returned for a sing-box template that uses a field current
// sing-box no longer accepts. Match it with errors.As on *SingBoxLegacyError to
// learn which field.
var ErrSingBoxLegacy = errors.New("sing-box template uses a removed field")

// SingBoxLegacyError names the first removed field a template uses, as a JSON path
// the operator can find in their own document ("dns.servers[1].address").
type SingBoxLegacyError struct{ Path string }

func (e *SingBoxLegacyError) Error() string {
	return fmt.Sprintf("%s: removed in sing-box 1.13/1.14, see sing-box.sagernet.org/migration", e.Path)
}

func (e *SingBoxLegacyError) Unwrap() error { return ErrSingBoxLegacy }

// singboxRemovedInbound are inbound fields that became route rule actions (sniff,
// resolve) in 1.11, and TUN address fields merged into address/route_address in 1.10.
// sing-box 1.13 refuses to start an inbound carrying any of them.
var singboxRemovedInbound = []string{
	"sniff", "sniff_override_destination", "sniff_timeout", "domain_strategy",
	"udp_disable_domain_unmapping",
	"inet4_address", "inet6_address", "inet4_route_address", "inet6_route_address",
	"inet4_route_exclude_address", "inet6_route_exclude_address",
}

// singboxRemovedRule are rule items backed by the geo databases, which sing-box
// dropped for rule sets in 1.12 — in route rules and DNS rules alike.
var singboxRemovedRule = []string{"geosite", "geoip", "source_geoip"}

// singboxLegacyPath reports the first field in a sing-box document that the client
// will refuse to load, or "" when there is none.
//
// It is here because a template is the operator's document, written against whatever
// sing-box was current when they wrote it, and a client refuses a whole profile over
// one removed field: a template carrying the pre-1.12 DNS server form stopped working
// for every user on sing-box 1.13 at once, with nothing on the panel side failing.
// Every entry below was confirmed against the real binaries (1.12.25, 1.13.21,
// 1.14.1) to be a load failure, not a warning — which is also why the legacy "block"
// outbound is absent: 1.14 still loads it.
func singboxLegacyPath(doc any) string {
	root, ok := doc.(map[string]any)
	if !ok {
		return ""
	}
	if dns, ok := root["dns"].(map[string]any); ok {
		for i, s := range asList(dns["servers"]) {
			// The typed form (1.12) has "type"; the old one is a single "address" URL.
			if m, ok := s.(map[string]any); ok && m["address"] != nil {
				return fmt.Sprintf("dns.servers[%d].address", i)
			}
		}
		if p := legacyRule(asList(dns["rules"]), "dns.rules", "outbound"); p != "" {
			return p
		}
	}
	for i, o := range asList(root["outbounds"]) {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		// The dns outbound became the hijack-dns action; WireGuard moved to endpoints.
		if t, _ := m["type"].(string); t == "dns" || t == "wireguard" {
			return fmt.Sprintf("outbounds[%d].type=%s", i, t)
		}
	}
	for i, in := range asList(root["inbounds"]) {
		m, ok := in.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range singboxRemovedInbound {
			if m[k] != nil {
				return fmt.Sprintf("inbounds[%d].%s", i, k)
			}
		}
	}
	if route, ok := root["route"].(map[string]any); ok {
		if p := legacyRule(asList(route["rules"]), "route.rules"); p != "" {
			return p
		}
	}
	return ""
}

// legacyRule finds a removed item in a rule list, descending into logical rules,
// whose "rules" hold more of the same. extra names items removed from this list
// only (the outbound DNS rule item, replaced by domain_resolver in 1.12).
func legacyRule(rules []any, path string, extra ...string) string {
	for i, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range append(append([]string(nil), singboxRemovedRule...), extra...) {
			if m[k] != nil {
				return fmt.Sprintf("%s[%d].%s", path, i, k)
			}
		}
		if p := legacyRule(asList(m["rules"]), fmt.Sprintf("%s[%d].rules", path, i), extra...); p != "" {
			return p
		}
	}
	return ""
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// singboxLegacyErr checks a template's text for removed fields. Unparseable text is
// not its concern — the JSON validation reports that.
func singboxLegacyErr(tpl string) error {
	var doc any
	if json.Unmarshal([]byte(tpl), &doc) != nil {
		return nil
	}
	if p := singboxLegacyPath(doc); p != "" {
		return &SingBoxLegacyError{Path: p}
	}
	return nil
}
