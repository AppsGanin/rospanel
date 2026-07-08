package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBuildOpenAPI checks the generated document is well-formed, reflects real
// struct fields, and honours json:"-" (secret fields must never leak into the
// public spec).
func TestBuildOpenAPI(t *testing.T) {
	doc := buildOpenAPI("https://host/apX/v1")

	// Round-trips through JSON (no unserializable values).
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	paths, _ := doc["paths"].(map[string]any)
	if _, ok := paths["/v1/users"]; !ok {
		t.Fatal("missing /v1/users path")
	}
	if _, ok := paths["/v1/billing/orders"]; !ok {
		t.Fatal("missing /v1/billing/orders path")
	}

	comps := doc["components"].(map[string]any)["schemas"].(map[string]any)
	uv, ok := comps["userView"].(map[string]any)
	if !ok {
		t.Fatal("userView schema not registered")
	}
	props := uv["properties"].(map[string]any)
	// Promoted fields from the embedded model.User plus userView's own fields.
	for _, want := range []string{"id", "name", "sub_url", "vless"} {
		if _, ok := props[want]; !ok {
			t.Errorf("userView schema missing promoted field %q", want)
		}
	}
	// json:"-" fields on model.User must be absent.
	for _, banned := range []string{"Password", "SubToken", "password", "sub_token"} {
		if _, ok := props[banned]; ok {
			t.Errorf("userView schema leaks secret field %q", banned)
		}
	}
}

// TestDocsUnauthenticated verifies the spec + Swagger UI are served without a key,
// while an unknown /v1 path without a key is rejected by apiAuth.
func TestDocsUnauthenticated(t *testing.T) {
	rt := &Router{}
	h := rt.apiHandler()

	for _, path := range []string{"/v1/openapi.json", "/v1/docs"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}

	// No key → 401 (apiAuth rejects before touching the manager).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/users", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/users without key = %d, want 401", rec.Code)
	}
}
