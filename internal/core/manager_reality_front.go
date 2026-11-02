package core

import (
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// A REALITY lane on the TCP-TLS lane's port is not a listener of its own: the front
// carries both and tells them apart by the name in the ClientHello (tlsfront.Front.
// Share). This file is the manager's half of that — it keeps the front's route in step
// with every config it applies.

// realitySharer is the front in front of the TCP-TLS lane's port (*tlsfront.Front):
// only what the manager needs from it, so this package does not depend on the listener.
type realitySharer interface {
	Share(target string, snis []string)
}

// SetFront gives the manager the front this process runs, so it can route REALITY by
// name. Called once at boot, before the first reconcile.
func (m *Manager) SetFront(f realitySharer) { m.front = f }

// shareFront makes the front send the donor names in cfg to the REALITY listener, or
// stop the split when the lane went back to a port of its own. Called after the config
// is applied, so a name never points at a listener Xray has not opened yet.
func (m *Manager) shareFront(set *model.Settings, cfg *xray.Config) {
	if m.front == nil {
		return
	}
	snis, behind := xray.RealityRoute(cfg)
	if !behind {
		m.front.Share("", nil)
		m.sup.SetPublicPort(xray.TagReality, 0)
		return
	}
	m.front.Share(xray.RealityInnerAddr, snis)
	m.sup.SetPublicPort(xray.TagReality, set.VLESSPort)
}
