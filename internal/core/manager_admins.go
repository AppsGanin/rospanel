package core

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The admin roster. Only the owner reaches any of this (the routes are gated); the
// rules below are the ones that survive even a legitimate owner making a mistake:
// the owner can neither delete themselves nor be deleted by anyone else, so the
// panel can never end up with nobody who can manage it.

var adminNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)

// minAdminPassword mirrors ChangeAdminPassword's floor — an assigned password is
// held to the same bar as a chosen one.
const minAdminPassword = 8

// ListAdmins returns the admin roster.
func (m *Manager) ListAdmins() ([]model.Admin, error) {
	return m.store.ListAdmins()
}

// CreateAdmin adds an account with a role and a password the owner picked. The
// account is gated on a password change at first login: a password chosen by someone
// else and delivered over a chat window is a bootstrap credential, not a permanent
// one.
func (m *Manager) CreateAdmin(username, password, role string) (model.Admin, error) {
	username = strings.TrimSpace(username)
	if !adminNameRe.MatchString(username) {
		return model.Admin{}, invalidCode("err.loginCharset", "логин: 3–32 символа, латиница, цифры, точка, дефис или подчёркивание")
	}
	if !model.GrantableRole(role) {
		return model.Admin{}, invalidCode("err.unknownRole", "неизвестная роль {{value}}", map[string]any{"value": role})
	}
	if len(password) < minAdminPassword {
		return model.Admin{}, invalidCode("err.passwordTooShort", "пароль должен быть не короче {{min}} символов", map[string]any{"min": minAdminPassword})
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return model.Admin{}, err
	}
	id, err := m.store.CreateAdmin(username, hash, role, true)
	if err != nil {
		return model.Admin{}, invalidCode("err.adminCreateFailed", "не удалось создать администратора (логин уже занят?)")
	}
	slog.Info("admin roster: created", "admin", username, "role", role, "id", id)
	return m.store.GetAdmin(id)
}

// DeleteAdmin removes an account. Deleting it revokes its sessions too (the
// admin_sessions rows cascade), so a colleague who is let go loses the panel on
// their next request, not when their cookie happens to expire.
func (m *Manager) DeleteAdmin(actorID, targetID int64) error {
	target, err := m.rosterTarget(actorID, targetID, opDelete)
	if err != nil {
		return err
	}
	if err := m.store.DeleteAdmin(targetID); err != nil {
		return err
	}
	slog.Info("admin roster: deleted", "admin", target.Username, "id", targetID)
	return nil
}

// SetAdminRole moves an account between roles.
func (m *Manager) SetAdminRole(actorID, targetID int64, role string) error {
	target, err := m.rosterTarget(actorID, targetID, opEdit)
	if err != nil {
		return err
	}
	if !model.GrantableRole(role) {
		return invalidCode("err.unknownRole", "неизвестная роль {{value}}", map[string]any{"value": role})
	}
	if err := m.store.SetAdminRole(targetID, role); err != nil {
		return err
	}
	slog.Info("admin roster: role changed", "admin", target.Username, "role", role)
	return nil
}

// ResetAdminPassword assigns a new password to another admin — for when a colleague
// is locked out. Like a freshly created account it is gated on a change at first
// login, and every session that account had is revoked: whoever was using the old
// password is out.
func (m *Manager) ResetAdminPassword(actorID, targetID int64, password string) error {
	target, err := m.rosterTarget(actorID, targetID, opReset)
	if err != nil {
		return err
	}
	if len(password) < minAdminPassword {
		return invalidCode("err.passwordTooShort", "пароль должен быть не короче {{min}} символов", map[string]any{"min": minAdminPassword})
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := m.store.UpdateAdminPassword(targetID, hash, true); err != nil {
		return err
	}
	if err := m.store.DeleteSessionsForAdmin(targetID); err != nil {
		return err
	}
	slog.Info("admin roster: password reset", "admin", target.Username)
	return nil
}

// rosterOp names the two refusals a roster action can raise. The verb ("delete",
// "edit") is part of the sentence, so it cannot travel as an argument: an argument
// reaches the panel verbatim and would land in Russian on an English screen. Hence
// a code per operation rather than one code with the verb filled in.
type rosterOp struct {
	ownerCode, ownerMsg string
	selfCode, selfMsg   string
}

var (
	opDelete = rosterOp{
		"err.cannotDeleteOwner", "нельзя удалить владельца панели",
		"err.cannotDeleteSelf", "нельзя удалить собственную учётную запись",
	}
	opEdit = rosterOp{
		"err.cannotEditOwner", "нельзя изменить владельца панели",
		"err.cannotEditSelf", "нельзя изменить собственную учётную запись",
	}
	opReset = rosterOp{
		"err.cannotResetOwner", "нельзя сбросить пароль владельца панели",
		"err.cannotResetSelf", "нельзя сбросить пароль собственной учётной записи",
	}
)

// rosterTarget resolves the admin an owner is acting on and rejects the two moves
// that would strand the panel: acting on the owner (there is exactly one, and it
// must remain) and acting on yourself through the roster (your own login and
// password live in the profile dialog, which re-verifies the current password —
// the roster does not).
func (m *Manager) rosterTarget(actorID, targetID int64, op rosterOp) (model.Admin, error) {
	target, err := m.store.GetAdmin(targetID)
	if errors.Is(err, store.ErrAdminNotFound) {
		return model.Admin{}, invalidCode("err.adminNotFound", "администратор не найден")
	}
	if err != nil {
		return model.Admin{}, err
	}
	if target.Role == model.RoleOwner {
		return model.Admin{}, invalidCode(op.ownerCode, op.ownerMsg)
	}
	if target.ID == actorID {
		return model.Admin{}, invalidCode(op.selfCode, op.selfMsg)
	}
	return target, nil
}
