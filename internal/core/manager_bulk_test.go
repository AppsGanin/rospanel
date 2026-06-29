package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/store"
)

// bulkTestManager builds a Manager over a fresh store with no Xray supervisor.
// The bulk actions exercised here (enable/disable/delete/extend) never touch the
// supervisor, and TriggerUserSync is a no-op on a nil reconcile channel (select +
// default), so this minimal wiring is sufficient.
func bulkTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bulk.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{store: st}
}

func mkUser(t *testing.T, m *Manager, name string, expireAt int64) int64 {
	t.Helper()
	u, err := m.store.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, expireAt, 0)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u.ID
}

func TestBulkEnableDisableDelete(t *testing.T) {
	m := bulkTestManager(t)
	a := mkUser(t, m, "a", 0)
	b := mkUser(t, m, "b", 0)
	c := mkUser(t, m, "c", 0)
	ids := []int64{a, b, c}

	// Disable all three in one pass.
	n, err := m.BulkUserAction(ids, "disable", 0)
	if err != nil || n != 3 {
		t.Fatalf("bulk disable: n=%d err=%v", n, err)
	}
	for _, id := range ids {
		u, _ := m.store.GetUser(id)
		if u.Enabled {
			t.Fatalf("user %d still enabled after bulk disable", id)
		}
	}

	// Re-enable just two.
	if n, err = m.BulkUserAction([]int64{a, b}, "enable", 0); err != nil || n != 2 {
		t.Fatalf("bulk enable: n=%d err=%v", n, err)
	}
	if u, _ := m.store.GetUser(a); !u.Enabled {
		t.Fatalf("user a not re-enabled")
	}
	if u, _ := m.store.GetUser(c); u.Enabled {
		t.Fatalf("user c should stay disabled")
	}

	// Delete all three.
	if n, err = m.BulkUserAction(ids, "delete", 0); err != nil || n != 3 {
		t.Fatalf("bulk delete: n=%d err=%v", n, err)
	}
	all, _ := m.store.ListUsers()
	if len(all) != 0 {
		t.Fatalf("expected no users after bulk delete, got %d", len(all))
	}
}

func TestBulkExtendSkipsUnlimited(t *testing.T) {
	m := bulkTestManager(t)
	now := time.Now().Unix()
	limited := mkUser(t, m, "limited", now+10*86400) // has an expiry → extended
	never := mkUser(t, m, "never", 0)                // no expiry → skipped

	n, err := m.BulkUserAction([]int64{limited, never}, "extend", 5)
	if err != nil {
		t.Fatalf("bulk extend: %v", err)
	}
	if n != 1 {
		t.Fatalf("affected = %d, want 1 (the unlimited user must be skipped)", n)
	}
	u, _ := m.store.GetUser(limited)
	if want := now + 15*86400; u.ExpireAt < want-5 || u.ExpireAt > want+5 {
		t.Fatalf("expiry = %d, want ~%d (stacked on current)", u.ExpireAt, want)
	}
	if u, _ := m.store.GetUser(never); u.ExpireAt != 0 {
		t.Fatalf("unlimited user gained an expiry: %d", u.ExpireAt)
	}
}

func TestBulkUserActionValidation(t *testing.T) {
	m := bulkTestManager(t)
	if _, err := m.BulkUserAction(nil, "enable", 0); err == nil {
		t.Fatal("expected error for empty selection")
	}
	id := mkUser(t, m, "x", 0)
	if _, err := m.BulkUserAction([]int64{id}, "bogus", 0); err == nil {
		t.Fatal("expected error for unknown action")
	}
	if _, err := m.BulkUserAction([]int64{id}, "extend", 0); err == nil {
		t.Fatal("expected error for extend with non-positive days")
	}
}
