package xray

import (
	"slices"
	"testing"
)

// What Xray 26.7.28 does with each form was checked against the binary: the forms it
// has a client for resolve, "tls://" and "udp://" become a UDP server that answers
// nothing, and an address with a port stops it from starting.
func TestCheckDNSServer(t *testing.T) {
	for s, want := range map[string]DNSIssue{
		"1.1.1.1":                            DNSOK,
		"2606:4700:4700::1111":               DNSOK,
		"localhost":                          DNSOK,
		"https://dns.google/dns-query":       DNSOK,
		"HTTPS://1.1.1.1/dns-query":          DNSOK,
		"https+local://dns.google/dns-query": DNSOK,
		"h2c://1.1.1.1/dns-query":            DNSOK,
		"h2c+local://1.1.1.1/dns-query":      DNSOK,
		"quic+local://dns.adguard.com":       DNSOK,
		"tcp://1.1.1.1:53":                   DNSOK,
		"tcp+local://1.1.1.1":                DNSOK,
		"tls://1.1.1.1":                      DNSScheme,
		"udp://1.1.1.1:53":                   DNSScheme,
		"8.8.8.8:53":                         DNSPort,
		"[2001:db8::1]:53":                   DNSPort,
		"dns.google":                         DNSBad,
		"https:///dns-query":                 DNSBad,
		"fakedns":                            DNSBad,
	} {
		if got := CheckDNSServer(s); got != want {
			t.Errorf("CheckDNSServer(%q) = %v, want %v", s, got, want)
		}
	}
}

// Remote-mode resolvers named by a host go after the rest, each group in the operator's
// order; ones Xray reaches without its own lookup keep their place.
func TestOrderDNS(t *testing.T) {
	in := []string{
		"https://xbox-dns.ru/dns-query",
		"111.88.96.50",
		"https://1.1.1.1/dns-query",
		"tcp://dns.example",
		"https+local://dns.google/dns-query",
		"111.88.96.51",
		"h2c://doh.example/dns-query",
		"localhost",
	}
	want := []string{
		"111.88.96.50",
		"https://1.1.1.1/dns-query",
		"https+local://dns.google/dns-query",
		"111.88.96.51",
		"localhost",
		"https://xbox-dns.ru/dns-query",
		"tcp://dns.example",
		"h2c://doh.example/dns-query",
	}
	if got := orderDNS(in); !slices.Equal(got, want) {
		t.Errorf("orderDNS =\n%q\nwant\n%q", got, want)
	}

	// The generated config carries the order.
	set := baseSettings()
	set.XrayDNS = "https://xbox-dns.ru/dns-query\n111.88.96.50"
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DNS == nil || !slices.Equal(cfg.DNS.Servers, []string{"111.88.96.50", "https://xbox-dns.ru/dns-query"}) {
		t.Errorf("generated DNS = %+v", cfg.DNS)
	}
}
