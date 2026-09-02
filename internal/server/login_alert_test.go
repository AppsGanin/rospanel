package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
)

// loginFrom is tryLogin with a client address and a User-Agent, which are what the
// alert is about.
func loginFrom(t *testing.T, rt *Router, body, addr, ua string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = addr
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	return w.Code
}

// A sign-in from an address the admin has not used alerts; the same address again
// does not; a different one does. A failed attempt is not a sign-in and alerts
// nobody — the lockout is that story's ending.
func TestLoginAlertsOnceFromANewAddress(t *testing.T) {
	rt, st := rolesTestRouter(t)
	hash, err := auth.HashPassword("a-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAdmin("owner", hash, model.RoleOwner, false); err != nil {
		t.Fatal(err)
	}
	alerts := make(chan core.LoginAlert, 8)
	rt.mgr.SetAdminLoginNotifier(func(a core.LoginAlert) { alerts <- a })
	body := `{"username":"owner","password":"a-password"}`
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"

	if code := loginFrom(t, rt, body, "203.0.113.7:4444", ua); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	a := <-alerts
	if a.Username != "owner" || a.IP != "203.0.113.7" || a.Client != "Chrome · macOS" || a.AdminID == 0 {
		t.Fatalf("alert carries the wrong facts: %+v", a)
	}

	if code := loginFrom(t, rt, body, "203.0.113.7:5555", ua); code != http.StatusOK {
		t.Fatalf("second login: %d", code)
	}
	if code := loginFrom(t, rt, `{"username":"owner","password":"wrong"}`, "198.51.100.1:1", ua); code != http.StatusUnauthorized {
		t.Fatalf("bad password: %d", code)
	}
	if code := loginFrom(t, rt, body, "198.51.100.2:1", ua); code != http.StatusOK {
		t.Fatalf("third login: %d", code)
	}
	a = <-alerts
	if a.IP != "198.51.100.2" {
		t.Fatalf("the next alert must be the new address, got %+v", a)
	}
	select {
	case extra := <-alerts:
		t.Fatalf("an alert for a known address or a failed attempt: %+v", extra)
	default:
	}
}
