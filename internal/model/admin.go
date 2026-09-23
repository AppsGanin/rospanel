package model

// Admin is a panel account.

// A role is a named set of permissions (see perms.go). Two roles ship with every
// panel — "admin" and "operator", the rungs of the ladder this replaced — and the
// owner adds their own. The owner is not a role in that table: they hold every
// permission implicitly, and a check against an unknown role key resolves to no
// permissions at all — a row with a corrupt role is powerless, not omnipotent.
const (
	RoleOperator = "operator" // preset: end users, stats, journal
	RoleAdmin    = "admin"    // preset: everything except the admin roster and its trail
	RoleOwner    = "owner"    // everything, plus the roster and the roles; exactly one
)

// IsPresetRole reports whether key is one of the two roles every panel ships with.
// They can be renamed and re-scoped but not deleted: the rescue CLI demotes a
// previous owner to "admin", and the migration moved every existing account onto
// one of them.
func IsPresetRole(key string) bool { return key == RoleAdmin || key == RoleOperator }

// AdminRole is one role in the roster's role list.
type AdminRole struct {
	Key string `json:"key"`
	// Name is what the operator called it. Empty on a preset nobody renamed: the
	// panel then shows the preset's name in the viewer's own language, which a name
	// stored in the database could not do.
	Name      string   `json:"name"`
	Preset    bool     `json:"preset"`
	Perms     []string `json:"perms"`
	CreatedAt int64    `json:"created_at"`
	// Admins and APIKeys count who holds the role — a role still held cannot be
	// deleted, so the editor says why before anyone tries.
	Admins  int `json:"admins"`
	APIKeys int `json:"api_keys"`
}

// PresetRoleNames are the names the panel shows for the owner and for a preset nobody
// renamed, in every language it speaks (web/src/i18n, "roles.*"). A custom role may not
// take one: the roster's picker would show two roles by the same name.
var PresetRoleNames = map[string][]string{
	RoleOwner:    {"Владелец", "Owner"},
	RoleAdmin:    {"Администратор", "Administrator"},
	RoleOperator: {"Оператор", "Operator"},
}

// MaxRoleName bounds a role's display name.
const MaxRoleName = 48

// Admin is one row of the admin roster. The password hash never leaves the store.
type Admin struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	CreatedAt          int64  `json:"created_at"`
	LastLoginAt        int64  `json:"last_login_at"` // 0 ⇒ never signed in
	// TOTPEnabled says this admin has a confirmed second factor. The SECRET never
	// leaves the server after setup — only this flag does, so the roster can show who
	// is protected without handing anyone else the means to log in as them.
	TOTPEnabled bool `json:"totp_enabled"`
}
