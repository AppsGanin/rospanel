package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/version"
)

// A plugin made in the panel: the builder's rules, checked, tested, tried, then
// handed to the ordinary install; and a code draft edited by hand.
func TestPluginDraftFlow(t *testing.T) {
	rt, st := rolesTestRouter(t)
	if err := st.SetSetupDone(true); err != nil {
		t.Fatal(err)
	}
	host := plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: version.Version, Logger: slog.New(slog.DiscardHandler)})
	rt.SetPlugins(host)
	t.Cleanup(func() { _ = host.Close(t.Context()) })
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)
	operator := signIn(t, st, "support", model.RoleOperator, false)

	do := func(c *http.Cookie, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd *bytes.Reader
		if b, ok := body.([]byte); ok {
			rd = bytes.NewReader(b)
		} else {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		if _, ok := body.([]byte); ok {
			req.Header.Set("Content-Type", "application/zip")
		}
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, _ := do(operator, "GET", "/api/plugin-drafts", nil); code != http.StatusForbidden {
		t.Fatalf("an operator listed drafts: %d", code)
	}
	code, v := do(owner, "POST", "/api/plugin-drafts", map[string]string{"from": "builder", "plugin_id": "greet"})
	if code != 200 || v["spec"] == nil {
		t.Fatalf("create: %d %v", code, v)
	}
	id := int64(v["draft"].(map[string]any)["id"].(float64))
	path := "/api/plugin-drafts/" + strconv.FormatInt(id, 10)

	// Half-made rules are kept, with the reasons.
	spec := map[string]any{"id": "greet", "version": "1.0.0", "name": "Greet", "rules": []any{map[string]any{
		"event": "user.registered", "actions": []any{map[string]any{"type": "telegram", "chat": "", "text": "Hi {{user.name}}"}},
	}}}
	code, v = do(owner, "PUT", path, map[string]any{"spec": spec})
	if code != 200 || v["problems"] == nil || !strings.Contains(v["problems"].([]any)[0].(string), "chat") {
		t.Fatalf("half-made rules: %d %v", code, v)
	}
	spec["rules"].([]any)[0].(map[string]any)["actions"].([]any)[0].(map[string]any)["chat"] = "{{user.telegram_id}}"
	code, v = do(owner, "PUT", path, map[string]any{"spec": spec})
	if code != 200 || v["problems"] != nil {
		t.Fatalf("save: %d %v", code, v)
	}
	if code, _ := do(owner, "PUT", path, map[string]any{"files": []any{}}); code != http.StatusConflict {
		t.Fatalf("hand-edited a builder draft: %d", code)
	}

	code, v = do(owner, "POST", path+"/check", nil)
	if code != 200 || v["ok"] != true {
		t.Fatalf("check: %d %v", code, v)
	}
	code, v = do(owner, "POST", path+"/test", nil)
	if code != 200 || v["error"] != nil || len(v["results"].([]any)) != 1 || v["results"].([]any)[0].(map[string]any)["ok"] != true {
		t.Fatalf("test: %d %v", code, v)
	}
	code, v = do(owner, "POST", path+"/run", map[string]any{"event": "user.registered", "data": map[string]any{"id": 3, "name": "Ann", "telegram_id": 42}})
	if code != 200 || !strings.Contains(v["error"].(string), "no mock") {
		t.Fatalf("a trial run without real_http must not reach Telegram: %d %v", code, v)
	}
	calls := v["calls"].([]any)
	if len(calls) != 1 || !strings.Contains(calls[0].(map[string]any)["body"].(string), `"chat_id":"42"`) {
		t.Fatalf("calls: %v", calls)
	}

	// Download: the package, then the sources, which come back as a draft.
	req := httptest.NewRequest("GET", path+"/download?kind=package", nil)
	req.AddCookie(owner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "greet-1.0.0.zip") {
		t.Fatalf("download: %d %v", rec.Code, rec.Header())
	}
	if names := zipNames(t, rec.Body.Bytes()); strings.Contains(names, "test.js") || !strings.Contains(names, "main.js") {
		t.Fatalf("package files: %s", names)
	}
	req = httptest.NewRequest("GET", path+"/download", nil)
	req.AddCookie(owner)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if names := zipNames(t, rec.Body.Bytes()); !strings.Contains(names, "test.js") || !strings.Contains(names, "rules.json") {
		t.Fatalf("source files: %s", names)
	}
	code, v = do(owner, "POST", "/api/plugin-drafts", rec.Body.Bytes())
	if code != 200 || v["draft"].(map[string]any)["mode"] != "builder" || v["spec"] == nil {
		t.Fatalf("import the sources: %d %v", code, v)
	}

	// Install: the ordinary consent screen and install.
	code, insp := do(owner, "POST", path+"/inspect", nil)
	if code != 200 || insp["sha256"] == nil {
		t.Fatalf("inspect: %d %v", code, insp)
	}
	m := insp["manifest"].(map[string]any)
	code, info := do(owner, "POST", "/api/plugins", map[string]any{"sha256": insp["sha256"], "perms": m["permissions"],
		"net": m["net"], "current_password": "a-password"})
	if code != 200 || info["id"] != "greet" {
		t.Fatalf("install: %d %v", code, info)
	}

	// Leave the builder; edit by hand; the installed plugin opens as a code draft.
	if code, v = do(owner, "PUT", path, map[string]any{"mode": "code"}); code != 200 || v["spec"] != nil {
		t.Fatalf("to code: %d %v", code, v)
	}
	files := v["files"].([]any)
	for _, f := range files {
		if f.(map[string]any)["path"] == "main.js" {
			f.(map[string]any)["text"] = f.(map[string]any)["text"].(string) + "\nexport function ping() { return 'pong'; }\n"
		}
	}
	if code, _ := do(owner, "PUT", path, map[string]any{"files": files}); code != 200 {
		t.Fatalf("save code: %d", code)
	}
	code, v = do(owner, "POST", path+"/run", map[string]any{"export": "ping"})
	if code != 200 || v["result"] != "pong" {
		t.Fatalf("run an export: %d %v", code, v)
	}
	if code, _ := do(owner, "PUT", path, map[string]any{"files": []any{map[string]any{"path": "../x", "text": "x"}}}); code != 400 {
		t.Fatalf("an unsafe path was taken: %d", code)
	}
	code, v = do(owner, "POST", "/api/plugin-drafts", map[string]string{"from": "installed", "plugin_id": "greet"})
	if code != 200 || v["draft"].(map[string]any)["plugin_id"] != "greet" {
		t.Fatalf("open the installed plugin: %d %v", code, v)
	}
	code, v = do(owner, "GET", "/api/plugin-drafts", nil)
	if code != 200 || len(v["drafts"].([]any)) != 3 {
		t.Fatalf("list: %d %v", code, v)
	}
	if code, _ := do(owner, "DELETE", path, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := do(owner, "GET", path, nil); code != 404 {
		t.Fatalf("a deleted draft: %d", code)
	}
}

func zipNames(t *testing.T, raw []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return strings.Join(names, " ")
}
