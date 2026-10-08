package core

import (
	"context"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Plugins take part in a few of the panel's decisions (internal/plugin): who may
// sign up, which device may take a slot, what a plan costs this user. Each is asked
// within a fraction of a second and fails open — a plugin that errs or is slow
// never blocks a sign-up or a purchase — and none is asked under the manager's own
// locks: a hook may read the panel back through its API.

// PluginHooks is what the manager asks the plugin host.
type PluginHooks interface {
	// Hooked reports whether any active plugin answers a decision ("beforeDeviceBind"…).
	Hooked(hook string) bool
	// Stopped reports whether a plugin is installed but not running.
	Stopped(id string) bool
	BeforeSignup(ctx context.Context, req model.SignupCheck) (allow bool, reason string)
	BeforeDeviceBind(ctx context.Context, req model.DeviceCheck) (allow bool, reason string)
	QuotePrice(ctx context.Context, req model.PriceRequest) (priceRub int, note string, ok bool)
}

// hookBudget bounds all plugins' answers to one decision together.
const hookBudget = 2 * time.Second

// SetPluginHooks connects the plugin host's decisions.
func (m *Manager) SetPluginHooks(h PluginHooks) {
	m.pluginsMu.Lock()
	m.hooks = h
	m.pluginsMu.Unlock()
}

func (m *Manager) pluginHooks() PluginHooks {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	return m.hooks
}

// SignupAllowed asks the plugins whether a sign-up may go ahead; reason is what
// the refused person is told.
func (m *Manager) SignupAllowed(ctx context.Context, req model.SignupCheck) (bool, string) {
	h := m.pluginHooks()
	if h == nil {
		return true, ""
	}
	// A Telegram that opened the bot first carries what it came with: the invite
	// link's referrer and the source tag, kept for it since /start.
	if req.TelegramID > 0 && req.Ref == "" && req.Source == "" {
		if name, refID, source, err := m.store.ChatOrigin(req.TelegramID); err == nil {
			if req.Username == "" {
				req.Username = name
			}
			req.Source = source
			if refID > 0 {
				req.Ref, _ = m.RefCode(refID)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, hookBudget)
	defer cancel()
	return h.BeforeSignup(ctx, req)
}

// restoresTelegram reports whether a Telegram would get back the account it unlinked
// rather than a new one: that is not a sign-up, and is never put to the plugins.
func (m *Manager) restoresTelegram(chat int64) bool {
	u, err := m.store.GetDetachedUserByPrevChat(chat)
	return err == nil && u != nil
}

// signupRefused is SignupAllowed as an error for the sign-up paths that answer with one.
func (m *Manager) signupRefused(ctx context.Context, req model.SignupCheck) error {
	if ok, reason := m.SignupAllowed(ctx, req); !ok {
		if reason == "" {
			reason = i18n.T(i18n.Normalize(req.Lang), "user.regRefused")
		}
		return invalidCode("err.pluginDenied", reason, map[string]any{"detail": reason})
	}
	return nil
}

// deviceAllowed asks the plugins whether a device the user has not bound yet may
// take a slot. A device already bound is never asked about: it is every refresh.
func (m *Manager) deviceAllowed(u model.User, d model.Device, capacity int) (bool, string) {
	h := m.pluginHooks()
	if h == nil || !h.Hooked("beforeDeviceBind") {
		return true, ""
	}
	bound, count, err := m.store.DeviceBound(u.ID, d.HWID)
	if err != nil || bound {
		return true, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()
	return h.BeforeDeviceBind(ctx, model.DeviceCheck{
		UserID: u.ID, HWID: d.HWID, DeviceOS: d.OS, DeviceModel: d.Model, UserAgent: d.App, IP: d.IP, Count: count, Cap: capacity,
		Lang: string(m.userLang(u.TgChatID)),
	})
}

// pluginPaymentStopped reports whether provider is a plugin's payment method whose
// plugin is installed but not running.
func (m *Manager) pluginPaymentStopped(provider string) bool {
	id, ok := strings.CutPrefix(provider, "plugin.")
	if !ok {
		return false
	}
	h := m.pluginHooks()
	return h != nil && h.Stopped(id)
}

// warmPluginPrice asks the plugins for a purchase's price before applyPlanMu is
// taken: the price quoted under the lock then comes from the plugin host's cache,
// and the lock every payment confirmation needs is never held while a plugin thinks.
func (m *Manager) warmPluginPrice(userID int64, p Purchase) {
	if h := m.pluginHooks(); h == nil || !h.Hooked("quotePrice") {
		return
	}
	if u, err := m.store.GetUser(userID); err == nil {
		_, _, _ = m.quotePurchase(*u, p, time.Now().Unix())
	}
}

// pluginPrice asks the plugins for this user's price of a plan. The answer is held
// to [half of base, base]: a plugin can give a discount, never charge more, never
// give the plan away.
func (m *Manager) pluginPrice(u model.User, req model.PriceRequest) (int, string, bool) {
	h := m.pluginHooks()
	if h == nil || req.BaseRub <= 0 || !h.Hooked("quotePrice") {
		return 0, "", false
	}
	req.Lang = string(m.userLang(u.TgChatID))
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()
	price, note, ok := h.QuotePrice(ctx, req)
	if !ok || price >= req.BaseRub || price < (req.BaseRub+1)/2 {
		return 0, "", false
	}
	return price, note, true
}

// PluginBot is what the user bot asks the plugins: buttons under its menu, the
// answers to their presses and to /commands.
type PluginBot interface {
	BotMenu(ctx context.Context, u model.BotUser, lang string) []model.BotButton
	BotCallback(ctx context.Context, data string, u model.BotUser, lang string) (*model.BotReply, error)
	BotCommand(ctx context.Context, command, args string, u model.BotUser, lang string) (*model.BotReply, error)
	BotCommands(lang string) []model.BotCommandInfo
}

// SetPluginBot connects the plugins to the user bot.
func (m *Manager) SetPluginBot(b PluginBot) {
	m.pluginsMu.Lock()
	m.pbot = b
	m.pluginsMu.Unlock()
}

// PluginBot is the plugins' part of the user bot; nil when none is connected.
func (m *Manager) PluginBot() PluginBot {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	return m.pbot
}
