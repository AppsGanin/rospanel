package nodeagent

import (
	"context"
	"time"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/hostfw"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncHostFW opens this node's listeners in the host firewall (internal/hostfw): the
// public inbounds of the config it runs, the front on the VLESS port, :80 for ACME,
// and the tunnel, mieru and relay ports the panel sent.
func (a *Agent) syncHostFW(data []byte, m nodeapi.NodeMeta, front int) {
	var skip []string
	if !m.HysteriaEnabled {
		skip = append(skip, xray.TagHysteria) // up with no users while the lane is off
	}
	var s hostfw.Spec
	s.Ports = append(xray.PublicListenersRaw(data, skip...), hostfw.TCP(80))
	if front > 0 {
		s.Ports = append(s.Ports, hostfw.TCP(front))
	}
	if m.AWG != nil && m.AWG.Port > 0 {
		s.Ports = append(s.Ports, hostfw.UDP(m.AWG.Port))
		s.Forward = append(s.Forward, awg.Iface)
	}
	if m.Mieru != nil && m.Mieru.Port > 0 {
		p := hostfw.TCP(m.Mieru.Port)
		if m.Mieru.Transport == model.MieruUDP {
			p = hostfw.UDP(m.Mieru.Port)
		}
		s.Ports = append(s.Ports, p)
	}
	for _, r := range m.TurnRelays {
		s.Ports = append(s.Ports, hostfw.UDP(r.Port))
	}
	hostfw.Ensure(s)
}

func keepHostFW(ctx context.Context) { hostfw.Keep(ctx, time.Minute) }
