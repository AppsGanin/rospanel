package nodeagent

import (
	"net/netip"
	"slices"
	"testing"
)

// The addresses a node needs — the panel's, its own — never reach its firewall, however
// they are written; everything else banned does.
func TestWithoutAddresses(t *testing.T) {
	keep := map[netip.Addr]bool{
		netip.MustParseAddr("198.51.100.1"): true, // the panel
		netip.MustParseAddr("2001:db8::9"):  true, // this node
	}
	got := withoutAddresses([]string{"203.0.113.7", "198.51.100.1", "::ffff:198.51.100.1", "2001:db8::9", "junk", "2001:db8::7"}, keep)
	if want := []string{"203.0.113.7", "2001:db8::7"}; !slices.Equal(got, want) {
		t.Errorf("bannable = %v, want %v", got, want)
	}
}

// A panel reached by address needs no lookup to be kept out of the list.
func TestBannableKeepsThePanelAddress(t *testing.T) {
	a := &Agent{ident: &Identity{PanelURL: "https://198.51.100.1:8443/x"}}
	if got := a.bannable([]string{"198.51.100.1", "203.0.113.7"}); !slices.Equal(got, []string{"203.0.113.7"}) {
		t.Errorf("bannable = %v", got)
	}
	if got := a.bannable(nil); got != nil {
		t.Errorf("no bans = %v", got)
	}
}
