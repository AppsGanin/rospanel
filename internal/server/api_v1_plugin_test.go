package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/store"
)

// A plugin's panel.api runs the real /v1 handlers with the plugin's permissions,
// and what it changes is attributed to the plugin.
func TestPluginAPI(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	rt := h.(*Router)
	if _, err := mgr.CreateUser(t.Context(), "existing", 0, 0); err != nil {
		t.Fatal(err)
	}
	call := func(perms []string, readOnly bool, method, path, body string) (int, string) {
		t.Helper()
		var b []byte
		if body != "" {
			b = []byte(body)
		}
		status, out, err := rt.PluginAPI(t.Context(), plugin.APIRequest{
			Plugin: "crm-sync", Perms: perms, ReadOnly: readOnly, Method: method, Path: path, Body: b,
		})
		if err != nil {
			return 0, err.Error()
		}
		return status, string(out)
	}

	if status, out := call([]string{model.PermUsersView}, false, http.MethodGet, "/v1/users", ""); status != 200 {
		t.Fatalf("list with users.view: %d %s", status, out)
	}
	if status, _ := call(nil, false, http.MethodGet, "/v1/users", ""); status != http.StatusForbidden {
		t.Fatalf("list without a permission: %d", status)
	}
	if status, _ := call([]string{model.PermUsersView}, false, http.MethodPost, "/v1/users", `{"name":"x"}`); status != http.StatusForbidden {
		t.Fatalf("create with users.view only: %d", status)
	}
	if status, out := call([]string{model.PermUsersManage}, true, http.MethodPost, "/v1/users", `{"name":"x"}`); status != 0 {
		t.Fatalf("a write from a decision hook went through: %d %s", status, out)
	}
	status, out := call([]string{model.PermUsersManage}, false, http.MethodPost, "/v1/users", `{"name":"from-plugin"}`)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create with users.manage: %d %s", status, out)
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Data.ID == 0 {
		t.Fatalf("created: %s", out)
	}
	evs, err := st.ListEvents(store.UserEventFilter{UserID: created.Data.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.ActorKind == model.ActorPlugin && e.ActorName == "crm-sync" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the creation is not attributed to the plugin: %+v", evs)
	}
}
