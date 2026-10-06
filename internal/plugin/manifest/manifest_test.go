package manifest

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// full is the manifest from docs/plugins-design.md, every point declared.
const full = `{
  "id": "lava-pay",
  "version": "1.2.0",
  "api": 1,
  "panel": ">=4.4.0",
  "name": {"ru": "Lava", "en": "Lava"},
  "description": {"ru": "Оплата через Lava", "en": "Payments via Lava"},
  "author": "someone",
  "homepage": "https://github.com/someone/rospanel-lava",
  "license": "MIT",
  "permissions": ["users.view"],
  "net": ["api.lava.ru", "*.lava.ru:8443"],
  "settings": [
    {"key": "shop_id", "kind": "text", "label": {"ru": "ID магазина", "en": "Shop ID"}},
    {"key": "secret", "kind": "secret", "label": "Секретный ключ"},
    {"key": "mode", "kind": "select", "label": "Mode", "options": [{"value": "live", "label": "Live"}]}
  ],
  "provides": {
    "events": ["payment.paid", "user.created"],
    "hooks": ["beforeSignup"],
    "cron": [{"name": "sync", "schedule": "*/15 * * * *"}],
    "payment": {"label": "Lava"},
    "http": true,
    "channel": {"label": "Email"},
    "user_fields": [{"key": "email", "label": "Email"}],
    "actions": [{"key": "receipt", "label": {"ru": "Выдать чек"}, "scope": "user", "perm": "users.manage", "confirm": true}],
    "widgets": [{"key": "revenue", "label": {"ru": "Чеки за неделю"}}],
    "sub_blocks": true,
    "bot": {"menu": true, "commands": ["receipt"]},
    "price": true,
    "subscription": true
  },
  "db_quota_mb": 100,
  "experimental": ["channel", "price", "subscription"]
}`

// allPoints opens every extension point for a test of the manifest's shape, which
// is checked whether or not this panel calls the point yet.
func allPoints(t *testing.T) {
	t.Helper()
	saved := Available
	Available = map[string]bool{}
	for _, p := range []string{"events", "hooks", "cron", "payment", "http", "channel", "user_fields",
		"actions", "widgets", "sub_blocks", "bot", "price", "subscription"} {
		Available[p] = true
	}
	t.Cleanup(func() { Available = saved })
}

func TestUnavailablePointsAreRefused(t *testing.T) {
	m, err := Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Validate("4.4.0")
	if err == nil || !strings.Contains(err.Error(), "provides.payment: not available") ||
		strings.Contains(err.Error(), "provides.events: not available") {
		t.Fatalf("got %v", err)
	}
}

func TestFullManifestValidates(t *testing.T) {
	allPoints(t)
	m, err := Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate("4.4.0"); err != nil {
		t.Fatal(err)
	}
	want := "onEvent beforeSignup sync payment.create payment.status payment.webhook onHttp channel.send userFields onAction widget subBlocks bot.menu bot.onCallback bot.onCommand quotePrice transformSubscription"
	if got := strings.Join(m.Exports(), " "); got != want {
		t.Fatalf("exports:\n got %s\nwant %s", got, want)
	}
	if m.Name.Get("de") != "Lava" || m.Settings[1].Label.Get("en") != "Секретный ключ" {
		t.Fatal("Text fallback")
	}
	if m.Quota() != 100<<20 || m.Memory() != DefaultMemoryMB<<20 {
		t.Fatal("quota/memory defaults")
	}
	// A plain Text round-trips as a plain string.
	b, _ := json.Marshal(m.Settings[1].Label)
	if string(b) != `"Секретный ключ"` {
		t.Fatalf("text marshal: %s", b)
	}
}

// edit applies a JSON merge to the full manifest: each case breaks one thing.
func edit(t *testing.T, patch string) *Manifest {
	t.Helper()
	var base, p map[string]any
	_ = json.Unmarshal([]byte(full), &base)
	if err := json.Unmarshal([]byte(patch), &p); err != nil {
		t.Fatal(err)
	}
	for k, v := range p {
		if v == nil {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	b, _ := json.Marshal(base)
	m, err := Parse(b)
	if err != nil {
		t.Fatalf("parse %s: %v", patch, err)
	}
	return m
}

func TestValidateRefuses(t *testing.T) {
	allPoints(t)
	for _, c := range []struct{ patch, want string }{
		{`{"id": "Lava"}`, "id"},
		{`{"id": "rospanel-x"}`, "reserved"},
		{`{"id": "ab"}`, "id"},
		{`{"version": "1.2"}`, "version"},
		{`{"api": 2}`, "api 2"},
		{`{"panel": ">=9.0.0"}`, "needs 9.0.0"},
		{`{"panel": "^4.0.0"}`, `">=X.Y.Z"`},
		{`{"name": ""}`, "name: required"},
		{`{"name": {"russian": "x"}}`, "language code"},
		{`{"homepage": "javascript:alert(1)"}`, "homepage"},
		{`{"permissions": ["api.manage"]}`, "never granted"},
		{`{"permissions": ["owner"]}`, "never granted"},
		{`{"permissions": ["users.fly"]}`, "unknown permission"},
		{`{"permissions": ["users.view", "users.view"]}`, "twice"},
		{`{"net": ["127.0.0.1"]}`, "host name"},
		{`{"net": ["localhost"]}`, "host name"},
		{`{"net": ["API.example.com"]}`, "lower case"},
		{`{"net": ["a.com:99999"]}`, "port out of range"},
		{`{"net": ["https://a.com"]}`, "host name"},
		{`{"settings": [{"key": "a", "kind": "color", "label": "x"}]}`, "kind"},
		{`{"settings": [{"key": "a", "kind": "select", "label": "x"}]}`, "needs options"},
		{`{"settings": [{"key": "A", "kind": "text", "label": "x"}]}`, "key"},
		{`{"settings": [{"key": "a", "kind": "text", "label": "x"}, {"key": "a", "kind": "bool", "label": "y"}]}`, "twice"},
		{`{"settings": [{"key": "a", "kind": "secret", "label": "x", "default": "s"}]}`, "secret cannot have a default"},
		{`{"settings": [{"key": "a", "kind": "text"}]}`, "label: required"},
		{`{"provides": {"events": ["user.flew"]}}`, "unknown event"},
		{`{"provides": {"hooks": ["afterEverything"]}}`, "hooks[0]"},
		{`{"provides": {"cron": [{"name": "sync", "schedule": "every minute"}]}}`, "schedule"},
		{`{"provides": {"cron": [{"name": "a.b", "schedule": "* * * * *"}]}}`, "exported function"},
		{`{"provides": {"payment": {}}}`, "payment.label"},
		{`{"provides": {"actions": [{"key": "a", "label": "x", "scope": "everyone"}]}}`, "scope"},
		{`{"provides": {"bot": {"commands": ["start"]}}}`, "not start"},
		{`{"provides": {"bot": {}}}`, "menu, commands or both"},
		{`{"experimental": []}`, `add "channel"`},
		{`{"experimental": ["channel", "price", "subscription", "time-travel"]}`, "not an experimental point"},
		{`{"db_quota_mb": 5000}`, "db_quota_mb"},
		{`{"memory_mb": 512}`, "memory_mb"},
	} {
		err := edit(t, c.patch).Validate("4.4.0")
		var p Problems
		if !errors.As(err, &p) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a problem mentioning %q, got %v", c.patch, c.want, err)
		}
	}
}

func TestParseIsStrict(t *testing.T) {
	for _, s := range []string{`{"id": "x", "permision": []}`, `{"id": "abc"} {}`, `[]`, `{"name": 5}`} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("parsed %s", s)
		}
	}
}

func TestAllProblemsAtOnce(t *testing.T) {
	allPoints(t)
	err := edit(t, `{"id": "X", "version": "1", "api": 7}`).Validate("4.4.0")
	var p Problems
	if !errors.As(err, &p) || len(p) < 3 {
		t.Fatalf("want every problem, got %v", err)
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{{"4.4.0", "4.10.0", true}, {"4.10.0", "4.4.0", false}, {"4.4.0", "4.4.0", false}, {"4.4.0-rc1", "4.4.0", false}, {"v3.9.9", "4.0.0", true}} {
		if versionLess(c.a, c.b) != c.less {
			t.Errorf("%s < %s should be %v", c.a, c.b, c.less)
		}
	}
}

// --- packages ---

type entry struct {
	name, body string
	mode       fs.FileMode
}

func zipOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(e.body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const minimal = `{"id": "hello", "version": "0.1.0", "api": 1, "name": "Hello", "provides": {"events": ["user.created"]}}`

func TestReadPackage(t *testing.T) {
	raw := zipOf(t,
		entry{name: "hello/plugin.json", body: minimal},
		entry{name: "hello/main.js", body: `export function onEvent(e) {}`},
		entry{name: "hello/migrations/0002_b.sql", body: `ALTER TABLE t ADD COLUMN b`},
		entry{name: "hello/migrations/0001_a.sql", body: `CREATE TABLE t(a)`},
		entry{name: "hello/i18n/ru.json", body: `{"denied": "Регистрация закрыта для {who}"}`},
		entry{name: "hello/README.md", body: "# Hello"},
		entry{name: "__MACOSX/hello/._main.js", body: "junk"},
		entry{name: "hello/.DS_Store", body: "junk"},
	)
	pkg, err := Read(raw, "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.ID != "hello" || pkg.Main == "" || len(pkg.Migrations) != 2 || pkg.Readme != "# Hello" || len(pkg.SHA256) != 64 {
		t.Fatalf("%+v", pkg)
	}
	if got := pkg.Translate("ru", "denied", map[string]string{"who": "ботов"}); got != "Регистрация закрыта для ботов" {
		t.Fatal(got)
	}
	if got := pkg.Translate("en", "missing.key", nil); got != "missing.key" {
		t.Fatal(got)
	}
}

func TestReadRefuses(t *testing.T) {
	ok := []entry{{name: "plugin.json", body: minimal}, {name: "main.js", body: "export function onEvent() {}"}}
	with := func(extra ...entry) []byte { return zipOf(t, append(append([]entry{}, ok...), extra...)...) }
	for name, c := range map[string]struct {
		raw  []byte
		want string
	}{
		"not a zip":        {[]byte("hello"), "not a zip"},
		"no manifest":      {zipOf(t, entry{name: "main.js", body: "x"}), "plugin.json missing"},
		"no main":          {zipOf(t, entry{name: "plugin.json", body: minimal}), "main.js missing"},
		"stray file":       {with(entry{name: "node_modules/x.js", body: "x"}), "not part of a plugin package"},
		"traversal":        {with(entry{name: "../evil.js", body: "x"}), "unsafe path"},
		"absolute":         {with(entry{name: "/etc/passwd", body: "x"}), "unsafe path"},
		"dot segments":     {with(entry{name: "i18n/../main.js", body: "x"}), "unsafe path"},
		"symlink":          {with(entry{name: "icon.svg", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}), "not a regular file"},
		"bad migration":    {with(entry{name: "migrations/0001.sql", body: "PRAGMA max_page_count = 9"}), "not allowed"},
		"bad i18n":         {with(entry{name: "i18n/ru.json", body: `{"a": 1}`}), "JSON object of strings"},
		"bad manifest":     {zipOf(t, entry{name: "plugin.json", body: `{"id": "X"}`}, entry{name: "main.js", body: "x"}), "id"},
		"main not utf8":    {zipOf(t, entry{name: "plugin.json", body: minimal}, entry{name: "main.js", body: "\xff\xfe"}), "not UTF-8"},
		"duplicate path":   {with(entry{name: "main.js", body: "again"}), "appears twice"},
		"migration naming": {with(entry{name: "migrations/init.sql", body: "SELECT 1"}), "not part of a plugin package"},
	} {
		_, err := Read(c.raw, "4.4.0")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
	}
}

// A zip bomb is refused by its sizes, never by expanding it.
func TestZipBomb(t *testing.T) {
	big := strings.Repeat("a", MaxUnpacked+1) // compresses to a few KB
	raw := zipOf(t, entry{name: "plugin.json", body: minimal}, entry{name: "main.js", body: big})
	if len(raw) > MaxPackage {
		t.Fatalf("test zip is %d bytes", len(raw))
	}
	if _, err := Read(raw, "4.4.0"); err == nil || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Fatalf("got %v", err)
	}
	if _, err := Read(make([]byte, MaxPackage+1), "4.4.0"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized upload: %v", err)
	}
}
