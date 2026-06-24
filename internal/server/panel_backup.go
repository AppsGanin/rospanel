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

	"github.com/msTimofeev/rospanel/internal/backup"
	"github.com/msTimofeev/rospanel/internal/netinfo"
	"github.com/msTimofeev/rospanel/internal/store"
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

// factoryReset wipes panel state — the database (users, settings, secret path),
// the TLS cert and ACME account, and the generated Xray config — but keeps the
// re-downloadable assets (the Xray binary in bin/ and the geo databases), then
// restarts so the next boot is a clean first-run. Irreversible.
//
// It replies with the address the panel will come back on. After a reset the host
// reverts to the auto-detected public IP and the default secret path, which can
// differ from where the admin is now (e.g. a custom domain) — so the client must
// redirect to this URL, not its current origin, to avoid a cert mismatch.
func (rt *Router) factoryReset(w http.ResponseWriter, _ *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "ошибка разбора загрузки")
		return
	}
	f, _, err := r.FormFile("backup")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "нет файла бэкапа")
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
		writeErr(w, http.StatusInternalServerError, "ошибка записи загрузки: "+err.Error())
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
		writeErr(w, http.StatusBadRequest, mErr.Error())
		return
	}

	// Extract to a throwaway dir and validate the embedded database.
	valid, issue, dbUsers, dbAdmins := true, "", 0, 0
	dir, err := os.MkdirTemp("", "rospanel-inspect-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	if err := backup.Restore(tmp.Name(), dir); err != nil {
		valid, issue = false, "архив повреждён: "+err.Error()
	} else if u, a, _, err := store.InspectDB(filepath.Join(dir, "rospanel.db")); err != nil {
		valid, issue = false, "база данных в бэкапе пуста или повреждена"
	} else if a == 0 {
		valid, issue, dbUsers, dbAdmins = false, "в бэкапе нет администратора — восстанавливать нечего", u, a
	} else {
		dbUsers, dbAdmins = u, a
	}

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
		writeErr(w, http.StatusBadRequest, "ошибка разбора загрузки")
		return
	}
	f, _, err := r.FormFile("backup")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "нет файла бэкапа")
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
		writeErr(w, http.StatusInternalServerError, "ошибка записи загрузки: "+err.Error())
		return
	}
	tmp.Close()

	// Stage the restore and apply it on the next boot (before the DB is opened),
	// so the live process's WAL can't checkpoint stale data over the restored DB.
	if err := backup.StageRestore(tmp.Name(), rt.dataDir); err != nil {
		writeErr(w, http.StatusBadRequest, "восстановление не удалось: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
	scheduleRestart() // restart applies the staged restore
}
