package netguard

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// The shape check is the half of the guard that a proxied fetch still gets, so it
// has to catch everything that does not need an address to judge.
func TestValidateFetchURLShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string // fragment of the error; "" means accepted
	}{
		{name: "https", raw: "https://example.com/x.js"},
		{name: "surrounding whitespace is tolerated", raw: "  https://example.com  "},
		{name: "port and query", raw: "https://example.com:8443/a?b=c"},
		{name: "empty", raw: "", want: "empty URL"},
		{name: "whitespace only", raw: "   ", want: "empty URL"},
		{name: "plain http", raw: "http://example.com", want: "only https"},
		{name: "scheme case is normalised by url.Parse", raw: "HTTPS://example.com"},
		{name: "other scheme", raw: "ftp://example.com", want: "only https"},
		{name: "credentials", raw: "https://user:pw@example.com", want: "credentials"},
		{name: "user only", raw: "https://user@example.com", want: "credentials"},
		{name: "no host", raw: "https:///path", want: "no host"},
		{name: "unparseable", raw: "https://exa mple.com", want: "invalid URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFetchURLShape(tc.raw)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("rejected %q: %v", tc.raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %q", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

// Every range an SSRF attempt reaches for: the box itself, the LAN, the cloud
// metadata service, and the addresses that mean "whatever is nearest".
func TestRejectPrivateIP(t *testing.T) {
	forbidden := []string{
		"127.0.0.1", "127.255.255.254", "::1", // loopback
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", // RFC 1918
		"fd00::1", "fc00::1", // IPv6 ULA
		"169.254.1.1", "fe80::1", // link-local
		"169.254.169.254",                         // cloud metadata (link-local too, but named in code)
		"224.0.0.1", "239.255.255.250", "ff02::1", // multicast
		"0.0.0.0", "::", // unspecified
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", // IPv4-mapped forms of the above
	}
	for _, s := range forbidden {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test address %q", s)
		}
		if err := rejectPrivateIP(ip); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2001:4860:4860::8888", "2606:4700::1111"} {
		if err := rejectPrivateIP(net.ParseIP(s)); err != nil {
			t.Errorf("public %s was rejected: %v", s, err)
		}
	}
	if err := rejectPrivateIP(nil); err == nil {
		t.Error("a nil IP was allowed")
	}
}

// A literal address needs no DNS, so the full check can run offline: the private
// literal is refused and the public one passes.
func TestValidateFetchURLWithLiteralAddresses(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1/x",
		"https://[::1]:8443/x",
		"https://10.1.2.3/",
		"https://169.254.169.254/latest/meta-data/",
		"https://[fd12::1]/",
	} {
		if err := ValidateFetchURL(raw); err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}
	if err := ValidateFetchURL("https://8.8.8.8/x"); err != nil {
		t.Errorf("a public literal was rejected: %v", err)
	}
}

// Get must refuse before it dials: a forbidden target never sees a packet.
func TestGetRefusesPrivateTargetsWithoutDialing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if _, err := Get(ctx, "https://10.0.0.1/secret", 1<<10); err == nil {
		t.Fatal("Get reached for a private address")
	}
	// A refusal is instant; a dial attempt to an unroutable LAN address would
	// have burned the whole timeout.
	if time.Since(start) > 500*time.Millisecond {
		t.Error("Get took long enough to have tried connecting")
	}
}

// The dialer is the second line of defence (DNS rebinding between validation and
// the request), so it must refuse a private literal on its own.
func TestDialValidatedRefusesPrivateLiterals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, addr := range []string{"127.0.0.1:443", "[::1]:443", "192.168.0.1:443", "169.254.169.254:80"} {
		if c, err := dialValidated(ctx, "tcp", addr); err == nil {
			c.Close()
			t.Errorf("dialValidated connected to %s", addr)
		}
	}
	if _, err := dialValidated(ctx, "tcp", "no-port-here"); err == nil {
		t.Error("an address without a port was accepted")
	}
}

// Zero means "the package default", never "no timeout" — a hung remote must not
// hang the caller forever.
func TestClientTimeoutDefaults(t *testing.T) {
	if got := Client(0).Timeout; got != defaultFetchTimeout {
		t.Errorf("Client(0).Timeout = %v, want %v", got, defaultFetchTimeout)
	}
	if got := Client(-1).Timeout; got != defaultFetchTimeout {
		t.Errorf("Client(-1).Timeout = %v, want %v", got, defaultFetchTimeout)
	}
	if got := Client(3 * time.Second).Timeout; got != 3*time.Second {
		t.Errorf("Client(3s).Timeout = %v", got)
	}
}
