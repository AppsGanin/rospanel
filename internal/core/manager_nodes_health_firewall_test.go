package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// A node that can drop addresses passes; one that cannot warns, since the bans the
// panel hands it do nothing there; an agent too old to say gets no row at all.
func TestNodeFirewallHealth(t *testing.T) {
	yes, no := true, false
	if _, ok := nodeFirewallHealth(nodeapi.HostStats{}); ok {
		t.Error("an agent that did not report was given a row")
	}
	if c, ok := nodeFirewallHealth(nodeapi.HostStats{Firewall: &yes}); !ok || c.Status != healthOK {
		t.Errorf("working firewall: %+v %v", c, ok)
	}
	if c, ok := nodeFirewallHealth(nodeapi.HostStats{Firewall: &no}); !ok || c.Status != healthWarn || c.HintKey == "" {
		t.Errorf("missing firewall: %+v %v", c, ok)
	}
}
