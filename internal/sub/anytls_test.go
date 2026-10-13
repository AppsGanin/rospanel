package sub

import "testing"

// Each form of the AnyTLS lane goes only to clients whose version proves they read it.
func TestAnyTLSClients(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fn   func(string) bool
		ua   string
		want bool
	}{
		{"clash core", clashAnyTLS, "mihomo/1.19.3", true},
		{"clash core old", clashAnyTLS, "mihomo/1.19.2", false},
		{"clashmi", clashAnyTLS, "ClashMeta/1.19.32; mihomo/1.19.32", true},
		{"party", clashAnyTLS, "mihomo.party/v1.7.3 (clash.meta)", true},
		{"party old", clashAnyTLS, "mihomo.party/v1.7.1 (clash.meta)", false},
		{"koala", clashAnyTLS, "koala-clash/v0.2.10", true},
		{"flclashx", clashAnyTLS, "FlClashX/v0.2.1 Platform/android", true},
		{"bare clash.meta", clashAnyTLS, "clash.meta", false},
		{"cmfa", clashAnyTLS, "ClashMetaForAndroid/2.11.7", true},
		{"cmfa old", clashAnyTLS, "ClashMetaForAndroid/2.11.6", false},
		{"clashfest old", clashAnyTLS, "ClashMetaForAndroid/1.1.0", true},
		{"flclash", clashAnyTLS, "FlClash/v0.8.77 clash-verge Platform/android", true},
		{"flclash old", clashAnyTLS, "FlClash/v0.8.76 clash-verge Platform/android", false},
		{"verge", clashAnyTLS, "clash-verge/v2.2.2", true},
		{"verge old", clashAnyTLS, "clash-verge/v2.1.2", false},
		{"koala 0.2 as verge", clashAnyTLS, "clash-verge/v0.2.5", true},
		{"original verge 0.0", clashAnyTLS, "clash-verge/v0.0.29", false},
		{"original verge 0.0 mieru", clashMieru, "clash-verge/v0.0.29", false},
		{"nekobox clash", clashAnyTLS, "NekoBox/Android/1.3.8 (Prefer ClashMeta Format)", true},
		{"nekobox clash old", clashAnyTLS, "NekoBox/Android/1.3.7 (Prefer ClashMeta Format)", false},
		{"stash ios", clashAnyTLS, "Stash/3.3.1 Clash/1.11.0", true},
		{"stash ios old", clashAnyTLS, "Stash/3.2.0 Clash/1.11.0", false},
		{"stash mac 4.0", clashAnyTLS, "Stash/4.0.2 Clash/1.11.0", false},
		{"stash mac", clashAnyTLS, "Stash/4.1.0 Clash/1.11.0", true},
		{"stash mieru ios", clashMieru, "Stash/3.6.0 Clash/1.11.0", true},
		{"stash mieru ios old", clashMieru, "Stash/3.5.0 Clash/1.11.0", false},
		{"stash mieru mac", clashMieru, "Stash/4.2.0 Clash/1.11.0", false},
		{"flclash mieru", clashMieru, "FlClash/v0.8.72 clash-verge Platform/android", true},
		{"exclave", AnyTLSLinkFor, "Exclave/0.13.11", true},
		{"exclave old", AnyTLSLinkFor, "Exclave/0.13.10", false},
		{"exclave mieru", MieruLinkFor, "Exclave/0.17.0", true},
		{"exclave mieru old", MieruLinkFor, "Exclave/0.16.9", false},
		{"karing mieru", MieruLinkFor, "Karing/1.2.23.2601 android/14", true},
		{"karing mieru old", MieruLinkFor, "Karing/1.2.22.2500 android/14", false},
		{"v2rayn naive", func(ua string) bool { return NaiveLinkFor(ua) != nil }, "v2rayN/7.20.0", true},
		{"v2rayn naive old", func(ua string) bool { return NaiveLinkFor(ua) != nil }, "v2rayN/7.19.5", false},
		{"loon", AnyTLSLinkFor, "Loon/898 CFNetwork/1568.100.1 Darwin/24.0.0", true},
		{"sing-box 1.12", SingBoxAnyTLS, "SFA (sing-box 1.12.0; language en_US)", true},
		{"sing-box 1.11", SingBoxAnyTLS, "SFI (sing-box 1.11.9; language en_US)", false},
		{"husi", SingBoxAnyTLS, "husi/0.10.9", true},
		{"husi old", SingBoxAnyTLS, "husi/0.10.8", false},
		{"v2rayn", AnyTLSLinkFor, "v2rayN/7.14.0", true},
		{"v2rayn old", AnyTLSLinkFor, "v2rayN/7.13.9", false},
		{"karing", AnyTLSLinkFor, "Karing/1.1.2", true},
		{"nekobox", AnyTLSLinkFor, "NekoBox/1.3.6", true},
		{"browser link list", AnyTLSLinkFor, "", true},
		{"shadowrocket", AnyTLSLinkFor, "Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0", true},
		{"streisand", AnyTLSLinkFor, "Streisand/1.6.40", false},
	} {
		if got := c.fn(c.ua); got != c.want {
			t.Errorf("%s (%q) = %v, want %v", c.name, c.ua, got, c.want)
		}
	}
}
