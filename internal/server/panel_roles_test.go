package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// The authorization boundary, exercised through the real panel mux with real
// sessions: what each role may call is the whole point of the feature, and it is the
// one thing that must not regress quietly. Xray never starts here — NewSupervisor
// with no binary generates config and nothing else — so the handlers run against a
// real store and a manager that simply has nothing to supervise.

func rolesTestRouter(t *testing.T) (*Router, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	mgr := core.New(st, sup, xray.Options{}, core.TLSPaths{}, dir)
	return &Router{mgr: mgr, dataDir: dir}, st
}

// signIn creates an admin with the given role and returns a request cookie for a
// live session of theirs.
func signIn(t *testing.T, st *store.Store, name, role string, gated bool) *http.Cookie {
	t.Helper()
	hash, err := auth.HashPassword("a-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id, err := st.CreateAdmin(name, hash, role, gated)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	token, err := st.CreateSession(id, time.Hour)
	if err != nil {
		t.Fatalf("session for %s: %v", name, err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func call(h http.Handler, method, path string, c *http.Cookie) int {
	req := httptest.NewRequest(method, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRouteTiersByRole(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	owner := signIn(t, st, "owner", model.RoleOwner, false)
	admin := signIn(t, st, "admin", model.RoleAdmin, false)
	operator := signIn(t, st, "support", model.RoleOperator, false)

	// One representative route per tier. 403 — not 401 — is the required refusal:
	// the session is valid, so the SPA must show "недостаточно прав" rather than
	// bounce the admin to the login screen.
	const denied = http.StatusForbidden
	cases := []struct {
		name, method, path       string
		wantOwner, wantAdmin     int
		wantOperator, wantNoAuth int
	}{
		{
			name: "own account (any role)", method: "GET", path: "/api/me",
			wantOwner: 200, wantAdmin: 200, wantOperator: 200, wantNoAuth: 401,
		},
		{
			name: "end users (operator and up)", method: "GET", path: "/api/users",
			wantOwner: 200, wantAdmin: 200, wantOperator: 200, wantNoAuth: 401,
		},
		{
			name: "journal (operator and up)", method: "GET", path: "/api/events",
			wantOwner: 200, wantAdmin: 200, wantOperator: 200, wantNoAuth: 401,
		},
		{
			name: "panel settings (admin and up)", method: "GET", path: "/api/settings",
			wantOwner: 200, wantAdmin: 200, wantOperator: denied, wantNoAuth: 401,
		},
		{
			name: "API keys (admin and up)", method: "GET", path: "/api/apikeys",
			wantOwner: 200, wantAdmin: 200, wantOperator: denied, wantNoAuth: 401,
		},
		{
			name: "admin roster (owner only)", method: "GET", path: "/api/admins",
			wantOwner: 200, wantAdmin: denied, wantOperator: denied, wantNoAuth: 401,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, who := range []struct {
				role string
				c    *http.Cookie
				want int
			}{
				{"owner", owner, tc.wantOwner},
				{"admin", admin, tc.wantAdmin},
				{"operator", operator, tc.wantOperator},
				{"anonymous", nil, tc.wantNoAuth},
			} {
				if got := call(h, tc.method, tc.path, who.c); got != who.want {
					t.Errorf("%s %s as %s = %d, want %d",
						tc.method, tc.path, who.role, got, who.want)
				}
			}
		})
	}
}

// A role the ladder doesn't recognize — a corrupt row, a hand-edited database —
// must clear nothing. The failure mode to avoid is the opposite one, where an
// unknown role sails past a check that only knows how to say "not operator".
func TestUnknownRoleClearsNothing(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	c := signIn(t, st, "weird", model.RoleOperator, false)
	admins, err := st.ListAdmins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := st.SetAdminRole(admins[0].ID, "superuser"); err != nil {
		t.Fatalf("corrupt role: %v", err)
	}

	for _, path := range []string{"/api/users", "/api/settings", "/api/admins"} {
		if got := call(h, "GET", path, c); got != http.StatusForbidden {
			t.Errorf("GET %s with an unknown role = %d, want 403", path, got)
		}
	}
	// Their own account still resolves — they can still change their password and
	// sign out; they just cannot *do* anything.
	if got := call(h, "GET", "/api/me", c); got != http.StatusOK {
		t.Errorf("GET /api/me with an unknown role = %d, want 200", got)
	}
}

// A colleague who has not yet replaced the password the owner handed them is pinned
// to the password screen — everything else is closed, whatever their role says.
func TestGatedAdminIsPinnedToThePasswordScreen(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	c := signIn(t, st, "colleague", model.RoleAdmin, true)

	for _, path := range []string{"/api/users", "/api/settings", "/api/events"} {
		if got := call(h, "GET", path, c); got != http.StatusForbidden {
			t.Errorf("GET %s while gated = %d, want 403", path, got)
		}
	}
	// The two doors left open are the ones that lead out of the gate.
	if got := call(h, "GET", "/api/me", c); got != http.StatusOK {
		t.Errorf("GET /api/me while gated = %d, want 200", got)
	}
	if got := call(h, "GET", "/api/backup/info", c); got != http.StatusOK {
		t.Errorf("GET /api/backup/info while gated = %d, want 200", got)
	}
}

// Every roster mutation re-asks the owner for their own password: a session cookie
// alone must not be enough to mint a second admin, which would turn a stolen cookie
// into permanent access.
func TestRosterMutationsRequireStepUp(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	owner := signIn(t, st, "owner", model.RoleOwner, false)
	// Note the roster does not use verifyStepUp, which waives the password check
	// while the first-run wizard is still running: it re-verifies unconditionally.
	// Adding an admin is not part of guided setup, and it is exactly what a stolen
	// cookie would want to do.
	post := func(body string) int {
		req := httptest.NewRequest("POST", "/api/admins", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(owner)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	const wrong = `{"username":"support","password":"temp-password","role":"operator","current_password":"not-it"}`
	if got := post(wrong); got != http.StatusForbidden {
		t.Fatalf("create with a wrong step-up password = %d, want 403", got)
	}
	admins, _ := st.ListAdmins()
	if len(admins) != 1 {
		t.Fatalf("roster = %d after a refused create, want 1", len(admins))
	}

	const right = `{"username":"support","password":"temp-password","role":"operator","current_password":"a-password"}`
	if got := post(right); got != http.StatusCreated {
		t.Fatalf("create with the correct step-up password = %d, want 201", got)
	}
	if admins, _ = st.ListAdmins(); len(admins) != 2 {
		t.Fatalf("roster = %d after a successful create, want 2", len(admins))
	}
}
