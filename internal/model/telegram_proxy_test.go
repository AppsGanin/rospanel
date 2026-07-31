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

// These are the addresses the Routing page shows an operator to paste elsewhere, so
// they must be exactly what the running egress listens on — and empty whenever it is
// not running, because an address for a switched-off egress is a dead port dressed as
// an instruction.
func TestLocalEgressAddresses(t *testing.T) {
	off := Settings{}
	if got := off.WarpProxyURL(); got != "" {
		t.Errorf("WarpProxyURL() = %q with WARP off, want empty", got)
	}
	if got := off.OperaProxyURL(); got != "" {
		t.Errorf("OperaProxyURL() = %q with Opera off, want empty", got)
	}

	// Enabled but never registered: the Xray generator refuses to emit the WARP
	// outbound in that state, so its entrance does not exist either.
	half := Settings{WarpEnabled: true}
	if got := half.WarpProxyURL(); got != "" {
		t.Errorf("WarpProxyURL() = %q for an unregistered account, want empty", got)
	}

	on := Settings{WarpEnabled: true, WarpPrivateKey: "k", OperaEnabled: true}
	if got, want := on.WarpProxyURL(), "socks5://127.0.0.1:18081"; got != want {
		t.Errorf("WarpProxyURL() = %q, want %q", got, want)
	}
	if got, want := on.OperaProxyURL(), "http://127.0.0.1:18080"; got != want {
		t.Errorf("OperaProxyURL() = %q, want %q", got, want)
	}
	on.OperaPort = 19999
	if got, want := on.OperaProxyURL(), "http://127.0.0.1:19999"; got != want {
		t.Errorf("OperaProxyURL() did not follow the moved helper port: %q, want %q", got, want)
	}
}

// Startup waits only for an egress this panel brings up itself. Mistaking an
// operator's own proxy for ours would delay every boot on their outage; missing ours
// costs the bots a retry backoff of silence.
func TestIsLocalEgressProxy(t *testing.T) {
	s := Settings{WarpEnabled: true, WarpPrivateKey: "k", OperaEnabled: true}
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"socks5://127.0.0.1:18081", true}, // the WARP entrance
		{"http://127.0.0.1:18080", true},   // the Opera helper
		{"socks5://127.0.0.1:1080", false}, // something else on loopback is not ours
		{"http://127.0.0.1:18081", false},  // right port, wrong scheme
		{"socks5://10.0.0.1:18081", false}, // right port, not loopback
		{"socks5://example.com:1080", false},
	} {
		if got := s.IsLocalEgressProxy(tc.raw); got != tc.want {
			t.Errorf("IsLocalEgressProxy(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	// With the egresses off, their addresses stop being ours: nothing is listening,
	// so waiting for them at boot would just burn the budget.
	off := Settings{}
	if off.IsLocalEgressProxy("socks5://127.0.0.1:18081") {
		t.Error("claimed the WARP address as local while WARP is off")
	}
}
