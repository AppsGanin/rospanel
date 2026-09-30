package model

import (
	"slices"
	"testing"
)

// Manage carries view: a form you may save but not read is not a permission anybody
// means, so a role saved with only "manage" gets both.
func TestNormalizePermsAddsViewAndDropsUnknown(t *testing.T) {
	got := NormalizePerms([]string{PermUsersManage, "superuser", PermUsersManage, " " + PermLogs + " "})
	want := []string{PermLogs, PermUsersManage, PermUsersView}
	if !slices.Equal(got, want) {
		t.Errorf("NormalizePerms = %v, want %v", got, want)
	}
	if got := SplitPerms(""); len(got) != 0 {
		t.Errorf("SplitPerms(\"\") = %v, want nothing", got)
	}
	if got := SplitPerms(JoinPerms(want)); !slices.Equal(got, want) {
		t.Errorf("stored form does not round-trip: %v", got)
	}
}

// The presets are the rungs of the old ladder, and nobody must lose or gain access
// by the move: the administrator had everything but the roster's trail, the operator
// had users, groups and statistics.
func TestPresetRolesMatchTheLadder(t *testing.T) {
	admin := NewPermSet(PresetAdminPerms())
	if admin.Has(PermAudit) {
		t.Error("the administrator preset can read the admin trail — that was the owner's alone")
	}
	for _, p := range []string{PermSettingsManage, PermServersManage, PermStatsManage, PermAPI, PermUsersExport} {
		if !admin.Has(p) {
			t.Errorf("the administrator preset lacks %s", p)
		}
	}
	op := NewPermSet(PresetOperatorPerms())
	for _, p := range []string{PermUsersView, PermUsersManage, PermUsersDelete, PermGroupsManage, PermStatsView} {
		if !op.Has(p) {
			t.Errorf("the operator preset lacks %s", p)
		}
	}
	for _, p := range []string{PermSettingsView, PermServersView, PermUsersExport, PermStatsManage, PermAPI, PermBillingView} {
		if op.Has(p) {
			t.Errorf("the operator preset gained %s", p)
		}
	}
}

// Covers is what stops a role from minting an API key broader than itself.
func TestPermSetCovers(t *testing.T) {
	full := FullPermSet()
	op := NewPermSet(PresetOperatorPerms())
	if !full.Covers(op) {
		t.Error("the full set does not cover the operator's")
	}
	if op.Covers(full) {
		t.Error("the operator's set covers everything")
	}
	if full.Covers(OwnerPermSet()) {
		t.Error("every catalog permission covers the owner's set — a role could reach backups")
	}
	if !op.Any() {
		t.Error("Any() with no permission named must be true")
	}
	if op.Any(PermSettingsManage, PermAPI) {
		t.Error("Any matched a permission the set lacks")
	}
}

// Every catalog row is either a view/manage pair or a single permission, and no
// permission appears twice — the editor draws one checkbox per entry.
func TestPermCatalogShape(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range PermCatalog {
		if s.Key == "" || (s.View == "" && s.Manage == "") {
			t.Errorf("catalog row %+v is empty", s)
		}
		for _, p := range []string{s.View, s.Manage} {
			if p == "" {
				continue
			}
			if seen[p] {
				t.Errorf("%s appears twice in the catalog", p)
			}
			seen[p] = true
		}
	}
}

// Webhooks reach further than their own section, so a role holding them holds what
// they reach — and says so in the editor. A stored permission this build no longer
// knows (telegram.manage, now the owner's) is dropped rather than kept.
func TestPermImpliesWhatTheyReach(t *testing.T) {
	if got := NormalizePerms([]string{"telegram.manage", "backup.manage", "system.restore", PermOwner}); len(got) != 0 {
		t.Errorf("a retired permission survived normalisation: %v", got)
	}
	wh := NewPermSet([]string{PermWebhooks})
	if !wh.Has(PermUsersView) || !wh.Has(PermBillingView) || wh.Has(PermUsersManage) {
		t.Errorf("webhooks.manage brings %v", wh.List())
	}
}

// A permission that acts on a section brings the view of it — otherwise the panel
// would have no screen to act from.
func TestActingPermsBringTheirView(t *testing.T) {
	for p, want := range map[string]string{
		PermUsersDelete: PermUsersView, PermUsersExport: PermUsersView, PermPayments: PermBillingView,
	} {
		if !NewPermSet([]string{p}).Has(want) {
			t.Errorf("%s does not bring %s", p, want)
		}
	}
}
