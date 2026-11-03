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
		fresh, shared              bool
		want                       string
	}{
		{"wizard sets a domain", "hh.ru", "", "vpn.example.com", true, false, "vpn.example.com"},
		{"wizard sets an address", "hh.ru", "", "", true, false, "hh.ru"},
		{"address to domain after setup", "hh.ru", "", "vpn.example.com", false, false, "hh.ru"},
		{"domain renamed", "old.example.com,hh.ru", "old.example.com", "new.example.com", false, false, "new.example.com,hh.ru"},
		{"domain to address", "old.example.com,hh.ru", "old.example.com", "", false, false, "hh.ru"},
		{"domain to address, nothing left", "old.example.com", "old.example.com", "", false, false, "drawn.ru"},
		{"donor kept", "max.ru", "old.example.com", "new.example.com", false, false, "max.ru"},
		{"no duplicate", "old.example.com,new.example.com", "old.example.com", "new.example.com", false, false, "new.example.com"},
		// A lane on the shared port masks as a donor, so a name arriving there is not
		// adopted: it is the decoy's, and REALITY would never see a client naming it.
		{"wizard sets a domain on the shared port", "hh.ru", "", "vpn.example.com", true, true, "hh.ru"},
		{"domain renamed on the shared port", "hh.ru", "old.example.com", "new.example.com", false, true, "hh.ru"},
	}
	for _, c := range cases {
		if got := realityDestAfterRename(c.dest, c.oldOwn, c.newOwn, c.fresh, c.shared, pick); got != c.want {
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

// A node created on a panel whose REALITY lane is on the shared port does not name
// itself: its own domain is the decoy's there, so it follows the panel's donor.
func TestNodeOnTheSharedPortFollowsThePanelDonor(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	if err := m.store.SetRealityPorts(443, "max.ru"); err != nil {
		t.Fatal(err)
	}
	set, _ := m.store.GetSettings()
	if !set.RealityShared() {
		t.Fatalf("settings reality %d/%d are not a shared port", set.RealityPort, set.VLESSPort)
	}
	n, err := m.CreateNode("NL", "nl.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if n.RealityDest != "" {
		t.Fatalf("node donor %q, want the panel's", n.RealityDest)
	}
	if got := nodeSettings(set, n).RealitySNI(); got != "max.ru" {
		t.Fatalf("node SNI %q, want the panel's donor", got)
	}
	if err := m.UpdateNode(n.ID, store.NodeEdit{Name: "NL", Host: "nl2.example.com"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetNode(n.ID); got.RealityDest != "" {
		t.Fatalf("renamed node donor %q, want the panel's", got.RealityDest)
	}
}

// The twin of a drawn donor is the same site's other name: a bare name gets its www.
// form, a www. name its bare form. Whether the certificate really covers it is checked
// live, so this only says which name to try — never two different sites.
func TestDonorCandidates(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ primary, twin string }{
		{"hh.ru", "www.hh.ru"},
		{"www.hh.ru", "hh.ru"},
		{"www.hh.ru.", "hh.ru."}, // a trailing dot survives the rewrite, it is not ours to fix
	} {
		got := donorCandidates(c.primary)
		if len(got) != 2 || got[0] != c.primary || got[1] != c.twin {
			t.Errorf("%q: %v, want %q then %q", c.primary, got, c.primary, c.twin)
		}
	}
}
