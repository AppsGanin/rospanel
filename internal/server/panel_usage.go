package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/store"
)

// userClients lists the apps a user fetched the subscription with.
func (rt *Router) userClients(w http.ResponseWriter, _ *http.Request, id int64) {
	list, err := rt.mgr.UserSubClients(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": list})
}

// userLanes is a user's time on each protocol over the range.
func (rt *Router) userLanes(w http.ResponseWriter, r *http.Request, id int64) {
	from, to, ok := rt.dateRange(w, r)
	if !ok {
		return
	}
	rows, err := rt.mgr.UserLaneBreakdown(id, from, to)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if rows == nil {
		rows = []core.UserLane{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// statsClients is how many users fetched with each app and from each platform over
// the range.
func (rt *Router) statsClients(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rt.dateRange(w, r)
	if !ok {
		return
	}
	apps, platforms, users, err := rt.mgr.ClientShares(from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if apps == nil {
		apps = []store.ClientShare{}
	}
	if platforms == nil {
		platforms = []store.ClientShare{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps, "platforms": platforms, "users": users})
}
