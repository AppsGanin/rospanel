package server

import (
	"strings"
	"testing"
)

// specDocRoutes are the two endpoints deliberately absent from the spec: they ARE
// the spec and the viewer for it, and describing them inside themselves adds
// nothing a reader can act on.
var specDocRoutes = map[string]bool{
	"GET /v1/openapi.json": true,
	"GET /v1/docs":         true,
	// The catch-all that turns an unmatched /v1 path into a JSON 404. Not an
	// operation, so there is nothing for the spec to describe.
	"/": true,
}

// TestAPISpecCoversEveryRoute keeps the published contract honest.
//
// The spec is hand-declared in openapi.go while routes are registered in
// api_v1.go, so nothing but this test stops the two drifting: an endpoint can ship,
// work, and be missing from the spec with no error anywhere. That is exactly what
// had happened to GET /v1/health — reachable, described in docs/api.md, invisible
// to every client generated from the spec.
func TestAPISpecCoversEveryRoute(t *testing.T) {
	rt := &Router{}
	rt.apiHandler() // registration is what fills rt.apiRoutes

	if len(rt.apiRoutes) == 0 {
		t.Fatal("no /v1 routes recorded — the registration helper stopped tracking them")
	}

	declared := map[string]bool{}
	for _, r := range apiSpecRoutes() {
		declared[r.method+" "+r.path] = true
	}

	for _, pattern := range rt.apiRoutes {
		if specDocRoutes[pattern] {
			continue
		}
		if !declared[pattern] {
			t.Errorf("%s is served but has no OpenAPI entry — clients generated from "+
				"the spec cannot see it; add it to apiSpecRoutes() in openapi.go", pattern)
		}
	}
}

// TestSpecDeclaresNothingImaginary is the other direction: a spec entry for a route
// that does not exist sends callers at a 404.
func TestSpecDeclaresNothingImaginary(t *testing.T) {
	rt := &Router{}
	rt.apiHandler()

	served := map[string]bool{}
	for _, p := range rt.apiRoutes {
		served[p] = true
	}

	for _, r := range apiSpecRoutes() {
		pattern := r.method + " " + r.path
		if !served[pattern] {
			t.Errorf("the spec declares %s, but nothing serves it", pattern)
		}
	}
}

// TestSpecPathsAreVersioned guards a smaller foot-gun: a path written without the
// /v1 prefix would generate a spec clients cannot call.
func TestSpecPathsAreVersioned(t *testing.T) {
	for _, r := range apiSpecRoutes() {
		if !strings.HasPrefix(r.path, "/v1/") {
			t.Errorf("spec path %q is not under /v1/", r.path)
		}
	}
}
