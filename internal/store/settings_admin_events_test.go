package store

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TestAdminEventsRoundTrip exercises the 0011 migration: a fresh DB defaults to
// "all categories on" (-1), and SetAdminEvents persists an explicit mask that
// GetSettings reads back, gating each AdminEvent* flag correctly.
func TestAdminEventsRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ev.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	// Default -1 → every catalog category enabled out of the box.
	for _, e := range model.AdminEventCatalog {
		if !set.AdminEventEnabled(e.Bit) {
			t.Fatalf("default: category %q should be enabled", e.Key)
		}
	}

	// Persist a mask with only two categories on; the rest must read back off.
	mask := model.AdminEventPayment | model.AdminEventXrayDown
	if err := st.SetAdminEvents(mask); err != nil {
		t.Fatalf("SetAdminEvents: %v", err)
	}
	set, err = st.GetSettings()
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if set.TGAdminEvents != mask {
		t.Fatalf("mask = %d, want %d", set.TGAdminEvents, mask)
	}
	if !set.AdminEventEnabled(model.AdminEventPayment) || !set.AdminEventEnabled(model.AdminEventXrayDown) {
		t.Fatalf("enabled categories not set: %d", set.TGAdminEvents)
	}
	if set.AdminEventEnabled(model.AdminEventExpired) || set.AdminEventEnabled(model.AdminEventRegistered) {
		t.Fatalf("disabled categories should be off: %d", set.TGAdminEvents)
	}

	// Disabling everything (empty mask) must be representable and distinct from default.
	if err := st.SetAdminEvents(0); err != nil {
		t.Fatalf("SetAdminEvents(0): %v", err)
	}
	set, _ = st.GetSettings()
	for _, e := range model.AdminEventCatalog {
		if set.AdminEventEnabled(e.Bit) {
			t.Fatalf("after clear: category %q should be off", e.Key)
		}
	}
}
