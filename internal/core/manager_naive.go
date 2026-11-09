package core

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// NaiveHandler puts the master's NaiveProxy lane in front of the panel's handler, which
// Xray's :443 fallback reaches: an authenticated CONNECT is a tunnel, everything else is
// the panel as before.
// TrustTunnel sits in front of it on the same fallback (its own login keeps the two
// apart), so one wrap answers both.
func (m *Manager) NaiveHandler(next http.Handler) http.Handler {
	return m.trusttunnel.Wrap(m.naive.Wrap(next))
}

// syncNaive hands the lane — and TrustTunnel, its twin — the users of the config just put in force, which ends the
// open tunnels of anyone it no longer holds.
func (m *Manager) syncNaive(cfg *xray.Config) {
	clients, addr := xray.NaiveClients(cfg)
	m.naive.SetUsers(addr, naiveUsers(clients))
	clients, addr = xray.TrustTunnelClients(cfg)
	m.trusttunnel.SetUsers(addr, naiveUsers(clients))
}

// naiveUsers turns naive-in's clients into the lane's credentials.
func naiveUsers(clients []xray.VLESSClient) []naive.User {
	out := make([]naive.User, 0, len(clients))
	for _, c := range clients {
		out = append(out, naive.User{Email: c.Email, UUID: c.ID})
	}
	return out
}
