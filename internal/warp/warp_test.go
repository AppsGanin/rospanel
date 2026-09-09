package warp

import (
	"net"
	"strings"
	"testing"
)

// A WireGuard endpoint Xray has to resolve is an endpoint that fails whenever the
// operator's resolvers do, and the failure is silent: the lane simply never comes up.
// Every address the panel can write must therefore come out as a literal IP.
func TestEndpointAddrNeedsNoResolver(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"cloudflare hostname", "engage.cloudflareclient.com:2408", "162.159.192.1:2408"},
		{"hostname keeps its port", "engage.cloudflareclient.com:500", "162.159.192.1:500"},
		{"hostname without a port", "engage.cloudflareclient.com", "162.159.192.1:2408"},
		{"mixed case hostname", "Engage.CloudflareClient.com:2408", "162.159.192.1:2408"},
		{"empty falls back", "", "162.159.192.1:2408"},
		{"an address is kept", "188.114.96.1:2408", "188.114.96.1:2408"},
		{"port 0 is not a port", "188.114.96.1:0", "188.114.96.1:2408"},
		{"bare IPv6 gets brackets", "2606:4700:d0::a29f:c001", "[2606:4700:d0::a29f:c001]:2408"},
		{"bracketed IPv6 is kept", "[2606:4700:d0::a29f:c001]:2408", "[2606:4700:d0::a29f:c001]:2408"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EndpointAddr(tc.in)
			if got != tc.want {
				t.Fatalf("EndpointAddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
			host, _, err := net.SplitHostPort(got)
			if err != nil || net.ParseIP(host) == nil {
				t.Fatalf("EndpointAddr(%q) = %q, which still needs a resolver", tc.in, got)
			}
		})
	}
}

// A peer the panel doesn't recognise is left alone rather than pointed at
// Cloudflare's anycast address, which would send the tunnel somewhere the operator
// never asked for.
func TestEndpointAddrLeavesAnUnknownHostAlone(t *testing.T) {
	const in = "wg.example.org:51820"
	if got := EndpointAddr(in); got != in {
		t.Fatalf("EndpointAddr(%q) = %q, want it unchanged", in, got)
	}
}

// Registration returns the anycast addresses with port 0 and the real port only on
// the hostname, so the address is preferred and the port borrowed.
func TestPeerEndpointPrefersTheAddress(t *testing.T) {
	for _, tc := range []struct {
		name, v4, host, want string
	}{
		{"address with no port", "162.159.192.1:0", "engage.cloudflareclient.com:2408", "162.159.192.1:2408"},
		{"address with a port", "162.159.193.10:500", "engage.cloudflareclient.com:2408", "162.159.193.10:500"},
		{"bare address", "162.159.192.1", "engage.cloudflareclient.com:2408", "162.159.192.1:2408"},
		{"no address at all", "", "engage.cloudflareclient.com:2408", "engage.cloudflareclient.com:2408"},
		{"nothing at all", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := peerEndpoint(tc.v4, tc.host); got != tc.want {
				t.Fatalf("peerEndpoint(%q, %q) = %q, want %q", tc.v4, tc.host, got, tc.want)
			}
		})
	}
}

// One endpoint is one point of failure: a provider can drop a Cloudflare range or a
// single UDP port. The pool has to vary both, or the second member covers nothing the
// first did not.
func TestPoolVariesAddressAndPort(t *testing.T) {
	for _, registered := range []string{
		"engage.cloudflareclient.com:2408", // what an older build stored
		"162.159.192.10:2408",              // what registration hands back now
		"",                                 // never provisioned
	} {
		t.Run(registered, func(t *testing.T) {
			pool := Pool(registered)
			if len(pool) < 2 {
				t.Fatalf("Pool(%q) = %v, want more than one endpoint", registered, pool)
			}
			if want := EndpointAddr(registered); pool[0] != want {
				t.Errorf("Pool(%q)[0] = %q, want the account's own endpoint %q", registered, pool[0], want)
			}
			hosts, ports, seen := map[string]bool{}, map[string]bool{}, map[string]bool{}
			for _, ep := range pool {
				host, port, err := net.SplitHostPort(ep)
				if err != nil || net.ParseIP(host) == nil {
					t.Fatalf("endpoint %q needs a resolver or is malformed", ep)
				}
				if seen[ep] {
					t.Errorf("endpoint %q is in the pool twice: %v", ep, pool)
				}
				seen[ep], hosts[host], ports[port] = true, true, true
			}
			if len(hosts) < 2 || len(ports) < 2 {
				t.Errorf("Pool(%q) = %v covers %d address(es) and %d port(s), want at least two of each",
					registered, pool, len(hosts), len(ports))
			}
		})
	}
}

// 162.159.193.0/24 answered no handshake at all when the pool was measured, on any
// port. A member that can never come up is not cover — it is an outbound retrying a
// handshake forever and filling the log.
func TestPoolLeavesOutTheRangeThatNeverAnswered(t *testing.T) {
	for _, ep := range Pool("162.159.192.10:2408") {
		if strings.HasPrefix(ep, "162.159.193.") {
			t.Errorf("pool carries %q, a range measured dead on every port", ep)
		}
	}
}
