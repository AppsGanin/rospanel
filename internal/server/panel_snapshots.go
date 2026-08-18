package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Server-config snapshots: undo a change that broke the server. A snapshot captures the
// whole server config (protocols, ports, REALITY, routing, egress, DNS, decoy, inbounds)
// minus the certificate/domain identity; one is taken automatically before every
// rollback (see manager.RollbackServerConfig) so an undo is itself undoable. These
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
	if _, err := rt.mgr.SnapshotServerConfig(req.Label); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) rollbackConfigSnapshot(w http.ResponseWriter, r *http.Request, id int64) {
	defer rt.syncDecoyFromSettings() // a snapshot can restore a different masquerade
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
