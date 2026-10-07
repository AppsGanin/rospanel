// Package manifest reads a plugin package — a zip with plugin.json and main.js —
// and checks it against everything that can be checked without running it.
//
// The manifest is how a plugin declares what it touches: the operator reads it on
// the consent screen before installing, and the panel wires the plugin from it
// without executing any code. Validation is therefore strict and complete — every
// problem is reported at once, with the path of the field — so an author fixes a
// package in one round rather than one error at a time.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/model"
)

// APIVersion is the host API major this panel serves. A manifest names the major
// it was written for; see Supported.
const APIVersion = 1

// Supported reports whether a manifest's api major can run here. The previous
// major stays supported for six months after a new one ships.
func Supported(api int) bool { return api == APIVersion }

// Text is a user-facing string, plain or per language: "Lava" or {"ru": …, "en": …}.
type Text map[string]string

// UnmarshalJSON accepts a string (stored under "") or an object of strings.
func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = Text{"": s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return errors.New(`expected a string or {"ru": …, "en": …}`)
	}
	*t = m
	return nil
}

// MarshalJSON writes a plain string back as one.
func (t Text) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		if s, ok := t[""]; ok {
			return json.Marshal(s)
		}
	}
	return json.Marshal(map[string]string(t))
}

// Get picks lang, then English, then Russian, then the plain form, then anything.
func (t Text) Get(lang string) string {
	for _, k := range []string{lang, "en", "ru", ""} {
		if s := t[k]; s != "" {
			return s
		}
	}
	for _, s := range t {
		return s
	}
	return ""
}

func (t Text) empty() bool { return strings.TrimSpace(t.Get("")) == "" }

// Manifest is plugin.json.
type Manifest struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	API         int    `json:"api"`
	Panel       string `json:"panel,omitempty"` // ">=X.Y.Z"
	Name        Text   `json:"name"`
	Description Text   `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`

	Permissions []string `json:"permissions,omitempty"`
	Net         []string `json:"net,omitempty"`
	Settings    []Field  `json:"settings,omitempty"`
	Provides    Provides `json:"provides"`

	DBQuotaMB    int      `json:"db_quota_mb,omitempty"`
	MemoryMB     int      `json:"memory_mb,omitempty"`
	Experimental []string `json:"experimental,omitempty"`
}

// Field is one input of the plugin's settings form — the payment providers' schema
// plus number and textarea.
type Field struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Label       Text     `json:"label"`
	Help        Text     `json:"help,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
	Default     string   `json:"default,omitempty"`
	Options     []Option `json:"options,omitempty"`
}

// Option is one choice of a select field.
type Option struct {
	Value string `json:"value"`
	Label Text   `json:"label"`
}

// Provides lists the extension points the plugin plugs into.
type Provides struct {
	Events       []string    `json:"events,omitempty"`
	Hooks        []string    `json:"hooks,omitempty"`
	Cron         []CronJob   `json:"cron,omitempty"`
	Payment      *Payment    `json:"payment,omitempty"`
	HTTP         bool        `json:"http,omitempty"`
	Channel      *Channel    `json:"channel,omitempty"`
	UserFields   []UserField `json:"user_fields,omitempty"`
	Actions      []Action    `json:"actions,omitempty"`
	Widgets      []Widget    `json:"widgets,omitempty"`
	SubBlocks    bool        `json:"sub_blocks,omitempty"`
	Bot          *Bot        `json:"bot,omitempty"`
	Price        bool        `json:"price,omitempty"`
	Subscription bool        `json:"subscription,omitempty"`
}

// CronJob runs the export of the same name on a schedule.
type CronJob struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
}

// Payment declares a payment provider.
type Payment struct {
	Label Text `json:"label"`
	Note  Text `json:"note,omitempty"`
}

// Channel declares a message delivery channel.
type Channel struct {
	Label Text `json:"label"`
}

// UserField is one value shown on a user's card.
type UserField struct {
	Key   string `json:"key"`
	Label Text   `json:"label"`
}

// Action is a button the admin panel draws and the plugin answers.
type Action struct {
	Key     string `json:"key"`
	Label   Text   `json:"label"`
	Scope   string `json:"scope"`          // user | users | global
	Perm    string `json:"perm,omitempty"` // who may press it; users.manage by default
	Confirm bool   `json:"confirm,omitempty"`
}

// Widget is a dashboard tile.
type Widget struct {
	Key   string `json:"key"`
	Label Text   `json:"label"`
}

// Bot declares the user bot's menu buttons and commands.
type Bot struct {
	Menu     bool         `json:"menu,omitempty"`
	Commands []BotCommand `json:"commands,omitempty"`
}

// BotCommand is a /command the plugin answers, with its line in the command menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description Text   `json:"description"`
}

// Hook names.
const (
	HookBeforeSignup     = "beforeSignup"
	HookBeforeDeviceBind = "beforeDeviceBind"
)

// Available are the extension points this panel calls. A manifest declaring one
// that is not here yet is refused: a plugin that installs, switches on and then
// silently never runs is worse than one the panel turns away with the reason.
// Each stage of docs/plugins-design.md adds its points here.
var Available = map[string]bool{
	"events": true, "cron": true, "payment": true, "http": true,
	"user_fields": true, "actions": true, "widgets": true, "sub_blocks": true, "channel": true,
	"hooks": true, "price": true, "bot": true, "subscription": true,
}

// declared lists the points a manifest uses, by their provides key.
func (m *Manifest) declared() []string {
	pr := m.Provides
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(len(pr.Events) > 0, "events")
	add(len(pr.Hooks) > 0, "hooks")
	add(len(pr.Cron) > 0, "cron")
	add(pr.Payment != nil, "payment")
	add(pr.HTTP, "http")
	add(pr.Channel != nil, "channel")
	add(len(pr.UserFields) > 0, "user_fields")
	add(len(pr.Actions) > 0, "actions")
	add(len(pr.Widgets) > 0, "widgets")
	add(pr.SubBlocks, "sub_blocks")
	add(pr.Bot != nil, "bot")
	add(pr.Price, "price")
	add(pr.Subscription, "subscription")
	return out
}

// Points that are experimental: a manifest using one must say so in Experimental,
// and they may change without a new API major.
var experimentalPoints = []string{"channel", "price", "subscription"}

// Permissions a plugin is never granted: they would let it mint keys, change who
// administers the panel, or replace the binary.
var withheldPerms = []string{model.PermOwner, model.PermAPI, model.PermSecurityManage, model.PermUpdate, model.PermWebhooks,
	model.PermPluginsView, model.PermPluginsManage} // a plugin that installs plugins is a way round the consent screen

// RiskyPerms are granted only with a red mark on the consent screen.
var RiskyPerms = []string{model.PermBillingManage, model.PermUsersDelete, model.PermSettingsManage, model.PermPayments}

const (
	MaxDBQuotaMB     = 1024
	DefaultDBQuotaMB = 100
	MaxMemoryMB      = 128
	DefaultMemoryMB  = 32
	maxCronJobs      = 10
	maxSettings      = 50
	maxNet           = 20
	maxListItems     = 20
)

var (
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{2,39}$`)
	keyRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	exportRe  = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,63}$`)
	commandRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	hostRe    = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}(:[0-9]{1,5})?$`)
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
	langRe    = regexp.MustCompile(`^[a-z]{2}$`)
)

var reservedIDs = []string{"rospanel", "core", "builtin", "panel", "plugin", "system"}

// Problems is every reason a manifest or package is refused.
type Problems []string

func (p Problems) Error() string { return "plugin: " + strings.Join(p, "; ") }

func (p *Problems) addf(format string, args ...any) { *p = append(*p, fmt.Sprintf(format, args...)) }

// Parse decodes plugin.json strictly: unknown fields are an error, so a typo in a
// field name is caught instead of silently doing nothing.
func Parse(b []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, Problems{"plugin.json: " + err.Error()}
	}
	if dec.More() {
		return nil, Problems{"plugin.json: trailing data after the object"}
	}
	return &m, nil
}

// Validate checks the manifest against this panel (panelVersion "X.Y.Z").
func (m *Manifest) Validate(panelVersion string) error {
	var p Problems
	switch {
	case !idRe.MatchString(m.ID):
		p.addf("id %q: 3-40 characters of a-z, 0-9 and -, starting with a letter", m.ID)
	case slices.ContainsFunc(reservedIDs, func(r string) bool { return m.ID == r || strings.HasPrefix(m.ID, r+"-") }):
		p.addf("id %q: reserved", m.ID)
	}
	if !versionRe.MatchString(m.Version) {
		p.addf("version %q: must be X.Y.Z", m.Version)
	}
	if !Supported(m.API) {
		p.addf("api %d: this panel serves api %d", m.API, APIVersion)
	}
	if m.Panel != "" {
		min, ok := strings.CutPrefix(m.Panel, ">=")
		switch {
		case !ok || !versionRe.MatchString(strings.TrimSpace(min)):
			p.addf(`panel %q: only the form ">=X.Y.Z" is supported`, m.Panel)
		case panelVersion != "" && versionLess(panelVersion, strings.TrimSpace(min)):
			p.addf("panel: needs %s, this panel is %s", strings.TrimSpace(min), panelVersion)
		}
	}
	if m.Name.empty() {
		p.addf("name: required")
	}
	checkText(&p, "name", m.Name, 80)
	checkText(&p, "description", m.Description, 500)
	if len(m.Author) > 100 || len(m.License) > 50 {
		p.addf("author/license: too long")
	}
	if m.Homepage != "" && !strings.HasPrefix(m.Homepage, "https://") && !strings.HasPrefix(m.Homepage, "http://") {
		p.addf("homepage: must be an http(s) URL")
	}

	known := model.AllPerms()
	seen := map[string]bool{}
	for i, perm := range m.Permissions {
		switch {
		case seen[perm]:
			p.addf("permissions[%d] %q: listed twice", i, perm)
		case slices.Contains(withheldPerms, perm):
			p.addf("permissions[%d] %q: never granted to plugins", i, perm)
		case !slices.Contains(known, perm):
			p.addf("permissions[%d] %q: unknown permission", i, perm)
		}
		seen[perm] = true
	}
	if len(m.Net) > maxNet {
		p.addf("net: at most %d hosts", maxNet)
	}
	for i, h := range m.Net {
		if err := checkHost(h); err != "" {
			p.addf("net[%d] %q: %s", i, h, err)
		}
	}
	m.validateSettings(&p)
	m.validateProvides(&p)
	for _, point := range m.declared() {
		if !Available[point] {
			p.addf("provides.%s: not available in this panel version yet", point)
		}
	}

	if m.DBQuotaMB < 0 || m.DBQuotaMB > MaxDBQuotaMB {
		p.addf("db_quota_mb: 0-%d", MaxDBQuotaMB)
	}
	if m.MemoryMB < 0 || m.MemoryMB > MaxMemoryMB {
		p.addf("memory_mb: 0-%d", MaxMemoryMB)
	}
	for i, e := range m.Experimental {
		if !slices.Contains(experimentalPoints, e) {
			p.addf("experimental[%d] %q: not an experimental point (%s)", i, e, strings.Join(experimentalPoints, ", "))
		}
	}
	for _, e := range m.usedExperimental() {
		if !slices.Contains(m.Experimental, e) {
			p.addf(`provides.%s is experimental: add "%s" to "experimental"`, e, e)
		}
	}
	if len(p) > 0 {
		return p
	}
	return nil
}

func (m *Manifest) usedExperimental() []string {
	var out []string
	if m.Provides.Channel != nil {
		out = append(out, "channel")
	}
	if m.Provides.Price {
		out = append(out, "price")
	}
	if m.Provides.Subscription {
		out = append(out, "subscription")
	}
	return out
}

func (m *Manifest) validateSettings(p *Problems) {
	if len(m.Settings) > maxSettings {
		p.addf("settings: at most %d fields", maxSettings)
	}
	seen := map[string]bool{}
	for i, f := range m.Settings {
		at := fmt.Sprintf("settings[%d]", i)
		if !keyRe.MatchString(f.Key) {
			p.addf("%s key %q: a-z, 0-9 and _, starting with a letter", at, f.Key)
		}
		if seen[f.Key] {
			p.addf("%s key %q: listed twice", at, f.Key)
		}
		seen[f.Key] = true
		switch f.Kind {
		case "text", "secret", "bool", "number", "textarea":
			if len(f.Options) > 0 {
				p.addf("%s: options are for kind select", at)
			}
		case "select":
			if len(f.Options) == 0 {
				p.addf("%s: a select needs options", at)
			}
			for j, o := range f.Options {
				if o.Value == "" || o.Label.empty() {
					p.addf("%s options[%d]: value and label required", at, j)
				}
			}
		default:
			p.addf("%s kind %q: one of text, secret, bool, number, textarea, select", at, f.Kind)
		}
		if f.Label.empty() {
			p.addf("%s label: required", at)
		}
		if f.Kind == "secret" && f.Default != "" {
			p.addf("%s: a secret cannot have a default", at)
		}
		if f.Kind == "number" && f.Default != "" {
			if _, err := strconv.ParseFloat(f.Default, 64); err != nil {
				p.addf("%s default: not a number", at)
			}
		}
	}
}

func (m *Manifest) validateProvides(p *Problems) {
	pr := m.Provides
	seen := map[string]bool{}
	for i, e := range pr.Events {
		if !model.ValidWebhookEvent(e) {
			p.addf("provides.events[%d] %q: unknown event", i, e)
		}
		if seen[e] {
			p.addf("provides.events[%d] %q: listed twice", i, e)
		}
		seen[e] = true
	}
	for i, h := range pr.Hooks {
		if h != HookBeforeSignup && h != HookBeforeDeviceBind {
			p.addf("provides.hooks[%d] %q: one of %s, %s", i, h, HookBeforeSignup, HookBeforeDeviceBind)
		}
	}
	if len(pr.Cron) > maxCronJobs {
		p.addf("provides.cron: at most %d jobs", maxCronJobs)
	}
	names := map[string]bool{}
	for i, c := range pr.Cron {
		if !exportRe.MatchString(c.Name) {
			p.addf("provides.cron[%d] name %q: must be the name of an exported function", i, c.Name)
		}
		if names[c.Name] {
			p.addf("provides.cron[%d] name %q: listed twice", i, c.Name)
		}
		names[c.Name] = true
		if _, err := cron.Parse(c.Schedule); err != nil {
			p.addf("provides.cron[%d] schedule %q: %v", i, c.Schedule, err)
		}
	}
	if pr.Payment != nil && pr.Payment.Label.empty() {
		p.addf("provides.payment.label: required")
	}
	if pr.Channel != nil && pr.Channel.Label.empty() {
		p.addf("provides.channel.label: required")
	}
	checkKeyed(p, "provides.user_fields", len(pr.UserFields), func(i int) (string, Text) { return pr.UserFields[i].Key, pr.UserFields[i].Label })
	checkKeyed(p, "provides.widgets", len(pr.Widgets), func(i int) (string, Text) { return pr.Widgets[i].Key, pr.Widgets[i].Label })
	checkKeyed(p, "provides.actions", len(pr.Actions), func(i int) (string, Text) { return pr.Actions[i].Key, pr.Actions[i].Label })
	known := model.AllPerms()
	for i, a := range pr.Actions {
		if a.Scope != "user" && a.Scope != "users" && a.Scope != "global" {
			p.addf("provides.actions[%d] scope %q: one of user, users, global", i, a.Scope)
		}
		if a.Perm != "" && !slices.Contains(known, a.Perm) {
			p.addf("provides.actions[%d] perm %q: unknown permission", i, a.Perm)
		}
	}
	if pr.Bot != nil {
		if len(pr.Bot.Commands) > maxListItems {
			p.addf("provides.bot.commands: at most %d", maxListItems)
		}
		seen := map[string]bool{}
		for i, c := range pr.Bot.Commands {
			if !commandRe.MatchString(c.Command) || c.Command == "start" {
				p.addf("provides.bot.commands[%d] %q: a-z, 0-9 and _, up to 32, not start", i, c.Command)
			}
			if seen[c.Command] {
				p.addf("provides.bot.commands[%d] %q: listed twice", i, c.Command)
			}
			seen[c.Command] = true
			if c.Description.empty() {
				p.addf("provides.bot.commands[%d].description: required", i)
			}
			checkText(p, fmt.Sprintf("provides.bot.commands[%d].description", i), c.Description, 200)
		}
		if !pr.Bot.Menu && len(pr.Bot.Commands) == 0 {
			p.addf("provides.bot: declare menu, commands or both")
		}
	}
}

// checkKeyed checks a list of {key, label} entries: valid unique keys, labels set.
func checkKeyed(p *Problems, at string, n int, get func(int) (string, Text)) {
	if n > maxListItems {
		p.addf("%s: at most %d", at, maxListItems)
	}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		k, l := get(i)
		if !keyRe.MatchString(k) {
			p.addf("%s[%d] key %q: a-z, 0-9 and _, starting with a letter", at, i, k)
		}
		if seen[k] {
			p.addf("%s[%d] key %q: listed twice", at, i, k)
		}
		seen[k] = true
		if l.empty() {
			p.addf("%s[%d] label: required", at, i)
		}
	}
}

func checkText(p *Problems, at string, t Text, max int) {
	for lang, s := range t {
		if lang != "" && !langRe.MatchString(lang) {
			p.addf("%s: %q is not a language code", at, lang)
		}
		if len(s) > max {
			p.addf("%s: longer than %d", at, max)
		}
	}
}

// checkHost accepts "api.example.com", "*.example.com" and either with ":port".
// IP literals and single-label names are refused: an allowlist entry is a public
// service by name, and the dialer refuses private addresses whatever the name.
func checkHost(h string) string {
	if h != strings.ToLower(h) {
		return "lower case only"
	}
	if !hostRe.MatchString(h) {
		return "a host name like api.example.com or *.example.com, optionally with :port"
	}
	if _, port, ok := strings.Cut(h, ":"); ok {
		if n, _ := strconv.Atoi(port); n < 1 || n > 65535 {
			return "port out of range"
		}
	}
	return ""
}

// VersionLess reports whether version a is older than b (X.Y.Z).
func VersionLess(a, b string) bool { return versionLess(a, b) }

// PanelAllows reports whether a manifest's "panel" requirement (">=X.Y.Z", or none)
// is met by panelVersion.
func PanelAllows(require, panelVersion string) bool {
	min, ok := strings.CutPrefix(strings.TrimSpace(require), ">=")
	if !ok || panelVersion == "" {
		return true
	}
	return !versionLess(panelVersion, strings.TrimSpace(min))
}

// versionLess compares strict X.Y.Z versions (a pre-release suffix on the panel's
// own version is ignored).
func versionLess(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(s)
	}
	return out
}

// Quota returns the database quota in bytes.
func (m *Manifest) Quota() int64 {
	mb := m.DBQuotaMB
	if mb == 0 {
		mb = DefaultDBQuotaMB
	}
	return int64(mb) << 20
}

// Memory returns the VM heap limit in bytes.
func (m *Manifest) Memory() int {
	mb := m.MemoryMB
	if mb == 0 {
		mb = DefaultMemoryMB
	}
	return mb << 20
}

// Exports lists the exported functions the manifest promises, as dotted paths —
// checked against the loaded module before the plugin is enabled.
func (m *Manifest) Exports() []string {
	var out []string
	pr := m.Provides
	if len(pr.Events) > 0 {
		out = append(out, "onEvent")
	}
	out = append(out, pr.Hooks...)
	for _, c := range pr.Cron {
		out = append(out, c.Name)
	}
	if pr.Payment != nil {
		out = append(out, "payment.create", "payment.status", "payment.webhook")
	}
	if pr.HTTP {
		out = append(out, "onHttp")
	}
	if pr.Channel != nil {
		out = append(out, "channel.send")
	}
	if len(pr.UserFields) > 0 {
		out = append(out, "userFields")
	}
	if len(pr.Actions) > 0 {
		out = append(out, "onAction")
	}
	if len(pr.Widgets) > 0 {
		out = append(out, "widget")
	}
	if pr.SubBlocks {
		out = append(out, "subBlocks")
	}
	if pr.Bot != nil {
		if pr.Bot.Menu {
			out = append(out, "bot.menu", "bot.onCallback")
		}
		if len(pr.Bot.Commands) > 0 {
			out = append(out, "bot.onCommand")
		}
	}
	if pr.Price {
		out = append(out, "quotePrice")
	}
	if pr.Subscription {
		out = append(out, "transformSubscription")
	}
	return out
}
