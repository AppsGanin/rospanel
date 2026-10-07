package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// The panel's decisions plugins take part in: who may sign up, which device may take
// a slot, what a plan costs. Every one fails open — an error, a timeout or a busy
// plugin means "no objection" — because a broken plugin must never stop people
// signing up or paying. Inside them panel.api only reads.

const (
	decisionTTL = time.Minute // a refusal, and a price, are kept this long
	maxDecided  = 10000
)

type decisionCache struct {
	mu sync.Mutex
	m  map[string]cachedDecision
}

type cachedDecision struct {
	raw json.RawMessage
	at  time.Time
}

func (c *decisionCache) get(key string) (json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.m[key]
	if !ok || time.Since(d.at) >= decisionTTL {
		return nil, false
	}
	return d.raw, true
}

func (c *decisionCache) put(key string, raw json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= maxDecided { // a bound, not an eviction policy
		c.m = map[string]cachedDecision{}
	}
	c.m[key] = cachedDecision{raw: raw, at: time.Now()}
}

// Hooked reports whether an active plugin answers a decision: callers skip the work
// of building its question otherwise.
func (h *Host) Hooked(hook string) bool {
	return len(h.Active(hookPick(hook))) > 0
}

func hookPick(hook string) func(*manifest.Manifest) bool {
	return func(m *manifest.Manifest) bool {
		switch hook {
		case "quotePrice":
			return m.Provides.Price
		default:
			return slices.Contains(m.Provides.Hooks, hook)
		}
	}
}

// BeforeSignup asks the plugins whether a new account may be made.
func (h *Host) BeforeSignup(ctx context.Context, req model.SignupCheck) (bool, string) {
	return h.decide(ctx, manifest.HookBeforeSignup, req, req.Lang, "")
}

// BeforeDeviceBind asks the plugins whether a new device may take a slot. A refusal
// is kept for a minute: the refused client asks again on every refresh.
func (h *Host) BeforeDeviceBind(ctx context.Context, req model.DeviceCheck) (bool, string) {
	return h.decide(ctx, manifest.HookBeforeDeviceBind, req, req.Lang,
		fmt.Sprintf("%d/%s/%s/%s", req.UserID, req.DeviceOS, req.DeviceModel, req.IP))
}

// decide asks each plugin with the hook in order; the first refusal wins. key, when
// set, caches each plugin's answer under it.
func (h *Host) decide(ctx context.Context, hook string, req any, lang, key string) (bool, string) {
	for _, id := range h.Active(hookPick(hook)) {
		if ctx.Err() != nil {
			break // the panel's budget for this decision is spent
		}
		raw, ok := h.ask(ctx, id, hook, req, lang, key)
		if !ok {
			continue
		}
		if allow, reason := verdict(raw); !allow {
			return false, h.reason(id, lang, reason)
		}
	}
	return true, ""
}

// ask calls a decision export, through the cache when key is set; ok is false when
// the plugin gave no answer.
func (h *Host) ask(ctx context.Context, id, export string, req any, lang, key string) (json.RawMessage, bool) {
	if key != "" {
		if raw, ok := h.decided.get(id + "/" + export + "/" + key); ok {
			return raw, true
		}
	}
	raw, err := h.call(ctx, id, export, req, HookTimeout, callOpts{readOnly: true, lang: lang, wait: HookTimeout})
	if err != nil {
		return nil, false
	}
	if key != "" {
		h.decided.put(id+"/"+export+"/"+key, raw)
	}
	return raw, true
}

// verdict reads {allow, reason} — or a bare false. Anything else allows.
func verdict(raw json.RawMessage) (bool, string) {
	var d struct {
		Allow  *bool  `json:"allow"`
		Reason string `json:"reason"`
	}
	if strings.TrimSpace(string(raw)) == "false" {
		return false, ""
	}
	if json.Unmarshal(raw, &d) != nil || d.Allow == nil {
		return true, ""
	}
	return *d.Allow, d.Reason
}

// reason turns a plugin's key into its text, from the plugin's own strings; a key
// that is not there is shown as it is, so a plain sentence works too.
func (h *Host) reason(id, lang, key string) string {
	if key == "" {
		return ""
	}
	inst, err := h.get(id)
	if err != nil {
		return ""
	}
	if p := inst.pub.Load(); p != nil && p.pkg != nil {
		return clip(p.pkg.Translate(lang, key, nil))
	}
	return clip(key)
}

// QuotePrice asks the plugins for a user's price of a plan: the first answer within
// [half of base, base] counts. An answer outside is ignored and logged for the
// plugin's author. Answers are kept for a minute — a plan screen asks for every
// period at once, and the order then has the price the screen showed.
func (h *Host) QuotePrice(ctx context.Context, req model.PriceRequest) (int, string, bool) {
	key := fmt.Sprintf("%d/%d/%d/%d/%d/%s", req.UserID, req.PlanID, req.Periods, req.Devices, req.BaseRub, req.Lang)
	floor := (req.BaseRub + 1) / 2
	for _, id := range h.Active(hookPick("quotePrice")) {
		if ctx.Err() != nil {
			break
		}
		raw, ok := h.ask(ctx, id, "quotePrice", req, req.Lang, key)
		if !ok {
			continue
		}
		var q struct {
			PriceRub *int   `json:"price_rub"`
			Note     string `json:"note"`
		}
		if json.Unmarshal(raw, &q) != nil || q.PriceRub == nil {
			continue // no opinion
		}
		if *q.PriceRub < floor || *q.PriceRub > req.BaseRub {
			if inst, err := h.get(id); err == nil {
				inst.logf("warn", "quotePrice: %d ₽ is outside %d-%d ₽ for a base of %d ₽, ignored", *q.PriceRub, floor, req.BaseRub, req.BaseRub)
			}
			continue
		}
		return *q.PriceRub, h.reason(id, req.Lang, q.Note), true
	}
	return 0, "", false
}
