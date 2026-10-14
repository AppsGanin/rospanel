package core

import (
	"strings"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/hostfw"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncHostFW opens this server's listeners in the host firewall (internal/hostfw):
// Xray's public inbounds, the :443 front, :80 for ACME, and the panel's own lanes as
// they are running right now. Called at the end of every reconcile, which every
// settings path reaches.
func (m *Manager) syncHostFW(set *model.Settings, cfg *xray.Config) {
	var skip []string
	if !set.HysteriaEnabled {
		skip = append(skip, xray.TagHysteria)
	}
	var s hostfw.Spec
	s.Ports = append(xray.PublicListeners(cfg, skip...), hostfw.TCP(80), hostfw.TCP(set.VLESSPort))
	if m.awg != nil && m.awg.Running() {
		s.Ports = append(s.Ports, hostfw.UDP(set.AWGPort))
		s.Forward = append(s.Forward, awg.Iface)
	}
	if m.mieru != nil {
		s.Ports = append(s.Ports, MieruHostPort(m.mieru.Listening().Port, m.mieru.Listening().Transport)...)
	}
	for _, p := range m.turn.Ports() {
		s.Ports = append(s.Ports, hostfw.UDP(p))
	}
	hostfw.Ensure(s)
}

// MieruHostPort is a running mieru listener as a firewall port; none for a lane
// that is off.
func MieruHostPort(port int, transport string) []hostfw.Port {
	if port <= 0 {
		return nil
	}
	if strings.EqualFold(transport, model.MieruUDP) {
		return []hostfw.Port{hostfw.UDP(port)}
	}
	return []hostfw.Port{hostfw.TCP(port)}
}
