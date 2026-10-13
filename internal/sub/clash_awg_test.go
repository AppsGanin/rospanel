package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

func TestClashAWGClients(t *testing.T) {
	for ua, want := range map[string]bool{
		"clash.meta/v1.19.32": true,
		"mihomo/1.19.30":      true,
		"mihomo/2.0.0":        true,
		"mihomo/1.19.29":      false, // reads the 1.5 half only
		"clash.meta/v1.18.10": false,
		"FlClash/v0.8.99 clash-verge Platform/android":              true,
		"FlClash/v0.8.98 clash-verge Platform/android":              false,
		"clash-verge/v2.4.2":                                        false, // bundles a core before 1.19.30
		"ClashMetaForAndroid/2.11.13.Meta":                          false,
		"HiddifyNext/4.1.1 (android) like ClashMeta v2ray sing-box": false,
		"NekoBox/Android/1.4.2 (Prefer ClashMeta Format)":           false,
		"ClashMeta/1.19.32; mihomo/1.19.32":                         true, // ClashMi
		"ClashMeta/1.19.29; mihomo/1.19.29":                         false,
		"ClashMeta/2.10.1.Meta-Alpha":                               false,
		"ClashMetaForAndroid/2.11.33.Meta":                          true,
		"ClashMetaForAndroid/2.11.32.Meta":                          false,
		"clash-verge/v2.5.4-rc":                                     true,
		"clash-verge/v2.5.2":                                        false, // core 1.19.29
		"mihomo.party/v2.0.3 (clash.meta)":                          true,
		"mihomo.party/v2.0.2 (clash.meta)":                          false,
		"koala-clash/1.4.0":                                         true,
		"koala-clash/1.3.1":                                         false,
		"FlClash X/v0.4.1 core/v1.19.28 Platform/ios":               false,
		"FlClash X/v0.5.0 core/v1.19.31 Platform/ios":               true,
		"": false,
	} {
		if got := ClashAWG(ua); got != want {
			t.Errorf("%q: %v, want %v", ua, got, want)
		}
	}
}

// The tunnel goes into the Clash profile of a client that reads 3.1, with every
// parameter the server enforces, and into no other.
func TestClashAWGProxy(t *testing.T) {
	_, pub, _ := awg.GenerateKey()
	priv, _, _ := awg.GenerateKey()
	p := awg.RandomParams()
	set := &model.Settings{Host: "vpn.example.com", AWGEnabled: true, AWGPort: 51999, AWGPublicKey: pub,
		AWGParams: awg.ToModel(p), AWGDNS: "1.1.1.1, 8.8.8.8"}
	u := model.User{ID: 7, WGPrivateKey: priv, AWGSlot: 9}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	out := ClashYAMLFor(u, servers, "mihomo/1.19.32")
	for _, want := range []string{
		`type: wireguard, server: "vpn.example.com", port: 51999, ip: "10.66.0.9"`,
		`private-key: "` + priv + `"`, `public-key: "` + pub + `"`,
		`dns: ["1.1.1.1", "8.8.8.8"]`,
		"amnezia-wg-option: {version: 3,",
		`h1: "` + p.H1.String() + `"`, `h4: "` + p.H4.String() + `"`,
		`header-protection-key: "` + p.HeaderKey + `"`,
		`content-padding-addition: "0-32"`, "random-trailers: true",
		`i1: `, `rekey-after-time: "110-130"`, `max-handshake-attempts: "10-15"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("profile lacks %s:\n%s", want, out)
		}
	}
	for _, ua := range []string{"mihomo/1.19.29", "clash-verge/v2.4.2", ""} {
		if strings.Contains(ClashYAMLFor(u, servers, ua), "amnezia-wg-option") {
			t.Errorf("%q got the AmneziaWG proxy", ua)
		}
	}
	if strings.Contains(ClashYAMLMulti(u, servers), "amnezia-wg-option") {
		t.Error("the plain Clash profile carries AmneziaWG")
	}
	// A user with no tunnel identity yet gets no half-built proxy.
	if strings.Contains(ClashYAMLFor(model.User{ID: 8}, servers, "mihomo/1.19.32"), "amnezia-wg-option") {
		t.Error("a user without a tunnel key got the AmneziaWG proxy")
	}
	// A group that does not grant the lane keeps it out.
	limited := []Server{{Set: set, Access: model.Access{Tokens: map[string]bool{model.BuiltinToken(0, model.LaneVLESS): true}}}}
	if strings.Contains(ClashYAMLFor(u, limited, "mihomo/1.19.32"), "amnezia-wg-option") {
		t.Error("AmneziaWG handed out against the user's groups")
	}
}
