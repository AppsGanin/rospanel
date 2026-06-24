package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/msTimofeev/rospanel/internal/backup"
	"github.com/msTimofeev/rospanel/internal/updater"
	"github.com/msTimofeev/rospanel/internal/version"
)

// updateRepo is the "owner/repo" the panel self-updates from: the baked-in
// updater.Repo, optionally overridden by the ROSPANEL_REPO env (handy for testing).
func updateRepo() string {
	if r := strings.TrimSpace(os.Getenv("ROSPANEL_REPO")); r != "" {
		return r
	}
	return updater.Repo
}

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
func (rt *Router) applyUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rel, err := updater.Latest(ctx, updateRepo())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !updater.IsNewer(rel.Version, version.Version) {
		writeErr(w, http.StatusBadRequest, "уже установлена последняя версия")
		return
	}
	backupFn := func() error {
		_ = rt.mgr.Store().Checkpoint() // flush WAL so the snapshot is current
		return backup.Create(rt.dataDir, filepath.Join(rt.dataDir, "pre-update-backup.tgz"))
	}
	// context.Background(): the download must outlive the HTTP request.
	if err := updater.Apply(context.Background(), rel, backupFn); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": rel.Version})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	updater.Restart()
}
