package core

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Admin roles — what each account may see and do in the panel (see model/perms.go).
// Only the owner reaches these (the routes are gated): a role that could edit roles
// could edit its own into everything.

// ListAdminRoles returns every role with who holds it.
func (m *Manager) ListAdminRoles() ([]model.AdminRole, error) {
	return m.store.ListAdminRoles()
}

// CreateAdminRole adds a role. Its permissions are stored normalised: unknown ones
// dropped, manage carrying view.
func (m *Manager) CreateAdminRole(name string, perms []string) (model.AdminRole, error) {
	name, err := m.checkRoleName("", name, false)
	if err != nil {
		return model.AdminRole{}, err
	}
	r, err := m.store.CreateAdminRole(name, perms)
	if err != nil {
		return model.AdminRole{}, err
	}
	slog.Info("admin roles: created", "role", r.Key, "name", r.Name, "perms", len(r.Perms))
	return r, nil
}

// UpdateAdminRole renames a role and replaces its permissions. A preset may be
// renamed back to "" — it then reads as its built-in name again.
func (m *Manager) UpdateAdminRole(key, name string, perms []string) (model.AdminRole, error) {
	if _, err := m.roleByKey(key); err != nil {
		return model.AdminRole{}, err
	}
	name, err := m.checkRoleName(key, name, model.IsPresetRole(key))
	if err != nil {
		return model.AdminRole{}, err
	}
	if err := m.store.UpdateAdminRole(key, name, perms); err != nil {
		return model.AdminRole{}, err
	}
	slog.Info("admin roles: changed", "role", key, "name", name)
	return m.store.GetAdminRole(key)
}

// DeleteAdminRole removes a role nobody holds. The presets stay: the rescue CLI
// demotes a previous owner to "admin", and every account from before roles existed
// was moved onto one of them.
func (m *Manager) DeleteAdminRole(key string) error {
	if model.IsPresetRole(key) {
		return invalidCode("err.rolePreset", "встроенную роль нельзя удалить")
	}
	if _, err := m.roleByKey(key); err != nil {
		return err
	}
	if err := m.store.DeleteAdminRole(key); err != nil {
		if errors.Is(err, store.ErrRoleInUse) {
			return invalidCode("err.roleInUse", "роль назначена администраторам или API-ключам — сначала смените им роль")
		}
		return err
	}
	slog.Info("admin roles: deleted", "role", key)
	return nil
}

// roleByKey reads a role, turning "no such role" into a validation error.
func (m *Manager) roleByKey(key string) (model.AdminRole, error) {
	r, err := m.store.GetAdminRole(key)
	if errors.Is(err, store.ErrRoleNotFound) {
		return r, invalidCode("err.roleNotFound", "роль не найдена")
	}
	return r, err
}

// checkRoleName trims a role name and holds it to the rules: required for a custom
// role (a preset may go back to its built-in name with ""), bounded, and not the
// name of another role — two roles called the same in the roster's picker is a
// mistake waiting to be made.
func (m *Manager) checkRoleName(key, name string, preset bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" && !preset {
		return "", invalidCode("err.roleNameRequired", "укажите название роли")
	}
	if utf8.RuneCountInString(name) > model.MaxRoleName {
		return "", invalidCode("err.roleNameTooLong", "название роли длиннее {{max}} символов", map[string]any{"max": model.MaxRoleName})
	}
	roles, err := m.store.ListAdminRoles()
	if err != nil {
		return "", err
	}
	// The name this role will be shown under: its own, or a preset's built-in one
	// when left empty — which must not collide either.
	mine := []string{name}
	if name == "" {
		mine = model.PresetRoleNames[key]
	}
	taken := func(other string) bool {
		return slices.ContainsFunc(mine, func(n string) bool { return strings.EqualFold(n, other) })
	}
	for _, r := range roles {
		if r.Key == key {
			continue
		}
		// A preset nobody renamed is shown under its built-in name.
		shown := []string{r.Name}
		if r.Name == "" {
			shown = model.PresetRoleNames[r.Key]
		}
		if slices.ContainsFunc(shown, taken) {
			return "", invalidCode("err.roleNameTaken", "роль с таким названием уже есть")
		}
	}
	if slices.ContainsFunc(model.PresetRoleNames[model.RoleOwner], taken) {
		return "", invalidCode("err.roleNameTaken", "роль с таким названием уже есть")
	}
	return name, nil
}

// CreateAPIKey mints a key with its own permissions — full access, or the set
// ticked for it — never broader than the caller's. Full access carries the owner's
// reach (backups), so only the owner mints it. routes, when given, hold the key to
// those API methods; the caller has checked them against the route table.
func (m *Manager) CreateAPIKey(name string, full bool, perms, routes []string, creator model.PermSet) (*model.APIKey, error) {
	want, err := keyGrant(full, perms, creator)
	if err != nil {
		return nil, err
	}
	return m.store.CreateAPIKey(name, full, want, routes)
}

// keyGrant checks what a key is to be given: something, and nothing the caller
// does not hold themselves.
func keyGrant(full bool, perms []string, caller model.PermSet) ([]string, error) {
	want := model.OwnerPermSet()
	var list []string
	if !full {
		list = model.NormalizePerms(perms)
		if len(list) == 0 {
			return nil, invalidCode("err.keyPermsRequired", "отметьте хотя бы один метод API")
		}
		want = model.NewPermSet(list)
	}
	if !caller.Covers(want) {
		return nil, invalidCode("err.keyRoleTooBroad", "нельзя выдать ключу больше прав, чем у вас самих")
	}
	return list, nil
}

// SetAPIKeyPerms changes what an active key may do: only a key the caller could
// have issued, and only to what they could issue now.
func (m *Manager) SetAPIKeyPerms(id int64, full bool, perms, routes []string, caller model.PermSet) error {
	cur, revoked, ok, err := m.store.APIKeyPerms(id)
	if err != nil {
		return err
	}
	if !ok || revoked {
		return invalidCode("err.keyNotFound", "ключ не найден")
	}
	if !caller.Covers(cur) {
		return invalidCode("err.keyOutranksYou", "у ключа больше прав, чем у вас, — менять или отзывать его может тот, у кого они есть")
	}
	want, err := keyGrant(full, perms, caller)
	if err != nil {
		return err
	}
	if err := m.store.SetAPIKeyPerms(id, full, want, routes); err != nil {
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			return invalidCode("err.keyNotFound", "ключ не найден")
		}
		return err
	}
	return nil
}

// RevokeAPIKey revokes a key the caller could have issued: one whose permissions
// their own cover. Otherwise holding the API permission would let a narrow role cut
// off the owner's full-access integrations.
func (m *Manager) RevokeAPIKey(id int64, caller model.PermSet) error {
	perms, _, ok, err := m.store.APIKeyPerms(id)
	if err != nil {
		return err
	}
	if !ok {
		return invalidCode("err.keyNotFound", "ключ не найден")
	}
	if !caller.Covers(perms) {
		return invalidCode("err.keyOutranksYou", "у ключа больше прав, чем у вас, — менять или отзывать его может тот, у кого они есть")
	}
	return m.store.RevokeAPIKey(id)
}
