package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPICreateUserValidationAndConsistency(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)

	postUser := func(payload map[string]any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, base+"/v1/users", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Invalid plan_id (non-existent)
	rec := postUser(map[string]any{
		"name":    "bad_plan_user",
		"plan_id": 99999,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-existent plan, got %d: %s", rec.Code, rec.Body.String())
	}
	// Verify no user was created
	users, err := st.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	for _, u := range users {
		if u.Name == "bad_plan_user" {
			t.Fatalf("user was created despite invalid plan_id!")
		}
	}

	// 2. Invalid group_id (non-existent)
	rec = postUser(map[string]any{
		"name":      "bad_group_user",
		"group_ids": []int64{88888},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-existent group, got %d: %s", rec.Code, rec.Body.String())
	}
	users, err = st.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	for _, u := range users {
		if u.Name == "bad_group_user" {
			t.Fatalf("user was created despite invalid group_id!")
		}
	}

	// 3. Valid user creation
	rec = postUser(map[string]any{
		"name": "good_user",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid user, got %d: %s", rec.Code, rec.Body.String())
	}
	users, err = st.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var found bool
	for _, u := range users {
		if u.Name == "good_user" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected good_user to be present in store")
	}
}
