package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netguard"
)

// SaveTelegram validates and persists the Telegram bot configuration: the enable
// flag, bot token, the backup schedule as a 5-field cron expression (empty = no
// scheduled backups), and the language the bot writes in. The authorized chat set
// and the pending link code are managed separately (linking happens in the bot /
// via GenerateTelegramLinkCode).
func (m *Manager) SaveTelegram(enabled bool, token, backupCron, lang string) error {
	token = strings.TrimSpace(token)
	backupCron = strings.TrimSpace(backupCron)
	// An unrecognised tag is normalised rather than rejected: this is a dropdown,
	// and a value the panel does not ship is a bug on our side, not the operator's.
	lang = string(i18n.Normalize(lang))
	if enabled && token == "" {
		return invalidCode("err.adminTokenRequired", "укажите токен бота (получите его у @BotFather)")
	}
	if token != "" && !strings.Contains(token, ":") {
		return invalidCode("err.badAdminToken", "токен бота выглядит неверно (формат «123456:ABC...»)")
	}
	if backupCron != "" {
		if _, err := cron.Parse(backupCron); err != nil {
			return invalidCode("err.badCron", "неверное расписание (cron): {{err}}", map[string]any{"err": err})
		}
	}
	if enabled && token != "" {
		set, err := m.store.GetSettings()
		if err != nil {
			return err
		}
		if strings.TrimSpace(set.TGUserBotToken) == token {
			return invalidCode("err.adminUserSameToken", "у админ-бота и пользовательского бота должны быть разные токены")
		}
		if strings.TrimSpace(set.TGSupportBotToken) == token {
			return invalidCode("err.adminSupportSameToken", "у админ-бота и бота поддержки должны быть разные токены")
		}
	}
	if err := m.store.SetTelegramBot(enabled, token, backupCron, lang); err != nil {
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
		return invalidCode("err.unknownRegMode", "неизвестный режим регистрации")
	}
	if regMode == model.RegInvite && regCode == "" {
		return invalidCode("err.inviteCodeRequired", "для регистрации по коду укажите код-приглашение")
	}
	if enabled && token == "" {
		return invalidCode("err.userTokenRequired", "укажите токен пользовательского бота")
	}
	if token != "" && !strings.Contains(token, ":") {
		return invalidCode("err.badUserToken", "токен пользовательского бота выглядит неверно (формат «123456:ABC...»)")
	}
	if enabled && token != "" {
		set, err := m.store.GetSettings()
		if err != nil {
			return err
		}
		if strings.TrimSpace(set.TGBotToken) == token {
			return invalidCode("err.adminUserSameToken", "у админ-бота и пользовательского бота должны быть разные токены")
		}
		if strings.TrimSpace(set.TGSupportBotToken) == token {
			return invalidCode("err.userSupportSameToken", "у пользовательского бота и бота поддержки должны быть разные токены")
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
		return invalidCode("err.badSupportToken", "токен бота поддержки выглядит неверно (формат «123456:ABC...»)")
	}
	if enabled {
		switch {
		case token == "":
			return invalidCode("err.supportTokenRequired", "укажите токен бота поддержки")
		case groupID == 0:
			return invalidCode("err.supportGroupRequired", "укажите группу поддержки (супергруппа с включёнными темами)")
		case username == "":
			return invalidCode("err.supportTokenUnverifiable", "не удалось проверить токен бота поддержки — проверьте его и попробуйте снова")
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
			return invalidCode("err.supportAdminSameToken", "у бота поддержки и админ-бота должны быть разные токены")
		}
		if strings.TrimSpace(set.TGUserBotToken) == token {
			return invalidCode("err.supportUserSameToken", "у бота поддержки и пользовательского бота должны быть разные токены")
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

// TelegramConfig is every Telegram bot's settings in one value, so they can be
// checked together and written together.
type TelegramConfig struct {
	Enabled    bool
	Token      string
	BackupCron string
	// Lang is the language the admin bot writes in. Panel-wide because the bot also
	// pushes unprompted alerts, which carry no Telegram update to read a language
	// from. Empty means the panel default.
	Lang string
	// ProxyMode/Proxy route every Telegram-bound request — all three bots and the
	// Mini App SDK fetch — for servers that cannot reach Telegram. ProxyMode is one
	// of model.TGProxy*; Proxy is the URL the custom mode uses.
	ProxyMode   string
	Proxy       string
	UserEnabled bool
	UserToken   string
	UserRegMode string
	UserRegCode string

	SupportEnabled  bool
	SupportToken    string
	SupportUsername string
	SupportGroupID  int64
	SupportGreeting string
}

// SaveTelegramConfig validates all three bots BEFORE persisting any of them.
//
// Saved one after another, a failure on the third left the first two committed while
// the caller reported failure — so the operator saw an error, the panel had changed,
// and (because the audit middleware skips a failed request) nothing recorded it. The
// third is the one that can fail for reasons outside anyone's control: it needs the
// support bot's @username, which comes from Telegram.
func (m *Manager) SaveTelegramConfig(c TelegramConfig) error {
	if err := m.checkTelegram(c.Enabled, c.Token, c.BackupCron); err != nil {
		return err
	}
	if err := m.checkTelegramUserBot(c.UserEnabled, c.UserToken, c.UserRegMode, c.UserRegCode); err != nil {
		return err
	}
	if _, err := checkTelegramProxy(c.ProxyMode, c.Proxy); err != nil {
		return err
	}
	if err := m.checkTelegramSupportCfg(c); err != nil {
		return err
	}
	// The proxy goes in first. It is what the other three are reached THROUGH, so a
	// save that wrote the bots and then failed would leave them pointed down a route
	// the operator had just replaced.
	if err := m.SaveTelegramProxy(c.ProxyMode, c.Proxy); err != nil {
		return err
	}
	if err := m.SaveTelegram(c.Enabled, c.Token, c.BackupCron, c.Lang); err != nil {
		return err
	}
	if err := m.SaveTelegramUserBot(c.UserEnabled, c.UserToken, c.UserRegMode, c.UserRegCode); err != nil {
		return err
	}
	return m.SaveTelegramSupport(c.SupportEnabled, c.SupportToken, c.SupportUsername,
		c.SupportGroupID, c.SupportGreeting)
}

// SaveTelegramProxy validates and persists how Telegram is reached: the mode, and
// the URL the custom mode uses.
//
// No reconcile. This setting does not change the generated Xray config — WARP's
// loopback entrance exists whenever WARP does, regardless of who dials it — so an
// unrelated Telegram save must not restart Xray and drop every live VPN connection.
func (m *Manager) SaveTelegramProxy(mode, raw string) error {
	raw = strings.TrimSpace(raw)
	mode, err := checkTelegramProxy(mode, raw)
	if err != nil {
		return err
	}
	return m.store.SetTelegramProxy(mode, raw)
}

// telegramEgressWait bounds how long the bots hold off for a local egress. Xray needs
// a couple of seconds past "process started" before its inbound accepts, and several
// more before the WireGuard tunnel behind it carries anything.
const telegramEgressWait = 30 * time.Second

// telegramEgressProbe bounds one readiness probe. Short: it runs in a loop, and a
// slow answer is indistinguishable from a tunnel that is not up yet.
const telegramEgressProbe = 5 * time.Second

// AwaitTelegramEgress blocks until Telegram is reachable through the configured
// proxy — but only when that proxy is an egress this panel brings up itself.
//
// Startup launches Xray and the three bots back to back. An operator who pointed the
// Telegram proxy at the WARP address the Routing page publishes has the bots dialling
// 127.0.0.1:PanelEgressPort while Xray is still coming up: first connection refused,
// then a tunnel that accepts but does not yet carry. They recover on their own, but
// only after the retry backoff unwinds — ~40 s of silent bots on every restart, which
// an operator reads as a broken panel.
//
// Anything else returns immediately. A proxy running elsewhere is not ours to wait on,
// and blocking boot because it happens to be down would turn their outage into our
// delay. A timeout is not fatal either: the bots retry regardless, so this only
// removes the part of the wait we can predict.
func (m *Manager) AwaitTelegramEgress(ctx context.Context) {
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	if !set.IsLocalEgressProxy(set.TelegramProxyURL()) {
		return
	}
	deadline := time.Now().Add(telegramEgressWait)
	for {
		if telegramEgressAlive(ctx, set.TelegramProxyURL()) {
			return
		}
		if time.Now().After(deadline) {
			// Worth a line: at this point the bots are about to start failing for a
			// reason that has nothing to do with their tokens.
			logWarn("telegram egress did not come up in time; the bots will start anyway and retry",
				"proxy", set.TelegramProxyURL(), "waited", telegramEgressWait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// telegramEgressAlive reports whether Telegram is actually reachable through proxy.
//
// It makes a real request rather than just connecting to the port, because for the
// WARP route the port proves nothing: Xray's inbound accepts from the instant the
// process starts, several seconds before the WireGuard handshake behind it finishes.
// A bot started on "the port is open" still talks into a tunnel that silently
// swallows its traffic, and pays a full retry backoff for it.
//
// A var so tests can drive AwaitTelegramEgress without a live proxy, the same way
// telegramSDKFetch is stubbed.
//
// api.telegram.org is the target on purpose — it is the host the bots need, so this
// answers the question that matters instead of a proxy one. No token, so any answer
// at all (302 to the docs, 404, anything) means the path works; only a transport
// failure counts as not-ready.
var telegramEgressAlive = func(ctx context.Context, proxy string) bool {
	ctx, cancel := context.WithTimeout(ctx, telegramEgressProbe)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://api.telegram.org/", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Transport: netguard.ProxyTransport(proxy), Timeout: telegramEgressProbe}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// checkTelegramProxy validates the mode/URL pair and returns the normalized mode.
func checkTelegramProxy(mode, raw string) (string, error) {
	switch mode {
	case "", model.TGProxyDirect:
		return model.TGProxyDirect, nil
	case model.TGProxyCustom:
		// Empty is checked first: ParseProxy accepts it (that is how "direct" is
		// spelled everywhere else), so on its own it would let the custom mode save
		// with no address and quietly behave as direct.
		if raw == "" {
			return "", invalidCode("err.telegramProxyRequired", "укажите адрес прокси")
		}
		// The plain-English reason from ParseProxy travels with the error: "invalid
		// proxy" alone leaves the operator re-reading a URL that looks fine to them,
		// when what's wrong is a missing port or an unsupported scheme.
		if _, err := netguard.ParseProxy(raw); err != nil {
			return "", invalidCode("err.badTelegramProxy", "неверный адрес прокси: {{err}}",
				map[string]any{"err": err})
		}
		return mode, nil
	default:
		return "", invalidCode("err.unknownTelegramProxyMode", "неизвестный режим прокси {{value}}",
			map[string]any{"value": mode})
	}
}

// The check* helpers below are the validation halves of the Save* methods, reusable
// without writing. Cross-token comparisons are made against the INCOMING values, not
// the stored ones: one request may legitimately move a token from one bot to another.
func (m *Manager) checkTelegram(enabled bool, token, backupCron string) error {
	token = strings.TrimSpace(token)
	if enabled && token == "" {
		return invalidCode("err.adminTokenRequired", "укажите токен бота (получите его у @BotFather)")
	}
	if token != "" && !strings.Contains(token, ":") {
		return invalidCode("err.badAdminToken", "токен бота выглядит неверно (формат «123456:ABC...»)")
	}
	if strings.TrimSpace(backupCron) != "" {
		if _, err := cron.Parse(strings.TrimSpace(backupCron)); err != nil {
			return invalidCode("err.badCron", "неверное расписание (cron): {{err}}", map[string]any{"err": err})
		}
	}
	return nil
}

func (m *Manager) checkTelegramUserBot(enabled bool, token, regMode, regCode string) error {
	switch regMode {
	case model.RegOff, model.RegOpen, model.RegModeration, model.RegInvite:
	default:
		return invalidCode("err.unknownRegMode", "неизвестный режим регистрации")
	}
	if regMode == model.RegInvite && strings.TrimSpace(regCode) == "" {
		return invalidCode("err.inviteCodeRequired", "для регистрации по коду укажите код-приглашение")
	}
	token = strings.TrimSpace(token)
	if enabled && token == "" {
		return invalidCode("err.userTokenRequired", "укажите токен пользовательского бота")
	}
	if token != "" && !strings.Contains(token, ":") {
		return invalidCode("err.badUserToken", "токен пользовательского бота выглядит неверно (формат «123456:ABC...»)")
	}
	return nil
}

func (m *Manager) checkTelegramSupportCfg(c TelegramConfig) error {
	token := strings.TrimSpace(c.SupportToken)
	if token != "" && !strings.Contains(token, ":") {
		return invalidCode("err.badSupportToken", "токен бота поддержки выглядит неверно (формат «123456:ABC...»)")
	}
	if c.SupportEnabled {
		switch {
		case token == "":
			return invalidCode("err.supportTokenRequired", "укажите токен бота поддержки")
		case normalizeGroupID(c.SupportGroupID) == 0:
			return invalidCode("err.supportGroupRequired", "укажите группу поддержки (супергруппа с включёнными темами)")
		case strings.TrimSpace(c.SupportUsername) == "":
			return invalidCode("err.supportTokenUnverifiable", "не удалось проверить токен бота поддержки — проверьте его и попробуйте снова")
		}
	}
	// Three distinct bots need three distinct tokens: two poll loops sharing one
	// would each steal half the other's updates.
	admin, user := strings.TrimSpace(c.Token), strings.TrimSpace(c.UserToken)
	switch {
	case admin != "" && admin == user:
		return invalidCode("err.adminUserSameToken", "у админ-бота и пользовательского бота должны быть разные токены")
	case admin != "" && admin == token:
		return invalidCode("err.adminSupportSameToken", "у админ-бота и бота поддержки должны быть разные токены")
	case user != "" && user == token:
		return invalidCode("err.userSupportSameToken", "у пользовательского бота и бота поддержки должны быть разные токены")
	}
	return nil
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
		return "", invalidCode("err.enableAdminBotFirst", "сначала включите админ-бота и сохраните настройки")
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
