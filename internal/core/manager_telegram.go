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
		if set.TGUserBotEnabled && strings.TrimSpace(set.TGUserBotToken) == token {
			return invalid("у админ-бота и пользовательского бота должны быть разные токены")
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
		if set.TGBotEnabled && strings.TrimSpace(set.TGBotToken) == token {
			return invalid("у админ-бота и пользовательского бота должны быть разные токены")
		}
	}
	return m.store.SetTelegramUserBot(enabled, token, regMode, regCode)
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
