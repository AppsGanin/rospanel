package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/core"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestPanelNodeRentalEndpoints(t *testing.T) {
	r, st := rolesTestRouter(t)
	h := r.panelMux()
	cookie := signIn(t, st, "admin", model.RoleAdmin, false)

	// Create a node
	node, err := st.CreateNode("NL Node", "nl.example.com", "test")
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}

	// 1. GET /api/nodes/{id}/rental
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/nodes/%d/rental", node.ID), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/nodes/{id}/rental status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rentalResp nodeRentalResp
	if err := json.Unmarshal(rec.Body.Bytes(), &rentalResp); err != nil {
		t.Fatalf("decode rental resp: %v", err)
	}
	if rentalResp.Settings.ShareEnabled {
		t.Errorf("want ShareEnabled = false initially")
	}

	// 2. POST /api/nodes/{id}/rental (update settings)
	updatePayload := model.NodeRentalSettings{
		ShareEnabled:      true,
		ShareQuotaPercent: 75,
		ShareSpeedLimit:   50000,
	}
	raw, _ := json.Marshal(updatePayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/nodes/%d/rental", node.ID), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/{id}/rental status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// 3. POST /api/nodes/{id}/rental/share-link
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/nodes/%d/rental/share-link", node.ID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/{id}/rental/share-link status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var linkResp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &linkResp)
	shareLink := linkResp["share_link"]
	if shareLink == "" {
		t.Fatalf("want non-empty share link in response")
	}

	// 4. POST /api/nodes/import-rented
	importBody := importRentedReq{
		ShareLink: shareLink,
		Name:      "Rented NL",
	}
	raw, _ = json.Marshal(importBody)
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/import-rented", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/nodes/import-rented status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rentedNode model.Node
	_ = json.Unmarshal(rec.Body.Bytes(), &rentedNode)
	if !rentedNode.IsRented || rentedNode.RentOwnerNodeID != node.ID {
		t.Errorf("unexpected rented node: %+v", rentedNode)
	}

	// 5. GET /api/nodes/{id}/reserved-ports
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/nodes/%d/reserved-ports", node.ID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/nodes/{id}/reserved-ports status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// 6. DELETE /api/nodes/{id}/tenants/{tenantId}
	_ = st.RegisterNodeTenant(node.ID, model.NodeTenant{TenantID: "tenant_to_delete", Name: "Demo"})
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/nodes/%d/tenants/tenant_to_delete", node.ID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/nodes/{id}/tenants/{tenantId} status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// 7. Verify security: critical endpoints must reject mutations on rentedNode
	// a. Routing
	routingPayload := `{"routing":{"rules":[]},"warp_enabled":false}`
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/nodes/%d/routing", rentedNode.ID), bytes.NewReader([]byte(routingPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /api/nodes/{rentedId}/routing want 403 Forbidden, got %d", rec.Code)
	}

	// b. DNS
	dnsPayload := `{"xray_dns":"1.1.1.1"}`
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/nodes/%d/dns", rentedNode.ID), bytes.NewReader([]byte(dnsPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /api/nodes/{rentedId}/dns want 403 Forbidden, got %d", rec.Code)
	}

	// c. ACME / TLS
	acmePayload := `{"target":"rented.com","email":"a@b.com","provider":"letsencrypt"}`
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/nodes/%d/tls", rentedNode.ID), bytes.NewReader([]byte(acmePayload)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /api/nodes/{rentedId}/tls want 403 Forbidden, got %d", rec.Code)
	}

	// d. Xray Restart
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/nodes/%d/xray-restart", rentedNode.ID), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /api/nodes/{rentedId}/xray-restart want 403 Forbidden, got %d", rec.Code)
	}

	// e. Node list shows rented node as online and joined
	req = httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/nodes status = %d", rec.Code)
	}
	var nodesList struct {
		Nodes []core.NodeView `json:"nodes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &nodesList)
	var foundRented *core.NodeView
	for i := range nodesList.Nodes {
		if nodesList.Nodes[i].ID == rentedNode.ID {
			foundRented = &nodesList.Nodes[i]
			break
		}
	}
	if foundRented == nil {
		t.Fatalf("rented node not found in /api/nodes response")
	}
	if !foundRented.Joined || !foundRented.Online || !foundRented.XrayRunning {
		t.Errorf("rented node view unexpected status: Joined=%v, Online=%v, XrayRunning=%v",
			foundRented.Joined, foundRented.Online, foundRented.XrayRunning)
	}
}

