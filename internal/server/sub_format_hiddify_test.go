package server

import "testing"

// Hiddify 5 and NyameBox 5.11.28 ask for Clash and get the link list; older ones keep it.
func TestSubFormatHiddify(t *testing.T) {
	t.Parallel()
	for ua, want := range map[string]string{
		"HiddifyNext/5.5.5 (android) like ClashMeta v2ray sing-box": "v2ray",
		"HiddifyNext/5.9.1 (macos) like ClashMeta v2ray sing-box":   "v2ray",
		"HiddifyNext/4.1.1 (android) like ClashMeta v2ray sing-box": "clash",
		"HiddifyNext/5.4.0 (android) like ClashMeta v2ray sing-box": "clash",
		"nekobox/5.11.28.3 (Prefer ClashMeta Format)":               "v2ray",
		"nekobox/5.11.24 (Prefer ClashMeta Format)":                 "clash",
		"NekoBox/Android/1.4.2 (Prefer ClashMeta Format)":           "clash",
	} {
		if got := subFormatFor(ua); got != want {
			t.Errorf("subFormatFor(%q) = %s, want %s", ua, got, want)
		}
	}
}
