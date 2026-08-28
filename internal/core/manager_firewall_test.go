package core

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

func TestHostFirewallRules(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Configure settings
	if err := st.SetVLESSPort(443); err != nil {
		t.Fatalf("set vless port: %v", err)
	}
	if err := st.SetProtocolEnabled("vless", true); err != nil {
		t.Fatalf("set vless enabled: %v", err)
	}
	if err := st.SetRealityPorts(8443, "dest.com:443"); err != nil {
		t.Fatalf("set reality ports: %v", err)
	}
	if err := st.SetProtocolEnabled("reality", true); err != nil {
		t.Fatalf("set reality enabled: %v", err)
	}
	if err := st.SetHysteriaPorts(9443, 20000, 25000, "10s"); err != nil {
		t.Fatalf("set hysteria ports: %v", err)
	}
	if err := st.SetProtocolEnabled("hysteria2", true); err != nil {
		t.Fatalf("set hysteria enabled: %v", err)
	}

	// Add custom inbounds
	_, err = st.CreateInbound(model.Inbound{
		ServerID: model.LocalNodeID,
		Name:     "custom-ss",
		Protocol: model.InbShadowsocks,
		Port:     10443,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("create custom inbound: %v", err)
	}

	_, err = st.CreateInbound(model.Inbound{
		ServerID: model.LocalNodeID,
		Name:     "custom-hy",
		Protocol: model.InbHysteria,
		Port:     11443,
		Enabled:  true,
		Opts: model.InboundOpts{
			HopStart: 30000,
			HopEnd:   35000,
		},
	})
	if err != nil {
		t.Fatalf("create custom hy inbound: %v", err)
	}

	rules, err := HostFirewallRules(st)
	if err != nil {
		t.Fatalf("HostFirewallRules failed: %v", err)
	}

	var formatted []string
	for _, r := range rules {
		formatted = append(formatted, r.Format())
	}
	sort.Strings(formatted)

	expected := []string{
		"10443/tcp",
		"11443/udp",
		"20000:25000/udp",
		"30000:35000/udp",
		"443/tcp",
		"80/tcp",
		"8443/tcp",
		"9443/udp",
	}
	sort.Strings(expected)

	if len(formatted) != len(expected) {
		t.Fatalf("rules length %d, want %d: got %v", len(formatted), len(expected), formatted)
	}
	for i := range expected {
		if formatted[i] != expected[i] {
			t.Errorf("rule[%d] = %q, want %q", i, formatted[i], expected[i])
		}
	}
}

func TestEnsureHostFirewall(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test_sync.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := EnsureHostFirewall(st); err != nil {
		t.Logf("EnsureHostFirewall returned (expected on non-linux/non-root): %v", err)
	}
}
