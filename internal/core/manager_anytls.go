package core

import (
	"net"

	"github.com/AppsGanin/rospanel/internal/anytls"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// AnyTLSListener puts the master's AnyTLS lane on the listener Xray's :443 fallback
// reaches: a connection that authenticates is a session, everything else reaches the
// panel as before.
func (m *Manager) AnyTLSListener(ln net.Listener) net.Listener {
	if m.anytls == nil {
		return ln
	}
	return m.anytls.WrapListener(ln)
}

// syncAnyTLS hands the lane the users of the config just put in force, which closes
// the connections of anyone it no longer holds.
func (m *Manager) syncAnyTLS(cfg *xray.Config) {
	if m.anytls == nil {
		return
	}
	clients, addr := xray.AnyTLSClients(cfg)
	m.anytls.SetUsers(addr, anytlsUsers(clients))
}

// anytlsUsers turns anytls-in's clients into the lane's accounts.
func anytlsUsers(clients []xray.VLESSClient) []anytls.User {
	out := make([]anytls.User, 0, len(clients))
	for _, c := range clients {
		out = append(out, anytls.User{Email: c.Email, UUID: c.ID})
	}
	return out
}
