package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeTrustedNets(t *testing.T) {
	got, err := NormalizeTrustedNets([]string{
		" 203.0.113.10 ",
		"198.51.100.77/24", // host bits cleared
		"",
		"203.0.113.10/32", // the same as the bare address
		"2001:db8::1",
		"::ffff:192.0.2.5", // an IPv4-mapped address is the IPv4 address
		"2001:db8:abcd::/48",
	})
	if err != nil {
		t.Fatalf("a valid list was refused: %v", err)
	}
	want := "192.0.2.5/32,198.51.100.0/24,203.0.113.10/32,2001:db8::1/128,2001:db8:abcd::/48"
	if strings.Join(got, ",") != want {
		t.Errorf("normalized:\n got %s\nwant %s", strings.Join(got, ","), want)
	}

	for _, c := range []struct{ entry, code string }{
		{"office", "err.trustedNetInvalid"},
		{"203.0.113.300", "err.trustedNetInvalid"},
		{"203.0.113.0/33", "err.trustedNetInvalid"},
		{"fe80::1%eth0", "err.trustedNetInvalid"},
		{"0.0.0.0/0", "err.trustedNetTooWide"},
		{"10.0.0.0/7", "err.trustedNetTooWide"},
		{"::/0", "err.trustedNetTooWide"},
		{"2001:db8::/31", "err.trustedNetTooWide"},
	} {
		_, err := NormalizeTrustedNets([]string{"203.0.113.1", c.entry})
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Code != c.code {
			t.Errorf("%q: got %v, want %s", c.entry, err, c.code)
		}
	}
	// The widest allowed are allowed.
	if _, err := NormalizeTrustedNets([]string{"10.0.0.0/8", "2001:db8::/32"}); err != nil {
		t.Errorf("a /8 and a /32 were refused: %v", err)
	}

	many := make([]string, MaxTrustedNets+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.%d.%d.1", i/250, i%250)
	}
	var fe *FieldError
	if _, err := NormalizeTrustedNets(many); !errors.As(err, &fe) || fe.Code != "err.trustedNetTooMany" {
		t.Errorf("%d entries: got %v, want err.trustedNetTooMany", len(many), err)
	}
}

func TestTrustedNetsContains(t *testing.T) {
	nets := ParseTrustedNets([]string{"198.51.100.0/24", "203.0.113.10/32", "2001:db8::/32", "garbage"})
	for ip, want := range map[string]bool{
		"198.51.100.1":        true,
		"198.51.100.255":      true,
		"198.51.101.1":        false,
		"203.0.113.10":        true,
		"203.0.113.11":        false,
		"::ffff:203.0.113.10": true, // how a dual-stack socket reports an IPv4 peer
		"2001:db8:1::5":       true,
		"2001:db9::5":         false,
		"":                    false,
		"not-an-ip":           false,
	} {
		if got := nets.Contains(ip); got != want {
			t.Errorf("Contains(%q) = %v, want %v", ip, got, want)
		}
	}
	if (TrustedNets{}).Contains("198.51.100.1") {
		t.Error("an empty list trusted an address")
	}
}
