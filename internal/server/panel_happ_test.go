package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/happ"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestPanelHappEndpoints(t *testing.T) {
	r, st := rolesTestRouter(t)
	h := r.panelMux()
	cookie := signIn(t, st, "admin", model.RoleAdmin, false)

	// 1. GET /api/happ/subscriptions (initially empty)
	req := httptest.NewRequest(http.MethodGet, "/api/happ/subscriptions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/happ/subscriptions failed: %d, body=%s", rec.Code, rec.Body.String())
	}

	// 2. Directly populate DB with a subscription and a node
	subID, err := st.CreateHappSubscription("TestSub", "https://example.com/sub")
	if err != nil {
		t.Fatalf("CreateHappSubscription: %v", err)
	}
	nodes := []happ.Node{
		{
			IdentityKey: happ.IdentityKeyFor(subID, "vless", "nl.example.com", 443, "u1"),
			Name:        "NL-Server",
			Protocol:    "vless",
			Host:        "nl.example.com",
			Port:        443,
			URI:         "vless://u1@nl.example.com:443#NL-Server",
		},
	}
	_, _, err = st.UpsertHappNodesFull(subID, nodes)
	if err != nil {
		t.Fatalf("UpsertHappNodesFull: %v", err)
	}

	// 3. GET /api/happ/subscriptions (now 1)
	req = httptest.NewRequest(http.MethodGet, "/api/happ/subscriptions", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/happ/subscriptions failed: %d", rec.Code)
	}

	// 4. GET /api/happ/nodes (returns 1 node)
	req = httptest.NewRequest(http.MethodGet, "/api/happ/nodes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/happ/nodes failed: %d", rec.Code)
	}
	bodyStr := rec.Body.String()
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"protocol":"vless"`)) {
		t.Fatalf("expected lowercase JSON key \"protocol\":\"vless\", got: %s", bodyStr)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"subscription_id":`)) {
		t.Fatalf("expected snake_case JSON key \"subscription_id\", got: %s", bodyStr)
	}
	var nodeResp []happ.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &nodeResp); err != nil || len(nodeResp) != 1 {
		t.Fatalf("unexpected nodes response: %v, len=%d", err, len(nodeResp))
	}
	nodeID := nodeResp[0].ID

	// 5. POST /api/happ/nodes/{id}/enabled
	enablePayload, _ := json.Marshal(map[string]bool{"enabled": false})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/happ/nodes/%d/enabled", nodeID), bytes.NewReader(enablePayload))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /api/happ/nodes/{id}/enabled failed: %d, body=%s", rec.Code, rec.Body.String())
	}

	// 6. DELETE /api/happ/nodes/{id}
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/happ/nodes/%d", nodeID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/happ/nodes/{id} failed: %d", rec.Code)
	}

	// 7. DELETE /api/happ/subscriptions/{id}
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/happ/subscriptions/%d", subID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/happ/subscriptions/{id} failed: %d", rec.Code)
	}
}
