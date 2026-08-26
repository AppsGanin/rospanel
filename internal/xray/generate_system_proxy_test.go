package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// inboundByTag finds a generated inbound, so the assertions below read as questions
// about one listener rather than about an index in a slice.
func inboundByTag(t *testing.T, cfg *Config, tag string) *Inbound {
	t.Helper()
	for i := range cfg.Inbounds {
		if cfg.Inbounds[i].Tag == tag {
			return &cfg.Inbounds[i]
		}
	}
	return nil
}

func systemProxySettings() *model.Settings {
	set := baseSettings()
	set.ProxySocksEnabled, set.ProxySocksPort = true, 1080
	set.ProxyHTTPEnabled, set.ProxyHTTPPort = true, 3128
	set.ProxyAccounts = []model.SystemProxyAccount{
		{User: "sys", Pass: "sys-pass"},
		{User: "bot", Pass: "bot-pass"},
	}
	return set
}

// Both listeners are generated, both demand the server's account, and SOCKS carries
// UDP — a SOCKS proxy that silently drops UDP looks to the caller like the
// destination is down.
func TestSystemProxyInboundsGenerated(t *testing.T) {
	cfg, err := Generate(systemProxySettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	socks := inboundByTag(t, cfg, "system-socks-in")
	http := inboundByTag(t, cfg, "system-http-in")
	if socks == nil || http == nil {
		t.Fatalf("missing listeners: socks=%v http=%v", socks != nil, http != nil)
	}
	if socks.Port != 1080 || http.Port != 3128 {
		t.Errorf("ports = %d/%d, want 1080/3128", socks.Port, http.Port)
	}
	if socks.Listen != "0.0.0.0" || http.Listen != "0.0.0.0" {
		t.Errorf("a system proxy must be reachable from the network: %q/%q", socks.Listen, http.Listen)
	}
	s, ok := socks.Settings.(SocksInboundSettings)
	if !ok || s.Auth != "password" || len(s.Accounts) != 2 || s.Accounts[0].User != "sys" {
		t.Fatalf("socks auth = %+v, want both of the server's accounts", socks.Settings)
	}
	if !s.UDP {
		t.Error("SOCKS without UDP breaks DNS and QUIC through the proxy")
	}
	h, ok := http.Settings.(HTTPInboundSettings)
	if !ok || len(h.Accounts) != 2 || h.Accounts[1].User != "bot" {
		t.Fatalf("http auth = %+v, want both of the server's accounts", http.Settings)
	}
	// Sniffing is what makes "it leaves where the VPN leaves" true: without it the
	// domain rules can't see where this traffic is going.
	if socks.Sniffing == nil || !socks.Sniffing.Enabled {
		t.Error("system proxy traffic must be sniffed or the routing rules can't match it")
	}
}

// Credentials are the one thing that cannot be skipped: an open proxy on a public
// port is a spam relay within hours. A configuration missing them produces NO
// listener at all rather than an anonymous one.
func TestSystemProxyNeverOpensAnonymously(t *testing.T) {
	set := systemProxySettings()
	set.ProxyAccounts = nil
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if inboundByTag(t, cfg, "system-socks-in") != nil || inboundByTag(t, cfg, "system-http-in") != nil {
		t.Fatal("a proxy without a password was generated")
	}
	// And the password never leaks into a config that has no proxy in it.
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "sys-pass") {
		t.Error("the proxy account is in the config despite no listener using it")
	}
}

// Off means off: no listener, and nothing left over from the disabled protocol.
func TestSystemProxyOneProtocolOnly(t *testing.T) {
	set := systemProxySettings()
	set.ProxyHTTPEnabled = false
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if inboundByTag(t, cfg, "system-socks-in") == nil {
		t.Fatal("the enabled SOCKS listener is missing")
	}
	if inboundByTag(t, cfg, "system-http-in") != nil {
		t.Fatal("a disabled protocol still produced a listener")
	}
}
