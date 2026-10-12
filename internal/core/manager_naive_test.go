package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Where naive-in is on a loopback port, the port is held against custom inbounds on
// TCP, and only on TCP; on a socket it holds none, not even the port it once had.
func TestNaivePortIsReserved(t *testing.T) {
	r := reservedPorts(&model.Settings{VLESSPort: 443, RealityPort: 8443, HysteriaPort: 60000})
	p := xray.InnerPort(xray.NaiveInnerAddr)
	if p == 0 {
		for _, old := range []int{18443, 18444} {
			if _, ok := r.OnTCP(old); ok {
				t.Fatalf("TCP %d is reserved with the inner lanes on sockets", old)
			}
		}
		return
	}
	if _, ok := r.OnTCP(p); !ok {
		t.Fatalf("TCP %d is not reserved", p)
	}
	if _, ok := r.OnUDP(p); ok {
		t.Fatalf("UDP %d is reserved", p)
	}
}
