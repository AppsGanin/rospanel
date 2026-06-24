package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

func (rt *Router) listUsers(w http.ResponseWriter, _ *http.Request) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	users, err := rt.mgr.Store().ListUsers()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, makeUserView(u, set))
	}
	writeJSON(w, http.StatusOK, views)
}

func (rt *Router) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		DataLimit int64  `json:"data_limit"`
		ExpireAt  int64  `json:"expire_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "укажите имя")
		return
	}
	u, err := rt.mgr.CreateUser(req.Name, req.DataLimit, req.ExpireAt)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeJSON(w, http.StatusCreated, makeUserView(*u, set))
}

func (rt *Router) deleteUser(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteUser(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) resetUserTraffic(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.ResetTraffic(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setUserLimits(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		DataLimit   int64 `json:"data_limit"`
		ExpireAt    int64 `json:"expire_at"`
		DeviceLimit int   `json:"device_limit"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceLimit < 0 {
		writeErr(w, http.StatusBadRequest, "лимит устройств не может быть отрицательным")
		return
	}
	if err := rt.mgr.SetUserLimits(id, req.DataLimit, req.ExpireAt, req.DeviceLimit); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// rotateSubToken issues a new subscription URL for a user. The old link stops
// working; protocol credentials are unchanged.
func (rt *Router) rotateSubToken(w http.ResponseWriter, _ *http.Request, id int64) {
	u, err := rt.mgr.RotateSubToken(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeJSON(w, http.StatusOK, makeUserView(*u, set))
}

func (rt *Router) userConnections(w http.ResponseWriter, _ *http.Request, id int64) {
	conns, err := rt.mgr.Connections(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if conns == nil {
		conns = []model.Connection{}
	}
	writeJSON(w, http.StatusOK, conns)
}

// renameUser updates a user's display name.
func (rt *Router) renameUser(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "имя не может быть пустым")
		return
	}
	if err := rt.mgr.RenameUser(id, name); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setUserEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserEnabled(id, req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
