package core

import (
	"context"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Factory reset of a server's connection surface: the state a fresh install has.
// Everything the connections editor can set goes back to its default, and the
// custom inbounds of that server go away with the grants that named them. Keys —
// REALITY, AmneziaWG — stay: a reset is "make it work like new", not "log every
// client out", and regenerating keys is its own explicit button.
//
// The reset is applied through the same path as any save (ApplyConnections /
// ApplyNodeConnections), so it is validated, reconciled and audited like one; the
// master takes an automatic config snapshot first, so a reset pressed in the wrong
// dialog is one rollback away.

// DefaultConnections is the factory state, as an update.
func DefaultConnections() ConnectionsUpdate {
	return ConnectionsUpdate{
		Protocols:    map[string]bool{"vless": true, "reality": true, "hysteria2": true, "awg": false},
		Fingerprints: map[string]string{"vless": "firefox", "reality": "firefox"},
		Names:        map[string]string{},
		HysteriaPort: 443, HopStart: 443, HopEnd: 443, HopInterval: "5-10",
		RealityPort: 8443, RealityDest: model.DefaultRealityDest,
		TLSFragment: true, TLSMin13: true, BlockQUIC: true,
	}
}

// ResetConnections resets the master and returns the surface as it now is.
func (m *Manager) ResetConnections(ctx context.Context) (*ConnectionsStatus, error) {
	// Undo point. Best-effort: a failed snapshot must not block the reset the
	// operator asked for, and the failure is logged by the snapshot code.
	_, _ = m.snapshotCurrentConfig("", true)
	if err := m.dropServerInbounds(model.LocalNodeID); err != nil {
		return nil, err
	}
	if err := m.ApplyConnections(DefaultConnections()); err != nil {
		return nil, err
	}
	logInfo("connections: master reset to defaults")
	return m.ConnectionsInfo()
}

// ResetNodeConnections resets one node the same way.
func (m *Manager) ResetNodeConnections(ctx context.Context, id int64) (*ConnectionsStatus, error) {
	if err := m.dropServerInbounds(id); err != nil {
		return nil, err
	}
	if err := m.ApplyNodeConnections(id, DefaultConnections()); err != nil {
		return nil, err
	}
	logInfo("connections: node reset to defaults", "node", id)
	return m.NodeConnectionsInfo(id)
}

// dropServerInbounds removes a server's custom inbounds and the grants that
// pointed at them. No reconcile of its own: the reset that follows does one.
func (m *Manager) dropServerInbounds(serverID int64) error {
	inbounds, err := m.store.Inbounds(serverID)
	if err != nil {
		return err
	}
	for _, in := range inbounds {
		if err := m.store.DeleteInboundGrants(in.ID); err != nil {
			return err
		}
	}
	if err := m.store.DeleteServerInbounds(serverID); err != nil {
		return err
	}
	if len(inbounds) > 0 {
		m.applyAccessChange()
	}
	return nil
}
