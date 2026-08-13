// Package mcp_test is external on purpose: it reaches for internal/server (to build
// the panel's real OpenAPI document), and internal/server imports internal/mcp for
// its own MCP endpoint. An in-package test importing server would be an import
// cycle; an external one is allowed to close that loop.
package mcp_test

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/mcp"
	"github.com/AppsGanin/rospanel/internal/server"
)

// The panel's real spec must produce the tools an operator expects to be able to
// use, and must keep the destructive half behind the flag. Generated from the same
// document the API serves, so this also fails if a route ships without an OpenAPI
// entry.
func TestRealSpecProducesUsableTools(t *testing.T) {
	spec := server.OpenAPISpec("https://panel.example/api")

	names := func(allowWrite bool) map[string]bool {
		out := map[string]bool{}
		for _, tool := range mcp.BuildTools(spec, allowWrite) {
			out[tool.Name] = true
		}
		return out
	}
	ro, rw := names(false), names(true)

	for _, want := range []string{"get_users", "get_users_by_id_devices", "get_metrics", "get_summary"} {
		if !ro[want] {
			t.Errorf("read-only mode does not offer %q", want)
		}
	}
	for _, mutating := range []string{"post_users", "delete_users_by_id", "post_users_by_id_devices_unbind"} {
		if ro[mutating] {
			t.Errorf("read-only mode offers %q", mutating)
		}
		if !rw[mutating] {
			t.Errorf("%q is missing even with writes enabled", mutating)
		}
	}
}

// An assistant is handed one tool's inputSchema and never the document it came
// from, so a $ref in it is a field list the assistant cannot read — which is
// exactly what "create a user" looked like: a body of unknown shape.
func TestBodySchemasCarryTheirFields(t *testing.T) {
	spec := server.OpenAPISpec("https://panel.example/api")
	var create mcp.Tool
	for _, tool := range mcp.BuildTools(spec, true) {
		if tool.Name == "post_users" {
			create = tool
		}
	}
	props, _ := create.InputSchema["properties"].(map[string]any)
	body, _ := props["body"].(map[string]any)
	if body == nil {
		t.Fatal("post_users takes no body")
	}
	if _, dangling := body["$ref"]; dangling {
		t.Fatalf("the body is still a reference the client cannot follow: %v", body)
	}
	fields, _ := body["properties"].(map[string]any)
	for _, want := range []string{"name", "data_limit", "expire_at", "device_limit", "speed_limit", "plan_id"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("field %q is not described to the assistant; got %v", want, keysOf(fields))
		}
	}

	// Nothing anywhere in the tool list may still be a reference: a client resolves
	// none of them.
	for _, tool := range mcp.BuildTools(spec, true) {
		if containsRef(tool.InputSchema) {
			t.Errorf("tool %q carries an unresolved $ref", tool.Name)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsRef(v any) bool {
	switch node := v.(type) {
	case map[string]any:
		if _, ok := node["$ref"]; ok {
			return true
		}
		for _, child := range node {
			if containsRef(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if containsRef(child) {
				return true
			}
		}
	}
	return false
}
