package awg

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The tunnel bypasses Xray, so its forward chain has to refuse the private ranges the
// Xray lanes refuse — before the accept, or the accept wins.
func TestRulesetDropsPrivateRangesBeforeAccepting(t *testing.T) {
	rs := Ruleset()
	accept := strings.Index(rs, `iifname "`+Iface+`" accept`)
	if accept < 0 {
		t.Fatalf("no accept for the tunnel:\n%s", rs)
	}
	for _, c := range model.PrivateEgressCIDRs {
		i := strings.Index(rs, c)
		if i < 0 {
			t.Errorf("%s not dropped", c)
		} else if i > accept {
			t.Errorf("%s dropped after the accept", c)
		}
	}
}
