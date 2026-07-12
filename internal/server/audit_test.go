package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// inSomeCategory reports whether an action is reachable from the journal's filter.
func inSomeCategory(action string) bool {
	for _, c := range model.AdminAuditCategories {
		for _, key := range model.AdminAuditActionsIn(c.Key) {
			if key == action {
				return true
			}
		}
	}
	return false
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	h, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

// This is the test the whole design exists for. The admin trail is written by one
// middleware keyed on the route pattern, which is only as complete as auditActions —
// so a new mutating endpoint added without a thought about auditing must fail the
// build, not ship silently unlogged. Adding the route to auditActions (with an action,
// or with "" and a reason) is the fix.
func TestEveryMutatingRouteIsAudited(t *testing.T) {
	rt, _ := rolesTestRouter(t)
	rt.panelMux() // registration is what fills rt.routes

	if len(rt.routes) == 0 {
		t.Fatal("no routes recorded — the registration helpers stopped tracking them")
	}
	for _, pattern := range rt.routes {
		if !mutatingPattern(pattern) {
			continue
		}
		if _, ok := auditActions[pattern]; !ok {
			t.Errorf("route %q changes state but has no entry in auditActions.\n"+
				"Add it: an action key to record it, or \"\" with a comment saying why not.",
				pattern)
		}
	}
}

// Every action the middleware can write must be renderable by the journal, or the UI
// shows a bare key like "settings.changed" to the owner.
func TestEveryAuditActionHasALabel(t *testing.T) {
	for pattern, route := range auditActions {
		if route.action == "" {
			continue
		}
		if model.AdminAuditLabel(route.action) == route.action {
			t.Errorf("action %q (route %q) is missing from model.AdminAuditCatalog",
				route.action, pattern)
		}
		// An action in no category is invisible to the journal's filter — it can only
		// be found by scrolling the whole trail.
		if !inSomeCategory(route.action) {
			t.Errorf("action %q (route %q) belongs to no category — the filter can't reach it",
				route.action, pattern)
		}
		// The settings share one action, so the section is the ONLY thing that says
		// what was changed. A settings route without one records "кто-то что-то
		// поменял", which is not an audit trail.
		if route.action == model.AuditSettings && route.section == "" {
			t.Errorf("settings route %q records no section — the row would not say what changed", pattern)
		}
		if route.action != model.AuditSettings && route.section != "" {
			t.Errorf("route %q sets a section but isn't a settings row", pattern)
		}
	}
}

// The settings all share one action, and the section rides in the target — that is
// what keeps the filter short without losing what was actually touched.
func TestAuditSettingsRowsCarryTheirSection(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)

	req := httptest.NewRequest("POST", "/api/settings/dns",
		strings.NewReader(`{"servers":["1.1.1.1"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(owner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code >= http.StatusBadRequest {
		t.Fatalf("save dns = %d, want success", rec.Code)
	}

	rows := auditRows(t, st)
	if len(rows) == 0 {
		t.Fatal("no audit row for a settings change")
	}
	if rows[0].Action != model.AuditSettings {
		t.Errorf("action = %q, want the shared %q", rows[0].Action, model.AuditSettings)
	}
	if rows[0].Target != "DNS" {
		t.Errorf("target = %q, want the section that was changed", rows[0].Target)
	}
}

// auditRows reads the trail straight from the store — what a test asserts on.
func auditRows(t *testing.T, st *store.Store) []model.AdminAudit {
	t.Helper()
	rows, err := st.ListAdminAudit(store.AdminAuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	return rows
}

// A refused request is not a change to the panel. Recording one as if it happened
// would make the trail lie in the most damaging direction: it would show an admin
// doing things they never managed to do.
func TestAuditRecordsSuccessNotAttempts(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)

	// Wrong step-up password → 403, nothing happened.
	req := httptest.NewRequest("POST", "/api/admins",
		strings.NewReader(`{"username":"support","password":"temp-password","role":"operator","current_password":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(owner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("refused create = %d, want 403", rec.Code)
	}

	for _, row := range auditRows(t, st) {
		if row.Action == model.AuditAdminCreated {
			t.Fatal("a refused create was recorded as if it had happened")
		}
	}
}

// The roster rows have to name their target: "admin.deleted" with no login is an
// audit row that tells you nothing.
func TestAuditRosterRowsNameTheirTarget(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)

	post := func(path, body string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(owner)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("/api/admins",
		`{"username":"support","password":"temp-password","role":"operator","current_password":"a-password"}`,
	); code != http.StatusCreated {
		t.Fatalf("create = %d, want 201", code)
	}

	admins, _ := st.ListAdmins()
	var supportID int64
	for _, a := range admins {
		if a.Username == "support" {
			supportID = a.ID
		}
	}
	if supportID == 0 {
		t.Fatal("support was not created")
	}

	// Delete it: the login must survive into the audit row even though the admins
	// row it came from is gone.
	req := httptest.NewRequest("DELETE", "/api/admins/"+strconv.FormatInt(supportID, 10), nil)
	req.Header.Set("X-Current-Password", "a-password")
	req.AddCookie(owner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d, want 200", rec.Code)
	}

	byAction := map[string]model.AdminAudit{}
	for _, row := range auditRows(t, st) {
		byAction[row.Action] = row
	}
	created, ok := byAction[model.AuditAdminCreated]
	if !ok {
		t.Fatal("no admin.created row")
	}
	if created.Target != "support" || created.ActorName != "owner" {
		t.Errorf("created row = target %q by %q, want support by owner",
			created.Target, created.ActorName)
	}
	deleted, ok := byAction[model.AuditAdminDeleted]
	if !ok {
		t.Fatal("no admin.deleted row")
	}
	if deleted.Target != "support" {
		t.Errorf("deleted row target = %q, want the login of the admin that is now gone", deleted.Target)
	}
}

// A failed sign-in is the row an audit log exists for. It has no session, so the
// middleware can't see it — the login handler records it itself.
func TestAuditRecordsSignInsIncludingFailures(t *testing.T) {
	rt, st := rolesTestRouter(t)
	rt.limiter = newLoginLimiter()
	h := rt.panelMux()
	if _, err := st.CreateAdmin("owner", mustHash(t, "a-password"), model.RoleOwner, false); err != nil {
		t.Fatalf("create owner: %v", err)
	}

	login := func(password string) int {
		req := httptest.NewRequest("POST", "/api/login",
			strings.NewReader(`{"username":"owner","password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := login("wrong-password"); code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", code)
	}
	if code := login("a-password"); code != http.StatusOK {
		t.Fatalf("good login = %d, want 200", code)
	}

	var failed, ok bool
	for _, row := range auditRows(t, st) {
		switch row.Action {
		case model.AuditLoginFailed:
			failed = true
			if row.Target != "owner" {
				t.Errorf("failed sign-in target = %q, want the attempted login", row.Target)
			}
		case model.AuditLogin:
			ok = true
		}
	}
	if !failed {
		t.Error("a failed sign-in left no trace")
	}
	if !ok {
		t.Error("a successful sign-in left no trace")
	}
}
