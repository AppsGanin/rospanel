package server

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/netinfo"
	"github.com/AppsGanin/rospanel/internal/store"
)

// scheduleRestart sends the process SIGTERM after a short delay so the current
// HTTP response flushes first; the systemd / Docker restart policy brings it back
// up (and the next boot reflects whatever state was just written/wiped).
func scheduleRestart() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)
	}()
}

// restartPanel restarts the panel process itself. The reply goes out first and the
// SIGTERM lands half a second later, so the SPA gets a 200 to react to; the service
// manager brings the process back (and Xray with it, since the panel supervises it
// — which is why the UI confirms first: live VPN connections drop for a moment).
func (rt *Router) restartPanel(w http.ResponseWriter, _ *http.Request) {
	writeOK(w)
	scheduleRestart()
}

// factoryReset wipes panel state — the database (users, settings, secret path),
// the TLS cert and ACME account, and the generated Xray config — but keeps the
// re-downloadable assets (the Xray binary in bin/ and the geo databases), then
// restarts so the next boot is a clean first-run. Irreversible.
//
// It replies with the address the panel will come back on. After a reset the host
// reverts to the auto-detected public IP and the default secret path, which can
// differ from where the admin is now (e.g. a custom domain) — so the client must
// redirect to this URL, not its current origin, to avoid a cert mismatch.
func (rt *Router) factoryReset(w http.ResponseWriter, r *http.Request) {
	// Re-authenticate. This wipes every user, the admin roster, the TLS identity and the
	// secret path, with no undo — a stolen session cookie must not be enough on its own.
	// Changing a payment key already re-prompts; this is strictly more destructive.
	var req stepUpBody
	if !decodeJSON(w, r, &req) {
		return
	}
	// The second factor too, when this admin has one: there is no restore path after
	// this, so a stolen session plus a reused password must not be able to reach it.
	if !rt.verifyStepUpTOTP(w, r, req.CurrentPassword, req.Code) {
		return
	}
	for _, name := range []string{"rospanel.db", "rospanel.db-wal", "rospanel.db-shm"} {
		_ = os.Remove(filepath.Join(rt.dataDir, name))
	}
	for _, dir := range []string{"certs", "acme", "xray"} {
		_ = os.RemoveAll(filepath.Join(rt.dataDir, dir))
	}
	// Mirror bootstrapTLS's host resolution so the redirect points where the panel
	// will actually come back: an explicit ROSPANEL_HOST (e.g. a domain) wins over
	// the auto-detected public IP.
	host := strings.TrimSpace(os.Getenv("ROSPANEL_HOST"))
	if host == "" {
		host = netinfo.PublicIP()
	}
	url := ""
	if host != "" {
		url = "https://" + host + "/rospanel/"
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
	scheduleRestart()
}

// downloadBackup streams the data directory as a tar.gz attachment, with a
// manifest.json prepended so the archive is self-describing.
func (rt *Router) downloadBackup(w http.ResponseWriter, _ *http.Request) {
	// Flush the WAL into the .db file first so the archived database is complete
	// (backups exclude the .db-wal sidecar where live data otherwise sits).
	if err := rt.mgr.Store().Checkpoint(); err != nil {
		log.Printf("backup: checkpoint: %v", err)
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="rospanel-backup.tar.gz"`)
	w.Header().Set("Cache-Control", "no-store")
	m := rt.mgr.BackupManifest()
	if err := backup.WriteWithManifest(rt.dataDir, m, w); err != nil {
		log.Printf("backup download: %v", err)
	}
}

// backupInfo returns a manifest describing the current server (shown before download).
func (rt *Router) backupInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rt.mgr.BackupManifest())
}

// inspectBackup previews an uploaded backup and validates it before a restore:
// it reads the manifest and extracts the embedded database to verify it's a real,
// non-empty panel DB (catching truncated/corrupt archives and the empty-backup
// case where the manifest looks fine but the DB has no data).
func (rt *Router) inspectBackup(w http.ResponseWriter, r *http.Request) {
	// A backup upload can be large/slow — lift the server's 60s ReadTimeout for the
	// duration of this request so a legitimate restore isn't cut off mid-upload.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.uploadParseError", "ошибка разбора загрузки")
		return
	}
	f, _, err := r.FormFile("backup")
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.noBackupFile", "нет файла бэкапа")
		return
	}
	defer f.Close()

	tmp, err := os.CreateTemp("", "rospanel-inspect-*.tar.gz")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		writeErrDetail(w, http.StatusInternalServerError, "err.uploadWriteFailed", "ошибка записи загрузки: ", err.Error())
		return
	}
	tmp.Close()

	mf, err := os.Open(tmp.Name())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	m, mErr := backup.ReadManifest(mf)
	mf.Close()
	if mErr != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.backupUnreadable",
			"не удалось прочитать архив: ", mErr.Error())
		return
	}

	// Extract to a throwaway dir and validate the embedded database.
	issue, dbUsers, dbAdmins := inspectArchive(tmp.Name())
	valid := issue == ""

	writeJSON(w, http.StatusOK, map[string]any{
		"manifest":  m,
		"valid":     valid,
		"db_users":  dbUsers,
		"db_admins": dbAdmins,
		"issue":     issue,
	})
}

// uploadRestore accepts a tar.gz upload, extracts it over the data directory,
// then signals the process to restart so the restored state is loaded.
func (rt *Router) uploadRestore(w http.ResponseWriter, r *http.Request) {
	// A backup upload can be large/slow — lift the server's 60s ReadTimeout for the
	// duration of this request so a legitimate restore isn't cut off mid-upload.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	const maxSize = 512 << 20 // 512 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.uploadParseError", "ошибка разбора загрузки")
		return
	}
	// Re-authenticate: a restore replaces the whole data directory, including the admin
	// roster the caller is authenticated against, and it is applied on the next boot
	// with no undo. Carried as a form field because this endpoint is multipart, not JSON.
	if !rt.verifyStepUp(w, r, r.FormValue("current_password")) {
		return
	}
	f, _, err := r.FormFile("backup")
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.noBackupFile", "нет файла бэкапа")
		return
	}
	defer f.Close()

	tmp, err := os.CreateTemp("", "rospanel-restore-*.tar.gz")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		writeErrDetail(w, http.StatusInternalServerError, "err.uploadWriteFailed", "ошибка записи загрузки: ", err.Error())
		return
	}
	tmp.Close()

	// Vet the archive HERE, not only in the inspect call the SPA makes first: this
	// endpoint is reachable by any API/MCP client and by a hand-crafted request, and
	// staging is the point of no return (ApplyPending replaces only the entries the
	// archive HAS, so one carrying secrets.key but no database swaps the encryption key
	// out from under an unchanged DB, and every secret then decrypts to "").
	if issue, _, _ := inspectArchive(tmp.Name()); issue != "" {
		writeErrCode(w, http.StatusBadRequest, archiveIssueErr[issue], "архив не прошёл проверку")
		return
	}

	// Stage the restore and apply it on the next boot (before the DB is opened),
	// so the live process's WAL can't checkpoint stale data over the restored DB.
	if err := backup.StageRestore(tmp.Name(), rt.dataDir); err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.restoreFailed", "восстановление не удалось: ", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
	scheduleRestart() // restart applies the staged restore
}

// archiveIssueErr maps an inspect issue (a restore-screen dictionary key) to the error
// code the restore endpoint answers with, so both surfaces name the same problem.
var archiveIssueErr = map[string]string{
	"restore.archiveCorrupt": "err.backupCorrupt",
	"restore.dbUnreadable":   "err.backupDbUnreadable",
	"restore.noAdmin":        "err.backupNoAdmin",
	"restore.schemaTooNew":   "err.backupSchemaTooNew",
}

// inspectArchive extracts a backup to a throwaway dir and reports whether it can be
// restored into THIS binary. issue is a dictionary key, not a sentence: the restore
// screen is the panel's, and its language is the admin's choice. Empty issue = usable.
//
// Shared by the inspect call and the restore itself — the SPA asks first, but the
// restore endpoint must not depend on a client having done so.
func inspectArchive(path string) (issue string, users, admins int) {
	dir, err := os.MkdirTemp("", "rospanel-inspect-*")
	if err != nil {
		return "restore.archiveCorrupt", 0, 0
	}
	defer os.RemoveAll(dir)

	if err := backup.Restore(path, dir); err != nil {
		return "restore.archiveCorrupt", 0, 0
	}
	dbPath := filepath.Join(dir, "rospanel.db")
	u, a, _, err := store.InspectDB(dbPath)
	if err != nil {
		return "restore.dbUnreadable", 0, 0
	}
	if a == 0 {
		return "restore.noAdmin", u, a
	}
	// A database from a NEWER panel cannot be restored into this one: the migration
	// runner skips versions already recorded, so nothing would run and the binary would
	// read columns its schema lacks — a boot loop with no way out from inside the panel.
	//
	// Fails CLOSED. An archive whose schema_migrations cannot be read is not one to take
	// a chance on: the whole point of the check is that the failure it prevents is
	// unrecoverable from inside the panel.
	v, err := store.DBSchemaVersion(dbPath)
	if err != nil || v > store.SchemaVersion() {
		return "restore.schemaTooNew", u, a
	}
	return "", u, a
}
