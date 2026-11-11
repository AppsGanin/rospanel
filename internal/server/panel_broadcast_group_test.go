package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The broadcast screen lists the groups, previews and creates a broadcast narrowed
// to one, and refuses a malformed group id.
func TestBroadcastGroupHTTP(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)
	u, err := rt.mgr.CreateUser(t.Context(), "vip1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateGroup("VIP", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupMembers(g.ID, []int64{u.ID}); err != nil {
		t.Fatal(err)
	}
	// A way to deliver, so a broadcast can be created; nothing is sent here.
	if _, err := st.CreateWebhook("https://hooks.example/b", []string{model.WebhookBroadcastSent}, true); err != nil {
		t.Fatal(err)
	}
	do := func(req *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do(httptest.NewRequest("GET", "/api/broadcasts/groups", nil))
	var groups []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Members int    `json:"members"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &groups) != nil ||
		len(groups) != 1 || groups[0].ID != g.ID || groups[0].Name != "VIP" || groups[0].Members != 1 {
		t.Fatalf("groups: %d %s", rec.Code, rec.Body.String())
	}

	if rec := do(httptest.NewRequest("GET", "/api/broadcasts/audience?audience=all&group_id=abc", nil)); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed group id: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(httptest.NewRequest("GET", "/api/broadcasts/audience?audience=all&group_id=999", nil)); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing group: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(httptest.NewRequest("GET", "/api/broadcasts/audience?audience=all&group_id="+strconv.FormatInt(g.ID, 10), nil))
	var reach struct {
		HookUsers int `json:"hook_users"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reach) != nil || reach.HookUsers != 1 {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	payload, _ := json.Marshal(map[string]any{"text": "vip", "audience": "all", "group_id": g.ID,
		"buttons": []any{}, "scheduled_at": time.Now().Add(time.Hour).Unix()})
	_ = mw.WriteField("payload", string(payload))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/broadcasts", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = do(req)
	var b model.Broadcast
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &b) != nil || b.GroupID != g.ID || b.GroupName != "VIP" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
}
