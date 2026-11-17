package xray

import "testing"

// The lanes keep the user-carrying inbounds, and NaiveProxy's bytes — which the TCP-TLS
// inbound's fallback counted too — come off VLESS.
func TestLanesOf(t *testing.T) {
	got := lanesOf(map[string]Traffic{
		TagVLESS:       {Up: 1500, Down: 9000},
		TagNaive:       {Up: 1000, Down: 8000},
		TagReality:     {Up: 10, Down: 20},
		"custom-7":     {Up: 3, Down: 4},
		"api":          {Up: 400, Down: 200},
		panelEgressTag: {Up: 5, Down: 5},
		TagHysteria:    {},
	})
	want := map[string]Traffic{
		TagVLESS: {Up: 500, Down: 1000}, TagNaive: {Up: 1000, Down: 8000},
		TagReality: {Up: 10, Down: 20}, "custom-7": {Up: 3, Down: 4},
	}
	if len(got) != len(want) {
		t.Fatalf("lanes %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %+v, want %+v", k, got[k], v)
		}
	}
	// All of VLESS's bytes were NaiveProxy's: no VLESS lane left.
	if l := lanesOf(map[string]Traffic{TagVLESS: {Up: 10, Down: 10}, TagNaive: {Up: 10, Down: 12}}); len(l) != 1 {
		t.Fatalf("lanes %v", l)
	}
}

func TestFoldInboundStat(t *testing.T) {
	out := map[string]Traffic{}
	foldInboundStat(out, "inbound>>>vless-in>>>traffic>>>uplink", 7)
	foldInboundStat(out, "inbound>>>vless-in>>>traffic>>>downlink", 9)
	foldInboundStat(out, "user>>>u1>>>traffic>>>uplink", 100)
	foldInboundStat(out, "outbound>>>direct>>>traffic>>>uplink", 100)
	foldInboundStat(out, "user>>>bridge-3>>>traffic>>>downlink", 5)
	if len(out) != 2 || out["vless-in"] != (Traffic{Up: 7, Down: 9}) || out["bridge-3"] != (Traffic{Down: 5}) {
		t.Fatalf("folded %v", out)
	}
}

func TestBridgeLaneEntry(t *testing.T) {
	for key, want := range map[string]int64{"bridge-0": 0, "bridge-12": 12} {
		if id, ok := BridgeLaneEntry(key); !ok || id != want {
			t.Fatalf("%s: %d %v", key, id, ok)
		}
	}
	for _, key := range []string{"bridge-", "bridge-01", "bridge--1", "bridge-1a", "u1", "vless-in", "bridge-99999999999999999999"} {
		if _, ok := BridgeLaneEntry(key); ok {
			t.Fatalf("%s accepted", key)
		}
	}
}
