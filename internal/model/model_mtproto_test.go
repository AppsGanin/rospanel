package model

import "testing"

func TestAddressName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"203.0.113.5":    "203-0-113-5.sslip.io",
		"[2001:db8::1]":  "2001-db8--1.sslip.io",
		"::ffff:1.2.3.4": "1-2-3-4.sslip.io",
		"vpn.example":    "",
		"":               "",
	} {
		if got := AddressName(in); got != want {
			t.Errorf("AddressName(%q) = %q, want %q", in, got, want)
		}
	}
}
