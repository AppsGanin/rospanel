package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// rosterManager returns a manager whose panel is already owned, mirroring a real
// install: bootstrap creates the owner, everyone else is added through the roster.
func rosterManager(t *testing.T) (*Manager, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "roster.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	hash, err := auth.HashPassword("owner-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ownerID, err := st.CreateAdmin("owner", hash, model.RoleOwner, false)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	return &Manager{store: st}, ownerID
}

func TestCreateAdminGatesTheAssignedPassword(t *testing.T) {
	m, _ := rosterManager(t)

	a, err := m.CreateAdmin("support", "temp-password", model.RoleOperator)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Role != model.RoleOperator {
		t.Errorf("role = %q, want %q", a.Role, model.RoleOperator)
	}
	// The owner picked this password and sent it over a chat window: it is a
	// bootstrap credential, and the account is useless until it's replaced.
	if !a.MustChangePassword {
		t.Error("a password chosen by someone else did not raise the change gate")
	}

	// And choosing their own lifts it.
	if err := m.ChangeAdminPassword(a.ID, "chosen-by-them"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	got, err := m.store.GetAdmin(a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MustChangePassword {
		t.Error("gate still up after the admin picked their own password")
	}
}

func TestCreateAdminRejectsBadInput(t *testing.T) {
	m, _ := rosterManager(t)

	if _, err := m.CreateAdmin("support", "temp-password", model.RoleOperator); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name, username, password, role string
	}{
		{"duplicate login", "support", "temp-password", model.RoleAdmin},
		{"short password", "helper", "short", model.RoleAdmin},
		{"login too short", "ab", "temp-password", model.RoleAdmin},
		{"login with spaces", "the helper", "temp-password", model.RoleAdmin},
		{"unknown role", "helper", "temp-password", "superuser"},
		// The one that matters: no path may quietly produce a second owner.
		{"owner is not grantable", "helper", "temp-password", model.RoleOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.CreateAdmin(tc.username, tc.password, tc.role); err == nil {
				t.Fatal("accepted, want rejected")
			}
		})
	}
	admins, err := m.ListAdmins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(admins) != 2 { // owner + support, nothing the rejected calls left behind
		t.Fatalf("roster grew to %d, want 2", len(admins))
	}
}

// The two moves that would strand the panel: removing the only account that can
// manage the roster, or the owner removing themselves.
func TestRosterCannotStrandThePanel(t *testing.T) {
	m, ownerID := rosterManager(t)

	admin, err := m.CreateAdmin("colleague", "temp-password", model.RoleAdmin)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.DeleteAdmin(ownerID, ownerID); err == nil {
		t.Error("the owner deleted themselves")
	}
	if err := m.DeleteAdmin(admin.ID, ownerID); err == nil {
		t.Error("an admin deleted the owner")
	}
	if err := m.SetAdminRole(ownerID, ownerID, model.RoleOperator); err == nil {
		t.Error("the owner demoted themselves to operator")
	}
	if err := m.ResetAdminPassword(ownerID, ownerID, "new-password"); err == nil {
		t.Error("the owner reset their own password through the roster (bypassing re-auth)")
	}

	// The owner survived all of it and is still the owner.
	owner, err := m.store.GetAdmin(ownerID)
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	if owner.Role != model.RoleOwner {
		t.Fatalf("owner role = %q, want %q", owner.Role, model.RoleOwner)
	}
}

func TestDeleteAdminRejectsUnknownID(t *testing.T) {
	m, ownerID := rosterManager(t)
	err := m.DeleteAdmin(ownerID, 4242)
	if err == nil {
		t.Fatal("deleted an admin that does not exist")
	}
	if !strings.Contains(err.Error(), "не найден") {
		t.Errorf("err = %v, want a not-found message", err)
	}
}

// Resetting a colleague's password is what you do when they are locked out — or
// when you no longer trust them. Either way every session they had must die with
// the old password, or the reset achieves nothing against a stolen cookie.
func TestResetAdminPasswordRevokesSessionsAndRegates(t *testing.T) {
	m, ownerID := rosterManager(t)

	admin, err := m.CreateAdmin("colleague", "temp-password", model.RoleAdmin)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.ChangeAdminPassword(admin.ID, "chosen-by-them"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	token, err := m.store.CreateSession(admin.ID, time.Hour)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	if err := m.ResetAdminPassword(ownerID, admin.ID, "reset-by-owner"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, ok := m.store.LookupSession(token); ok {
		t.Error("session survived a password reset")
	}
	got, err := m.store.GetAdmin(admin.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.MustChangePassword {
		t.Error("an owner-assigned password did not raise the change gate")
	}
	if err := m.ResetAdminPassword(ownerID, admin.ID, "short"); err == nil {
		t.Error("accepted a password below the minimum length")
	}
}
