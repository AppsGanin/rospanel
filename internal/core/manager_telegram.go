package core

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/model"
)

// SaveTelegram validates and persists the Telegram bot configuration: the enable
// flag, bot token, and backup schedule as a 5-field cron expression (empty = no
// scheduled backups). The authorized chat set and the pending link code are managed
// separately (linking happens in the bot / via GenerateTelegramLinkCode).
func (m *Manager) SaveTelegram(enabled bool, token, backupCron string) error {
	token = strings.TrimSpace(token)
	backupCron = strings.TrimSpace(backupCron)
	if enabled && token == "" {
		return invalid("укажите токен бота (получите его у @BotFather)")
	}
	if token != "" && !strings.Contains(token, ":") {
		return invalid("токен бота выглядит неверно (формат «123456:ABC...»)")
	}
	if backupCron != "" {
		if _, err := cron.Parse(backupCron); err != nil {
			return invalid("неверное расписание (cron): %v", err)
		}
	}
	if enabled && token != "" {
		set, err := m.store.GetSettings()
		if err != nil {
			return err
		}
		if strings.TrimSpace(set.TGUserBotToken) == token {
			return invalid("у админ-бота и пользовательского бота должны быть разные токены")
		}
		if strings.TrimSpace(set.TGSupportBotToken) == token {
			return invalid("у админ-бота и бота поддержки должны быть разные токены")
		}
	}
	if err := m.store.SetTelegramBot(enabled, token, backupCron); err != nil {
		return err
	}
	// Disabling the bot drops any pending link request — it can't be completed
	// while the bot isn't polling, so leaving it would be misleading.
	if !enabled {
		return m.store.SetTelegramLinkCode("")
	}
	return nil
}

// SaveTelegramUserBot validates and persists the public user bot configuration:
// the enable flag, its (separate) bot token, the self-registration mode and (for
// the invite mode) the invite code. The token must differ from the admin bot's.
func (m *Manager) SaveTelegramUserBot(enabled bool, token, regMode, regCode string) error {
	token = strings.TrimSpace(token)
	regCode = strings.TrimSpace(regCode)
	switch regMode {
	case model.RegOff, model.RegOpen, model.RegModeration, model.RegInvite:
	default:
		return invalid("неизвестный режим регистрации")
	}
	if regMode == model.RegInvite && regCode == "" {
		return invalid("для регистрации по коду укажите код-приглашение")
	}
	if enabled && token == "" {
		return invalid("укажите токен пользовательского бота")
	}
	if token != "" && !strings.Contains(token, ":") {
		return invalid("токен пользовательского бота выглядит неверно (формат «123456:ABC...»)")
	}
	if enabled && token != "" {
		set, err := m.store.GetSettings()
		if err != nil {
			return err
		}
		if strings.TrimSpace(set.TGBotToken) == token {
			return invalid("у админ-бота и пользовательского бота должны быть разные токены")
		}
		if strings.TrimSpace(set.TGSupportBotToken) == token {
			return invalid("у пользовательского бота и бота поддержки должны быть разные токены")
		}
	}
	return m.store.SetTelegramUserBot(enabled, token, regMode, regCode)
}

// SaveTelegramSupport validates and persists the support relay: its own bot token,
// the forum supergroup admins answer in, and the /start greeting. username is the
// bot's resolved @username — the caller looks it up (core deliberately doesn't talk
// to Telegram) and enabling without one is refused, because the user bot renders its
// support button only for a non-empty username and the operator would be left with
// support switched on and no visible way in.
func (m *Manager) SaveTelegramSupport(enabled bool, token, username string, groupID int64, greeting string) error {
	groupID = normalizeGroupID(groupID)
	token = strings.TrimSpace(token)
	username = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	greeting = strings.TrimSpace(greeting)
	// Shape first: a token with an obvious typo gets the message that says what a
	// token looks like, not the generic "couldn't verify" it would otherwise hit —
	// getMe rejects a malformed token exactly like an unknown one.
	if token != "" && !strings.Contains(token, ":") {
		return invalid("токен бота поддержки выглядит неверно (формат «123456:ABC...»)")
	}
	if enabled {
		switch {
		case token == "":
			return invalid("укажите токен бота поддержки")
		case groupID == 0:
			return invalid("укажите группу поддержки (супергруппа с включёнными темами)")
		case username == "":
			return invalid("не удалось проверить токен бота поддержки — проверьте его и попробуйте снова")
		}
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	// Compared regardless of whether the other bot is currently enabled: sharing a
	// token with a disabled bot saves fine today and breaks the day it is switched
	// on, when two poll loops race for one update stream and each steals half the
	// other's messages.
	if token != "" {
		if strings.TrimSpace(set.TGBotToken) == token {
			return invalid("у бота поддержки и админ-бота должны быть разные токены")
		}
		if strings.TrimSpace(set.TGUserBotToken) == token {
			return invalid("у бота поддержки и пользовательского бота должны быть разные токены")
		}
	}
	// No mapping reset here. Topic rows carry the group that issued them, so a
	// mapping from another group simply never matches — which is what makes every
	// transition (A→B, A→0→B, re-picking A after clearing the field) safe by
	// construction. A reset had to be exactly right on all of them, and each way of
	// getting it wrong either delivered one customer's messages into another's thread
	// or orphaned live conversations Telegram gives no way to find again.
	return m.store.SetTelegramSupport(enabled, token, username, groupID, greeting)
}

// normalizeGroupID repairs the one mistake everyone makes when typing a supergroup
// id by hand. Telegram shows the bare internal id (in a web URL, or via an id-printing
// bot), while the API wants it prefixed with -100 — so a pasted "1234567890" has to
// become "-1001234567890" or every call reports the group as unreachable.
//
// Only a positive number is repaired. A negative one is already in some -prefixed
// form, and guessing which would risk pointing support at a different chat entirely.
func normalizeGroupID(id int64) int64 {
	if id <= 0 {
		return id
	}
	full, err := strconv.ParseInt("-100"+strconv.FormatInt(id, 10), 10, 64)
	if err != nil {
		return id // absurdly long: leave it and let validation complain
	}
	return full
}

// ListSupportGroups returns the groups the support bot has been added to, for the
// settings picker. They are options only — see the store for why none is ever
// applied on its own.
func (m *Manager) ListSupportGroups() ([]model.SupportGroup, error) {
	return m.store.ListSupportGroups()
}

// CancelTelegramLink clears the pending one-time link code (cancels a link request).
func (m *Manager) CancelTelegramLink() error {
	return m.store.SetTelegramLinkCode("")
}

// GenerateTelegramLinkCode issues a fresh one-time linking code and persists it.
// The operator sends "/start <code>" to the bot once to authorize their chat; the
// bot burns the code on use. Refused when the bot is disabled — it isn't polling
// then, so the code could never be redeemed.
func (m *Manager) GenerateTelegramLinkCode() (string, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return "", err
	}
	if !set.TGBotEnabled {
		return "", invalid("сначала включите админ-бота и сохраните настройки")
	}
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b[:]) // 10 hex chars, easy to type
	if err := m.store.SetTelegramLinkCode(code); err != nil {
		return "", err
	}
	return code, nil
}

// UnlinkTelegramChat removes one authorized chat (revokes its bot access).
func (m *Manager) UnlinkTelegramChat(id int64) error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	var kept []int64
	found := false
	for _, c := range set.TelegramChatIDs() {
		if c == id {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if err := m.store.SetTelegramChats(joinChatIDs(kept)); err != nil {
		return err
	}
	if found {
		slog.Info("telegram: chat unlinked", "id", id)
	}
	return nil
}

// joinChatIDs renders chat IDs as the comma-separated tg_chat_ids column value.
func joinChatIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}
