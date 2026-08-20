package link

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The range a client is told to hop over must be the range the firewall actually funnels
// back to the listener. They disagreed: every renderer advertised base..hopEnd while
// internal/hop redirects hopStart..hopEnd, so an operator running Hysteria2 on 443 with a
// hop window of 20000-30000 handed clients ~19500 ports where nothing answers. A client
// rolling onto one stalls for a whole hop interval, which reads as "Hysteria randomly
// dies every few seconds" rather than as a misconfiguration.
func TestAdvertisedHopRangeMatchesTheFirewall(t *testing.T) {
	const base, hopStart, hopEnd = 443, 20000, 30000

	// What the firewall installs: internal/hop redirects Start..End to Target, bumping
	// Start past the target because the base port is served directly.
	fwStart := hopStart
	if fwStart <= base {
		fwStart = base + 1
	}

	// What the client is told.
	advertised := model.HopAdvertised(base, hopStart)
	if advertised != fwStart && advertised != base {
		t.Errorf("clients are told to hop from %d, the firewall funnels from %d — "+
			"%d ports where nothing answers", advertised, fwStart, fwStart-advertised)
	}

	// And the same through the real link builder, so a future renderer cannot drift.
	set := &model.Settings{
		HysteriaPort: base, HopStart: hopStart, HopEnd: hopEnd,
		SNI: "example.org", Host: "example.org", HysteriaEnabled: true,
	}
	got := Hysteria2(model.User{Password: "pw"}, set)
	if !strings.Contains(got, "20000-30000") {
		t.Errorf("the link does not advertise the funnelled range: %s", got)
	}
}

// With no hop window configured the base port is the start — that is what the firewall's
// own normalisation assumes, and narrowing it would break the ordinary single-port case.
func TestAdvertisedHopRangeKeepsTheBasePortWhenUnset(t *testing.T) {
	if got := model.HopAdvertised(443, 0); got != 443 {
		t.Errorf("HopAdvertised(443, 0) = %d, want 443", got)
	}
	if got := model.HopAdvertised(443, 100); got != 443 {
		t.Errorf("a hop start below the base must not narrow the range: got %d", got)
	}
}
