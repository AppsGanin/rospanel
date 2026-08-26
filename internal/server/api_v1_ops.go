package server

import (
	"log"
	"net/http"

	"github.com/Shu1t3/rospanel-shu1t3/internal/backup"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// The rest of what an integration needs and the panel kept to itself: the moderated
// signup queue, the blocklist hits behind the abuse view, the two vocabularies that
// make grants and inbounds constructible from outside, an off-box backup, and a
// node's own health and logs.

// apiRegistrationsResp is the moderated signup queue. Moderation says whether the
// panel is even in that mode — an empty queue means nothing on its own.
type apiRegistrationsResp struct {
	Moderation bool                        `json:"moderation"`
	Requests   []model.RegistrationRequest `json:"requests"`
}

func (rt *Router) apiListRegistrations(w http.ResponseWriter, _ *http.Request) {
	reqs, err := rt.mgr.ListRegistrationRequests()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if reqs == nil {
		reqs = []model.RegistrationRequest{}
	}
	moderation := false
	if set, err := rt.mgr.Settings(); err == nil && set != nil {
		moderation = set.RegMode() == model.RegModeration
	}
	writeAPIData(w, http.StatusOK, apiRegistrationsResp{Moderation: moderation, Requests: reqs})
}

// apiApproveRegistration creates the account and links the applicant's Telegram chat.
func (rt *Router) apiApproveRegistration(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.ApproveRegistrationRequest(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) apiRejectRegistration(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.RejectRegistrationRequest(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiUserAbuse returns one user's blocklist matches, newest first.
func (rt *Router) apiUserAbuse(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := rt.mgr.UserAbuse(id, sitesLimit(r, 20))
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if rows == nil {
		rows = []store.AbuseMatch{}
	}
	writeAPIData(w, http.StatusOK, rows)
}

// apiStatsAbuse returns the fleet's recent blocklist matches, newest first.
func (rt *Router) apiStatsAbuse(w http.ResponseWriter, r *http.Request) {
	rows, err := rt.mgr.RecentAbuse(sitesLimit(r, 50))
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if rows == nil {
		rows = []store.AbuseMatch{}
	}
	writeAPIData(w, http.StatusOK, rows)
}

// apiGroupTargets lists every server with the connections a group can grant, each
// with the token to put in `grants`. Without it those tokens have to be assembled by
// hand from the node list and each server's inbounds — and a typo grants nothing,
// silently.
func (rt *Router) apiGroupTargets(w http.ResponseWriter, _ *http.Request) {
	targets, err := rt.mgr.GroupTargets()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, targets)
}

// apiInboundCatalog publishes which protocol × transport × security combinations
// exist, which subscription formats can't carry each one, and the enum values the
// advanced fields accept — the same table the panel editor drives its dropdowns from.
func (rt *Router) apiInboundCatalog(w http.ResponseWriter, _ *http.Request) {
	writeAPIData(w, http.StatusOK, makeInboundCatalog())
}

// apiBackupInfo describes what a backup taken right now would contain — WITHOUT the
// panel's secret path, which the manifest carries for the restore side. The panel
// shows it to an admin who already knows it; publishing it here would hand the hidden
// panel URL to every integration holding a key, and that path is the layer keeping the
// panel invisible to scanners.
func (rt *Router) apiBackupInfo(w http.ResponseWriter, _ *http.Request) {
	m := rt.mgr.BackupManifest()
	m.SecretPath = ""
	writeAPIData(w, http.StatusOK, m)
}

// apiBackup streams the data directory as a tar.gz — the panel's "download backup",
// available to a scheduler that keeps copies off the box. NOT in the {"data": …}
// envelope: the body is the archive itself.
//
// Restore is deliberately absent. It is staged on disk and applied at the next start,
// so over an API it would be a request that quietly replaces everything on the next
// restart — that belongs on a screen with a confirmation, not on an API key.
func (rt *Router) apiBackup(w http.ResponseWriter, _ *http.Request) {
	// Flush the WAL into the .db file first so the archived database is complete.
	if err := rt.mgr.Store().Checkpoint(); err != nil {
		log.Printf("api backup: checkpoint: %v", err)
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="rospanel-backup.tar.gz"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := backup.WriteWithManifest(rt.dataDir, rt.mgr.BackupManifest(), w); err != nil {
		// The status is already written and bytes are on the wire; all that is left is
		// to record why the archive is short.
		log.Printf("api backup: %v", err)
	}
}

// apiNodeHealth is one server's own health report (the panel's node health card).
func (rt *Router) apiNodeHealth(w http.ResponseWriter, _ *http.Request, id int64) {
	rep, err := rt.mgr.NodeHealth(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, rep)
}

// apiNodeLogsResp is a node's recent log lines with the time they were collected.
type apiNodeLogsResp struct {
	Lines []string `json:"lines"`
	At    int64    `json:"at"`
}

// apiNodeLogs pulls a node's recent log lines through the panel. The node answers on
// its next long-poll, so a freshly-woken node may return the previous batch — `at`
// says how old it is.
func (rt *Router) apiNodeLogs(w http.ResponseWriter, _ *http.Request, id int64) {
	lines, at := rt.mgr.RequestNodeLogs(id)
	if lines == nil {
		lines = []string{}
	}
	writeAPIData(w, http.StatusOK, apiNodeLogsResp{Lines: lines, At: at})
}
