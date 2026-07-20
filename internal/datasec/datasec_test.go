package datasec

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// settingsCols are the encrypted settings columns the guard knows about. Kept as a
// list so the fixture can encrypt exactly one at a time.
var settingsCols = []string{
	"tg_bot_token", "tg_user_bot_token", "tg_support_bot_token",
	"reality_private_key", "warp_private_key", "proxy_mode_pass", "zerossl_eab_hmac",
}

// writeDB builds a minimal panel database with one settings row, encrypting only
// the named column (empty = nothing encrypted).
func writeDB(t *testing.T, encrypted string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	cols := ""
	for _, c := range settingsCols {
		cols += ", " + c + " TEXT NOT NULL DEFAULT ''"
	}
	if _, err := db.Exec(`CREATE TABLE settings (id INTEGER PRIMARY KEY` + cols + `)`); err != nil {
		t.Fatalf("create settings: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, password TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if encrypted != "" {
		if _, err := db.Exec(`UPDATE settings SET ` + encrypted + ` = 'enc:v1:deadbeef' WHERE id = 1`); err != nil {
			t.Fatalf("encrypt %s: %v", encrypted, err)
		}
	}
	return path
}

// TestGuardSeesEveryEncryptedColumn is a data-loss guard, so it has to cover every
// column rather than the ones an install usually has.
//
// dbHasEncryptedSecrets is what tells "fresh install, no key yet" apart from "the
// key is gone". A column missing from its checks makes an install that uses only
// that secret look fresh — so Init mints a new key and the existing ciphertext
// becomes unreadable for good, silently. tg_support_bot_token was that column: an
// install running only the support bot would have been wiped by its own boot.
func TestGuardSeesEveryEncryptedColumn(t *testing.T) {
	for _, col := range settingsCols {
		t.Run(col, func(t *testing.T) {
			got, err := dbHasEncryptedSecrets(writeDB(t, col))
			if err != nil {
				t.Fatalf("guard: %v", err)
			}
			if !got {
				t.Fatalf("settings.%s holds ciphertext but the guard reports no secrets — "+
					"a boot without the key would mint a new one and orphan it", col)
			}
		})
	}
}

// TestGuardQuietOnFreshInstall: with nothing encrypted the guard must NOT claim
// there are secrets, or a genuine first boot would refuse to start.
func TestGuardQuietOnFreshInstall(t *testing.T) {
	got, err := dbHasEncryptedSecrets(writeDB(t, ""))
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if got {
		t.Fatal("guard reports secrets on a database that has none — a fresh install " +
			"would refuse to boot")
	}
}

// TestGuardQuietWithoutDB: no database at all is the very first boot.
func TestGuardQuietWithoutDB(t *testing.T) {
	got, err := dbHasEncryptedSecrets(filepath.Join(t.TempDir(), "absent.db"))
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if got {
		t.Fatal("guard reports secrets with no database present")
	}
}

// TestGuardSurvivesOlderSchema: the checks name columns that only exist on newer
// installs (the nodes table, later settings columns). A database predating them
// must still be read, not error out.
func TestGuardSurvivesOlderSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Only the oldest encrypted column exists here; no nodes table at all.
	if _, err := db.Exec(`CREATE TABLE settings (id INTEGER PRIMARY KEY,
		tg_bot_token TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO settings (id, tg_bot_token) VALUES (1, 'enc:v1:old')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	got, err := dbHasEncryptedSecrets(path)
	if err != nil {
		t.Fatalf("guard errored on an older schema: %v", err)
	}
	if !got {
		t.Fatal("guard missed the one encrypted column an older install has")
	}
}
