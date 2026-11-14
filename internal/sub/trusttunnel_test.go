package sub

import (
	"html"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// TrustTunnel is on the page, with a button that opens the link in the app, and in the
// subscriptions of the clients that import tt:// (TrustTunnelLinkFor) alone.
func TestTrustTunnelPageAndSubscription(t *testing.T) {
	set := &model.Settings{
		Host: "vpn.example.com", SubPath: "sub", SNI: "vpn.example.com",
		ServerID: model.LocalNodeID, VLESSEnabled: true, VLESSPort: 443,
		TrustTunnelEnabled: true, SubShowConfigs: true,
	}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", SubToken: "tok"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	page, err := Page(u, set, servers, Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	tt := link.TrustTunnel(u, set)
	s := html.UnescapeString(string(page))
	if !strings.Contains(s, `href="`+tt+`"`) {
		t.Fatalf("no open-in-app button for %s", tt)
	}
	hasTT := func(links []string) bool {
		for _, l := range links {
			if l == tt {
				return true
			}
		}
		return false
	}
	for ua, want := range map[string]bool{"Throne/1.4.1": true, "Throne/1.1.9": false, "Happ/3.8.1": false, "": true} {
		if got := hasTT(ShareLinksFor(u, servers, ua)); got != want {
			t.Errorf("tt:// in the subscription for %q = %v, want %v", ua, got, want)
		}
	}
	if !hasTT(ShareLinksRule(u, servers, "Happ/3.8.1", Overrides{model.LaneTrustTunnel: model.SubProtoOn})) {
		t.Error("a rule giving TrustTunnel did not")
	}
	hide := Overrides{model.LaneTrustTunnel: model.SubProtoOff}
	if hasTT(ShareLinksRule(u, hide.Apply(servers), "Throne/1.4.1", hide)) {
		t.Error("a rule hiding TrustTunnel did not")
	}

	// A user whose groups leave the lane out gets no link.
	limited := []Server{{Set: set, Access: model.Access{Tokens: map[string]bool{
		model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true,
	}}}}
	page, err = Page(u, set, limited, Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "tt://") {
		t.Fatal("tt:// link for a user the lane is not granted to")
	}
}

// A mihomo core with the trusttunnel type gets the lane in its Clash profile, as the
// tt:// link has it; an older one does not.
func TestTrustTunnelInClash(t *testing.T) {
	set := &model.Settings{
		Host: "203.0.113.5", SubPath: "sub", SNI: "vpn.example.com",
		ServerID: model.LocalNodeID, VLESSEnabled: true, VLESSPort: 443, TrustTunnelEnabled: true,
	}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", SubToken: "tok"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}
	want := `type: trusttunnel, server: "203.0.113.5", port: 443, username: "tt-u7", password: "11111111-2222-3333-4444-555555555555", sni: "vpn.example.com", alpn: [h2]`
	if y := ClashYAMLRule(u, servers, "clash.meta/v1.19.34", nil); !strings.Contains(y, want) {
		t.Fatalf("no TrustTunnel proxy for mihomo 1.19.34:\n%s", y)
	}
	if y := ClashYAMLRule(u, servers, "clash.meta/v1.19.20", nil); strings.Contains(y, "trusttunnel") {
		t.Fatal("TrustTunnel for mihomo 1.19.20, which has no such type")
	}
}
