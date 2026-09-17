package xray

import (
	"net"
	"net/url"
	"strings"
)

// dnsSchemes are the resolver URL schemes Xray builds a client for (app/dns NewServer),
// each marked by whether that client reaches its server through Xray's own routing
// ("remote" mode) rather than dialling it directly. Any other scheme is not refused by
// Xray: it becomes a plain UDP server whose address is the whole string, which answers
// nothing — DNS over TLS ("tls://") and "udp://" among them.
var dnsSchemes = map[string]bool{
	"https":       true,
	"h2c":         true,
	"tcp":         true,
	"https+local": false,
	"h2c+local":   false,
	"tcp+local":   false,
	"quic+local":  false,
}

// DNSIssue names what is wrong with one entry of a DNS setting.
type DNSIssue int

const (
	DNSOK     DNSIssue = iota
	DNSBad             // neither an address nor a URL
	DNSScheme          // a URL whose scheme Xray has no client for
	DNSPort            // an address with a port, which stops Xray from starting
)

// CheckDNSServer tells whether Xray can use s as a DNS server: an IP address,
// "localhost", or a URL of a scheme it has a client for, naming a host.
//
// An address with a port ("8.8.8.8:53") is refused on its own: Xray reads it as a URL,
// fails to parse it, and does not start at all. A port goes in a URL ("tcp://8.8.8.8:53").
func CheckDNSServer(s string) DNSIssue {
	s = strings.TrimSpace(s)
	switch {
	case s == "localhost", net.ParseIP(s) != nil:
		return DNSOK
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return DNSBad
		}
		if _, ok := dnsSchemes[strings.ToLower(u.Scheme)]; !ok {
			return DNSScheme
		}
		return DNSOK
	}
	if host, _, err := net.SplitHostPort(s); err == nil && net.ParseIP(host) != nil {
		return DNSPort
	}
	return DNSBad
}

// orderDNS puts the servers Xray reaches without a lookup of its own ahead of those it
// does not, keeping the operator's order within each group.
//
// A remote-mode server named by a host — "https://dns.google/dns-query", "h2c://…",
// "tcp://…" — is dialled through Xray's routing, so its host is resolved by this very
// list. Listed first, that lookup lands on the server itself, which refuses it ("tries
// to resolve itself! Use IP or set hosts instead") before the next server answers; the
// error returns each time the cached address expires. Behind a server that needs no
// lookup the name resolves at once. The price is that Xray, asking servers in order,
// uses such a server only when the ones ahead of it fail.
func orderDNS(servers []string) []string {
	direct := make([]string, 0, len(servers))
	var lookedUp []string
	for _, s := range servers {
		if needsOwnLookup(s) {
			lookedUp = append(lookedUp, s)
		} else {
			direct = append(direct, s)
		}
	}
	return append(direct, lookedUp...)
}

// needsOwnLookup reports whether s is a remote-mode resolver named by a host.
func needsOwnLookup(s string) bool {
	if !strings.Contains(s, "://") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return dnsSchemes[strings.ToLower(u.Scheme)] && net.ParseIP(u.Hostname()) == nil
}
