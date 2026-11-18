package model

import "testing"

func TestURLHost(t *testing.T) {
	for in, want := range map[string]string{
		"2001:db8::1":     "[2001:db8::1]",
		"[2001:db8::1]":   "[2001:db8::1]",
		"203.0.113.7":     "203.0.113.7",
		"vpn.example.com": "vpn.example.com",
		"":                "",
	} {
		if got := URLHost(in); got != want {
			t.Errorf("URLHost(%q) = %q, want %q", in, got, want)
		}
	}
}
