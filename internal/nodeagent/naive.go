package nodeagent

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// naiveHandler puts the node's NaiveProxy lane in front of its decoy. Nil-safe for an
// Agent built without one (tests).
func (a *Agent) naiveHandler(next http.Handler) http.Handler {
	if a.naive == nil {
		return next
	}
	return a.naive.Wrap(next)
}

// syncNaive hands the lane naive-in's clients from the config just applied, which ends
// the open tunnels of anyone it no longer holds.
func (a *Agent) syncNaive(cfg []byte) {
	if a.naive == nil {
		return
	}
	clients, addr := xray.NaiveClientsRaw(cfg)
	users := make([]naive.User, 0, len(clients))
	for _, c := range clients {
		users = append(users, naive.User{Email: c.Email, UUID: c.ID})
	}
	a.naive.SetUsers(addr, users)
}
