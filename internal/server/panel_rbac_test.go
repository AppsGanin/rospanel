package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Custom roles: what each account may reach is the role's permission set, resolved on
// every request. Exercised through the real mux with real sessions, like the preset
// roles in panel_roles_test.go.

func callBody(h http.Handler, method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A role holds exactly what was ticked: view without manage reads but cannot save,
// and a section it was not given is closed.
func TestCustomRoleGatesBySection(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	role, err := st.CreateAdminRole("Поддержка", []string{model.PermUsersView, model.PermSettingsView})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	c := signIn(t, st, "support", role.Key, false)

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/users", 200},
		{"GET", "/api/events", 200},
		{"GET", "/api/settings", 200},
		{"GET", "/api/billing", 200}, // the user card's plan list rides on users.view
		{"POST", "/api/settings/maintenance", 403},
		{"POST", "/api/users", 403},
		{"DELETE", "/api/users/1", 403},
		{"GET", "/api/users/export", 403},
		{"GET", "/api/nodes", 403},
		{"GET", "/api/stats/users", 403},
		{"GET", "/api/apikeys", 403},
		{"GET", "/api/admin-audit", 403},
		{"GET", "/api/admins", 403},
		{"GET", "/api/roles", 403},
		{"GET", "/api/me", 200},
	} {
		if got := callBody(h, tc.method, tc.path, "{}", c).Code; got != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}

	// /api/me hands the SPA the permissions, so it can leave out what is closed.
	rec := callBody(h, "GET", "/api/me", "", c)
	var me struct {
		Perms []string `json:"perms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if want := []string{model.PermSettingsView, model.PermUsersView}; !slices.Equal(me.Perms, want) {
		t.Errorf("me.perms = %v, want %v", me.Perms, want)
	}
}

// Editing a role changes what its holders can do on their very next request — the
// session resolves the role on every lookup, it does not carry a copy from sign-in.
func TestRoleEditTakesEffectImmediately(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	role, err := st.CreateAdminRole("Серверы", []string{model.PermServersView})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	c := signIn(t, st, "ops", role.Key, false)
	if got := call(h, "GET", "/api/nodes", c); got != 200 {
		t.Fatalf("GET /api/nodes = %d, want 200", got)
	}
	if err := st.UpdateAdminRole(role.Key, role.Name, []string{model.PermUsersView}); err != nil {
		t.Fatalf("update role: %v", err)
	}
	if got := call(h, "GET", "/api/nodes", c); got != 403 {
		t.Errorf("GET /api/nodes after the role lost servers = %d, want 403", got)
	}
	if got := call(h, "GET", "/api/users", c); got != 200 {
		t.Errorf("GET /api/users after the role gained users = %d, want 200", got)
	}
}

// Deleting in bulk is still deleting: the bulk route needs users.manage, and its
// delete action needs users.delete as well.
func TestBulkDeleteNeedsTheDeletePermission(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	role, err := st.CreateAdminRole("Без удаления", []string{model.PermUsersManage})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	c := signIn(t, st, "editor", role.Key, false)
	if got := callBody(h, "POST", "/api/users/bulk", `{"ids":[1],"action":"delete"}`, c).Code; got != 403 {
		t.Errorf("bulk delete without users.delete = %d, want 403", got)
	}
	// Any other bulk action goes through to the manager (and fails there on the
	// missing user, not on the permission).
	if got := callBody(h, "POST", "/api/users/bulk", `{"ids":[1],"action":"disable"}`, c).Code; got == 403 {
		t.Errorf("bulk disable with users.manage = 403, want it let through")
	}
}

// Roles are the owner's: no permission reaches them, the owner's password is re-asked
// for every change, a preset cannot be deleted and a role still held cannot either.
func TestRolesAreOwnerOnlyAndGuarded(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	owner := signIn(t, st, "owner", model.RoleOwner, false)
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	if got := call(h, "GET", "/api/roles", admin); got != 403 {
		t.Errorf("GET /api/roles as the admin preset = %d, want 403", got)
	}
	// The bots too: the admin bot's chat can end the owner's sessions.
	if got := call(h, "GET", "/api/telegram", admin); got != 403 {
		t.Errorf("GET /api/telegram as the admin preset = %d, want 403", got)
	}
	rec := callBody(h, "GET", "/api/roles", "", owner)
	if rec.Code != 200 {
		t.Fatalf("GET /api/roles as owner = %d", rec.Code)
	}
	var list struct {
		Roles   []model.AdminRole   `json:"roles"`
		Catalog []model.PermSection `json:"catalog"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Roles) != 2 || list.Roles[0].Key != model.RoleAdmin || list.Roles[1].Key != model.RoleOperator {
		t.Fatalf("roles = %+v, want the two presets", list.Roles)
	}
	if list.Roles[0].Admins != 1 || !list.Roles[0].Preset {
		t.Errorf("admin preset = %+v, want preset held by one admin", list.Roles[0])
	}
	if len(list.Catalog) != len(model.PermCatalog) {
		t.Errorf("catalog has %d rows, want %d", len(list.Catalog), len(model.PermCatalog))
	}

	body := `{"name":"Бухгалтер","perms":["billing.manage"],"current_password":"%s"}`
	if got := callBody(h, "POST", "/api/roles", strings.Replace(body, "%s", "not-it", 1), owner).Code; got != 403 {
		t.Errorf("create with a wrong password = %d, want 403", got)
	}
	rec = callBody(h, "POST", "/api/roles", strings.Replace(body, "%s", "a-password", 1), owner)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var created model.AdminRole
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if want := []string{model.PermBillingManage, model.PermBillingView}; !slices.Equal(created.Perms, want) {
		t.Errorf("created perms = %v, want %v (manage brings view)", created.Perms, want)
	}
	// The same name twice is refused, whatever the case — a preset's built-in name and
	// the owner's included.
	for _, n := range []string{"бухгалтер", "Администратор", "operator", "Владелец"} {
		if got := callBody(h, "POST", "/api/roles", `{"name":"`+n+`","perms":[],"current_password":"a-password"}`, owner).Code; got != 400 {
			t.Errorf("name %q taken = %d, want 400", n, got)
		}
	}

	// Held → cannot be deleted; released → can.
	signIn(t, st, "accountant", created.Key, false)
	del := `{"current_password":"a-password"}`
	if got := callBody(h, "DELETE", "/api/roles/"+created.Key, del, owner).Code; got != 400 {
		t.Errorf("delete a held role = %d, want 400", got)
	}
	if got := callBody(h, "DELETE", "/api/roles/"+model.RoleOperator, del, owner).Code; got != 400 {
		t.Errorf("delete a preset = %d, want 400", got)
	}
	accountant, _, _, _ := st.GetAdminAuth("accountant")
	if err := st.SetAdminRole(accountant, model.RoleOperator); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if got := callBody(h, "DELETE", "/api/roles/"+created.Key, del, owner).Code; got != 200 {
		t.Errorf("delete a released role = %d, want 200", got)
	}

	// The owner cannot be granted: ownership moves by rescue, never by the roster.
	if got := callBody(h, "POST", "/api/admins",
		`{"username":"second","password":"temp-password","role":"owner","current_password":"a-password"}`, owner).Code; got != 400 {
		t.Errorf("create an admin with role owner = %d, want 400", got)
	}
}

// A role holding the API permission cannot mint a key broader than itself: that
// would turn "may manage keys" into "may do anything".
func TestAPIKeyCannotOutrankItsCreator(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	narrow, err := st.CreateAdminRole("Интеграции", []string{model.PermAPI, model.PermUsersView})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	reader, err := st.CreateAdminRole("Чтение", []string{model.PermUsersView})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	c := signIn(t, st, "integrator", narrow.Key, false)

	for _, tc := range []struct {
		role string
		want int
	}{
		{"", 400},              // full access
		{model.RoleAdmin, 400}, // broader than the creator
		{model.RoleOwner, 400}, // never a key role
		{"no-such-role", 400},  // unknown
		{reader.Key, 201},      // a subset
		{narrow.Key, 201},      // exactly the creator's own
	} {
		body := `{"name":"k","role":"` + tc.role + `"}`
		if got := callBody(h, "POST", "/api/apikeys", body, c).Code; got != tc.want {
			t.Errorf("create key with role %q = %d, want %d", tc.role, got, tc.want)
		}
	}
}

// A key with a role reaches over /v1 exactly what the role reaches in the panel, and
// the MCP endpoint offers only the tools behind those routes.
func TestAPIKeyRoleGatesRESTAndMCP(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)

	role, err := st.CreateAdminRole("Только пользователи", []string{model.PermUsersView})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	k, err := st.CreateAPIKey("reader", role.Key)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	if rec := apiGet(t, h, base+"/v1/users", k.RawKey); rec.Code != 200 {
		t.Errorf("GET /v1/users = %d, want 200", rec.Code)
	}
	for _, path := range []string{"/v1/settings", "/v1/nodes", "/v1/backup/info", "/v1/admin-audit", "/v1/webhooks"} {
		rec := apiGet(t, h, base+path, k.RawKey)
		if rec.Code != 403 {
			t.Errorf("GET %s = %d, want 403", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), `"forbidden"`) {
			t.Errorf("GET %s answered outside the API envelope: %s", path, rec.Body.String())
		}
	}
	rec := postJSON(t, h, base+"/v1/users", k.RawKey, map[string]any{"name": "x"})
	if rec.Code != 403 {
		t.Errorf("POST /v1/users with a read-only role = %d, want 403", rec.Code)
	}

	out := rpcResult(t, rpc(t, h, base+"/v1/mcp/"+k.RawKey,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	tools, _ := out["tools"].([]any)
	var names []string
	for _, raw := range tools {
		if m, ok := raw.(map[string]any); ok {
			names = append(names, m["name"].(string))
		}
	}
	if !slices.Contains(names, "get_users") {
		t.Errorf("tools %v lack get_users, which the role allows", names)
	}
	closed := []string{"get_settings", "get_nodes", "get_backup_info", "get_admin_audit", "get_webhooks", "get_stats_users"}
	for _, n := range names {
		if strings.HasPrefix(n, "post_") || strings.HasPrefix(n, "delete_") || strings.HasPrefix(n, "patch_") ||
			slices.Contains(closed, n) {
			t.Errorf("tool %s offered to a key whose role cannot call it", n)
		}
	}
}

// Every panel route carries a permission unless it is deliberately the caller's own
// account or the owner's roster — the list below is the whole exception set, and a
// route added later lands in neither by accident.
func TestEveryPanelRouteHasAPermission(t *testing.T) {
	t.Parallel()
	rt, _ := rolesTestRouter(t)
	rt.panelMux()
	unguarded := map[string]bool{
		"GET /api/me": true, "POST /api/setup/password": true, "POST /api/account/credentials": true,
		"GET /api/account/totp": true, "POST /api/account/totp/start": true,
		"POST /api/account/totp/enable": true, "POST /api/account/totp/disable": true,
		"GET /api/changelog": true, "GET /api/account/sessions": true,
		"DELETE /api/account/sessions/{id}": true, "POST /api/account/sessions/revoke-others": true,
		"GET /api/admins": true, "POST /api/admins": true, "POST /api/admins/{id}/role": true,
		"POST /api/admins/{id}/password": true, "DELETE /api/admins/{id}": true,
		"GET /api/roles": true, "POST /api/roles": true, "POST /api/roles/{key}": true, "DELETE /api/roles/{key}": true,
	}
	for _, p := range rt.routes {
		if strings.HasPrefix(p, "GET /api/telegram") || strings.HasPrefix(p, "POST /api/telegram") {
			unguarded[p] = true // the bots are the owner's
		}
	}
	// Backups, restore and the factory reset are the owner's too.
	for _, p := range []string{"POST /api/settings/local-backup", "GET /api/backup", "GET /api/backup/info",
		"POST /api/backup/inspect", "POST /api/restore", "POST /api/reset"} {
		unguarded[p] = true
	}
	for _, p := range rt.routes {
		perms, gated := rt.routePerms[p]
		if unguarded[p] {
			if gated {
				t.Errorf("%s is listed as the caller's own or the owner's, but carries %v", p, perms)
			}
			continue
		}
		if !gated || len(perms) == 0 {
			t.Errorf("%s is registered without a permission", p)
		}
		for _, perm := range perms {
			if !slices.Contains(model.AllPerms(), perm) {
				t.Errorf("%s needs %q, which is not in the catalog", p, perm)
			}
		}
	}
}

// apiRoutePerms names no route that does not exist — a stale entry would be a
// permission check nobody runs, and a reader would trust it.
func TestAPIRoutePermsHasNoStaleEntries(t *testing.T) {
	t.Parallel()
	rt := &Router{}
	rt.apiHandler()
	for pattern := range apiRoutePerms {
		if !slices.Contains(rt.apiRoutes, pattern) {
			t.Errorf("apiRoutePerms lists %s, which is not registered", pattern)
		}
	}
}

// A role that may delete users but not edit them deletes in bulk the same way in the
// panel and over the API — and does nothing else in bulk on either.
func TestBulkDeleteWorksForADeleteOnlyRole(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)
	role, err := st.CreateAdminRole("Чистка", []string{model.PermUsersView, model.PermUsersDelete})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	k, err := st.CreateAPIKey("cleaner", role.Key)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if rec := postJSON(t, h, base+"/v1/users/bulk", k.RawKey, map[string]any{"ids": []int{1}, "action": "delete"}); rec.Code == 403 {
		t.Errorf("bulk delete with users.delete = 403: %s", rec.Body.String())
	}
	if rec := postJSON(t, h, base+"/v1/users/bulk", k.RawKey, map[string]any{"ids": []int{1}, "action": "disable"}); rec.Code != 403 {
		t.Errorf("bulk disable without users.manage = %d, want 403", rec.Code)
	}
}

// Clearing a preset's name brings its built-in name back — which must not collide
// with a custom role that took that name while the preset was renamed.
func TestPresetNameResetCannotDuplicate(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)
	post := func(path, body string) int {
		return callBody(h, "POST", path, body, owner).Code
	}
	if got := post("/api/roles/operator", `{"name":"Поддержка","perms":["users.view"],"current_password":"a-password"}`); got != 200 {
		t.Fatalf("rename preset = %d", got)
	}
	if got := post("/api/roles", `{"name":"Оператор","perms":[],"current_password":"a-password"}`); got != 201 {
		t.Fatalf("take the freed name = %d", got)
	}
	if got := post("/api/roles/operator", `{"name":"","perms":["users.view"],"current_password":"a-password"}`); got != 400 {
		t.Errorf("reset the preset onto a taken name = %d, want 400", got)
	}
}
