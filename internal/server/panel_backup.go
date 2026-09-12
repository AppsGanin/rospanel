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

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/netinfo"
	"github.com/AppsGanin/rospanel/internal/store"
)

// scheduleRestart sends the process SIGTERM after a short delay so the current
// HTTP response flushes first; the systemd / Docker restart policy brings it back
// up (and the next boot reflects whatever state was just written/wiped).
//
// A variable so a test can drive a restore all the way through: the real one signals
// the process it runs in, which under `go test` is the test binary itself.
var scheduleRestart = func() {
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
	rep := inspectArchive(tmp.Name())

	writeJSON(w, http.StatusOK, map[string]any{
		"manifest":  m,
		"valid":     rep.issue == "",
		"db_users":  rep.users,
		"db_admins": rep.admins,
		"issue":     rep.issue,
		// Whether restoring it will ask for a code from ITS authenticator. Only the
		// fact, never the secrets: the dialog needs to know to show the field.
		"totp": len(rep.totpSecrets) > 0,
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
	// with no undo. Carried as form fields because this endpoint is multipart, not JSON.
	if !rt.verifyRestoreStepUp(w, r, r.FormValue("current_password"), r.FormValue("code")) {
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
	rep := inspectArchive(tmp.Name())
	if rep.issue != "" {
		writeErrCode(w, http.StatusBadRequest, archiveIssueErr[rep.issue], "архив не прошёл проверку")
		return
	}
	if !rt.verifyBackupTOTP(w, r, rep.totpSecrets, r.FormValue("backup_code"), r.FormValue("code")) {
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
	"restore.totpUnreadable": "err.backupTotpUnreadable",
}

// inspectArchive extracts a backup to a throwaway dir and reports whether it can be
// restored into THIS binary. issue is a dictionary key, not a sentence: the restore
// screen is the panel's, and its language is the admin's choice. Empty issue = usable.
//
// Shared by the inspect call and the restore itself — the SPA asks first, but the
// restore endpoint must not depend on a client having done so.
func inspectArchive(path string) archiveReport {
	dir, err := os.MkdirTemp("", "rospanel-inspect-*")
	if err != nil {
		return archiveReport{issue: "restore.archiveCorrupt"}
	}
	defer os.RemoveAll(dir)

	if err := backup.Restore(path, dir); err != nil {
		return archiveReport{issue: "restore.archiveCorrupt"}
	}
	dbPath := filepath.Join(dir, "rospanel.db")
	u, a, _, err := store.InspectDB(dbPath)
	if err != nil {
		return archiveReport{issue: "restore.dbUnreadable"}
	}
	rep := archiveReport{users: u, admins: a}
	if a == 0 {
		rep.issue = "restore.noAdmin"
		return rep
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
		rep.issue = "restore.schemaTooNew"
		return rep
	}
	// The backup's own second factor, if its admins had one. Unreadable is an issue,
	// not "none": a restored panel whose 2FA secrets will not decrypt is one nobody
	// can sign in to — the login refuses an unreadable secret rather than waving the
	// password through — and it is better found out here than after the reboot.
	secrets, err := store.BackupAdminTOTPSecrets(dir)
	if err != nil {
		rep.issue = "restore.totpUnreadable"
		return rep
	}
	rep.totpSecrets = secrets
	return rep
}

// archiveReport is what inspectArchive found. totpSecrets are the decrypted
// second-factor secrets of the backup's owner and admins, held only for as long as
// the request that checks a code against them.
type archiveReport struct {
	issue         string
	users, admins int
	totpSecrets   []string
}

// verifyRestoreStepUp gates a restore once the panel is set up exactly as the factory
// reset is gated: the password and, when this admin has bound an authenticator, a
// fresh code.
//
// It used to ask for the password alone, which put the more dangerous of the two
// behind the lower bar. A factory reset wipes the panel; a restore REPLACES it with a
// database the uploader chose — its admin roster included, so whoever holds a stolen
// session and a reused password could install an admin of their own with no second
// factor on it, the next boot applies it, and nothing undoes it. That is a takeover,
// not a wipe, and it was the one of the two that did not ask for the code.
//
// During first run it keeps verifyStepUp's waiver, deliberately: the wizard's own
// "restore from backup" is how a new install becomes an old one, it runs before this
// install has an admin worth protecting or any second factor to ask for, and the
// wizard sends no credentials at all.
func (rt *Router) verifyRestoreStepUp(w http.ResponseWriter, r *http.Request, password, code string) bool {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return false
	}
	if !set.SetupDone {
		return rt.verifyStepUp(w, r, password)
	}
	return rt.verifyStepUpTOTP(w, r, password, code)
}

// verifyBackupTOTP asks for the second factor of the BACKUP being restored, whenever
// that backup's owner or admins had one — on top of whatever verifyRestoreStepUp
// already asked of the admin doing it.
//
// The step-up proves who is at this panel; this proves they can operate the panel they
// are about to bring back. It matters most where the step-up can ask nothing: in the
// first-run wizard there is no admin of this install yet, so a backup of a
// 2FA-protected panel could be restored by anyone holding the file. Now it needs a
// code from that panel's authenticator too. And it catches the honest mistake of
// restoring a backup whose authenticator is long gone, before the reboot rather than
// at a login that will never succeed.
//
// A code that matches any one of those admins is enough. The admin restoring does
// not have to be the one who made the backup, only someone that panel trusted to
// restore one. backupCode is the field the dialog fills; when it is empty the
// step-up code is tried against the backup too, so rolling back a panel to its own
// backup — the commonest restore, where both come from the same authenticator —
// needs one code, not the same six digits typed twice.
//
// Guesses count against the same per-IP step-up throttle as every other code asked
// for here: a restore is not a softer place to try six-digit numbers.
//
// What it does not do is mark the code spent: the backup's replay marker lives in the
// backup's own database, which is not opened for writing before it is applied. So a
// code that authorised a restore can still sign in to the restored panel within its
// window — to the same person who just proved they hold it.
func (rt *Router) verifyBackupTOTP(w http.ResponseWriter, r *http.Request, secrets []string, backupCode, stepUpCode string) bool {
	if len(secrets) == 0 {
		return true
	}
	code := strings.TrimSpace(backupCode)
	if code == "" {
		code = strings.TrimSpace(stepUpCode)
	}
	if code == "" {
		writeErrCode(w, http.StatusForbidden, "err.backupTotpRequired", "введите код из приложения для этого бэкапа")
		return false
	}
	ip := clientIP(r)
	if rt.stepUp.blocked(ip, "") {
		writeErrCode(w, http.StatusTooManyRequests, "err.tooManyAttempts", "слишком много попыток, попробуйте позже")
		return false
	}
	now := time.Now()
	for _, secret := range secrets {
		if _, ok := auth.VerifyTOTP(secret, code, now, 0); ok {
			rt.stepUp.success(ip, "")
			return true
		}
	}
	rt.stepUp.fail(ip, "")
	writeErrCode(w, http.StatusForbidden, "err.backupTotpInvalid", "код не подходит к этому бэкапу")
	return false
}
