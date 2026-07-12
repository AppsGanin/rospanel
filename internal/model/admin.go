package model

// Admin is a panel account.

// Roles form a ladder, not a permission matrix: each role can do everything the one
// below it can, plus more. That keeps the check on a route a rank comparison instead
// of a set lookup, and leaves no combination of checkboxes that locks the owner out
// of their own panel.
const (
	RoleOperator = "operator" // end users, stats, journal — no settings, backups or API
	RoleAdmin    = "admin"    // everything except the admin roster
	RoleOwner    = "owner"    // everything, plus the roster; exactly one, undeletable
)

// roleRank orders the ladder. An unknown role ranks 0 and therefore fails every
// tier check — a row with a corrupt role is powerless, not omnipotent.
var roleRank = map[string]int{
	RoleOperator: 1,
	RoleAdmin:    2,
	RoleOwner:    3,
}

// RoleAtLeast reports whether role clears the given tier.
func RoleAtLeast(role, tier string) bool {
	return roleRank[role] != 0 && roleRank[role] >= roleRank[tier]
}

// GrantableRoles are the roles an owner may hand out. RoleOwner is deliberately
// absent: ownership is singular and moves by transfer, never by grant, so there is
// no path that quietly produces a second owner.
var GrantableRoles = []string{RoleAdmin, RoleOperator}

// GrantableRole reports whether role is one an owner may assign to someone else.
func GrantableRole(role string) bool {
	for _, r := range GrantableRoles {
		if r == role {
			return true
		}
	}
	return false
}

// RoleLabel is the Russian name shown in the panel.
func RoleLabel(role string) string {
	switch role {
	case RoleOwner:
		return "Владелец"
	case RoleAdmin:
		return "Администратор"
	case RoleOperator:
		return "Оператор"
	}
	return role
}

// Admin is one row of the admin roster. The password hash never leaves the store.
type Admin struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	CreatedAt          int64  `json:"created_at"`
	LastLoginAt        int64  `json:"last_login_at"` // 0 — ни разу не входил
}
