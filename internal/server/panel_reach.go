package server

import (
	"context"
	"net/http"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
)

// Reachability from other countries through Globalping (core/manager_reach.go).

// reachCheckTimeout bounds a manual check: Globalping takes up to ~40 s to run one.
const reachCheckTimeout = 2 * time.Minute

// reachReq is the settings form. Token is left out to keep the stored one, "" clears it.
type reachReq struct {
	core.ReachConfig
	Token *string `json:"token,omitempty"`
}

func (rt *Router) getReach(w http.ResponseWriter, _ *http.Request) {
	c, err := rt.mgr.ReachConfig()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (rt *Router) setReach(w http.ResponseWriter, r *http.Request) {
	var req reachReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetReachConfig(req.ReachConfig, req.Token); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.getReach(w, r)
}

// nodeReach is a server's last check (at 0 ⇒ not checked since the panel started).
func (rt *Router) nodeReach(w http.ResponseWriter, _ *http.Request, id int64) {
	writeJSON(w, http.StatusOK, rt.mgr.LastReach(id))
}

func (rt *Router) checkNodeReach(w http.ResponseWriter, r *http.Request, id int64) {
	ctx, cancel := context.WithTimeout(r.Context(), reachCheckTimeout)
	defer cancel()
	rep, err := rt.mgr.CheckReach(ctx, id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (rt *Router) apiGetReach(w http.ResponseWriter, _ *http.Request) {
	c, err := rt.mgr.ReachConfig()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, c)
}

func (rt *Router) apiSetReach(w http.ResponseWriter, r *http.Request) {
	var req reachReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetReachConfig(req.ReachConfig, req.Token); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiGetReach(w, r)
}

func (rt *Router) apiNodeReach(w http.ResponseWriter, _ *http.Request, id int64) {
	writeAPIData(w, http.StatusOK, rt.mgr.LastReach(id))
}

func (rt *Router) apiCheckNodeReach(w http.ResponseWriter, r *http.Request, id int64) {
	ctx, cancel := context.WithTimeout(r.Context(), reachCheckTimeout)
	defer cancel()
	rep, err := rt.mgr.CheckReach(ctx, id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, rep)
}
