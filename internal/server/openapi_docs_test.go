package server

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// The docs site ships a static copy of the spec (docs/openapi.json), so a route
// change must refresh it. This test is the reminder: `go generate ./internal/server`
// rewrites the file. Compare bytes, not parsed JSON, because the generator's
// formatting is what the docs page serves.
func TestDocsOpenAPIIsCurrent(t *testing.T) {
	want, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatalf("docs/openapi.json: %v", err)
	}
	got, err := json.MarshalIndent(OpenAPISpec("https://panel.example.com"), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if !bytes.Equal(want, got) {
		t.Fatalf("docs/openapi.json is stale: run `go generate ./internal/server` and commit the result")
	}
}
