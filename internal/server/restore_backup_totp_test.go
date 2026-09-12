package server

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// A restore now asks for the second factor OF THE BACKUP whenever that backup's owner
// or admins had one. These drive the real routes with a real archive, and the archive's
// 2FA secret is encrypted under its OWN secrets.key — never the running panel's, which
// in this process is not even installed — because reading it under the wrong key is
// exactly the mistake that would make the check silently pass or silently lock out.

type backupAdmin struct {
	role   string
	secret string // "" = no second factor
}

// makeBackup builds a backup archive of a panel with the given admins, each 2FA secret
// encrypted under a key generated for that panel alone. withKey=false leaves the key out
// of the archive, which is how a hand-copied or truncated backup looks.
func makeBackup(t *testing.T, admins []backupAdmin, withKey bool) string {
	t.Helper()
	src := t.TempDir()
	st, err := store.Open(filepath.Join(src, "rospanel.db"))
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}
	hash, err := auth.HashPassword("backup-password")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	for i, a := range admins {
		id, err := st.CreateAdmin("admin"+string(rune('a'+i)), hash, a.role, false)
		if err != nil {
			t.Fatalf("create admin: %v", err)
		}
		if a.secret == "" {
			continue
		}
		enc, err := datasec.EncryptWith(key, a.secret)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		st.Close()
		db, err := sql.Open("sqlite", "file:"+filepath.Join(src, "rospanel.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE admins SET totp_secret = ? WHERE id = ?`, enc, id); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if st, err = store.Open(filepath.Join(src, "rospanel.db")); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if withKey {
		if err := os.WriteFile(filepath.Join(src, "secrets.key"), key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := backup.Create(src, out); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	return out
}

// restoreWith posts a real archive to /api/restore with the given fields.
func restoreWith(t *testing.T, rt *Router, c *http.Cookie, archive string, fields map[string]string) (int, string) {
	t.Helper()
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("backup", "backup.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(c)
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env.Code
}

// inspectWith posts a real archive to /api/backup/inspect and returns the report.
func inspectWith(t *testing.T, rt *Router, c *http.Cookie, archive string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("backup", "backup.tar.gz")
	_, _ = fw.Write(body)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/backup/inspect", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(c)
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	var rep map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("inspect %d: %s", w.Code, w.Body.String())
	}
	return rep
}

// stubRestart swaps out the process restart for the length of a test — the real one
// signals the test binary — and reports whether a restore was staged and scheduled.
func stubRestart(t *testing.T, rt *Router) func() bool {
	t.Helper()
	prev := scheduleRestart
	scheduled := false
	scheduleRestart = func() { scheduled = true }
	t.Cleanup(func() { scheduleRestart = prev })
	return func() bool {
		_, err := os.Stat(filepath.Join(rt.dataDir, ".restore", ".ready"))
		return scheduled && err == nil
	}
}

func newSecret(t *testing.T) string {
	t.Helper()
	s, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// After setup, by an admin of THIS panel who has no 2FA of their own: the password is
// all the step-up asks, so the backup's code is the only second factor in the request —
// and it has to be the backup's, not any six digits.
func TestRestoreAsksForTheBackupsSecondFactor(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie := signIn(t, st, "owner", model.RoleOwner, false)
	staged := stubRestart(t, rt)

	secret := newSecret(t)
	archive := makeBackup(t, []backupAdmin{{model.RoleOwner, secret}}, true)

	if rep := inspectWith(t, rt, cookie, archive); rep["totp"] != true || rep["valid"] != true {
		t.Fatalf("inspect did not report the backup's 2FA: %v", rep)
	}
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{"current_password": "a-password"}); errCode != "err.backupTotpRequired" {
		t.Fatalf("no backup code: %d %s — want err.backupTotpRequired", code, errCode)
	}
	// A code valid for a DIFFERENT authenticator is not a code for this backup.
	other := codeNow(t, newSecret(t))
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{"current_password": "a-password", "backup_code": other}); errCode != "err.backupTotpInvalid" {
		t.Fatalf("someone else's code: %d %s — want err.backupTotpInvalid", code, errCode)
	}
	if staged() {
		t.Fatal("a restore was staged before the backup's code was given")
	}
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{"current_password": "a-password", "backup_code": codeNow(t, secret)}); code != http.StatusNoContent {
		t.Fatalf("the backup's own code: %d %s — want 204", code, errCode)
	}
	if !staged() {
		t.Error("the backup's own code passed but no restore was staged")
	}
}

// The first-run wizard has no admin of its own to ask anything of. That was a hole: a
// backup of a 2FA-protected panel could be restored onto a fresh install by anyone
// holding the file. It now needs the backup's code, and still nothing else.
func TestTheWizardRestoreAsksForTheBackupsSecondFactor(t *testing.T) {
	rt, st := rolesTestRouter(t) // setup not done
	cookie := signIn(t, st, "owner", model.RoleOwner, false)
	staged := stubRestart(t, rt)

	secret := newSecret(t)
	archive := makeBackup(t, []backupAdmin{{model.RoleAdmin, secret}}, true)

	if code, errCode := restoreWith(t, rt, cookie, archive, nil); errCode != "err.backupTotpRequired" {
		t.Fatalf("wizard, no code: %d %s — want err.backupTotpRequired", code, errCode)
	}
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{"backup_code": codeNow(t, secret)}); code != http.StatusNoContent {
		t.Fatalf("wizard, the backup's code: %d %s — want 204", code, errCode)
	}
	if !staged() {
		t.Error("no restore was staged")
	}
}

// Rolling a panel back to its own backup is the commonest restore, and there the
// admin's authenticator and the backup's are the same one. The step-up code is tried
// against the backup when the backup field is empty, so one code does both.
func TestOneCodeDoesBothWhenTheAuthenticatorIsTheSame(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie, secret := adminWithTOTP(t, st, "owner")
	staged := stubRestart(t, rt)

	archive := makeBackup(t, []backupAdmin{{model.RoleOwner, secret}}, true)
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{
		"current_password": "a-password", "code": codeNow(t, secret),
	}); code != http.StatusNoContent {
		t.Fatalf("one code for the same authenticator: %d %s — want 204", code, errCode)
	}
	if !staged() {
		t.Error("no restore was staged")
	}
}

// Only an admin who could restore a backup themselves is asked for: owner or admin. An
// operator's authenticator neither demands a code nor answers for one.
func TestAnOperatorsSecondFactorDoesNotGateARestore(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie := signIn(t, st, "owner", model.RoleOwner, false)
	stubRestart(t, rt)

	archive := makeBackup(t, []backupAdmin{{model.RoleOwner, ""}, {model.RoleOperator, newSecret(t)}}, true)
	if rep := inspectWith(t, rt, cookie, archive); rep["totp"] == true {
		t.Errorf("an operator's 2FA made the backup ask for a code: %v", rep)
	}
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{"current_password": "a-password"}); code != http.StatusNoContent {
		t.Errorf("no owner/admin 2FA, password only: %d %s — want 204", code, errCode)
	}
}

// A backup whose 2FA secrets will not decrypt — here, one missing its secrets.key — is
// refused outright. Reading "no key" as "no second factor" would wave it through; and
// restoring it would leave a panel whose login refuses the unreadable secret.
func TestABackupWithUnreadable2FAIsRefused(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie := signIn(t, st, "owner", model.RoleOwner, false)
	staged := stubRestart(t, rt)

	archive := makeBackup(t, []backupAdmin{{model.RoleOwner, newSecret(t)}}, false)
	if rep := inspectWith(t, rt, cookie, archive); rep["valid"] != false || rep["issue"] != "restore.totpUnreadable" {
		t.Fatalf("inspect: %v — want invalid with restore.totpUnreadable", rep)
	}
	if code, errCode := restoreWith(t, rt, cookie, archive, map[string]string{"current_password": "a-password", "backup_code": "123456"}); errCode != "err.backupTotpUnreadable" {
		t.Fatalf("restore: %d %s — want err.backupTotpUnreadable", code, errCode)
	}
	if staged() {
		t.Error("a backup with unreadable 2FA was staged")
	}
}
