package nodeagent

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// naiveHandler puts the node's NaiveProxy and TrustTunnel lanes in front of its decoy.
// Nil-safe for an Agent built without them (tests).
func (a *Agent) naiveHandler(next http.Handler) http.Handler {
	if a.naive != nil {
		next = a.naive.Wrap(next)
	}
	if a.trusttunnel != nil {
		next = a.trusttunnel.Wrap(next)
	}
	return next
}

// syncNaive hands the lane naive-in's clients from the config just applied, which ends
// the open tunnels of anyone it no longer holds; its twin, TrustTunnel, gets
// trusttunnel-in's.
func (a *Agent) syncNaive(cfg []byte) {
	if a.naive != nil {
		a.naive.SetUsers(laneUsers(xray.NaiveClientsRaw(cfg)))
	}
	if a.trusttunnel != nil {
		a.trusttunnel.SetUsers(laneUsers(xray.TrustTunnelClientsRaw(cfg)))
	}
}

// laneUsers turns an inner inbound's clients into the lane's credentials, with the
// address it listens on.
func laneUsers(clients []xray.VLESSClient, addr string) (string, []naive.User) {
	users := make([]naive.User, 0, len(clients))
	for _, c := range clients {
		users = append(users, naive.User{Email: c.Email, UUID: c.ID})
	}
	return addr, users
}
