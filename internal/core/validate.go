package core

import (
	"net"
	"net/mail"
	"strings"
)

// validEmail reports whether s is a single, well-formed e-mail address. It uses
// net/mail (RFC 5322) and additionally requires a dotted domain part so inputs
// like "a@localhost" are rejected — ACME CAs won't accept those.
func validEmail(s string) bool {
	s = strings.TrimSpace(s)
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false // reject display-name forms like "Name <a@b.com>"
	}
	at := strings.LastIndexByte(s, '@')
	domain := s[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") &&
		!strings.HasSuffix(domain, ".")
}

// validDomain reports whether s is a syntactically valid DNS hostname (a FQDN
// with at least one dot, each label 1–63 chars of [A-Za-z0-9-], not starting or
// ending with a hyphen). It is intentionally NOT an IP — callers test IPs
// separately with net.ParseIP.
func validDomain(s string) bool {
	s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	if len(s) == 0 || len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	// The TLD (last label) is never all-numeric — this also rejects IPv4
	// addresses, which are syntactically valid label sequences.
	tld := labels[len(labels)-1]
	for i := 0; i < len(tld); i++ {
		if tld[i] < '0' || tld[i] > '9' {
			return true
		}
	}
	return false
}

// NormalizeACMEHost lowercases domain targets for ACME (CAs canonicalize DNS
// names; mixed case like WaifuVPN.example.com makes lego reject the order).
func NormalizeACMEHost(target string) string {
	target = strings.TrimSpace(strings.TrimSuffix(target, "."))
	if validDomain(target) {
		return strings.ToLower(target)
	}
	return target // IP addresses and odd inputs pass through unchanged
}

// validACMETarget reports whether target is acceptable for the given provider:
// Let's Encrypt accepts a domain OR an IP; ZeroSSL accepts domains only.
func validACMETarget(target, provider string) bool {
	if validDomain(target) {
		return true
	}
	if provider != "zerossl" && net.ParseIP(target) != nil {
		return true
	}
	return false
}
