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
