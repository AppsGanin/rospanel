package store

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The migration moves every account onto a role row that holds exactly what its rung
// of the old ladder could reach — nobody gains or loses access by upgrading.
func TestPresetRolesAreSeededWithTheLadder(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	for key, want := range map[string][]string{
		model.RoleAdmin:    model.NormalizePerms(model.PresetAdminPerms()),
		model.RoleOperator: model.NormalizePerms(model.PresetOperatorPerms()),
	} {
		r, err := st.GetAdminRole(key)
		if err != nil {
			t.Fatalf("preset %s: %v", key, err)
		}
		if !slices.Equal(r.Perms, want) {
			t.Errorf("preset %s = %v, want %v", key, r.Perms, want)
		}
		if !r.Preset || r.Name != "" {
			t.Errorf("preset %s = %+v, want an unnamed preset", key, r)
		}
	}
}

// A session resolves its permissions from the role on every lookup: everything for
// the owner, nothing for a role key no row answers to (a hand-edited or corrupt row).
func TestSessionPermsResolveFromTheRole(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	sessionPerms := func(role string) model.PermSet {
		t.Helper()
		id, err := st.CreateAdmin("a-"+role, "h", role, false)
		if err != nil {
			t.Fatalf("admin %s: %v", role, err)
		}
		tok, err := st.CreateSession(id, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		a, ok := st.LookupSession(tok)
		if !ok {
			t.Fatal("session did not resolve")
		}
		return a.Perms
	}
	if p := sessionPerms(model.RoleOwner); !p.Covers(model.FullPermSet()) {
		t.Errorf("the owner holds %v, want every permission", p.List())
	}
	if p := sessionPerms(model.RoleOperator); !p.Has(model.PermUsersManage) || p.Has(model.PermSettingsView) {
		t.Errorf("the operator holds %v", p.List())
	}

	id, err := st.CreateAdmin("corrupt", "h", model.RoleOperator, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE admins SET role = 'superuser' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	tok, _ := st.CreateSession(id, time.Hour)
	if a, ok := st.LookupSession(tok); !ok || len(a.Perms) != 0 {
		t.Errorf("an unknown role holds %v, want nothing", a.Perms.List())
	}
}

// A role key is written only while the role exists: a role deleted a moment before
// cannot land on an account or a key.
func TestRoleWritesRefuseAMissingRole(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	if _, err := st.CreateAdmin("x", "h", "r-gone", false); !errors.Is(err, ErrRoleNotFound) {
		t.Errorf("create with a missing role = %v, want ErrRoleNotFound", err)
	}
	id, err := st.CreateAdmin("y", "h", model.RoleOperator, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminRole(id, "r-gone"); !errors.Is(err, ErrRoleNotFound) {
		t.Errorf("set a missing role = %v, want ErrRoleNotFound", err)
	}
	if err := st.SetAdminRole(9999, model.RoleOperator); !errors.Is(err, ErrAdminNotFound) {
		t.Errorf("set on a missing admin = %v, want ErrAdminNotFound", err)
	}
	if _, err := st.CreateAPIKey("k", "r-gone"); !errors.Is(err, ErrRoleNotFound) {
		t.Errorf("key with a missing role = %v, want ErrRoleNotFound", err)
	}
}

// A role still held by an admin or a live API key cannot go; a revoked key does not
// hold it, and keeps the role it was issued with as its record.
func TestDeleteAdminRoleRefusesWhileHeld(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	r, err := st.CreateAdminRole("Поддержка", []string{model.PermUsersView})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id, err := st.CreateAdmin("support", "h", r.Key, false)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	k, err := st.CreateAPIKey("bot", r.Key)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if err := st.DeleteAdminRole(r.Key); !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("delete held by an admin and a key = %v, want ErrRoleInUse", err)
	}
	if err := st.SetAdminRole(id, model.RoleOperator); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAdminRole(r.Key); !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("delete held by a key = %v, want ErrRoleInUse", err)
	}
	if err := st.RevokeAPIKey(k.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAdminRole(r.Key); err != nil {
		t.Fatalf("delete after release: %v", err)
	}
	if _, err := st.GetAdminRole(r.Key); !errors.Is(err, ErrRoleNotFound) {
		t.Errorf("role still readable after delete: %v", err)
	}
	keys, err := st.ListAPIKeys()
	if err != nil || len(keys) != 1 || keys[0].Role != r.Key {
		t.Errorf("revoked key after the role went = %+v (%v), want it to keep %s", keys, err, r.Key)
	}
}

// A key without a role is full access (every key issued before roles), a key with one
// holds the role's set, and a key whose role row vanished holds nothing.
func TestAPIKeyPermsFollowTheRole(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	full, err := st.CreateAPIKey("legacy", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.LookupAPIKey(full.RawKey)
	if err != nil || !got.Perms.Covers(model.FullPermSet()) {
		t.Fatalf("a key with no role = %v (%v), want every permission", got, err)
	}

	op, err := st.CreateAPIKey("ops", model.RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	got, err = st.LookupAPIKey(op.RawKey)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Perms.Has(model.PermUsersManage) || got.Perms.Has(model.PermSettingsView) {
		t.Errorf("operator key perms = %v", got.Perms.List())
	}
	if _, err := st.db.Exec(`UPDATE api_keys SET role = 'gone' WHERE id = ?`, op.ID); err != nil {
		t.Fatal(err)
	}
	got, err = st.LookupAPIKey(op.RawKey)
	if err != nil || len(got.Perms) != 0 {
		t.Errorf("a key whose role vanished = %v (%v), want nothing", got.Perms, err)
	}
}

// A restore asks for the owner's or an administrator's second factor — never fewer
// (a backup where only an administrator had 2FA must not read as having none), and
// never an operator's.
func TestBackupTOTPSecretsAreTheRestorers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "rospanel.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{
		model.RoleOwner:    "OWNERSECRETOWNERSECRET2345",
		model.RoleOperator: "OPERATORSECRETOPERATOR2345",
		model.RoleAdmin:    "ADMINSECRETADMINSECRET2345",
	}
	// Written under a key of the backup's own, the way the restore reads them.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	for role, secret := range secrets {
		id, err := st.CreateAdmin("a-"+role, "h", role, false)
		if err != nil {
			t.Fatal(err)
		}
		enc, err := datasec.EncryptWith(key, secret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.Exec(`UPDATE admins SET totp_secret = ? WHERE id = ?`, enc, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.WriteFile(filepath.Join(dir, "secrets.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := BackupAdminTOTPSecrets(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	slices.Sort(got)
	want := []string{secrets[model.RoleOwner], secrets[model.RoleAdmin]}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("secrets = %v, want the owner's and the administrator's", got)
	}

}
