package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

// The REALITY donor list follows the server's own name and leaves a donor alone.
func TestRealityDestAfterRename(t *testing.T) {
	t.Parallel()
	pick := func() string { return "drawn.ru" }
	cases := []struct {
		name, dest, oldOwn, newOwn string
		fresh                      bool
		want                       string
	}{
		{"wizard sets a domain", "hh.ru", "", "vpn.example.com", true, "vpn.example.com"},
		{"wizard sets an address", "hh.ru", "", "", true, "hh.ru"},
		{"address to domain after setup", "hh.ru", "", "vpn.example.com", false, "hh.ru"},
		{"domain renamed", "old.example.com,hh.ru", "old.example.com", "new.example.com", false, "new.example.com,hh.ru"},
		{"domain to address", "old.example.com,hh.ru", "old.example.com", "", false, "hh.ru"},
		{"domain to address, nothing left", "old.example.com", "old.example.com", "", false, "drawn.ru"},
		{"donor kept", "max.ru", "old.example.com", "new.example.com", false, "max.ru"},
		{"no duplicate", "old.example.com,new.example.com", "old.example.com", "new.example.com", false, "new.example.com"},
	}
	for _, c := range cases {
		if got := realityDestAfterRename(c.dest, c.oldOwn, c.newOwn, c.fresh, pick); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A node added by its domain names itself; one renamed carries the name along, and one
// moved to an address goes back to the panel's donor.
func TestNodeRealityFollowsHost(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	n, err := m.CreateNode("NL", "nl.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if n.RealityDest != "nl.example.com" {
		t.Fatalf("created: donor %q, want the node's domain", n.RealityDest)
	}
	set, _ := m.store.GetSettings()
	if got := nodeSettings(set, n).RealityTarget(); got != "127.0.0.1:443" {
		t.Fatalf("created: target %q, want the node's own :443", got)
	}
	for _, step := range []struct{ host, want string }{
		{"nl2.example.com", "nl2.example.com"},
		{"203.0.113.7", ""},
	} {
		if err := m.UpdateNode(n.ID, store.NodeEdit{Name: "NL", Host: step.host}); err != nil {
			t.Fatalf("update to %s: %v", step.host, err)
		}
		got, _ := m.store.GetNode(n.ID)
		if got.RealityDest != step.want {
			t.Fatalf("host %s: donor %q, want %q", step.host, got.RealityDest, step.want)
		}
	}
	ip, err := m.CreateNode("DE", "198.51.100.9")
	if err != nil {
		t.Fatal(err)
	}
	if ip.RealityDest != "" || nodeSettings(set, ip).RealitySNI() != set.RealitySNI() {
		t.Fatalf("address node: donor %q, want the panel's", ip.RealityDest)
	}
}
