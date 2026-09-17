package xray

import "testing"

func TestParseAccess(t *testing.T) {
	line := "2024/01/01 00:00:00.000000 from tcp:203.0.113.10:54321 accepted tcp:example.com:443 [inbound-vless >> direct] email: u1"
	email, ip, dest := parseAccess(line)
	if email != "u1" || ip != "203.0.113.10" || dest != "example.com" {
		t.Fatalf("got email=%q ip=%q dest=%q", email, ip, dest)
	}

	email, ip, _ = parseAccess("from 203.0.113.20:1234 accepted ... email: u2")
	if email != "u2" || ip != "203.0.113.20" {
		t.Fatalf("plain from: got email=%q ip=%q", email, ip)
	}

	if email, ip, _ = parseAccess("from 127.0.0.1:9999 accepted email: u3"); email != "" {
		t.Fatalf("loopback should be ignored, got %q %q", email, ip)
	}
}

// TestParseAccessDest covers the destination half on its own. The rule it pins: a
// line we cannot read a host out of yields dest "" but still yields email+ip, so a
// parsing gap can never cost us a device sighting.
func TestParseAccessDest(t *testing.T) {
	const src = "from tcp:203.0.113.10:54321 "

	cases := []struct {
		name string
		tail string
		want string
	}{
		{"domain", "accepted tcp:example.com:443 [in >> out] ", "example.com"},
		{"udp domain", "accepted udp:dns.google:53 [in >> out] ", "dns.google"},
		{"uppercase SNI", "accepted tcp:CDN.Example.COM:443 [in >> out] ", "cdn.example.com"},
		{"fqdn root dot", "accepted tcp:example.com.:443 [in >> out] ", "example.com"},
		{"subdomain", "accepted tcp:a.b.c.example.com:443 [in >> out] ", "a.b.c.example.com"},
		{"underscore label", "accepted tcp:_dmarc.example.com:443 [in >> out] ", "_dmarc.example.com"},
		{"literal v4", "accepted tcp:1.2.3.4:443 [in >> out] ", "1.2.3.4"},
		{"literal v6", "accepted tcp:[2606:4700::1111]:443 [in >> out] ", "2606:4700::1111"},
		{"no port", "accepted tcp:example.com [in >> out] ", "example.com"},

		// Everything below must yield no destination rather than a junk one.
		{"no accepted segment", "rejected tcp:example.com:443 [in >> out] ", ""},
		{"truncated line", "accepted ", ""},
		{"marker is the email tail", "accepted ", ""},
		{"ellipsis placeholder", "accepted ... ", ""},
		{"dotless host", "accepted tcp:localhost:443 [in >> out] ", ""},
		{"empty labels", "accepted tcp:a..b:443 [in >> out] ", ""},
		{"leading dot", "accepted tcp:.example.com:443 [in >> out] ", ""},
		{"illegal characters", "accepted tcp:ex$ample.com:443 [in >> out] ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			email, ip, dest := parseAccess(src + tc.tail + "email: u1")
			if email != "u1" || ip != "203.0.113.10" {
				t.Fatalf("sighting lost: email=%q ip=%q", email, ip)
			}
			if dest != tc.want {
				t.Fatalf("dest = %q, want %q", dest, tc.want)
			}
		})
	}
}

// TestValidHostRejectsOverlongName pins the length bound separately: 253 is the DNS
// limit, and an unbounded name would become an unbounded map key downstream.
func TestValidHostRejectsOverlongName(t *testing.T) {
	long := ""
	for len(long) < 260 {
		long += "aaaaaaaaa."
	}
	if validHost(long + "com") {
		t.Fatal("overlong name accepted")
	}
}

// A WireGuard inbound's lines carry no email; the tap names the user by the tunnel
// address they come from, whichever way the route was matched, and leaves every other
// inbound's lines and every unknown address alone.
func TestTunnelUsersAttribute(t *testing.T) {
	users := tunnelUsersOf(map[string]map[string]wireGuardPeerState{
		"custom-18": {
			"u7": {allowedIPs: "10.66.0.2/32"},
			"u9": {allowedIPs: "10.66.0.3/32,10.66.0.0/16"}, // a wide prefix names nobody
		},
	})
	cases := []struct {
		line, email, ip, dest string
	}{
		{"2026/09/17 13:31:59.176153 from tcp:10.66.0.2:31239 accepted tcp:Example.com.:443 [custom-18 >> direct]", "u7", "10.66.0.2", "example.com"},
		{"from udp:10.66.0.3:5353 accepted udp:1.1.1.1:53 [custom-18 -> warp]", "u9", "10.66.0.3", "1.1.1.1"},
		{"from tcp:10.66.0.2:1 accepted tcp:[2001:db8::1]:443 [custom-18 ==> block]", "u7", "10.66.0.2", "2001:db8::1"},
		{"from tcp:10.66.0.4:1 accepted tcp:example.com:443 [custom-18 >> direct]", "", "", ""}, // no such peer
		{"from tcp:10.66.0.2:1 accepted tcp:example.com:443 [vless-in >> direct]", "", "", ""},  // another inbound
		{"from tcp:10.66.0.2:1 rejected tcp:example.com:443 [custom-18 >> direct]", "", "", ""}, // not a connection
		{"from tcp:10.66.0.2:1 accepted", "", "", ""},
	}
	for _, c := range cases {
		email, ip, dest := users.attribute(c.line)
		if email != c.email || ip != c.ip || dest != c.dest {
			t.Errorf("%q\n got %q %q %q, want %q %q %q", c.line, email, ip, dest, c.email, c.ip, c.dest)
		}
	}
	if (*users)["custom-18"]["10.66.0.0"] != "" {
		t.Error("a /16 was taken as one user's address")
	}
	var none *tunnelUsers
	if email, _, _ := none.attribute(cases[0].line); email != "" {
		t.Error("a process with no WireGuard inbound attributed a line")
	}
}
