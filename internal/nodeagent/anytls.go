package nodeagent

import (
	"net"

	"github.com/AppsGanin/rospanel/internal/anytls"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// anytlsListener puts the node's AnyTLS lane on its decoy's listener. Nil-safe for an
// Agent built without one (tests).
func (a *Agent) anytlsListener(ln net.Listener) net.Listener {
	if a.anytls == nil {
		return ln
	}
	return a.anytls.WrapListener(ln)
}

// syncAnyTLS hands the lane anytls-in's clients from the config just applied, which
// closes the connections of anyone it no longer holds.
func (a *Agent) syncAnyTLS(cfg []byte) {
	if a.anytls == nil {
		return
	}
	clients, addr := xray.AnyTLSClientsRaw(cfg)
	users := make([]anytls.User, 0, len(clients))
	for _, c := range clients {
		users = append(users, anytls.User{Email: c.Email, UUID: c.ID})
	}
	a.anytls.SetUsers(addr, users)
}
