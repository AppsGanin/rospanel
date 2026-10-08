// Package builder turns rules an operator puts together in the panel — "when this
// event comes and this holds, do that" — into an ordinary plugin: plugin.json,
// main.js and a test.js. The plugin it makes is installed, sandboxed and consented
// to like any other, and its code can be opened and carried on by hand.
//
// main.js is a fixed interpreter (runtime.js) with the rules embedded as JSON: no
// text an operator typed ever becomes code, and what "show the code" shows stays
// the same short file whatever the rules are.
package builder

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/devkit"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

//go:embed runtime.js
var runtimeJS string

// Spec is what the builder edits.
type Spec struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Rules       []Rule `json:"rules"`
}

// Rule is one "when … if … then …".
type Rule struct {
	Name string `json:"name,omitempty"`
	// Event or Schedule: an event of the panel, or a cron schedule.
	Event      string      `json:"event,omitempty"`
	Schedule   string      `json:"schedule,omitempty"`
	Match      string      `json:"match,omitempty"` // all (default) | any
	Conditions []Condition `json:"conditions,omitempty"`
	Actions    []Action    `json:"actions"`
}

// Condition compares one value of the event with a constant.
type Condition struct {
	Field string `json:"field"` // a path: data.plan_id, user.lang, event
	Op    string `json:"op"`    // eq ne contains gt lt empty not_empty
	Value string `json:"value,omitempty"`
}

// Action is one thing a rule does. Texts take {{path}} placeholders.
type Action struct {
	Type string `json:"type"` // telegram discord http extend enable disable tag untag log
	// Chat (telegram): {{user.telegram_id}} for the user themselves; empty for the
	// group or channel the operator sets in the plugin's settings.
	Chat   string `json:"chat,omitempty"`
	Text   string `json:"text,omitempty"`
	URL    string `json:"url,omitempty"`
	Method string `json:"method,omitempty"`
	Body   string `json:"body,omitempty"`
	Auth   bool   `json:"auth,omitempty"` // http: send the http_authorization setting
	Days   int    `json:"days,omitempty"`
	Tag    string `json:"tag,omitempty"`
}

// Kinds of actions, and which need a user (an event about one).
var (
	ActionTypes = []string{"telegram", "discord", "http", "extend", "enable", "disable", "tag", "untag", "log"}
	userActions = []string{"extend", "enable", "disable", "tag", "untag"}
	ops         = []string{"eq", "ne", "contains", "gt", "lt", "empty", "not_empty"}
)

const (
	maxRules   = 50
	maxActions = 10
	maxConds   = 10
	maxText    = 4000
)

var (
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{2,39}$`)
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
	fieldRe   = regexp.MustCompile(`^[a-z_][a-z0-9_]*(\.[a-z0-9_]+){0,5}$`)
	tagRe     = regexp.MustCompile(`^[^,\s][^,]{0,31}$`)
)

// Validate lists what keeps the spec from making a plugin, each problem naming
// where it is (rules[2].actions[0].chat: …).
func Validate(s Spec) []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	if !idRe.MatchString(s.ID) {
		add("id: 3-40 characters of a-z, 0-9 and -, starting with a letter")
	}
	if !versionRe.MatchString(s.Version) {
		add("version: X.Y.Z")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 100 {
		add("name: required, up to 100 characters")
	}
	if len(s.Description) > 500 {
		add("description: up to 500 characters")
	}
	if len(s.Rules) == 0 || len(s.Rules) > maxRules {
		add("rules: 1-%d", maxRules)
	}
	for i, r := range s.Rules {
		at := fmt.Sprintf("rules[%d]", i)
		switch {
		case r.Event != "" && r.Schedule != "":
			add("%s: an event or a schedule, not both", at)
		case r.Event != "":
			if !model.ValidWebhookEvent(r.Event) {
				add("%s.event %q: unknown event", at, r.Event)
			}
		case r.Schedule != "":
			if _, err := cron.Parse(r.Schedule); err != nil {
				add("%s.schedule %q: %v", at, r.Schedule, err)
			}
		default:
			add("%s: choose an event or a schedule", at)
		}
		if r.Match != "" && r.Match != "all" && r.Match != "any" {
			add("%s.match: all or any", at)
		}
		if len(r.Conditions) > maxConds {
			add("%s.conditions: at most %d", at, maxConds)
		}
		for j, c := range r.Conditions {
			if !fieldRe.MatchString(c.Field) {
				add("%s.conditions[%d].field %q: a path like data.plan_id", at, j, c.Field)
			}
			if !slices.Contains(ops, c.Op) {
				add("%s.conditions[%d].op: one of %s", at, j, strings.Join(ops, ", "))
			}
			if (c.Op == "gt" || c.Op == "lt") && !isNumber(c.Value) {
				add("%s.conditions[%d].value: a number", at, j)
			}
			if len(c.Value) > 500 {
				add("%s.conditions[%d].value: up to 500 characters", at, j)
			}
		}
		if len(r.Actions) == 0 || len(r.Actions) > maxActions {
			add("%s.actions: 1-%d", at, maxActions)
		}
		for j, a := range r.Actions {
			validateAction(fmt.Sprintf("%s.actions[%d]", at, j), a, r.Schedule != "", add)
		}
	}
	return p
}

func validateAction(at string, a Action, scheduled bool, add func(string, ...any)) {
	if !slices.Contains(ActionTypes, a.Type) {
		add("%s.type: one of %s", at, strings.Join(ActionTypes, ", "))
		return
	}
	if scheduled && slices.Contains(userActions, a.Type) {
		add("%s: a scheduled rule has no user to %s", at, a.Type)
	}
	text := func(field, v string, required bool) {
		if required && strings.TrimSpace(v) == "" {
			add("%s.%s: required", at, field)
		}
		if len(v) > maxText {
			add("%s.%s: up to %d characters", at, field, maxText)
		}
	}
	switch a.Type {
	case "telegram":
		if scheduled && strings.Contains(a.Chat, "{{user.") {
			add("%s.chat: a scheduled rule has no user to write to", at)
		}
		text("chat", a.Chat, false)
		text("text", a.Text, true)
	case "discord", "log":
		text("text", a.Text, true)
	case "http":
		if _, err := httpHost(a.URL); err != nil {
			add("%s.url: %v", at, err)
		}
		if m := strings.ToUpper(a.Method); m != "" && !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, m) {
			add("%s.method: GET, POST, PUT, PATCH or DELETE", at)
		}
		text("body", a.Body, false)
	case "extend":
		if a.Days < 1 || a.Days > 3650 {
			add("%s.days: 1-3650", at)
		}
	case "tag", "untag":
		if !tagRe.MatchString(a.Tag) {
			add("%s.tag: up to 32 characters, no commas", at)
		}
	}
}

// httpHost is the allowlist entry a request to raw needs: its host, with the port
// when it is not 80 or 443. The host is fixed — a placeholder may go in the path or
// the query only — since the operator consents to the hosts at install.
func httpHost(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("required")
	}
	if _, rest, ok := strings.Cut(raw, "://"); ok {
		if authority, _, _ := strings.Cut(strings.SplitN(strings.SplitN(rest, "?", 2)[0], "#", 2)[0], "/"); strings.Contains(authority, "{{") {
			return "", fmt.Errorf("the host cannot hold a placeholder")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("an http(s) address")
	}
	if strings.Contains(u.Host, "{{") || strings.Contains(u.Host, "%7B") {
		return "", fmt.Errorf("the host cannot hold a placeholder")
	}
	host := strings.ToLower(u.Hostname())
	if !strings.Contains(host, ".") || isIP(host) {
		return "", fmt.Errorf("a host name, not an IP address")
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return host + ":" + p, nil
	}
	return host, nil
}

func isIP(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.Contains(host, ":") {
		return true
	}
	for _, part := range strings.Split(host, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

// Compile makes the plugin's files from a valid spec: plugin.json, main.js,
// test.js, dev.config.json and README.md.
func Compile(s Spec, panelVersion string) (devkit.Files, error) {
	if p := Validate(s); len(p) > 0 {
		return nil, manifest.Problems(p)
	}
	m := manifest.Manifest{
		ID: s.ID, Version: s.Version, API: manifest.APIVersion, Name: manifest.Text{"": s.Name},
	}
	if panelVersion != "" {
		m.Panel = ">=" + panelVersion
	}
	if s.Description != "" {
		m.Description = manifest.Text{"": s.Description}
	}
	var events []string
	uses := map[string]bool{}
	for i, r := range s.Rules {
		if r.Event != "" {
			if !slices.Contains(events, r.Event) {
				events = append(events, r.Event)
			}
		} else {
			m.Provides.Cron = append(m.Provides.Cron, manifest.CronJob{Name: cronExport(i), Schedule: r.Schedule})
		}
		for _, a := range r.Actions {
			uses[a.Type] = true
			if a.Type == "telegram" && a.Chat == "" {
				uses["telegram_chat"] = true
			}
			if a.Type == "http" {
				h, _ := httpHost(a.URL)
				if !slices.Contains(m.Net, h) {
					m.Net = append(m.Net, h)
				}
				if a.Auth {
					uses["http_auth"] = true
				}
			}
		}
	}
	m.Provides.Events = events
	if uses["telegram"] {
		m.Net = append(m.Net, "api.telegram.org")
		m.Settings = append(m.Settings, manifest.Field{Key: "telegram_token", Kind: "secret",
			Label: manifest.Text{"ru": "Токен Telegram-бота", "en": "Telegram bot token"},
			Help:  manifest.Text{"ru": "От @BotFather.", "en": "From @BotFather."}})
	}
	if uses["telegram_chat"] {
		m.Settings = append(m.Settings, manifest.Field{Key: "telegram_chat", Kind: "text",
			Label:       manifest.Text{"ru": "Чат Telegram", "en": "Telegram chat"},
			Placeholder: "-1001234567890",
			Help: manifest.Text{"ru": "ID группы (начинается с -100) или @имя канала. Бот должен быть в нём участником, в канале — администратором.",
				"en": "A group's ID (starts with -100) or a channel's @name. The bot must be a member of it — an admin, in a channel."}})
	}
	if uses["discord"] {
		m.Net = append(m.Net, "discord.com")
		m.Settings = append(m.Settings, manifest.Field{Key: "discord_webhook", Kind: "secret",
			Label:       manifest.Text{"ru": "Вебхук Discord", "en": "Discord webhook"},
			Placeholder: "https://discord.com/api/webhooks/…"})
	}
	if uses["http_auth"] {
		m.Settings = append(m.Settings, manifest.Field{Key: "http_authorization", Kind: "secret",
			Label: manifest.Text{"ru": "Заголовок Authorization", "en": "Authorization header"},
			Help:  manifest.Text{"ru": "Например: Bearer abc123", "en": "For example: Bearer abc123"}})
	}
	// What the rules touch decides what they are granted — as little as that.
	held := []string{}
	if len(events) > 0 {
		held = append(held, model.PermUsersView)
	}
	for _, a := range userActions {
		if uses[a] {
			held = append(held, model.PermUsersManage)
			break
		}
	}
	m.Permissions = held
	for _, n := range m.PointPerms() {
		if !slices.Contains(m.Permissions, n.Perm) {
			m.Permissions = append(m.Permissions, n.Perm)
		}
	}
	slices.Sort(m.Permissions)
	m.Permissions = slices.Compact(m.Permissions)

	mf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	rules, err := json.MarshalIndent(s.Rules, "", "  ")
	if err != nil {
		return nil, err
	}
	main := strings.Replace(mainFor(s, uses), "__RULES__", string(rules), 1)
	for i, r := range s.Rules {
		if r.Schedule != "" {
			main += fmt.Sprintf("export function %s() { runScheduled(%d); }\n", cronExport(i), i)
		}
	}
	spec, _ := json.MarshalIndent(s, "", "  ")
	return devkit.Files{
		"plugin.json":     append(mf, '\n'),
		"main.js":         []byte(main),
		"test.js":         []byte(smokeTest(s)),
		"dev.config.json": devConfig(uses),
		"README.md":       []byte(readme(s)),
		SpecFile:          append(spec, '\n'),
	}, nil
}

// mainFor puts main.js together from the parts of runtime.js these rules use: the
// code shown is the code that runs, and an action taken out of the rules takes its
// function with it.
func mainFor(s Spec, uses map[string]bool) string {
	parts := runtimeParts()
	var events, scheduled, conds bool
	for _, r := range s.Rules {
		events = events || r.Event != ""
		scheduled = scheduled || r.Schedule != ""
		conds = conds || len(r.Conditions) > 0
	}
	userAct := slices.ContainsFunc(userActions, func(a string) bool { return uses[a] })
	answers := uses["telegram"] || uses["discord"] || uses["http"] || userAct
	fills := uses["telegram"] || uses["discord"] || uses["http"] || uses["log"]
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, parts[name])
		}
	}
	add(true, "head")
	add(events, "events")
	add(scheduled, "scheduled")
	add(fills || conds || answers, "text")
	add(fills, "fill")
	add(conds, "conditions")
	add(!conds, "noconditions")
	// act: a case for each kind of action the rules have.
	var act []string
	for _, line := range strings.Split(parts["act"], "\n") {
		if t, ok := caseOf(line); ok && !uses[t] {
			continue
		}
		act = append(act, line)
	}
	out = append(out, strings.Join(act, "\n"))
	add(uses["telegram"], "telegram")
	add(uses["discord"], "discord")
	add(uses["http"], "http")
	add(uses["tag"] || uses["untag"], "retag")
	add(userAct, "user")
	add(answers, "answered")
	add(events, "once")
	return strings.Join(out, "\n\n") + "\n"
}

// runtimeParts splits runtime.js at its "// @part name" lines.
func runtimeParts() map[string]string {
	parts := map[string]string{}
	var name string
	var cur []string
	flush := func() {
		if name != "" {
			parts[name] = strings.TrimSpace(strings.Join(cur, "\n"))
		}
	}
	for _, line := range strings.Split(runtimeJS, "\n") {
		if n, ok := strings.CutPrefix(line, "// @part "); ok {
			flush()
			name, cur = strings.TrimSpace(n), nil
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return parts
}

// caseOf reads the action a `case "x":` line of act handles.
func caseOf(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), `case "`)
	if !ok {
		return "", false
	}
	t, _, ok := strings.Cut(rest, `"`)
	return t, ok
}

// SpecFile keeps the rules beside the code they made: a draft opened again in the
// builder reads them back. It is not packaged.
const SpecFile = "rules.json"

func cronExport(i int) string { return fmt.Sprintf("rule_%d", i+1) }

func devConfig(uses map[string]bool) []byte {
	settings := map[string]string{}
	if uses["telegram"] {
		settings["telegram_token"] = "123:test"
	}
	if uses["telegram_chat"] {
		settings["telegram_chat"] = "-100123"
	}
	if uses["discord"] {
		settings["discord_webhook"] = "https://discord.com/api/webhooks/1/test"
	}
	if uses["http_auth"] {
		settings["http_authorization"] = "Bearer test"
	}
	b, _ := json.MarshalIndent(map[string]any{"settings": settings}, "", "  ")
	return append(b, '\n')
}

// smokeTest delivers each event rule's event with a sample user, everything the
// rules reach mocked: the rules run without throwing. The operator adds real
// checks in the code.
func smokeTest(s Spec) string {
	var b strings.Builder
	b.WriteString(`// Made by the rule builder: each rule's event, with a sample user, runs without
// an error. Add your own checks below; plugin.event, mock.http and mock.calls are
// described in docs/plugins/testing.md.

function mockAll() {
  mock.http("https://", { status: 200, body: "{}" });
  mock.http("http://", { status: 200, body: "{}" });
  mock.api("GET", "/v1/users/*", { status: 200, body: { data: { id: 1, tags: [] } } });
  mock.api("PATCH", "/v1/users/*", { status: 200, body: { data: {} } });
  mock.api("POST", "/v1/users/bulk", { status: 200, body: { data: {} } });
}

const sample = { id: 1, name: "test-user", status: "active", enabled: true, plan_id: 0, telegram_id: 0, lang: "ru",
  user_id: 1, user: { id: 1, name: "test-user", telegram_id: 0, lang: "ru" } };
`)
	seen := map[string]bool{}
	for i, r := range s.Rules {
		switch {
		case r.Event != "" && !seen[r.Event]:
			seen[r.Event] = true
			fmt.Fprintf(&b, "\ntest(%q, () => {\n  mockAll();\n  plugin.event(%q, sample);\n});\n", "the "+r.Event+" rules run", r.Event)
		case r.Schedule != "":
			fmt.Fprintf(&b, "\ntest(%q, () => {\n  mockAll();\n  plugin.call(%q);\n});\n", "rule "+strconv.Itoa(i+1)+" runs on its schedule", cronExport(i))
		}
	}
	return b.String()
}

func readme(s Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	if s.Description != "" {
		b.WriteString(s.Description + "\n\n")
	}
	b.WriteString("Made in the RosPanel rule builder.\n\n")
	for i, r := range s.Rules {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("Rule %d", i+1)
		}
		when := r.Event
		if when == "" {
			when = "cron " + r.Schedule
		}
		var acts []string
		for _, a := range r.Actions {
			acts = append(acts, a.Type)
		}
		fmt.Fprintf(&b, "- **%s** — %s → %s\n", name, when, strings.Join(acts, ", "))
	}
	return b.String()
}
