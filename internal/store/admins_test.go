package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// openLegacy builds a database at the schema the panel had *before* the multi-admin
// migration (everything up to, but not including, 0023), seeds it the way a
// single-admin install looked, and hands back the path. Opening it with Open() then
// runs 0023 against real legacy data — which is exactly what happens on the first
// boot after an upgrade, and on the next boot after restoring an old backup.
func openLegacy(t *testing.T, mustChange bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL DEFAULT (unixepoch()))`); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "0023" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no pre-0023 migrations found")
	}
	for _, name := range files {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
	// The single admin a legacy install had, plus the install-wide password gate the
	// panel used to keep on the settings singleton.
	if _, err := db.Exec(
		`INSERT INTO admins (username, password_hash) VALUES ('admin', 'legacy-hash')`,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	gate := 0
	if mustChange {
		gate = 1
	}
	if _, err := db.Exec(
		`UPDATE settings SET must_change_password = ? WHERE id = 1`, gate,
	); err != nil {
		t.Fatalf("seed gate: %v", err)
	}
	return path
}

// The admin who installed the panel must come out of the migration as its owner —
// otherwise nobody can manage the roster and the panel has no way back.
func TestMigrationPromotesLegacyAdminToOwner(t *testing.T) {
	st, err := Open(openLegacy(t, false))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	admins, err := st.ListAdmins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(admins) != 1 {
		t.Fatalf("admins = %d, want 1", len(admins))
	}
	if admins[0].Role != model.RoleOwner {
		t.Errorf("role = %q, want %q", admins[0].Role, model.RoleOwner)
	}
	if admins[0].MustChangePassword {
		t.Error("gate raised on an admin who had already changed the default password")
	}
}

// An install still sitting on admin/admin must stay gated across the upgrade: the
// gate moves from the settings singleton onto the account, and a panel that was
// locked to the password screen before the upgrade is still locked after it.
func TestMigrationCarriesPasswordGateToOwner(t *testing.T) {
	st, err := Open(openLegacy(t, true))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	admins, err := st.ListAdmins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !admins[0].MustChangePassword {
		t.Error("gate lost in the migration: the default password would now be usable")
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "roster.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAdminRosterCRUD(t *testing.T) {
	st := newStore(t)

	owner, err := st.CreateAdmin("owner", "h1", model.RoleOwner, true)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	opID, err := st.CreateAdmin("support", "h2", model.RoleOperator, true)
	if err != nil {
		t.Fatalf("create operator: %v", err)
	}

	// The roster lists the owner first, whoever was created when.
	admins, err := st.ListAdmins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(admins) != 2 || admins[0].ID != owner {
		t.Fatalf("roster = %+v, want owner first of two", admins)
	}

	op, err := st.GetAdmin(opID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if op.Role != model.RoleOperator || !op.MustChangePassword || op.LastLoginAt != 0 {
		t.Errorf("fresh operator = %+v, want operator/gated/never-signed-in", op)
	}

	if err := st.SetAdminRole(opID, model.RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	// Picking your own password lifts the gate; an assigned one re-raises it.
	if err := st.UpdateAdminPassword(opID, "h3", false); err != nil {
		t.Fatalf("update password: %v", err)
	}
	if op, _ = st.GetAdmin(opID); op.Role != model.RoleAdmin || op.MustChangePassword {
		t.Errorf("after promote+password = %+v, want admin/ungated", op)
	}

	if err := st.TouchAdminLogin(opID); err != nil {
		t.Fatalf("touch login: %v", err)
	}
	if op, _ = st.GetAdmin(opID); op.LastLoginAt == 0 {
		t.Error("last_login_at not recorded")
	}

	if err := st.DeleteAdmin(opID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetAdmin(opID); !errors.Is(err, ErrAdminNotFound) {
		t.Errorf("get after delete: err = %v, want ErrAdminNotFound", err)
	}
	if err := st.DeleteAdmin(opID); !errors.Is(err, ErrAdminNotFound) {
		t.Errorf("double delete: err = %v, want ErrAdminNotFound", err)
	}
}

// Deleting an admin has to take their live cookies with it — otherwise a colleague
// who was just removed keeps a working panel until their session happens to expire.
func TestDeleteAdminRevokesSessions(t *testing.T) {
	st := newStore(t)

	id, err := st.CreateAdmin("support", "h", model.RoleOperator, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	token, err := st.CreateSession(id, time.Hour)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, ok := st.LookupSession(token); !ok {
		t.Fatal("fresh session does not resolve")
	}
	if err := st.DeleteAdmin(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := st.LookupSession(token); ok {
		t.Error("session still resolves after its admin was deleted")
	}
}

// Every authenticated request reads the role and the password gate off the session,
// so a role change or a password reset must land on the very next request rather
// than at the next login.
func TestLookupSessionCarriesRoleAndGate(t *testing.T) {
	st := newStore(t)

	id, err := st.CreateAdmin("support", "h", model.RoleOperator, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	token, err := st.CreateSession(id, time.Hour)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	a, ok := st.LookupSession(token)
	if !ok {
		t.Fatal("session does not resolve")
	}
	if a.ID != id || a.Username != "support" {
		t.Fatalf("session admin = %+v, want id %d / support", a, id)
	}
	if a.Role != model.RoleOperator || !a.MustChangePassword {
		t.Errorf("session = %+v, want operator + gated", a)
	}

	if err := st.SetAdminRole(id, model.RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if a, _ = st.LookupSession(token); a.Role != model.RoleAdmin {
		t.Errorf("role = %q on an existing session, want the new %q", a.Role, model.RoleAdmin)
	}
}
