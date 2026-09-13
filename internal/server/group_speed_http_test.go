package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A group's speed cap over the panel routes: set on create, kept by an edit that
// does not mention it (an older panel posts name and grants only), changed by one
// that does — and shown on the member's group refs, which the user card reads.
func TestPanelGroupSpeedLimit(t *testing.T) {
	rt, st := rolesTestRouter(t)
	c := signIn(t, st, "owner", model.RoleOwner, false)

	req := httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(`{"name":"slow","grants":[],"speed_limit":2000}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var g model.Group
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil || g.SpeedLimit != 2000 {
		t.Fatalf("created group: %+v %v", g, err)
	}

	edit := fmt.Sprintf("/api/groups/%d", g.ID)
	if code, errCode := send(t, rt, http.MethodPost, edit, `{"name":"slow","grants":[]}`, c, nil); code != http.StatusOK {
		t.Fatalf("edit without speed: %d %s", code, errCode)
	}
	if got, _ := st.GetGroup(g.ID); got.SpeedLimit != 2000 {
		t.Errorf("an edit that did not mention the speed changed it to %d", got.SpeedLimit)
	}
	if code, errCode := send(t, rt, http.MethodPost, edit, `{"name":"slow","grants":[],"speed_limit":5000}`, c, nil); code != http.StatusOK {
		t.Fatalf("edit with speed: %d %s", code, errCode)
	}
	if got, _ := st.GetGroup(g.ID); got.SpeedLimit != 5000 {
		t.Errorf("speed after edit: %d, want 5000", got.SpeedLimit)
	}
	if code, errCode := send(t, rt, http.MethodPost, edit, `{"name":"slow","grants":[],"speed_limit":-1}`, c, nil); code != http.StatusBadRequest {
		t.Errorf("negative speed: %d %s — want 400", code, errCode)
	}

	u, err := rt.mgr.CreateUser(t.Context(), "member", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.mgr.SetUserGroups(u.ID, []int64{g.ID}); err != nil {
		t.Fatal(err)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	listReq.AddCookie(c)
	lw := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(lw, listReq)
	var users []struct {
		ID     int64            `json:"id"`
		Groups []model.GroupRef `json:"groups"`
	}
	if err := json.Unmarshal(lw.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	for _, x := range users {
		if x.ID == u.ID && (len(x.Groups) != 1 || x.Groups[0].SpeedLimit != 5000) {
			t.Errorf("the user list's group refs: %+v, want the group's 5000 kbit/s", x.Groups)
		}
	}
}
