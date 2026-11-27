package model

import "testing"

func TestParseSubClient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ua, os                 string
		app, version, platform string
	}{
		{"Happ/3.7.0", "iOS", "Happ", "3.7.0", PlatformIOS},
		{"Happ/1.6.3/Android", "", "Happ", "1.6.3", PlatformAndroid},
		{"v2rayNG/1.9.30", "", "v2rayNG", "1.9.30", PlatformAndroid},
		{"v2rayN/7.4.2", "", "v2rayN", "7.4.2", ""},
		{"v2rayTun/5.12.65 (iOS 18.0)", "", "v2RayTun", "5.12.65", PlatformIOS},
		{"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box", "", "Hiddify", "2.5.7", PlatformAndroid},
		{"FlClash/v0.8.70 clash-verge Platform/windows", "", "FlClash", "0.8.70", PlatformWindows},
		{"clash-verge/v2.0.3", "", "Clash Verge", "2.0.3", ""},
		{"FlClashX/v0.5.7 core/mihomo", "Android", "FlClashX", "0.5.7", PlatformAndroid},
		{"Happ/4.11.2/Android/1764000000", "", "Happ", "4.11.2", PlatformAndroid},
		{"ClashMetaForAndroid/2.10.1.Meta", "", "Clash Meta for Android", "2.10.1", PlatformAndroid},
		{"Shadowrocket/2070 CFNetwork/1568.100.1 Darwin/24.0.0", "", "Shadowrocket", "2070", PlatformIOS},
		{"NekoBox/Android/1.3.4 (Prefer ClashMeta Format)", "", "NekoBox", "1.3.4", PlatformAndroid},
		{"SFI/1.11.4 (Build 1; sing-box 1.11.4)", "", "sing-box", "1.11.4", PlatformIOS},
		{"sing-box 1.12.0", "", "sing-box", "1.12.0", ""},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", "", "Browser", "", PlatformWindows},
		{"MyClient/2.1 (linux)", "", "MyClient", "2.1", PlatformLinux},
		{"<script>/1", "", "script", "1", ""},
		{"", "", AppUnknown, "", ""},
		// Lower-casing may change a string's byte length (U+023A is two bytes, its
		// lower case three), so a match found in the lower-cased copy must never be
		// used as an index into the original: that panics.
		{"ȺȺȺȺȺhapp", "", "Happ", "", ""},
		{"ȺȺȺȺȺȺȺȺȺȺsing-box 1.12.0", "", "sing-box", "1.12.0", ""},
	}
	for _, c := range cases {
		got := ParseSubClient(c.ua, c.os)
		want := SubClientInfo{App: c.app, Version: c.version, Platform: c.platform}
		if got != want {
			t.Errorf("ParseSubClient(%q, %q) = %+v, want %+v", c.ua, c.os, got, want)
		}
	}
}

func TestSubClientKey(t *testing.T) {
	t.Parallel()
	info := SubClientInfo{App: "Happ", Platform: PlatformIOS}
	if got := SubClientKey("abc", info); got != "h:abc" {
		t.Errorf("with hwid: %q", got)
	}
	if got := SubClientKey("", info); got != "a:Happ|iOS" {
		t.Errorf("without hwid: %q", got)
	}
}
