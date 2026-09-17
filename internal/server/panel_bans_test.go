package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// An admin sees which of a user's addresses can be banned, bans one, sees it in the
// ban list and marked on the user, and lifts it; an operator can do none of it, and no
// one can ban the address they are calling from.
func TestBanRoutes(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)
	operator := signIn(t, st, "support", model.RoleOperator, false)
	u, err := st.CreateUser("alice", "uuid-a", "pw-a", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if err := st.AddConnections([]store.ConnectionHit{
		{UserID: u.ID, IP: "203.0.113.7", SeenAt: now, Hits: 2},
		{UserID: u.ID, IP: "10.66.0.8", SeenAt: now, Hits: 1},
	}); err != nil {
		t.Fatal(err)
	}

	do := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:40000"
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	conns := func(c *http.Cookie) map[string]map[string]any {
		t.Helper()
		rec := do("GET", fmt.Sprintf("/api/users/%d/connections", u.ID), "", c)
		var rows []map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rows) != nil {
			t.Fatalf("connections = %d %s", rec.Code, rec.Body)
		}
		out := map[string]map[string]any{}
		for _, r := range rows {
			out[r["ip"].(string)] = r
		}
		return out
	}

	byIP := conns(admin)
	if byIP["203.0.113.7"]["can_ban"] != true || byIP["10.66.0.8"]["can_ban"] != false {
		t.Errorf("can_ban for an admin: %v", byIP)
	}
	if byIP["203.0.113.7"]["approx_seconds"] != float64(90) {
		t.Errorf("approx_seconds = %v, want 90 for two sightings", byIP["203.0.113.7"]["approx_seconds"])
	}
	if conns(operator)["203.0.113.7"]["can_ban"] != false {
		t.Error("an operator is offered the ban button")
	}

	ban := fmt.Sprintf(`{"ip":"203.0.113.7","user_id":%d}`, u.ID)
	if rec := do("POST", "/api/security/bans", ban, operator); rec.Code != http.StatusForbidden {
		t.Errorf("operator ban = %d, want 403", rec.Code)
	}
	if rec := do("POST", "/api/security/bans", `{"ip":"192.0.2.1"}`, admin); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "err.banSelf") {
		t.Errorf("banning one's own address = %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/security/bans", ban, admin); rec.Code != http.StatusOK {
		t.Fatalf("ban = %d %s", rec.Code, rec.Body)
	}
	// The journal says who banned which address, as its own action.
	if rows := auditRows(t, st); len(rows) == 0 || rows[0].Action != model.AuditIPBanned || rows[0].Target != "203.0.113.7" {
		t.Errorf("audit after the ban: %+v", rows)
	}
	if byIP = conns(admin); byIP["203.0.113.7"]["banned"] != true || byIP["203.0.113.7"]["can_ban"] != false {
		t.Errorf("after the ban: %v", byIP["203.0.113.7"])
	}
	rec := do("GET", "/api/security/bans", "", admin)
	var list struct {
		Bans []model.Ban `json:"bans"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil ||
		len(list.Bans) != 1 || list.Bans[0].IP != "203.0.113.7" || list.Bans[0].UserName != "alice" {
		t.Errorf("ban list = %d %s", rec.Code, rec.Body)
	}

	if rec := do("POST", "/api/security/unban", `{"ip":"203.0.113.7"}`, admin); rec.Code != http.StatusOK {
		t.Errorf("unban = %d %s", rec.Code, rec.Body)
	}
	if rows := auditRows(t, st); len(rows) == 0 || rows[0].Action != model.AuditIPUnbanned || rows[0].Target != "203.0.113.7" {
		t.Errorf("audit after the unban: %+v", rows)
	}
	if rec := do("POST", "/api/security/unban", `{"ip":"203.0.113.7"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("second unban = %d, want 404", rec.Code)
	}
	if conns(admin)["203.0.113.7"]["banned"] != false {
		t.Error("still marked banned after the unban")
	}
}
