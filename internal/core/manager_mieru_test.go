package core

import (
	"net"
	"testing"

	"github.com/AppsGanin/rospanel/internal/mieru"
	"github.com/AppsGanin/rospanel/internal/model"
)

func TestResolveMieruPort(t *testing.T) {
	none := model.ReservedPorts{}
	// Off keeps what there is; nothing is picked or checked.
	if p, tr, err := resolveMieruPort(0, "", false, false, 0, model.MieruTCP, none, true); err != nil || p != 0 || tr != model.MieruTCP {
		t.Fatalf("off: %d %s %v", p, tr, err)
	}
	// On without a port: one is picked.
	if p, _, err := resolveMieruPort(0, "", true, false, 0, model.MieruTCP, none, true); err != nil || p < mieruPortMin || p >= mieruPortMax {
		t.Fatalf("picked %d %v", p, err)
	}
	// Bad input.
	if _, _, err := resolveMieruPort(70000, "", true, false, 0, model.MieruTCP, none, true); err == nil {
		t.Fatal("port 70000 taken")
	}
	for _, tr := range []string{"QUIC", "UDP"} { // UDP: see model.MieruTransportOr
		if _, _, err := resolveMieruPort(1234, tr, true, false, 0, model.MieruTCP, none, true); err == nil {
			t.Fatalf("transport %s taken", tr)
		}
	}
	// A port another listener of the server holds is refused, and a picked one steps
	// past it; the Hysteria hop range is UDP, which mieru is not.
	held := reservedPorts(&model.Settings{VLESSPort: 20000, RealityPort: 20001, HysteriaPort: 20002, HopStart: 20002, HopEnd: 20100})
	if _, _, err := resolveMieruPort(20001, "TCP", true, false, 0, model.MieruTCP, held, false); err == nil {
		t.Fatal("the REALITY port was taken")
	}
	if _, _, err := resolveMieruPort(20050, "TCP", true, false, 0, model.MieruTCP, held, false); err != nil {
		t.Fatalf("the hop range is UDP only: %v", err)
	}
	if p := clearOf(held, model.MieruTCP, 20000); p != 20002 {
		t.Fatalf("stepped to %d, want 20002", p)
	}
	// A port something here holds is refused for the master, not for a node (whose
	// agent reports it), and not when it is the lane's own.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	if _, _, err := resolveMieruPort(busy, "TCP", true, false, 0, model.MieruTCP, none, true); err == nil {
		t.Fatal("a busy port was taken for the master")
	}
	if p, _, err := resolveMieruPort(busy, "TCP", true, false, 0, model.MieruTCP, none, false); err != nil || p != busy {
		t.Fatalf("node: %d %v", p, err)
	}
	if _, _, err := resolveMieruPort(busy, "TCP", true, true, busy, model.MieruTCP, none, true); err != nil {
		t.Fatalf("the lane's own port: %v", err)
	}
}

func TestDefaultMieruPort(t *testing.T) {
	a, b := defaultMieruPort("key-a"), defaultMieruPort("key-b")
	if a != defaultMieruPort("key-a") || a == b || a < mieruPortMin || a >= mieruPortMax || b < mieruPortMin || b >= mieruPortMax {
		t.Fatalf("%d %d", a, b)
	}
}

// The port is checked against the ports the save is about to put in place, the system
// proxy's included — not against the ones there were.
func TestMieruPortAgainstTheSave(t *testing.T) {
	set := model.Settings{VLESSPort: 443, RealityPort: 8443, HysteriaPort: 443, HopStart: 443, HopEnd: 443, ProxySocksPort: 10808}
	u := ConnectionsUpdate{Protocols: map[string]bool{"reality": true}, RealityPort: 21000, HysteriaPort: 443, HopStart: 443, HopEnd: 443}
	held := mieruHeldOn(withConnPorts(set, u, 0), nil)
	if _, _, err := resolveMieruPort(21000, "TCP", true, false, 0, model.MieruTCP, held, false); err == nil {
		t.Fatal("mieru took the port REALITY is moving to in the same save")
	}
	if _, _, err := resolveMieruPort(8443, "TCP", true, false, 0, model.MieruTCP, held, false); err != nil {
		t.Fatalf("REALITY's old port is free after the save: %v", err)
	}
	if _, _, err := resolveMieruPort(10808, "TCP", true, false, 0, model.MieruTCP, held, false); err == nil {
		t.Fatal("mieru took the system proxy's port")
	}
	custom := []model.Inbound{{Name: "old", Protocol: model.InbVLESS, Port: 22000}}
	if _, _, err := resolveMieruPort(22000, "TCP", true, false, 0, model.MieruTCP, mieruHeldOn(&set, custom), false); err == nil {
		t.Fatal("mieru took a custom inbound's port")
	}
}

// Whatever got there, mieru never listens on a port the running Xray config holds.
func TestMieruStaysOffXraysPorts(t *testing.T) {
	l := mieru.Listen{Port: 22000, Transport: mieru.TCP}
	if got, err := mieruClearOfXray(l, map[int]string{22000: "custom-3"}); err == nil || got.Port != 0 {
		t.Fatalf("listen %+v err %v, want off with an error", got, err)
	}
	if got, err := mieruClearOfXray(l, map[int]string{443: "vless-in"}); err != nil || got != l {
		t.Fatalf("listen %+v err %v", got, err)
	}
}
