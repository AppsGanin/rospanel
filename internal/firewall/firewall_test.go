package firewall

import (
	"context"
	"sort"
	"testing"
)

func TestRuleNormalizeAndFormat(t *testing.T) {
	tests := []struct {
		name       string
		in         Rule
		wantValid  bool
		wantFormat string
	}{
		{
			name:       "valid single tcp port",
			in:         Rule{PortStart: 443, Proto: "tcp"},
			wantValid:  true,
			wantFormat: "443/tcp",
		},
		{
			name:       "valid single udp port",
			in:         Rule{PortStart: 8443, Proto: "UDP"},
			wantValid:  true,
			wantFormat: "8443/udp",
		},
		{
			name:       "valid udp port range",
			in:         Rule{PortStart: 20000, PortEnd: 30000, Proto: "udp"},
			wantValid:  true,
			wantFormat: "20000:30000/udp",
		},
		{
			name:       "identical start and end collapsed to single port",
			in:         Rule{PortStart: 80, PortEnd: 80, Proto: "tcp"},
			wantValid:  true,
			wantFormat: "80/tcp",
		},
		{
			name:       "no proto specified",
			in:         Rule{PortStart: 8080},
			wantValid:  true,
			wantFormat: "8080",
		},
		{
			name:      "invalid zero port",
			in:        Rule{PortStart: 0, Proto: "tcp"},
			wantValid: false,
		},
		{
			name:      "invalid negative port",
			in:        Rule{PortStart: -1, Proto: "tcp"},
			wantValid: false,
		},
		{
			name:      "invalid port above 65535",
			in:        Rule{PortStart: 70000, Proto: "tcp"},
			wantValid: false,
		},
		{
			name:      "invalid port end smaller than start",
			in:        Rule{PortStart: 5000, PortEnd: 4000, Proto: "udp"},
			wantValid: false,
		},
		{
			name:      "invalid proto",
			in:        Rule{PortStart: 80, Proto: "icmp"},
			wantValid: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			norm, ok := tc.in.Normalize()
			if ok != tc.wantValid {
				t.Fatalf("Normalize() ok = %v, want %v", ok, tc.wantValid)
			}
			if ok && norm.Format() != tc.wantFormat {
				t.Errorf("Format() = %q, want %q", norm.Format(), tc.wantFormat)
			}
		})
	}
}

func TestDeduplicateRules(t *testing.T) {
	rules := []Rule{
		{PortStart: 80, Proto: "tcp"},
		{PortStart: 80, Proto: "TCP"},
		{PortStart: 443, Proto: "tcp"},
		{PortStart: 20000, PortEnd: 30000, Proto: "udp"},
		{PortStart: 20000, PortEnd: 30000, Proto: "UDP"},
		{PortStart: 0, Proto: "tcp"}, // invalid, should be filtered out
	}

	deduped := DeduplicateRules(rules)
	if len(deduped) != 3 {
		t.Fatalf("DeduplicateRules() returned %d rules, want 3", len(deduped))
	}

	var formatted []string
	for _, r := range deduped {
		formatted = append(formatted, r.Format())
	}
	sort.Strings(formatted)

	expected := []string{"20000:30000/udp", "443/tcp", "80/tcp"}
	for i, exp := range expected {
		if formatted[i] != exp {
			t.Errorf("rule[%d] = %q, want %q", i, formatted[i], exp)
		}
	}
}

func TestParseSSHDConfigPorts(t *testing.T) {
	sample := `
# Package generated configuration file
# See the sshd_config(5) manpage for details

# What ports, IPs and protocols we listen for
Port 2222
# Port 22
ListenAddress 0.0.0.0
ListenAddress ::
# Authentication:
Port 2200
#Port 3333
port 4444
`
	ports := parseSSHDConfigPorts(sample)
	sort.Ints(ports)

	expected := []int{2200, 2222, 4444}
	if len(ports) != len(expected) {
		t.Fatalf("parseSSHDConfigPorts() found %v, want %v", ports, expected)
	}
	for i, exp := range expected {
		if ports[i] != exp {
			t.Errorf("port[%d] = %d, want %d", i, ports[i], exp)
		}
	}
}

func TestDetectSSHPortsContains22(t *testing.T) {
	ports := DetectSSHPorts()
	found22 := false
	for _, p := range ports {
		if p == 22 {
			found22 = true
			break
		}
	}
	if !found22 {
		t.Fatalf("DetectSSHPorts() should always include port 22, got %v", ports)
	}
}

func TestConstructors(t *testing.T) {
	r1 := TCPRule(443, "https")
	if r1.PortStart != 443 || r1.Proto != "tcp" || r1.Comment != "https" {
		t.Errorf("TCPRule failed: %+v", r1)
	}

	r2 := UDPRule(8443, "hysteria")
	if r2.PortStart != 8443 || r2.Proto != "udp" || r2.Comment != "hysteria" {
		t.Errorf("UDPRule failed: %+v", r2)
	}

	r3 := UDPRangeRule(20000, 30000, "hop")
	if r3.PortStart != 20000 || r3.PortEnd != 30000 || r3.Proto != "udp" || r3.Comment != "hop" {
		t.Errorf("UDPRangeRule failed: %+v", r3)
	}
}

func TestSyncGracefulOnNonLinuxOrNonRoot(t *testing.T) {
	ctx := context.Background()
	// Should not panic or fail in test environment
	err := Sync(ctx, []Rule{
		TCPRule(80, "http"),
		TCPRule(443, "vless"),
		UDPRangeRule(20000, 30000, "hop"),
	})
	if err != nil {
		t.Logf("Sync returned (expected on non-linux/non-root): %v", err)
	}
}
