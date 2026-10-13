package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestClashMieruByCore(t *testing.T) {
	for ua, want := range map[string]bool{
		"clash.meta/v1.19.32":                         true,
		"mihomo/1.19.10":                              true,
		"clash.meta/v1.18.10":                         false,
		"ClashMeta/1.19.32; mihomo/1.19.32":           true,
		"ClashMeta/2.10.1.Meta-Alpha":                 false, // a CMFA version, not a core
		"clash.meta/v1.19.32 SlothClash/0.9.4":        true,
		"mihomo/1.19.32 ClashFest/1.2.0":              true,
		"clash.meta/v1.19.12":                         true, // Sparkle 1.6.11+: its running core
		"clash.meta/alpha-de19f92":                    false,
		"koala-clash/v0.2.10":                         true,
		"koala-clash/1.5.0":                           true,
		"koala-clash/alpha-e89af72":                   true,
		"koala-clash/v0.0.9":                          false,
		"FlClashX/v0.2.1 Platform/android":            true,
		"FlClash X/v0.4.1 core/v1.19.28 Platform/ios": true,
		"clash-nyanpasu/v2.0.0":                       false, // core is the user's pick
		"ClashMetaForAndroid/2.11.25.Meta":            true,
		"ClashMetaForAndroid/2.11.2":                  false,
		"FlClash/v0.8.99 clash-verge Platform/ios":    true,
		"FlClash/v0.8.71 clash-verge Platform/ios":    false,
		"clash-verge/v2.4.5":                          true,
		"clash-verge/v2.5.4-rc":                       true,
		"clash-verge/v2.0.2":                          false, // bundled mihomo 1.18.10
		"mihomo.party/v1.7.3 (clash.meta)":            true,
		"mihomo.party/v1.7.2 (clash.meta)":            false,
		"clash.meta":                                  false, // Mihomo Party before 1.7.3, or any core
		"HiddifyNext/4.0.4 (android) like ClashMeta":  false,
		"": false,
	} {
		if got := clashMieru(ua); got != want {
			t.Errorf("%q: %v, want %v", ua, got, want)
		}
	}
}

func TestMieruInSubscriptions(t *testing.T) {
	set := &model.Settings{Host: "vpn.example.com", MieruEnabled: true, MieruPort: 34567, MieruTransport: model.MieruTCP}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}
	has := func(links []string) bool {
		for _, l := range links {
			if strings.HasPrefix(l, "mierus://") {
				return true
			}
		}
		return false
	}
	for ua, want := range map[string]bool{
		"": true, "Exclave/0.17.22": true, "Karing/1.2.23.2606": true,
		"v2rayNG/1.10.0": false, "Happ/3.0": false, "Shadowrocket/2.2.90": true, "Throne/1.2.0": true, "Throne/1.1.9": false, "v2rayN/7.0": false,
	} {
		if got := has(ShareLinksFor(u, servers, ua)); got != want {
			t.Errorf("%q: mieru link %v, want %v", ua, got, want)
		}
	}
	if y := ClashYAMLFor(u, servers, "clash.meta/v1.19.32"); !strings.Contains(y, "type: mieru") || !strings.Contains(y, "udp: true") {
		t.Errorf("no mieru proxy for mihomo 1.19.32:\n%s", y)
	}
	if y := ClashYAMLFor(u, servers, "clash-verge/v2.0.2"); strings.Contains(y, "type: mieru") {
		t.Error("mieru handed to a core older than 1.19")
	}
	// UDP stored by hand is still served as TCP (model.MieruTransportOr).
	set.MieruTransport = model.MieruUDP
	if y := ClashYAMLFor(u, servers, "clash.meta/v1.19.32"); !strings.Contains(y, "transport: TCP") {
		t.Errorf("not TCP:\n%s", y)
	}
	// A group without the lane gets none of it.
	servers[0].Access = model.Access{Tokens: map[string]bool{}}
	if has(ShareLinksFor(u, servers, "")) || strings.Contains(ClashYAMLFor(u, servers, "clash.meta/v1.19.32"), "type: mieru") {
		t.Error("mieru handed to a user whose groups do not allow it")
	}
}

// Which NaiveProxy link each client is handed, if any.
func TestNaiveLinkForClients(t *testing.T) {
	set := &model.Settings{Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443, NaiveEnabled: true}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	for ua, want := range map[string]string{
		"":                    "naive+https://",
		"Shadowrocket/2.2.90": "http2://",
		"Throne/1.1.2":        "naive+https://", // with uot=1, below
		"Throne/1.1.1":        "",
		"v2rayN/7.20.0":       "naive+https://",
		"Karing/1.2.26.2904":  "naive+https://",
		"Exclave/0.17.22":     "naive+https://",
		"v2rayNG/1.10.0":      "",
		"Happ/3.0":            "",
	} {
		f := NaiveLinkFor(ua)
		got := ""
		if f != nil {
			got = f(u, set)
		}
		if want == "" && got != "" || want != "" && !strings.HasPrefix(got, want) {
			t.Errorf("%q: %q, want %s…", ua, got, want)
		}
		if strings.HasPrefix(ua, "Throne/1.1.2") && !strings.Contains(got, "?uot=1#") {
			t.Errorf("Throne link %q has no uot=1", got)
		}
	}
}
