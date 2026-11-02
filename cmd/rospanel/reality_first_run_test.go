package main

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A fresh install starts REALITY on the TCP-TLS lane's port and masks as a donor:
// the schema's 8443 + one donor everywhere was the signature this replaces, and 443
// is the port a client is least likely to find blocked. The lane's own name is not
// available there — the decoy serves that name on the same port.
func TestFirstRunReality(t *testing.T) {
	drawn := func() string { return "hh.ru" }
	for _, c := range []struct {
		name string
		set  model.Settings
	}{
		{"schema TLS port", model.Settings{VLESSPort: 443}},
		{"no TLS port", model.Settings{}},
		{"an install that already has a name", model.Settings{Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443}},
	} {
		port, donor := firstRunReality(&c.set, drawn)
		if port != 443 {
			t.Errorf("%s: port %d, want 443", c.name, port)
		}
		if donor != "hh.ru" {
			t.Errorf("%s: donor %q, want the drawn one", c.name, donor)
		}
	}
}
