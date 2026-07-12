package model

import "testing"

// The role ladder is what every route check reduces to, so its edges matter more
// than its middle: an unknown role must clear nothing (a corrupt row is powerless,
// not omnipotent), and no role but the owner clears the owner tier.
func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		role, tier string
		want       bool
	}{
		{RoleOwner, RoleOwner, true},
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleOperator, true},
		{RoleAdmin, RoleOwner, false},
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleOperator, true},
		{RoleOperator, RoleOwner, false},
		{RoleOperator, RoleAdmin, false},
		{RoleOperator, RoleOperator, true},
		// Anything the ladder doesn't know clears nothing at all.
		{"", RoleOperator, false},
		{"superuser", RoleOperator, false},
		{"OWNER", RoleOwner, false},
	}
	for _, tc := range cases {
		if got := RoleAtLeast(tc.role, tc.tier); got != tc.want {
			t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", tc.role, tc.tier, got, tc.want)
		}
	}
}

// Ownership is singular: it can be held, but never handed out.
func TestOwnerIsNotGrantable(t *testing.T) {
	if GrantableRole(RoleOwner) {
		t.Error("owner is grantable — the panel could end up with two owners")
	}
	if !GrantableRole(RoleAdmin) || !GrantableRole(RoleOperator) {
		t.Error("admin and operator must both be grantable")
	}
	if GrantableRole("superuser") || GrantableRole("") {
		t.Error("an unknown role is grantable")
	}
}
