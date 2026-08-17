package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Routing/egress snapshots: undo a change that broke the tunnels. One is taken
// automatically before every routing change (see manager.ApplyRouting); these
// endpoints list them, take a manual save-point, roll back, and prune.

func (rt *Router) listConfigSnapshots(w http.ResponseWriter, _ *http.Request) {
	snaps, err := rt.mgr.ConfigSnapshots()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if snaps == nil {
		snaps = []model.ConfigSnapshot{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (rt *Router) createConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SnapshotServerConfig(req.Label); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) rollbackConfigSnapshot(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.RollbackServerConfig(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) deleteConfigSnapshot(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteConfigSnapshot(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
