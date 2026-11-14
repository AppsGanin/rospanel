package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The REALITY lane rides XHTTP: a mihomo core before v1.19.22 would dial it as plain TCP,
// so a client known to run one gets a Clash profile without the lane — unless a rule
// gives it. A client the panel cannot place keeps it: at worst, one dead entry.
func TestClashRealityNeedsXHTTP(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	servers := []Server{{Set: testSet("vpn.example.com"), Access: model.UnrestrictedAccess()}}
	has := func(yaml string) bool { return strings.Contains(yaml, "reality-opts") }

	for _, ua := range []string{"mihomo/1.19.22", "ClashMetaForAndroid/2.11.25", "FlClash/v0.8.93",
		"clash-verge/v2.5.0", "mihomo.party/v1.9.5", "koala-clash/v0.2.10", "Stash/3.3.1", ""} {
		if !has(ClashYAMLFor(u, servers, ua)) {
			t.Errorf("%s lost the REALITY lane", ua)
		}
	}
	for _, ua := range []string{"mihomo/1.19.21", "ClashMetaForAndroid/2.11.24", "FlClash/v0.8.92",
		"clash-verge/v2.4.7", "mihomo.party/v1.9.4", "NekoBox/Android/1.3.8 (Prefer ClashMeta Format)"} {
		if has(ClashYAMLFor(u, servers, ua)) {
			t.Errorf("%s got the REALITY lane", ua)
		}
	}
	on := Overrides{model.LaneReality: model.SubProtoOn}
	if !has(ClashYAMLRule(u, servers, "mihomo/1.19.21", on)) {
		t.Error("a rule could not give the REALITY lane")
	}
	if Protocols(model.SubActionClash, "mihomo/1.19.21", nil)[model.LaneReality] ||
		!Protocols(model.SubActionClash, "mihomo/1.19.21", on)[model.LaneReality] ||
		!Protocols(model.SubActionClash, "mihomo/1.19.30", nil)[model.LaneReality] {
		t.Error("the rule preview is wrong about the REALITY lane")
	}
	// The links and Xray JSON are not Clash: every Xray-core client has XHTTP.
	if l := ShareLinksFor(u, servers, "mihomo/1.19.21"); !strings.Contains(strings.Join(l, "\n"), "security=reality") {
		t.Error("the link list lost the REALITY lane")
	}
}
