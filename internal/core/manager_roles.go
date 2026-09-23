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

// CreateAPIKey mints a key acting with role ("" = full access), refusing one broader
// than the admin creating it: holding the API permission must not become a way to
// get a credential for what your own role withholds.
func (m *Manager) CreateAPIKey(name, role string, creator model.PermSet) (*model.APIKey, error) {
	// Full access carries the owner's reach (backups), so only the owner mints it.
	want := model.OwnerPermSet()
	if role != "" {
		if role == model.RoleOwner {
			return nil, invalidCode("err.unknownRole", "неизвестная роль {{value}}", map[string]any{"value": role})
		}
		r, err := m.store.GetAdminRole(role)
		if errors.Is(err, store.ErrRoleNotFound) {
			return nil, invalidCode("err.unknownRole", "неизвестная роль {{value}}", map[string]any{"value": role})
		}
		if err != nil {
			return nil, err
		}
		want = model.NewPermSet(r.Perms)
	}
	if !creator.Covers(want) {
		return nil, invalidCode("err.keyRoleTooBroad", "нельзя выдать ключу больше прав, чем у вас самих")
	}
	k, err := m.store.CreateAPIKey(name, role)
	if errors.Is(err, store.ErrRoleNotFound) { // deleted between the check and the write
		return nil, invalidCode("err.unknownRole", "неизвестная роль {{value}}", map[string]any{"value": role})
	}
	return k, err
}

// RevokeAPIKey revokes a key the caller could have issued: one whose permissions
// their own cover. Otherwise holding the API permission would let a narrow role cut
// off the owner's full-access integrations.
func (m *Manager) RevokeAPIKey(id int64, caller model.PermSet) error {
	perms, ok, err := m.store.APIKeyPerms(id)
	if err != nil {
		return err
	}
	if !ok {
		return invalidCode("err.keyNotFound", "ключ не найден")
	}
	if !caller.Covers(perms) {
		return invalidCode("err.keyOutranksYou", "у ключа больше прав, чем у вас, — отозвать его может тот, у кого они есть")
	}
	return m.store.RevokeAPIKey(id)
}
