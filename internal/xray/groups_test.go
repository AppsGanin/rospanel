package xray

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
)

var testGroups = geo.GroupSet{
	"russia/vk": {
		Domains: []string{"vk.com", "vk.ru"},
		IPs:     []string{"87.240.128.0/18", "2a00:bdc0::/32"},
	},
	"global/ai": {
		Domains: []string{"claude.ai", "openai.com"},
		IPs:     []string{"104.18.0.0/16"},
	},
}

func TestExpandGroupsByField(t *testing.T) {
	rc := model.RoutingConfig{
		DirectDomains: []string{"example.com", "iplist:russia/vk"},
		DirectIPs:     []string{"iplist:russia/vk", "1.2.3.4/32"},
	}
	got := expandGroups(rc, testGroups)

	// The same ref resolves by the field it sits in: domains in a domain list,
	// CIDRs in an IP list. Order is preserved around the expansion.
	wantDomains := []string{"example.com", "vk.com", "vk.ru"}
	if !slices.Equal(got.DirectDomains, wantDomains) {
		t.Errorf("DirectDomains = %v, want %v", got.DirectDomains, wantDomains)
	}
	wantIPs := []string{"87.240.128.0/18", "2a00:bdc0::/32", "1.2.3.4/32"}
	if !slices.Equal(got.DirectIPs, wantIPs) {
		t.Errorf("DirectIPs = %v, want %v", got.DirectIPs, wantIPs)
	}
}

func TestExpandGroupsAllFields(t *testing.T) {
	ref := []string{"iplist:global/ai"}
	rc := model.RoutingConfig{
		BlockDomains: ref, BlockIPs: ref,
		WarpDomains: ref, WarpIPs: ref,
		OperaDomains: ref, OperaIPs: ref,
		DirectDomains: ref, DirectIPs: ref,
		Lanes: []model.EgressLane{{ID: "ru", Domains: ref, IPs: ref}},
	}
	got := expandGroups(rc, testGroups)
	for name, list := range map[string][]string{
		"BlockDomains": got.BlockDomains, "BlockIPs": got.BlockIPs,
		"WarpDomains": got.WarpDomains, "WarpIPs": got.WarpIPs,
		"OperaDomains": got.OperaDomains, "OperaIPs": got.OperaIPs,
		"DirectDomains": got.DirectDomains, "DirectIPs": got.DirectIPs,
		"Lanes[0].Domains": got.Lanes[0].Domains, "Lanes[0].IPs": got.Lanes[0].IPs,
	} {
		if slices.Contains(list, "iplist:global/ai") {
			t.Errorf("%s: ref survived expansion: %v", name, list)
		}
		if len(list) == 0 {
			t.Errorf("%s: expanded to nothing", name)
		}
	}
	// Expansion must not write through to the caller's lane slice.
	if rc.Lanes[0].Domains[0] != "iplist:global/ai" {
		t.Errorf("caller's lanes were mutated: %v", rc.Lanes[0].Domains)
	}
}

func TestExpandGroupsUnknownRefDropped(t *testing.T) {
	rc := model.RoutingConfig{
		DirectDomains: []string{"iplist:russia/nosuchgroup", "keep.me"},
		DirectIPs:     []string{"iplist:global/nosuchgroup"},
	}
	got := expandGroups(rc, testGroups)
	if !slices.Equal(got.DirectDomains, []string{"keep.me"}) {
		t.Errorf("unknown ref not dropped: %v", got.DirectDomains)
	}
	if len(got.DirectIPs) != 0 {
		t.Errorf("unknown ref not dropped: %v", got.DirectIPs)
	}
}

// A ref must never reach Xray verbatim. normDomains passes anything containing
// ":" through as a matcher, so a leak would be emitted as a literal domain rule
// — silently matching nothing, or being rejected outright.
func TestGeneratedConfigNeverContainsRef(t *testing.T) {
	rc := model.RoutingConfig{
		DirectDomains: []string{"iplist:russia/vk"},
		DirectIPs:     []string{"iplist:russia/vk"},
		// An unresolvable ref (empty group set at generation time) must vanish too.
		WarpDomains: []string{"iplist:global/ai"},
	}
	for _, tc := range []struct {
		name   string
		groups geo.GroupSet
	}{
		{"resolved", testGroups},
		{"no databases", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Generate(laneSettings(rc), nil,
				Options{PanelDest: "127.0.0.1:8080", Groups: tc.groups}, nil)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if strings.Contains(string(raw), geo.RefPrefix) {
				t.Errorf("generated config leaked an %s ref:\n%s", geo.RefPrefix, raw)
			}
		})
	}
}

func TestGeneratedConfigRoutesGroupDomains(t *testing.T) {
	rc := model.RoutingConfig{
		Lanes:        []model.EgressLane{{ID: "ru", Name: "RU", Enabled: true, Domains: []string{"iplist:russia/vk"}}},
		RoutingOrder: []string{"ru", "warp", "opera", "direct"},
	}
	cfg, err := Generate(laneSettings(rc), nil,
		Options{PanelDest: "127.0.0.1:8080", Groups: testGroups},
		map[string][]model.ProxyEndpoint{"ru": {ep("10.0.0.1")}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := ruleTarget(cfg, "domain:vk.com"); got != laneBalancerTag("ru") {
		t.Errorf("vk.com routed to %q, want the ru lane balancer", got)
	}
}
