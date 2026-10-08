package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// Plugins in the panel's user bot: buttons under its menu and /commands. The bot
// answers every chat from one loop, so a plugin is given little time there — the
// menu is asked within botMenuTimeout and kept for botMenuTTL, a press or a command
// within botTimeout — and a plugin that fails leaves the bot's own screens as they are.

const (
	botMenuTimeout = 300 * time.Millisecond
	botMenuTTL     = 5 * time.Minute
	botTimeout     = 3 * time.Second
	// BotPrefix starts the callback data of a plugin's button: px:<plugin>:<data>.
	BotPrefix      = "px:"
	maxCallbackLen = 64 // Telegram's limit on callback_data, in bytes
	maxBotButtons  = 6  // per plugin in the menu
	maxReplyRows   = 8
	maxReplyText   = 3500
	maxButtonText  = 64
)

type botCache struct {
	mu sync.Mutex
	m  map[string]cachedMenu
}

type cachedMenu struct {
	buttons []model.BotButton
	at      time.Time
}

// BotMenu returns the plugins' buttons for one user's menu, their data already
// prefixed for the bot to route back.
func (h *Host) BotMenu(ctx context.Context, u model.BotUser, lang string) []model.BotButton {
	ids := h.Active(func(m *manifest.Manifest) bool { return m.Provides.Bot != nil && m.Provides.Bot.Menu })
	parts := make([][]model.BotButton, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		ck := fmt.Sprintf("%s/%d/%d/%s", id, u.ID, u.TelegramID, lang)
		h.bot.mu.Lock()
		c, ok := h.bot.m[ck]
		h.bot.mu.Unlock()
		if ok && time.Since(c.at) < botMenuTTL {
			parts[i] = c.buttons
			continue
		}
		wg.Add(1)
		go func(i int, id, ck string) {
			defer wg.Done()
			var buttons []model.BotButton
			raw, err := h.call(ctx, id, "bot.menu", map[string]any{"user": u, "lang": lang}, botMenuTimeout,
				callOpts{lang: lang, readOnly: true, wait: botMenuTimeout})
			if err == nil {
				var got []model.BotButton
				if json.Unmarshal(raw, &got) == nil {
					for _, b := range got {
						if b, ok := botButton(id, b, true); ok && len(buttons) < maxBotButtons {
							buttons = append(buttons, b)
						}
					}
				}
			}
			parts[i] = buttons
			if err != nil {
				return // a busy or failing plugin is asked again next time, not remembered empty
			}
			h.bot.mu.Lock()
			if h.bot.m == nil || len(h.bot.m) > 10000 {
				h.bot.m = map[string]cachedMenu{}
			}
			h.bot.m[ck] = cachedMenu{buttons: buttons, at: time.Now()}
			h.bot.mu.Unlock()
		}(i, id, ck)
	}
	wg.Wait()
	var out []model.BotButton
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ErrNoBot is a press or a command no active plugin answers.
var ErrNoBot = errors.New("plugin: no such bot button or command")

// BotCallback answers a press of a plugin's button; data is what follows px:.
func (h *Host) BotCallback(ctx context.Context, data string, u model.BotUser, lang string) (*model.BotReply, error) {
	id, rest, ok := strings.Cut(data, ":")
	m := h.manifestOf(id)
	if !ok || m == nil || m.Provides.Bot == nil || !m.Provides.Bot.Menu {
		return nil, ErrNoBot
	}
	raw, err := h.call(ctx, id, "bot.onCallback", map[string]any{"user": u, "data": rest, "lang": lang}, botTimeout,
		callOpts{lang: lang, wait: botTimeout})
	if err != nil {
		return nil, err
	}
	return botReply(id, m, raw), nil
}

// BotCommand answers /command from the first plugin (in order) that declares it.
func (h *Host) BotCommand(ctx context.Context, command, args string, u model.BotUser, lang string) (*model.BotReply, error) {
	for _, id := range h.Active(func(m *manifest.Manifest) bool { return hasCommand(m, command) }) {
		m := h.manifestOf(id)
		if m == nil {
			continue
		}
		raw, err := h.call(ctx, id, "bot.onCommand", map[string]any{"user": u, "command": command, "args": args, "lang": lang},
			botTimeout, callOpts{lang: lang, wait: botTimeout})
		if err != nil {
			return nil, err
		}
		return botReply(id, m, raw), nil
	}
	return nil, ErrNoBot
}

// BotCommands lists the active plugins' commands for the bot's command menu; a
// command two plugins declare is listed once, for the first.
func (h *Host) BotCommands(lang string) []model.BotCommandInfo {
	var out []model.BotCommandInfo
	seen := map[string]bool{"start": true}
	for _, id := range h.Active(func(m *manifest.Manifest) bool { return m.Provides.Bot != nil }) {
		m := h.manifestOf(id)
		if m == nil {
			continue
		}
		for _, c := range m.Provides.Bot.Commands {
			if !seen[c.Command] {
				seen[c.Command] = true
				out = append(out, model.BotCommandInfo{Command: c.Command, Description: c.Description.Get(lang)})
			}
		}
	}
	return out
}

func hasCommand(m *manifest.Manifest, command string) bool {
	if m.Provides.Bot == nil {
		return false
	}
	for _, c := range m.Provides.Bot.Commands {
		if c.Command == command {
			return true
		}
	}
	return false
}

// botReply reads {text, buttons}: buttons as rows, or a flat list of one per row.
// A data button needs the plugin's onCallback (a menu); others are dropped.
func botReply(id string, m *manifest.Manifest, raw json.RawMessage) *model.BotReply {
	var r struct {
		Text    string            `json:"text"`
		Buttons []json.RawMessage `json:"buttons"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			r.Text = s
		}
	}
	out := &model.BotReply{Text: clipText(r.Text, maxReplyText)}
	withData := m.Provides.Bot != nil && m.Provides.Bot.Menu
	for _, el := range r.Buttons {
		if len(out.Buttons) == maxReplyRows {
			break
		}
		var row []model.BotButton
		if json.Unmarshal(el, &row) != nil {
			var one model.BotButton
			if json.Unmarshal(el, &one) != nil {
				continue
			}
			row = []model.BotButton{one}
		}
		var clean []model.BotButton
		for _, b := range row {
			if b, ok := botButton(id, b, withData); ok && len(clean) < 3 {
				clean = append(clean, b)
			}
		}
		if len(clean) > 0 {
			out.Buttons = append(out.Buttons, clean)
		}
	}
	return out
}

// botButton keeps a button the bot can send: text, and either an https link or
// data that fits Telegram's 64 bytes once prefixed.
func botButton(id string, b model.BotButton, withData bool) (model.BotButton, bool) {
	b.Text = clipText(strings.TrimSpace(b.Text), maxButtonText)
	if b.Text == "" {
		return b, false
	}
	switch {
	case b.URL != "":
		if !strings.HasPrefix(b.URL, "https://") || len(b.URL) > 2048 {
			return b, false
		}
		b.Data = ""
	case b.Data != "" && withData:
		b.Data = BotPrefix + id + ":" + b.Data
		if len(b.Data) > maxCallbackLen {
			return b, false
		}
	default:
		return b, false
	}
	return b, true
}

// clipText cuts s to n bytes on a rune boundary.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
