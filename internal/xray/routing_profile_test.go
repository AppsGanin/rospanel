package xray

import (
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestRoutingProfileIsolationAndSecurityFloor(t *testing.T) {
	rc := model.RoutingConfig{
		BlockBittorrent: true,
		BlockAds:        true,
		BlockDomains:    []string{"evil.com"},
		BlockIPs:        []string{"1.2.3.4/32"},
		DirectDomains:   []string{"example.com"},
	}

	order := []string{"direct"}
	routing := compileRouting(rc, order, "direct", false, false, map[string]bool{})

	if len(routing.Rules) < 4 {
		t.Fatalf("expected at least 4 routing rules, got %d", len(routing.Rules))
	}

	// 1. First rule MUST be API dispatch
	if len(routing.Rules[0].InboundTag) != 1 || routing.Rules[0].InboundTag[0] != "api" {
		t.Errorf("rule 0 must be api inbound, got %+v", routing.Rules[0])
	}

	// 2. Second rule MUST be private IP/CIDR block (security floor)
	foundPrivateIPFloor := false
	for _, r := range routing.Rules[:4] {
		if r.OutboundTag == "block" && len(r.IP) > 0 {
			foundPrivateIPFloor = true
			break
		}
	}
	if !foundPrivateIPFloor {
		t.Errorf("security floor private IP rule not found in top rules")
	}

	// 3. Block rules must precede egress rules
	var lastBlockIdx, firstDirectIdx int = -1, -1
	for idx, r := range routing.Rules {
		if r.OutboundTag == "block" {
			lastBlockIdx = idx
		}
		if r.OutboundTag == "direct" && firstDirectIdx == -1 {
			firstDirectIdx = idx
		}
	}

	if firstDirectIdx != -1 && lastBlockIdx > firstDirectIdx {
		t.Errorf("block rule at %d appeared after direct rule at %d", lastBlockIdx, firstDirectIdx)
	}
}
