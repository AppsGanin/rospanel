package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/updater"
	"github.com/AppsGanin/rospanel/internal/version"
)

// updateRepo is the "owner/repo" the panel self-updates from.
func updateRepo() string { return updater.ConfiguredRepo() }

// checkUpdate reports the running version and, if the update repo is configured,
// whether a newer GitHub release exists.
func (rt *Router) checkUpdate(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"current": version.Version, "available": false}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rel, err := updater.Latest(ctx, updateRepo())
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["latest"] = rel.Version
	resp["notes"] = rel.Notes
	resp["available"] = rel.AssetURL != "" && updater.IsNewer(rel.Version, version.Version)
	writeJSON(w, http.StatusOK, resp)
}

// applyUpdate downloads the latest release, snapshots the DB, atomically swaps the
// running binary, then schedules a service restart so systemd re-execs it. The
// restart briefly drops Xray (all connections) — the client polls back to life.
//
// With ?nodes=1 every node is told to update as well, once the panel's own binary is
// in place: a panel whose download failed never leaves its nodes a release ahead of
// it. The command is kept on disk, so a node takes it on its next sync whether that
// lands before the restart or after, and updates itself to the same latest release.
func (rt *Router) applyUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rel, err := updater.Latest(ctx, updateRepo())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !updater.IsNewer(rel.Version, version.Version) {
		writeErrCode(w, http.StatusBadRequest, "err.alreadyLatest", "уже установлена последняя версия")
		return
	}
	backupFn := func() error {
		_ = rt.mgr.Store().Checkpoint() // flush WAL so the snapshot is current
		return backup.Create(rt.dataDir, filepath.Join(rt.dataDir, "pre-update-backup.tgz"))
	}
	// context.Background(): the download must outlive the HTTP request.
	if err := updater.Apply(context.Background(), rel, backupFn); err != nil {
		if errors.Is(err, updater.ErrInProgress) {
			writeErrCode(w, http.StatusConflict, "err.updateInProgress", "обновление уже идёт")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"ok": true, "version": rel.Version}
	if r.URL.Query().Get("nodes") == "1" {
		n, err := rt.mgr.RequestAllNodesUpdate()
		if err != nil {
			slog.Error("update: the nodes were not told to update", "err", err)
			resp["nodes_error"] = err.Error()
		}
		resp["nodes"] = n
	}
	writeJSON(w, http.StatusOK, resp)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	updater.Restart()
}

// autoUpdateView is the auto-update schedule and the last attempt's outcome.
type autoUpdateView struct {
	Cron   string `json:"cron"`    // 5-field cron in the panel's timezone; "" = off
	Nodes  bool   `json:"nodes"`   // the servers follow the panel
	LastAt int64  `json:"last_at"` // unix, 0 = never tried
	// Last is the outcome: updated:<version> | latest | nodes:<n> | unsupported |
	// error:<message>.
	Last string `json:"last"`
	// Supported: the panel runs as a systemd service, which the restart after an
	// install needs. A container updates with its image instead.
	Supported bool `json:"supported"`
}

// autoUpdateReq changes the schedule; a field left out keeps its value.
type autoUpdateReq struct {
	Cron  *string `json:"cron"`
	Nodes *bool   `json:"nodes"`
}

func (rt *Router) autoUpdateState() (autoUpdateView, error) {
	set, err := rt.mgr.Settings()
	if err != nil {
		return autoUpdateView{}, err
	}
	return autoUpdateView{Cron: set.AutoUpdateCron, Nodes: set.AutoUpdateNodes,
		LastAt: set.AutoUpdateLastAt, Last: set.AutoUpdateLast, Supported: updater.UnderSystemd()}, nil
}

// saveAutoUpdate applies req over the stored schedule.
func (rt *Router) saveAutoUpdate(req autoUpdateReq) (autoUpdateView, error) {
	cur, err := rt.autoUpdateState()
	if err != nil {
		return cur, err
	}
	if req.Cron != nil {
		cur.Cron = *req.Cron
	}
	if req.Nodes != nil {
		cur.Nodes = *req.Nodes
	}
	if err := rt.mgr.SaveAutoUpdate(cur.Cron, cur.Nodes); err != nil {
		return cur, err
	}
	return rt.autoUpdateState()
}

func (rt *Router) getAutoUpdate(w http.ResponseWriter, _ *http.Request) {
	v, err := rt.autoUpdateState()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (rt *Router) postAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var req autoUpdateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	v, err := rt.saveAutoUpdate(req)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (rt *Router) apiGetAutoUpdate(w http.ResponseWriter, _ *http.Request) {
	v, err := rt.autoUpdateState()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, v)
}

func (rt *Router) apiPostAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var req autoUpdateReq
	if !apiDecode(w, r, &req) {
		return
	}
	v, err := rt.saveAutoUpdate(req)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, v)
}
