package xray

import (
	"encoding/json"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Generate must be deterministic: the same inputs have to marshal to the same bytes,
// every time.
//
// The whole point of skipping a restart for an unchanged config (see
// TestApplyIsANoOpForAnIdenticalConfig) rests on this. One map iterated into a slice
// somewhere in here and the output reorders itself between calls — the comparison
// then never matches, every save restarts Xray again, and the regression is invisible
// because everything still "works".
func TestGenerateIsDeterministic(t *testing.T) {
	set := warpSettings()
	set.OperaEnabled = true
	set.RealityEnabled, set.RealityPrivateKey = true, "priv"
	set.RealityPublicKey, set.RealityShortID = "pub", "aabb"
	set.RealityDest = "www.microsoft.com:443"
	set.ProxySocksEnabled, set.ProxySocksPort = true, 1080
	set.ProxyHTTPEnabled, set.ProxyHTTPPort = true, 3128
	set.ProxyAccounts = []model.SystemProxyAccount{{User: "sys", Pass: "sys-pass"}}
	set.XrayDNS = "1.1.1.1\n8.8.8.8"
	set.Routing = model.RoutingConfig{
		BlockAds:        true,
		BlockBittorrent: true,
		BlockDomains:    []string{"a.example", "b.example"},
		BlockIPs:        []string{"10.1.0.0/16", "10.2.0.0/16"},
		WarpDomains:     []string{"chat.openai.com", "claude.ai"},
		WarpIPs:         []string{"1.2.3.0/24"},
		OperaDomains:    []string{"netflix.com"},
		DirectDomains:   []string{"vk.com", "ya.ru"},
		RoutingOrder:    []string{"warp", "opera", "direct"},
	}

	users := []model.User{
		{ID: 1, Name: "one", UUID: "11111111-1111-1111-1111-111111111111", Password: "p1", Enabled: true},
		{ID: 2, Name: "two", UUID: "22222222-2222-2222-2222-222222222222", Password: "p2", Enabled: true},
		{ID: 3, Name: "three", UUID: "33333333-3333-3333-3333-333333333333", Password: "p3", Enabled: true},
	}
	opts := Options{
		PanelDest: "127.0.0.1:8080",
		Custom: []model.Inbound{
			{ID: 7, ServerID: 0, Enabled: true, Name: "extra", Protocol: "vless", Port: 9443,
				Opts: model.InboundOpts{Transport: "tcp", Security: "tls"}},
		},
		// A per-user access map: this one IS a map, so if anything ranged over it
		// directly the output would shuffle between runs.
		Access: map[int64]model.Access{
			1: {Tokens: map[string]bool{"srv:0:vless": true, "srv:0:hysteria": true}},
			2: {Tokens: map[string]bool{"srv:0:vless": true}},
			3: {Tokens: map[string]bool{"srv:0:reality": true, "inbound:7": true}},
		},
	}
	proxies := map[string][]model.ProxyEndpoint{}

	first, err := marshalConfig(t, set, users, opts, proxies)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Several rounds: a map with few keys can happen to iterate in the same order
	// twice in a row, and this has to fail loudly rather than one run in ten.
	for i := 0; i < 12; i++ {
		next, err := marshalConfig(t, set, users, opts, proxies)
		if err != nil {
			t.Fatalf("generate (round %d): %v", i, err)
		}
		if next != first {
			t.Fatalf("round %d produced different bytes — the config is not deterministic, "+
				"so an unchanged save would restart Xray every time", i)
		}
	}
}

func marshalConfig(t *testing.T, set *model.Settings, users []model.User, opts Options,
	proxies map[string][]model.ProxyEndpoint) (string, error) {
	t.Helper()
	cfg, err := Generate(set, users, opts, proxies)
	if err != nil {
		return "", err
	}
	// The same marshalling Supervisor.Apply uses, since that is what gets compared.
	b, err := json.MarshalIndent(cfg, "", "  ")
	return string(b), err
}
