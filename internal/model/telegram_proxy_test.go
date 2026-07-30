package model

import "testing"

// One resolver decides where every Telegram-bound byte goes, so its mapping is worth
// pinning: a wrong answer here is a panel whose bots go quiet with a saved,
// plausible-looking setting.
func TestTelegramProxyURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  Settings
		want string
	}{
		{
			name: "unset is direct",
			set:  Settings{},
		},
		{
			name: "explicit direct ignores a leftover custom URL",
			set:  Settings{TGProxyMode: TGProxyDirect, TGProxy: "socks5://10.0.0.1:1080"},
		},
		{
			name: "a mode the panel no longer has falls back to direct rather than guessing",
			set:  Settings{TGProxyMode: "warp"},
		},
		{
			name: "custom returns the operator's URL",
			set:  Settings{TGProxyMode: TGProxyCustom, TGProxy: " socks5://user:pass@10.0.0.1:1080 "},
			want: "socks5://user:pass@10.0.0.1:1080",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.TelegramProxyURL(); got != tc.want {
				t.Errorf("TelegramProxyURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The custom URL must survive a switch to direct: an operator turning the proxy off
// for a minute should find their typed address still in the box afterwards.
func TestTelegramProxyKeepsCustomURLAcrossModes(t *testing.T) {
	s := Settings{TGProxyMode: TGProxyDirect, TGProxy: "socks5://10.0.0.1:1080"}
	if got := s.TelegramProxyURL(); got != "" {
		t.Fatalf("direct mode resolved to %q, want direct", got)
	}
	s.TGProxyMode = TGProxyCustom
	if got := s.TelegramProxyURL(); got != "socks5://10.0.0.1:1080" {
		t.Errorf("the custom URL did not survive the round trip; got %q", got)
	}
}
