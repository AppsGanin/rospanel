package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/store"
)

// An admin hands a plugin only permissions they hold: one without audit.view
// cannot install a plugin that reads the journal.
func TestPluginInstallNeedsTheInstallersPerms(t *testing.T) {
	rt, st := rolesTestRouter(t)
	host := plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: "4.4.0", Logger: slog.New(slog.DiscardHandler)})
	rt.SetPlugins(host)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	admin := store.SessionAdmin{Perms: model.NewPermSet([]string{model.PermPluginsManage, model.PermUsersView})}
	body := `{"sha256":"x","perms":["users.view","audit.view"],"net":[],"current_password":"irrelevant"}`
	r := httptest.NewRequest(http.MethodPost, "/api/plugins", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyAdmin{}, admin))
	w := httptest.NewRecorder()
	rt.installPlugin(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusForbidden || out["code"] != "err.pluginPermsBeyond" || !strings.Contains(w.Body.String(), "audit.view") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// A rollback brings back what the previous version was granted: the admin must hold
// that as for an install.
func TestPluginRollbackNeedsThePrevPerms(t *testing.T) {
	rt, st := rolesTestRouter(t)
	host := plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: "4.4.0", Logger: slog.New(slog.DiscardHandler)})
	rt.SetPlugins(host)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	ctx := context.Background()
	install := func(version, perms string, update bool) {
		raw := pluginZip(t, map[string]string{
			"plugin.json": `{"id": "rbk", "version": "` + version + `", "api": 1, "name": "Rb", "permissions": [` + perms + `]}`,
			"main.js":     `export function info() { return 1; }`,
		})
		pkg, err := host.Inspect(raw)
		if err != nil {
			t.Fatal(err)
		}
		consent := plugin.Consent{SHA256: pkg.SHA256, Perms: pkg.Manifest.Permissions}
		if update {
			_, err = host.Update(ctx, raw, consent)
		} else {
			_, err = host.Install(ctx, raw, consent)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	install("1.0.0", `"users.view", "audit.view"`, false)
	install("1.1.0", `"users.view"`, true)
	rollback := func(perms ...string) *httptest.ResponseRecorder {
		admin := store.SessionAdmin{Perms: model.NewPermSet(append(perms, model.PermPluginsManage))}
		r := httptest.NewRequest(http.MethodPost, "/api/plugins/rbk/rollback", nil)
		r.SetPathValue("id", "rbk")
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyAdmin{}, admin))
		w := httptest.NewRecorder()
		rt.rollbackPlugin(w, r)
		return w
	}
	if w := rollback(model.PermUsersView); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "audit.view") {
		t.Fatalf("rolled back past the admin's perms: %d %s", w.Code, w.Body.String())
	}
	if w := rollback(model.PermUsersView, model.PermAudit); w.Code != http.StatusOK {
		t.Fatalf("rollback: %d %s", w.Code, w.Body.String())
	}
}
