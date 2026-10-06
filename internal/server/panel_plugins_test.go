package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
)

func pluginZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The whole admin round: inspect → consent with the password → configure → enable
// → logs, code → disable → remove; and who may do it.
func TestPluginAdminFlow(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	if err := st.SetSetupDone(true); err != nil { // the password is waived during setup
		t.Fatal(err)
	}
	rt.SetPlugins(plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: "4.4.0", Logger: slog.New(slog.DiscardHandler)}))
	t.Cleanup(func() { _ = rt.plugins.Close(t.Context()) })
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)
	operator := signIn(t, st, "support", model.RoleOperator, false)

	do := func(c *http.Cookie, method, path, ctype string, body []byte) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	raw := pluginZip(t, map[string]string{
		"plugin.json": `{"id": "hello", "version": "1.0.0", "api": 1, "name": "Hello",
			"permissions": ["users.view", "billing.manage"], "net": ["api.example.com"],
			"settings": [{"key": "token", "kind": "secret", "label": "Token"}],
			"provides": {"events": ["user.created"]}}`,
		"main.js": `export function onEvent(e) { console.log("got", e.event); }`,
	})

	if code, _ := do(operator, "POST", "/api/plugins/inspect", "application/zip", raw); code != http.StatusForbidden {
		t.Fatalf("an operator inspected a plugin: %d", code)
	}
	code, insp := do(owner, "POST", "/api/plugins/inspect", "application/zip", raw)
	if code != 200 {
		t.Fatalf("inspect: %d %v", code, insp)
	}
	if insp["risky_perms"] == nil || len(insp["exports"].([]any)) != 1 {
		t.Fatalf("inspection: %v", insp)
	}
	sha := insp["sha256"].(string)

	if code, out := do(owner, "POST", "/api/plugins/inspect", "application/zip", []byte("not a zip")); code != 400 || out["code"] != "err.pluginInvalid" {
		t.Fatalf("bad package: %d %v", code, out)
	}

	install := func(body string) (int, map[string]any) {
		return do(owner, "POST", "/api/plugins", "application/json", []byte(body))
	}
	if code, _ := install(`{"sha256":"` + sha + `","perms":["users.view","billing.manage"],"net":["api.example.com"],"current_password":"wrong"}`); code != http.StatusForbidden {
		t.Fatalf("installed with a wrong password: %d", code)
	}
	// A refused password consumed nothing: the upload is still there.
	if code, out := install(`{"sha256":"` + sha + `","perms":["users.view"],"net":["api.example.com"],"current_password":"a-password"}`); code != http.StatusConflict || out["code"] != "err.pluginConsent" {
		t.Fatalf("installed against a narrower consent: %d %v", code, out)
	}
	// The mismatch spent the upload; inspect again and install for real.
	do(owner, "POST", "/api/plugins/inspect", "application/zip", raw)
	if code, out := install(`{"sha256":"` + sha + `","perms":["users.view","billing.manage"],"net":["api.example.com"],"current_password":"a-password"}`); code != 200 || out["status"] != "disabled" {
		t.Fatalf("install: %d %v", code, out)
	}

	if code, out := do(owner, "POST", "/api/plugins/hello/enable", "", nil); code != 422 || !strings.Contains(out["error"].(string), "token") {
		t.Fatalf("enabled without its settings: %d %v", code, out)
	}
	if code, _ := do(owner, "POST", "/api/plugins/hello/config", "application/json", []byte(`{"values":{"token":"t0k"}}`)); code != 200 {
		t.Fatalf("config: %d", code)
	}
	if code, out := do(owner, "POST", "/api/plugins/hello/enable", "", nil); code != 200 || out["status"] != "active" {
		t.Fatalf("enable: %d %v", code, out)
	}
	if code, out := do(operator, "GET", "/api/plugins", "", nil); code != http.StatusForbidden {
		t.Fatalf("an operator listed plugins: %d %v", code, out)
	}
	code, list := do(owner, "GET", "/api/plugins", "", nil)
	plugins, _ := list["plugins"].([]any)
	if code != 200 || len(plugins) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	if p := plugins[0].(map[string]any); p["config"].(map[string]any)["token"] != nil {
		t.Fatalf("the secret came back in the list: %v", p)
	}
	if code, out := do(owner, "GET", "/api/plugins/hello/code", "", nil); code != 200 || !strings.Contains(out["code"].(string), "onEvent") {
		t.Fatalf("code: %d %v", code, out)
	}
	if code, _ := do(owner, "GET", "/api/plugins/hello/logs", "", nil); code != 200 {
		t.Fatalf("logs: %d", code)
	}
	if code, out := do(owner, "POST", "/api/plugins/hello/disable", "", nil); code != 200 || out["status"] != "disabled" {
		t.Fatalf("disable: %d %v", code, out)
	}
	if code, _ := do(owner, "DELETE", "/api/plugins/hello", "", nil); code != 200 {
		t.Fatalf("uninstall: %d", code)
	}
	if code, out := do(owner, "POST", "/api/plugins/hello/enable", "", nil); code != 404 || out["code"] != "err.pluginNotFound" {
		t.Fatalf("after uninstall: %d %v", code, out)
	}
}
