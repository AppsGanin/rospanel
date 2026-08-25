package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestPanelSessionsEndpoints(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()

	ownerCookie := signIn(t, st, "owner", model.RoleOwner, false)
	admin1Cookie := signIn(t, st, "admin1", model.RoleAdmin, false)
	admin2Cookie := signIn(t, st, "admin2", model.RoleAdmin, false)
	op1Cookie := signIn(t, st, "operator1", model.RoleOperator, false)

	admins, err := st.ListAdmins()
	if err != nil {
		t.Fatalf("list admins: %v", err)
	}
	var ownerID, admin1ID, admin2ID, op1ID int64
	for _, a := range admins {
		switch a.Username {
		case "owner":
			ownerID = a.ID
		case "admin1":
			admin1ID = a.ID
		case "admin2":
			admin2ID = a.ID
		case "operator1":
			op1ID = a.ID
		}
	}

	// Create second session for admin1
	admin1Tok2, _ := st.CreateSession(admin1ID, time.Hour, "10.0.0.2", "Admin1Phone")
	admin1Hash2, _ := st.TokenHash(admin1Tok2)

	// Create second session for op1
	op1Tok2, _ := st.CreateSession(op1ID, time.Hour, "10.0.0.3", "Op1Tablet")
	op1Hash2, _ := st.TokenHash(op1Tok2)

	// 1. GET /api/account/sessions
	req := httptest.NewRequest("GET", "/api/account/sessions", nil)
	req.AddCookie(admin1Cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/account/sessions = %d, want 200", rec.Code)
	}
	var mySess struct {
		Sessions []model.AdminSession `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mySess); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(mySess.Sessions) != 2 {
		t.Fatalf("admin1 sessions count = %d, want 2", len(mySess.Sessions))
	}
	hasCurrent := false
	for _, s := range mySess.Sessions {
		if s.IsCurrent {
			hasCurrent = true
		}
	}
	if !hasCurrent {
		t.Error("none of the sessions marked as is_current")
	}

	// 2. DELETE /api/account/sessions/{hash} (admin1 revokes their second session)
	req = httptest.NewRequest("DELETE", "/api/account/sessions/"+admin1Hash2, nil)
	req.AddCookie(admin1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/account/sessions/{hash} = %d, want 200", rec.Code)
	}
	if _, ok := st.LookupSession(admin1Tok2); ok {
		t.Error("revoked session still resolves")
	}

	// 3. Admin1 accessing operator sessions: GET /api/admins/{id}/sessions
	req = httptest.NewRequest("GET", "/api/admins/"+string(rune('0'+op1ID))+"/sessions", nil)
	req = httptest.NewRequest("GET", "/api/admins/"+jsonNumber(op1ID)+"/sessions", nil)
	req.AddCookie(admin1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Admin GET op sessions = %d, want 200", rec.Code)
	}

	// 4. Admin1 CANNOT view Admin2 sessions (400/403)
	req = httptest.NewRequest("GET", "/api/admins/"+jsonNumber(admin2ID)+"/sessions", nil)
	req.AddCookie(admin1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest { // invalidCode maps to 400 with err.cannotViewAdminSessions
		t.Fatalf("Admin GET admin2 sessions = %d, want 400 error", rec.Code)
	}

	// 5. Admin1 CANNOT delete Admin2 session
	admin2Hash, _ := st.TokenHash(admin2Cookie.Value)
	req = httptest.NewRequest("DELETE", "/api/admins/"+jsonNumber(admin2ID)+"/sessions/"+admin2Hash, nil)
	req.AddCookie(admin1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Admin DELETE admin2 session = %d, want 400 error", rec.Code)
	}

	// Admin1 CANNOT delete Owner session
	ownerHash, _ := st.TokenHash(ownerCookie.Value)
	req = httptest.NewRequest("DELETE", "/api/admins/"+jsonNumber(ownerID)+"/sessions/"+ownerHash, nil)
	req.AddCookie(admin1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Admin DELETE owner session = %d, want 400 error", rec.Code)
	}

	// 6. Admin1 CAN delete Operator session
	req = httptest.NewRequest("DELETE", "/api/admins/"+jsonNumber(op1ID)+"/sessions/"+op1Hash2, nil)
	req.AddCookie(admin1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Admin DELETE op session = %d, want 200", rec.Code)
	}
	if _, ok := st.LookupSession(op1Tok2); ok {
		t.Error("operator session 2 should be deleted")
	}

	// 7. Operator CANNOT delete other operator/admin sessions
	req = httptest.NewRequest("DELETE", "/api/admins/"+jsonNumber(admin1ID)+"/sessions", nil)
	req.AddCookie(op1Cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
		t.Fatalf("Operator DELETE admin sessions = %d, want 400/403", rec.Code)
	}

	// 8. Owner CAN delete any session
	req = httptest.NewRequest("DELETE", "/api/admins/"+jsonNumber(admin2ID)+"/sessions/"+admin2Hash, nil)
	req.AddCookie(ownerCookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Owner DELETE admin2 session = %d, want 200", rec.Code)
	}
	if _, ok := st.LookupSession(admin2Cookie.Value); ok {
		t.Error("admin2 session should be deleted by owner")
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
