package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// What plugins add to the panel's own surfaces — a user's card, the admin's
// buttons, the dashboard, the subscription page. All of it is declarative: the
// panel draws it with its own components from plain values, so a plugin never gets
// to put HTML or script in front of an admin or a user.

const (
	userFieldsTimeout = 300 * time.Millisecond
	widgetTimeout     = 2 * time.Second
	widgetTTL         = time.Minute
	actionTimeout     = 30 * time.Second
	subBlocksTimeout  = 300 * time.Millisecond
	subBlocksTTL      = 5 * time.Minute
	maxValueLen       = 1000
)

// --- user fields ---

// FieldValue is one value a plugin shows on a user's card.
type FieldValue struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// PluginFields is one plugin's block on the card.
type PluginFields struct {
	Plugin string       `json:"plugin"`
	Name   string       `json:"name"`
	Fields []FieldValue `json:"fields"`
}

// UserFields asks every plugin with user fields about one user, in parallel, each
// within userFieldsTimeout. A plugin that fails or is slow simply has no block.
func (h *Host) UserFields(ctx context.Context, userID int64, lang string) []PluginFields {
	ids := h.Active(func(m *manifest.Manifest) bool { return len(m.Provides.UserFields) > 0 })
	out := make([]*PluginFields, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			m := h.manifestOf(id)
			if m == nil {
				return
			}
			raw, err := h.Call(ctx, id, "userFields", userID, userFieldsTimeout)
			if err != nil {
				return
			}
			var vals map[string]any
			if json.Unmarshal(raw, &vals) != nil {
				return
			}
			pf := &PluginFields{Plugin: id, Name: m.Name.Get(lang)}
			for _, f := range m.Provides.UserFields {
				v, ok := vals[f.Key]
				if !ok || v == nil {
					continue
				}
				pf.Fields = append(pf.Fields, FieldValue{Key: f.Key, Label: f.Label.Get(lang), Value: clip(text(v))})
			}
			if len(pf.Fields) > 0 {
				out[i] = pf
			}
		}(i, id)
	}
	wg.Wait()
	var res []PluginFields
	for _, p := range out {
		if p != nil {
			res = append(res, *p)
		}
	}
	return res
}

// --- actions ---

// ActionInfo is one button a plugin adds, as the admin panel lists it.
type ActionInfo struct {
	Plugin  string `json:"plugin"`
	Key     string `json:"key"`
	Label   string `json:"label"`
	Scope   string `json:"scope"`
	Perm    string `json:"perm"`
	Confirm bool   `json:"confirm"`
}

// Actions lists the active plugins' buttons; held reports whether the admin holds
// a permission, so each admin sees only what they may press.
func (h *Host) Actions(lang string, held func(perm string) bool) []ActionInfo {
	var out []ActionInfo
	for _, id := range h.Active(func(m *manifest.Manifest) bool { return len(m.Provides.Actions) > 0 }) {
		m := h.manifestOf(id)
		if m == nil {
			continue
		}
		for _, a := range m.Provides.Actions {
			perm := actionPerm(a)
			if !held(perm) {
				continue
			}
			out = append(out, ActionInfo{Plugin: id, Key: a.Key, Label: a.Label.Get(lang), Scope: a.Scope, Perm: perm, Confirm: a.Confirm})
		}
	}
	return out
}

func actionPerm(a manifest.Action) string {
	if a.Perm != "" {
		return a.Perm
	}
	return "users.manage"
}

// ActionResult is what the admin sees after pressing a button.
type ActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ErrNoAction is returned for a button the plugin does not declare.
var ErrNoAction = errors.New("plugin: no such action")

// RunAction presses a plugin's button for users (scope user/users) or none
// (global). held is checked against the action's permission first.
func (h *Host) RunAction(ctx context.Context, id, key string, userIDs []int64, held func(perm string) bool) (*ActionResult, string, error) {
	m := h.manifestOf(id)
	if m == nil {
		return nil, "", ErrNotActive
	}
	i := slices.IndexFunc(m.Provides.Actions, func(a manifest.Action) bool { return a.Key == key })
	if i < 0 {
		return nil, "", ErrNoAction
	}
	a := m.Provides.Actions[i]
	perm := actionPerm(a)
	if !held(perm) {
		return nil, perm, ErrForbidden
	}
	switch a.Scope {
	case "user":
		if len(userIDs) != 1 {
			return nil, perm, errors.New("plugin: this action takes one user")
		}
	case "users":
		if len(userIDs) == 0 || len(userIDs) > 1000 {
			return nil, perm, errors.New("plugin: this action takes 1-1000 users")
		}
	default:
		userIDs = []int64{}
	}
	if userIDs == nil {
		userIDs = []int64{} // an array always, never null
	}
	raw, err := h.Call(ctx, id, "onAction", map[string]any{"key": key, "user_ids": userIDs}, actionTimeout)
	if err != nil {
		return nil, perm, err
	}
	res := &ActionResult{OK: true}
	var v struct {
		OK      *bool  `json:"ok"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &v) == nil {
		if v.OK != nil {
			res.OK = *v.OK
		}
		res.Message = clip(v.Message)
	}
	return res, perm, nil
}

// ErrForbidden is returned when the admin lacks an action's permission.
var ErrForbidden = errors.New("plugin: not allowed")

// --- widgets ---

// Widget is one dashboard tile: a stat, a table or a list, as plain values.
type Widget struct {
	Plugin string          `json:"plugin"`
	Key    string          `json:"key"`
	Label  string          `json:"label"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type widgetCache struct {
	mu sync.Mutex
	m  map[string]cachedWidget
}

type cachedWidget struct {
	data json.RawMessage
	err  string
	at   time.Time
}

// Widgets returns every active plugin's widgets, each answered within
// widgetTimeout and kept for widgetTTL — a dashboard viewer must not make every
// plugin compute on each refresh.
func (h *Host) Widgets(ctx context.Context, lang string) []Widget {
	type job struct {
		id string
		w  manifest.Widget
	}
	var jobs []job
	for _, id := range h.Active(func(m *manifest.Manifest) bool { return len(m.Provides.Widgets) > 0 }) {
		if m := h.manifestOf(id); m != nil {
			for _, w := range m.Provides.Widgets {
				jobs = append(jobs, job{id, w})
			}
		}
	}
	out := make([]Widget, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		out[i] = Widget{Plugin: j.id, Key: j.w.Key, Label: j.w.Label.Get(lang)}
		ck := j.id + "/" + j.w.Key
		h.widgets.mu.Lock()
		c, ok := h.widgets.m[ck]
		h.widgets.mu.Unlock()
		if ok && time.Since(c.at) < widgetTTL {
			out[i].Data, out[i].Error = c.data, c.err
			continue
		}
		wg.Add(1)
		go func(i int, ck string, j job) {
			defer wg.Done()
			raw, err := h.Call(ctx, j.id, "widget", j.w.Key, widgetTimeout)
			c := cachedWidget{at: time.Now()}
			switch {
			case err != nil:
				c.err = "unavailable"
			case !validWidget(raw):
				c.err = "the plugin returned an unknown widget"
			default:
				c.data = raw
			}
			h.widgets.mu.Lock()
			if h.widgets.m == nil {
				h.widgets.m = map[string]cachedWidget{}
			}
			h.widgets.m[ck] = c
			h.widgets.mu.Unlock()
			out[i].Data, out[i].Error = c.data, c.err
		}(i, ck, j)
	}
	wg.Wait()
	return out
}

// validWidget accepts {type: stat|table|list, …} of a bounded size, with the fields
// its type needs in the shape the dashboard draws: a value that is a text or a number,
// lists that are arrays of those. Anything else is "unknown widget", never passed on.
func validWidget(raw json.RawMessage) bool {
	if len(raw) > 64<<10 {
		return false
	}
	var w struct {
		Type    string              `json:"type"`
		Value   json.RawMessage     `json:"value"`
		Hint    json.RawMessage     `json:"hint"`
		Items   []json.RawMessage   `json:"items"`
		Columns []json.RawMessage   `json:"columns"`
		Rows    [][]json.RawMessage `json:"rows"`
	}
	if json.Unmarshal(raw, &w) != nil {
		return false
	}
	scalars := func(vs []json.RawMessage) bool {
		for _, v := range vs {
			if !scalarJSON(v) {
				return false
			}
		}
		return true
	}
	switch w.Type {
	case "stat":
		return scalarJSON(w.Value) && (len(w.Hint) == 0 || w.Hint[0] == '"')
	case "list":
		return w.Items != nil && scalars(w.Items)
	case "table":
		if w.Columns == nil || w.Rows == nil || !scalars(w.Columns) {
			return false
		}
		for _, r := range w.Rows {
			if !scalars(r) {
				return false
			}
		}
		return true
	}
	return false
}

// scalarJSON is a JSON string, number, boolean or null.
func scalarJSON(v json.RawMessage) bool {
	v = json.RawMessage(strings.TrimSpace(string(v)))
	if len(v) == 0 {
		return false
	}
	switch v[0] {
	case '{', '[':
		return false
	}
	return json.Valid(v)
}

// --- subscription page blocks ---

// SubBlock is one block a plugin adds to the subscription page.
type SubBlock struct {
	Type  string `json:"type"`            // text | markdown | button | notice
	Text  string `json:"text,omitempty"`  // text, markdown, notice
	Label string `json:"label,omitempty"` // button
	URL   string `json:"url,omitempty"`   // button: https only
}

type subCache struct {
	mu sync.Mutex
	m  map[string]cachedBlocks
}

type cachedBlocks struct {
	blocks []SubBlock
	at     time.Time
}

// SubBlocks asks the plugins with page blocks what to show one user, each within
// subBlocksTimeout, and keeps the answer for subBlocksTTL: the page is opened
// often, and it must never wait on a plugin longer than that.
func (h *Host) SubBlocks(ctx context.Context, user map[string]any, lang string) []SubBlock {
	ids := h.Active(func(m *manifest.Manifest) bool { return m.Provides.SubBlocks })
	if len(ids) == 0 {
		return nil
	}
	uid := fmt.Sprint(user["id"])
	parts := make([][]SubBlock, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		ck := id + "/" + uid + "/" + lang
		h.subs.mu.Lock()
		c, ok := h.subs.m[ck]
		h.subs.mu.Unlock()
		if ok && time.Since(c.at) < subBlocksTTL {
			parts[i] = c.blocks
			continue
		}
		wg.Add(1)
		go func(i int, id, ck string) {
			defer wg.Done()
			raw, err := h.Call(ctx, id, "subBlocks", map[string]any{"user": user, "lang": lang}, subBlocksTimeout)
			var blocks []SubBlock
			if err == nil {
				var got []SubBlock
				if json.Unmarshal(raw, &got) == nil {
					blocks = cleanBlocks(got)
				}
			}
			parts[i] = blocks
			if err != nil {
				return // a busy or failing plugin is asked again next time, not remembered empty
			}
			h.subs.mu.Lock()
			if h.subs.m == nil {
				h.subs.m = map[string]cachedBlocks{}
			}
			if len(h.subs.m) > 10000 { // a bound, not an eviction policy: start over
				h.subs.m = map[string]cachedBlocks{}
			}
			h.subs.m[ck] = cachedBlocks{blocks: blocks, at: time.Now()}
			h.subs.mu.Unlock()
		}(i, id, ck)
	}
	wg.Wait()
	var out []SubBlock
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// cleanBlocks keeps what the page can show safely: known types, bounded text, and
// buttons to https only (no javascript:, no data:).
func cleanBlocks(in []SubBlock) []SubBlock {
	var out []SubBlock
	for _, b := range in {
		if len(out) == 10 {
			break
		}
		b.Text, b.Label = clip(b.Text), clip(b.Label)
		switch b.Type {
		case "text", "markdown", "notice":
			if b.Text == "" {
				continue
			}
			b.Label, b.URL = "", ""
		case "button":
			if b.Label == "" || !strings.HasPrefix(b.URL, "https://") || len(b.URL) > 2048 {
				continue
			}
			b.Text = ""
		default:
			continue
		}
		out = append(out, b)
	}
	return out
}

func (h *Host) manifestOf(id string) *manifest.Manifest {
	inst, err := h.get(id)
	if err != nil {
		return nil
	}
	if p := inst.pub.Load(); p != nil {
		return p.manifest
	}
	return nil
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64, bool:
		return fmt.Sprint(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func clip(s string) string {
	if len(s) > maxValueLen {
		return s[:maxValueLen] + "…"
	}
	return s
}
