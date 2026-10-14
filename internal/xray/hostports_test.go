package xray

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/hostfw"
)

func TestPublicListeners(t *testing.T) {
	cfg := &Config{Inbounds: []Inbound{
		{Tag: "api", Listen: "127.0.0.1", Port: APIPort, Protocol: "dokodemo-door", Settings: DokodemoSettings{Address: "127.0.0.1"}},
		{Tag: TagVLESS, Listen: "@rospanel-vless", Protocol: "vless"}, // behind the front
		{Tag: TagHysteria, Listen: "0.0.0.0", Port: 443, Protocol: "hysteria"},
		{Tag: "reality-in", Listen: "0.0.0.0", Port: 8443, Protocol: "vless", StreamSettings: &StreamSettings{Network: "xhttp"}},
		{Tag: "ss", Listen: "0.0.0.0", Port: 8388, Protocol: "shadowsocks", Settings: ShadowsocksInboundSettings{Network: "tcp,udp"}},
		{Tag: "socks", Listen: "0.0.0.0", Port: 1080, Protocol: "socks", Settings: SocksInboundSettings{UDP: true}},
		{Tag: "http", Listen: "0.0.0.0", Port: 3128, Protocol: "http"},
		{Tag: "wg", Listen: "127.0.0.1", Port: 40000, Protocol: ProtoWireGuard}, // the relay holds the public port
		{Tag: "warp-in", Listen: "127.0.0.1", Port: 18081, Protocol: "socks", Settings: SocksInboundSettings{}},
		{Tag: "v6", Listen: "::", Port: 9000, Protocol: "trojan"},
	}}
	want := []hostfw.Port{
		hostfw.UDP(443), hostfw.TCP(8443),
		hostfw.TCP(8388), hostfw.UDP(8388),
		hostfw.TCP(1080), hostfw.UDP(1080),
		hostfw.TCP(3128), hostfw.TCP(9000),
	}
	if got := PublicListeners(cfg); !slices.Equal(got, want) {
		t.Fatalf("PublicListeners = %v\nwant %v", got, want)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := PublicListenersRaw(raw); !slices.Equal(got, want) {
		t.Fatalf("PublicListenersRaw = %v\nwant %v", got, want)
	}
	// The built-in Hysteria2 listener of a lane that is off is skipped, by tag.
	if got := PublicListenersRaw(raw, TagHysteria); slices.Contains(got, hostfw.UDP(443)) {
		t.Fatalf("skipped hysteria still opened: %v", got)
	}
	if got := PublicListeners(cfg, TagHysteria); slices.Contains(got, hostfw.UDP(443)) || len(got) != len(want)-1 {
		t.Fatalf("skip = %v", got)
	}
	// Xray also takes a port range as a string.
	got := PublicListenersRaw([]byte(`{"inbounds":[{"port":"20000-20010","protocol":"dokodemo-door","settings":{"network":"udp"}}]}`))
	if !slices.Equal(got, []hostfw.Port{{Proto: "udp", From: 20000, To: 20010}}) {
		t.Fatalf("range = %v", got)
	}
}
