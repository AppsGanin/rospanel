package xray

import "testing"

func TestParseAccess(t *testing.T) {
	line := "2024/01/01 00:00:00.000000 from tcp:203.0.113.10:54321 accepted tcp:example.com:443 [inbound-vless >> direct] email: u1"
	email, ip := parseAccess(line)
	if email != "u1" || ip != "203.0.113.10" {
		t.Fatalf("got email=%q ip=%q", email, ip)
	}

	email, ip = parseAccess("from 203.0.113.20:1234 accepted ... email: u2")
	if email != "u2" || ip != "203.0.113.20" {
		t.Fatalf("plain from: got email=%q ip=%q", email, ip)
	}

	if email, ip = parseAccess("from 127.0.0.1:9999 accepted email: u3"); email != "" {
		t.Fatalf("loopback should be ignored, got %q %q", email, ip)
	}
}
