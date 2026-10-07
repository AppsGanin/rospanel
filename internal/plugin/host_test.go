package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

var quiet = slog.New(slog.DiscardHandler)

// memStore is the host's store in memory.
type memStore struct {
	mu sync.Mutex
	m  map[string]model.Plugin
}

func (s *memStore) ListPlugins() ([]model.Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Plugin
	for _, p := range s.m {
		out = append(out, p)
	}
	return out, nil
}

func (s *memStore) GetPlugin(id string) (*model.Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.m[id]; ok {
		return &p, nil
	}
	return nil, nil
}

func (s *memStore) SavePlugin(p model.Plugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[p.ID] = p
	return nil
}

func (s *memStore) SetPluginState(id string, enabled bool, status, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[id]
	p.Enabled, p.Status, p.StatusError = enabled, status, msg
	s.m[id] = p
	return nil
}

func (s *memStore) SetPluginConfig(id string, cfg map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[id]
	p.Config = cfg
	s.m[id] = p
	return nil
}

func (s *memStore) DeletePlugin(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

type harness struct {
	t      *testing.T
	host   *Host
	store  *memStore
	dir    string
	api    []APIRequest
	notes  chan string
	apiMu  sync.Mutex
	fetchs []FetchRequest
}

type fakeFetcher struct{ h *harness }

func (f fakeFetcher) Fetch(_ context.Context, allow []string, req FetchRequest) (*FetchResponse, error) {
	if req.BodyReader != nil {
		b, err := io.ReadAll(req.BodyReader)
		if err != nil {
			return nil, err
		}
		req.Body = string(b)
	}
	f.h.apiMu.Lock()
	f.h.fetchs = append(f.h.fetchs, req)
	f.h.apiMu.Unlock()
	body := "allow=" + strings.Join(allow, ",") + " " + req.Method + " " + req.Body
	if req.Sink != nil {
		_, err := io.WriteString(req.Sink, body)
		return &FetchResponse{Status: 200}, err
	}
	return &FetchResponse{Status: 200, Body: body}, nil
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, store: &memStore{m: map[string]model.Plugin{}}, dir: t.TempDir(), notes: make(chan string, 10)}
	h.host = New(Deps{
		Store: h.store, DataDir: h.dir, PanelVersion: "4.4.0",
		API: func(_ context.Context, req APIRequest) (int, []byte, error) {
			h.apiMu.Lock()
			h.api = append(h.api, req)
			h.apiMu.Unlock()
			if req.Path == "/v1/text" {
				return 200, []byte("plain"), nil
			}
			if req.Sink != nil {
				_, err := req.Sink.Write([]byte(`{"id": 12, "name": "bob"}`))
				return 200, nil, err
			}
			return 200, []byte(`{"id": 12, "name": "bob"}`), nil
		},
		Fetch:  fakeFetcher{h},
		Notify: func(id, msg string) { h.notes <- id + ": " + msg },
		Logger: quiet,
	})
	t.Cleanup(func() { _ = h.host.Close(context.Background()) })
	return h
}

// pkg builds a plugin zip.
func pkg(t *testing.T, manifestJSON, mainJS string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	files := map[string]string{"plugin.json": manifestJSON, "main.js": mainJS}
	for k, v := range extra {
		files[k] = v
	}
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func consentFor(t *testing.T, h *Host, raw []byte) Consent {
	t.Helper()
	p, err := h.Inspect(raw)
	if err != nil {
		t.Fatal(err)
	}
	return Consent{SHA256: p.SHA256, Perms: p.Manifest.Permissions, Net: p.Manifest.Net}
}

func (h *harness) install(raw []byte) *Info {
	h.t.Helper()
	info, err := h.host.Install(context.Background(), raw, consentFor(h.t, h.host, raw))
	if err != nil {
		h.t.Fatal(err)
	}
	return info
}

func (h *harness) enable(id string) *Info {
	h.t.Helper()
	info, err := h.host.Enable(context.Background(), id)
	if err != nil {
		h.t.Fatalf("enable: %v (%+v)", err, info)
	}
	return info
}

func (h *harness) call(id, export string, arg any) (string, error) {
	h.t.Helper()
	out, err := h.host.Call(context.Background(), id, export, arg, 5*time.Second)
	return string(out), err
}

func (h *harness) mustCall(id, export string, arg any) string {
	h.t.Helper()
	out, err := h.call(id, export, arg)
	if err != nil {
		h.t.Fatalf("%s.%s: %v", id, export, err)
	}
	return out
}

const confManifest = `{
	"id": "conf", "version": "1.0.0", "api": 1, "name": "Conformance",
	"permissions": ["users.view"], "net": ["api.example.com"],
	"settings": [
		{"key": "token", "kind": "secret", "label": "Token"},
		{"key": "mode", "kind": "select", "label": "Mode", "default": "a", "options": [{"value": "a", "label": "A"}, {"value": "b", "label": "B"}]},
		{"key": "note", "kind": "text", "label": "Note", "optional": true}
	],
	"provides": {"events": ["user.created"], "cron": [{"name": "sync", "schedule": "* * * * *"}]}
}`

// confMain exercises the whole panel API; each export returns what it saw.
const confMain = `
let enabled = 0;
export function onEnable() { enabled++; }
export function onEvent(e) { panel.kv.set("last", e); return e.event; }
export function sync() { return "synced"; }
export function info() { return {plugin: panel.plugin, config: panel.config, enabled}; }
export function kv() {
	panel.kv.set("a/1", {n: 1}); panel.kv.set("a/2", [2]); panel.kv.set("b", "x");
	const got = panel.kv.get("a/1"), missing = panel.kv.get("zzz");
	const list = panel.kv.list("a/");
	panel.kv.delete("a/1");
	return {got, missing: missing === undefined, list, after: panel.kv.list("a/").length};
}
export function db() {
	panel.db.exec("INSERT INTO notes(user_id, text) VALUES (?, ?)", 12, "hi");
	try { panel.db.tx(() => { panel.db.exec("INSERT INTO notes(user_id, text) VALUES (?, ?)", 13, "x"); throw new Error("abort"); }); } catch (e) {}
	panel.db.tx(() => panel.db.exec("INSERT INTO notes(user_id, text) VALUES (?, ?)", 14, "y"));
	let pragma = "ran";
	try { panel.db.exec("PRAGMA max_page_count = 1000000"); } catch (e) { pragma = String(e).includes("not allowed") ? "refused" : String(e); }
	return {rows: panel.db.query("SELECT user_id, text FROM notes ORDER BY id"), pragma};
}
export function leaveTxOpen() { panel.db.tx; __leak(); }
export function api() {
	return {user: panel.users.get(12), text: panel.api("GET", "/v1/text"),
		bad: (() => { try { panel.api("GET", "/admin/x"); return "ran"; } catch (e) { return "refused"; } })()};
}
export function fetch() { return panel.http.fetch("https://api.example.com/x", {method: "POST", body: {a: 1}}); }
export function crypto(req) {
	return {
		md5: panel.crypto.hash("md5", "abc"),
		sha256: panel.crypto.hash("sha256", "abc", "base64"),
		hmac: panel.crypto.hmac("sha256", "key", "The quick brown fox jumps over the lazy dog"),
		bytes: panel.crypto.hash("sha1", {base64: "AAE="}),
		sig: panel.crypto.sign("RS256", req.rsa, "payload"),
		jwt: panel.crypto.jwt("ES256", req.ec, {iss: "me"}),
		rnd: panel.crypto.randomHex(8).length,
		uuid: panel.crypto.randomUUID().length,
	};
}
export function verify(req) { return panel.crypto.verify("RS256", req.pub, "payload", req.sig); }
export function t(req) { return [panel.t("hello", {name: "Ann"}, req.lang), panel.t("missing")]; }
export function logs() { console.log("plain", {a: 1}); panel.log.warn("careful"); return 1; }
export function boom() { throw new Error("boom"); }
export function spin() { while (true) {} }
export function hog() { const a = []; for (let i = 0; i < 3e5; i++) a.push("x".repeat(64) + i); globalThis.keep = a; return a.length; }
export function count() { return enabled; }
`

var confExtra = map[string]string{
	"migrations/0001_notes.sql": `CREATE TABLE notes(id INTEGER PRIMARY KEY, user_id INTEGER, text TEXT)`,
	"i18n/ru.json":              `{"hello": "Привет, {name}"}`,
	"i18n/en.json":              `{"hello": "Hello, {name}"}`,
}

func installConf(h *harness) {
	h.t.Helper()
	h.install(pkg(h.t, confManifest, confMain, confExtra))
	if _, err := h.host.SetConfig(context.Background(), "conf", map[string]string{"token": "s3cret"}); err != nil {
		h.t.Fatal(err)
	}
	h.enable("conf")
}

func TestInstallNeedsMatchingConsent(t *testing.T) {
	h := newHarness(t)
	raw := pkg(t, confManifest, confMain, confExtra)
	c := consentFor(t, h.host, raw)
	for _, bad := range []Consent{
		{SHA256: "other", Perms: c.Perms, Net: c.Net},
		{SHA256: c.SHA256, Perms: nil, Net: c.Net},
		{SHA256: c.SHA256, Perms: c.Perms, Net: []string{"evil.com"}},
	} {
		if _, err := h.host.Install(context.Background(), raw, bad); !errors.Is(err, ErrConsent) {
			t.Fatalf("installed against %+v: %v", bad, err)
		}
	}
	info, err := h.host.Install(context.Background(), raw, c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != model.PluginDisabled || info.Enabled || info.Config["mode"] != "a" {
		t.Fatalf("a new plugin is installed off, with defaults: %+v", info)
	}
	if _, err := h.host.Install(context.Background(), raw, c); !errors.Is(err, ErrExists) {
		t.Fatalf("second install: %v", err)
	}
	// Required settings first.
	if _, err := h.host.Enable(context.Background(), "conf"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("enabled without its token: %v", err)
	}
	if _, err := h.host.SetConfig(context.Background(), "conf", map[string]string{"token": "x", "mode": "z"}); err == nil {
		t.Fatal("a select value outside its options was saved")
	}
	if _, err := h.host.SetConfig(context.Background(), "conf", map[string]string{"bogus": "1"}); err == nil {
		t.Fatal("an unknown setting was saved")
	}
}

func TestPanelAPI(t *testing.T) {
	h := newHarness(t)
	installConf(h)

	var info struct {
		Plugin  map[string]string `json:"plugin"`
		Config  map[string]string `json:"config"`
		Enabled int               `json:"enabled"`
	}
	_ = json.Unmarshal([]byte(h.mustCall("conf", "info", nil)), &info)
	if info.Plugin["id"] != "conf" || info.Config["token"] != "s3cret" || info.Config["mode"] != "a" || info.Enabled != 1 {
		t.Fatalf("info: %+v", info)
	}
	// Secrets never leave in Info.
	gi, _ := h.host.Get("conf")
	if _, ok := gi.Config["token"]; ok || len(gi.SecretsSet) != 1 {
		t.Fatalf("Info leaks or loses the secret: %+v", gi)
	}
	// An empty secret on save keeps the stored one.
	if _, err := h.host.SetConfig(context.Background(), "conf", map[string]string{"token": "", "mode": "b"}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(h.mustCall("conf", "info", nil)), &info)
	if info.Config["token"] != "s3cret" || info.Config["mode"] != "b" {
		t.Fatalf("after save: %+v", info.Config)
	}

	if got := h.mustCall("conf", "kv", nil); got != `{"got":{"n":1},"missing":true,"list":[{"key":"a/1","value":{"n":1}},{"key":"a/2","value":[2]}],"after":1}` {
		t.Fatalf("kv: %s", got)
	}
	if got := h.mustCall("conf", "db", nil); got != `{"rows":[{"text":"hi","user_id":12},{"text":"y","user_id":14}],"pragma":"refused"}` {
		t.Fatalf("db: %s", got)
	}

	got := h.mustCall("conf", "api", nil)
	if got != `{"user":{"status":200,"body":{"id":12,"name":"bob"}},"text":{"status":200,"body":"plain"},"bad":"refused"}` {
		t.Fatalf("api: %s", got)
	}
	if len(h.api) != 2 || h.api[0].Path != "/v1/users/12" || h.api[0].Plugin != "conf" || strings.Join(h.api[0].Perms, ",") != "users.view" || h.api[0].ReadOnly {
		t.Fatalf("api requests: %+v", h.api)
	}

	if got := h.mustCall("conf", "fetch", nil); got != `{"status":200,"headers":null,"body":"allow=api.example.com POST {\"a\":1}"}` {
		t.Fatalf("fetch: %s", got)
	}
	if h.fetchs[0].Headers["Content-Type"] != "application/json" {
		t.Fatalf("fetch headers: %+v", h.fetchs[0])
	}

	if got := h.mustCall("conf", "t", map[string]string{"lang": "en"}); got != `["Hello, Ann","missing"]` {
		t.Fatalf("t: %s", got)
	}
	h.mustCall("conf", "logs", nil)
	lines, _ := h.host.Logs("conf")
	var msgs []string
	for _, l := range lines {
		msgs = append(msgs, l.Level+":"+l.Msg)
	}
	if s := strings.Join(msgs, "|"); !strings.Contains(s, `info:plain {"a":1}`) || !strings.Contains(s, "warn:careful") {
		t.Fatalf("logs: %s", s)
	}
}

func TestCryptoOps(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rsaPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rk)}))
	ecDER, _ := x509.MarshalECPrivateKey(ek)
	ecPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER}))
	pubDER, _ := x509.MarshalPKIXPublicKey(&rk.PublicKey)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	var r map[string]any
	if err := json.Unmarshal([]byte(h.mustCall("conf", "crypto", map[string]string{"rsa": rsaPEM, "ec": ecPEM})), &r); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"md5":    "900150983cd24fb0d6963f7d28e17f72",
		"sha256": "ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=",
		"hmac":   "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
		"bytes":  "3f29546453678b855931c174a97d6c0894b8f546", // sha1 of the bytes 0x00 0x01
		"rnd":    float64(16),
		"uuid":   float64(36),
	}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("%s: got %v, want %v", k, r[k], v)
		}
	}
	if parts := strings.Split(r["jwt"].(string), "."); len(parts) != 3 {
		t.Errorf("jwt: %v", r["jwt"])
	}
	if got := h.mustCall("conf", "verify", map[string]any{"pub": pubPEM, "sig": r["sig"]}); got != "true" {
		t.Fatalf("verify own signature: %s", got)
	}
}

func TestBreakerPausesAndNotifies(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	for i := 0; i < breakerLimit; i++ {
		if _, err := h.call("conf", "boom", nil); err == nil {
			t.Fatal("boom returned")
		}
	}
	info, _ := h.host.Get("conf")
	if info.Status != model.PluginPaused || !info.Enabled || !strings.Contains(info.StatusError, "failed calls in a row") {
		t.Fatalf("after %d failures: %+v", breakerLimit, info)
	}
	select {
	case n := <-h.notes:
		if !strings.HasPrefix(n, "conf: ") {
			t.Fatal(n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admins were not told")
	}
	if _, err := h.call("conf", "info", nil); !errors.Is(err, ErrNotActive) {
		t.Fatalf("a paused plugin answered: %v", err)
	}
	if h.store.m["conf"].Status != model.PluginPaused {
		t.Fatal("pause not saved")
	}
	h.enable("conf") // resume
	h.mustCall("conf", "info", nil)
}

// A success resets the count: only failures in a row pause.
func TestBreakerCountsOnlyARun(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	for i := 0; i < 3*breakerLimit; i++ {
		if i%5 == 4 {
			h.mustCall("conf", "info", nil)
			continue
		}
		_, _ = h.call("conf", "boom", nil)
	}
	if info, _ := h.host.Get("conf"); info.Status != model.PluginActive {
		t.Fatalf("paused on scattered failures: %+v", info)
	}
}

// A call that hits its deadline costs the plugin a failure, not the panel a VM.
func TestTimeoutKeepsPluginUsable(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	if _, err := h.host.Call(context.Background(), "conf", "spin", nil, 100*time.Millisecond); err == nil {
		t.Fatal("spin returned")
	}
	if got := h.mustCall("conf", "count", nil); got != "1" {
		t.Fatalf("state lost after an interrupt: %s", got)
	}
}

func TestMissingExportFailsEnable(t *testing.T) {
	h := newHarness(t)
	h.install(pkg(t, confManifest, `export function onEvent() {}`, confExtra)) // no sync()
	_, _ = h.host.SetConfig(context.Background(), "conf", map[string]string{"token": "x"})
	info, err := h.host.Enable(context.Background(), "conf")
	if err == nil || !strings.Contains(err.Error(), "sync") || info.Status != model.PluginError {
		t.Fatalf("got %v %+v", err, info)
	}
}

func TestUpdateRollbackAndFailedUpdate(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	h.mustCall("conf", "db", nil)

	v2man := strings.Replace(confManifest, `"version": "1.0.0"`, `"version": "2.0.0"`, 1)
	v2extra := map[string]string{"migrations/0002_more.sql": `ALTER TABLE notes ADD COLUMN v2 INTEGER DEFAULT 2`}
	for k, v := range confExtra {
		v2extra[k] = v
	}
	v2main := confMain + `export function version() { return panel.db.query("SELECT v2 FROM notes LIMIT 1"); }`
	raw2 := pkg(t, v2man, v2main, v2extra)
	info, err := h.host.Update(context.Background(), raw2, consentFor(t, h.host, raw2))
	if err != nil || info.Version != "2.0.0" || info.PrevVersion != "1.0.0" || info.Status != model.PluginActive {
		t.Fatalf("update: %v %+v", err, info)
	}
	if got := h.mustCall("conf", "version", nil); got != `[{"v2":2}]` {
		t.Fatalf("v2 migration: %s", got)
	}
	if got, _ := h.call("conf", "info", nil); !strings.Contains(got, `"token":"s3cret"`) {
		t.Fatalf("settings lost across the update: %s", got)
	}

	// Roll back: v1 code and the database as it was before v2's migration.
	info, err = h.host.Rollback(context.Background(), "conf")
	if err != nil || info.Version != "1.0.0" {
		t.Fatalf("rollback: %v %+v", err, info)
	}
	if _, err := h.call("conf", "version", nil); err == nil {
		t.Fatal("v2 code still answering after rollback")
	}
	if got := h.mustCall("conf", "db", nil); strings.Contains(got, "v2") {
		t.Fatalf("v2 column survived rollback: %s", got)
	}

	// An update that cannot start rolls itself back.
	bad := pkg(t, v2man, `export function onEvent() {} export function sync() {} export function onEnable() { throw new Error("nope"); }`, v2extra)
	if _, err := h.host.Update(context.Background(), bad, consentFor(t, h.host, bad)); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("bad update: %v", err)
	}
	if info, _ := h.host.Get("conf"); info.Version != "1.0.0" || info.Status != model.PluginActive {
		t.Fatalf("after failed update: %+v", info)
	}
	h.mustCall("conf", "info", nil)
}

func TestDisableUninstall(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	h.mustCall("conf", "db", nil)
	if _, err := h.host.Disable(context.Background(), "conf"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.call("conf", "info", nil); !errors.Is(err, ErrNotActive) {
		t.Fatal(err)
	}
	path := h.host.dbPath("conf")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("database gone on disable")
	}
	if err := h.host.Uninstall(context.Background(), "conf", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("database left after uninstall")
	}
	if _, err := h.host.Get("conf"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// After a restart the enabled plugins come back by themselves.
func TestStartLoadsEnabled(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	h.mustCall("conf", "db", nil)
	_ = h.host.Close(context.Background())

	h2 := New(Deps{Store: h.store, DataDir: h.dir, PanelVersion: "4.4.0", Logger: quiet})
	defer h2.Close(context.Background())
	if err := h2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h2.Active(func(m *manifest.Manifest) bool { return len(m.Provides.Events) > 0 }); fmt.Sprint(got) != "[conf]" {
		t.Fatalf("active: %v", got)
	}
	out, err := h2.Call(context.Background(), "conf", "db", nil, 5*time.Second)
	if err != nil || !strings.Contains(string(out), `"text":"hi"`) {
		t.Fatalf("data after restart: %s %v", out, err)
	}
}

// A VM that grew past its share of the cap is replaced before the next call.
func TestBloatedVMIsReplaced(t *testing.T) {
	h := newHarness(t)
	man := strings.Replace(confManifest, `"provides"`, `"memory_mb": 8, "provides"`, 1)
	h.install(pkg(t, man, confMain, confExtra))
	_, _ = h.host.SetConfig(context.Background(), "conf", map[string]string{"token": "x"})
	h.enable("conf")
	if got := h.mustCall("conf", "count", nil); got != "1" {
		t.Fatal(got)
	}
	_, _ = h.call("conf", "hog", nil) // may also run out of memory; either way the VM is big now
	if got := h.mustCall("conf", "count", nil); got != "0" {
		t.Fatalf("a bloated VM kept serving (count %s)", got)
	}
}

func TestStormMeter(t *testing.T) {
	var s stormMeter
	base := time.Unix(1_700_000_000, 0).Truncate(time.Minute)
	storm := false
	for minute := 0; minute < stormMinutes && !storm; minute++ {
		for i := 0; i <= stormRate+1; i++ {
			storm = s.note(base.Add(time.Duration(minute) * time.Minute))
		}
		if minute < stormMinutes-1 && storm {
			t.Fatalf("storm declared in minute %d", minute)
		}
	}
	if !storm {
		t.Fatal("no storm after three hot minutes")
	}
	// A quiet minute resets it.
	var q stormMeter
	for i := 0; i <= stormRate+1; i++ {
		q.note(base)
	}
	q.note(base.Add(2 * time.Minute))
	if q.hot != 0 {
		t.Fatal("a gap did not reset the count")
	}
}

func TestDeliverEvent(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	gone, err := h.host.DeliverEvent(context.Background(), "conf", []byte(`{"id":"e1","event":"user.created","data":{"id":5}}`))
	if gone || err != nil {
		t.Fatal(gone, err)
	}
	if got := h.host.EventSubscribers("user.created"); fmt.Sprint(got) != "[conf]" {
		t.Fatal(got)
	}
	if got := h.host.EventSubscribers("payment.paid"); len(got) != 0 {
		t.Fatal(got)
	}
	_, _ = h.host.Disable(context.Background(), "conf")
	if gone, _ := h.host.DeliverEvent(context.Background(), "conf", []byte(`{}`)); !gone {
		t.Fatal("a disabled plugin must report gone")
	}
	if gone, _ := h.host.DeliverEvent(context.Background(), "nope", []byte(`{}`)); !gone {
		t.Fatal("a missing plugin must report gone")
	}
}

func TestCron(t *testing.T) {
	h := newHarness(t)
	man := strings.Replace(confManifest, `"cron": [{"name": "sync", "schedule": "* * * * *"}]`,
		`"cron": [{"name": "sync", "schedule": "* * * * *"}, {"name": "nightly", "schedule": "0 3 * * *"}]`, 1)
	main := confMain + `
let nightlyRuns = 0;
export function nightly() { nightlyRuns++; }
export function cronRuns() { return [panel.kv.get("runs") || 0, nightlyRuns]; }
`
	main = strings.Replace(main, `export function sync() { return "synced"; }`,
		`export function sync() { panel.kv.set("runs", (panel.kv.get("runs") || 0) + 1); }`, 1)
	h.install(pkg(t, man, main, confExtra))
	_, _ = h.host.SetConfig(context.Background(), "conf", map[string]string{"token": "x"})
	h.enable("conf")

	at := time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC)
	h.host.RunCron(context.Background(), at)
	h.host.RunCron(context.Background(), at.Add(10*time.Second)) // the same minute: ignored
	h.host.WaitCron(5 * time.Second)
	h.host.RunCron(context.Background(), at.Add(time.Minute))
	h.host.WaitCron(5 * time.Second)
	h.host.RunCron(context.Background(), time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC))
	h.host.WaitCron(5 * time.Second)
	if got := h.mustCall("conf", "cronRuns", nil); got != "[3,1]" {
		t.Fatalf("runs: %s", got)
	}
}

// An empty log is an empty list: the panel's log dialog reads null as "loading".
func TestEmptyLogIsAList(t *testing.T) {
	h := newHarness(t)
	h.install(pkg(t, `{"id": "quiet", "version": "1.0.0", "api": 1, "name": "Quiet"}`, `export {};`, nil))
	lines, err := h.host.Logs("quiet")
	if err != nil || lines == nil {
		t.Fatalf("%#v %v", lines, err)
	}
	if b, _ := json.Marshal(map[string]any{"lines": lines}); string(b) != `{"lines":[]}` {
		t.Fatalf("%s", b)
	}
}
