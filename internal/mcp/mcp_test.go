package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// spec is a miniature OpenAPI document with one read and one write operation —
// enough to exercise naming, schemas and the read-only gate without pulling the
// whole panel in.
func spec() map[string]any {
	return map[string]any{
		"paths": map[string]any{
			"/v1/users": map[string]any{
				"get": map[string]any{
					"summary": "List users",
					"parameters": []any{
						map[string]any{
							"name": "limit", "in": "query",
							"schema": map[string]any{"type": "integer"},
						},
					},
				},
				"post": map[string]any{
					"summary": "Create a user",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
							},
						},
					},
				},
			},
			"/v1/users/{id}/reset": map[string]any{
				"post": map[string]any{"summary": "Reset traffic"},
			},
		},
	}
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestBuildToolsNamesAndSchemas(t *testing.T) {
	tools := BuildTools(spec(), true)
	got := strings.Join(toolNames(tools), " ")
	for _, want := range []string{"get_users", "post_users", "post_users_by_id_reset"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing tool %q, have %s", want, got)
		}
	}
	for _, tool := range tools {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		switch tool.Name {
		case "get_users":
			if _, ok := props["limit"]; !ok {
				t.Error("get_users has no limit parameter")
			}
		case "post_users":
			if _, ok := props["body"]; !ok {
				t.Error("post_users takes no body")
			}
		case "post_users_by_id_reset":
			if _, ok := props["id"]; !ok {
				t.Error("the path parameter didn't become an argument")
			}
		}
	}
}

// A client decides whether to ask the human by these hints, so a delete must not
// look like a list.
func TestToolAnnotations(t *testing.T) {
	byName := map[string]Tool{}
	for _, tool := range BuildTools(spec(), true) {
		byName[tool.Name] = tool
	}
	read := byName["get_users"].Annotations
	if read["readOnlyHint"] != true || read["destructiveHint"] != false {
		t.Errorf("a list tool is annotated %v", read)
	}
	if byName["get_users"].Title == "" {
		t.Error("no human title on the tool")
	}
	// "reset" is a POST that throws data away — the word is what marks it, since the
	// method alone says only "this writes".
	if got := byName["post_users_by_id_reset"].Annotations["destructiveHint"]; got != true {
		t.Errorf("resetting traffic is annotated destructive=%v, want true", got)
	}
	// Creating a user writes but destroys nothing.
	if got := byName["post_users"].Annotations["destructiveHint"]; got != false {
		t.Errorf("creating a user is annotated destructive=%v, want false", got)
	}
	if got := byName["get_users"].Annotations["openWorldHint"]; got != false {
		t.Errorf("openWorldHint = %v, want false — these tools only reach the panel", got)
	}
}

// The result carries the panel's JSON as data as well as text, so a client that
// understands structured results doesn't have to re-parse a blob.
func TestToolResultCarriesStructuredContent(t *testing.T) {
	srv := NewServer("rospanel", "test", BuildTools(spec(), false),
		func(context.Context, Tool, map[string]any) (string, error) {
			return `{"data":[{"id":1,"name":"alice"}],"meta":{"total":1}}`, nil
		})
	body, status := srv.HandleHTTP(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_users","arguments":{}}}`))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	var out struct {
		Result struct {
			Content           []map[string]any `json:"content"`
			StructuredContent map[string]any   `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(out.Result.Content) == 0 {
		t.Error("the text half of the result is gone — older clients read that one")
	}
	meta, _ := out.Result.StructuredContent["meta"].(map[string]any)
	if meta["total"] != float64(1) {
		t.Errorf("structuredContent = %v, want the panel's own envelope", out.Result.StructuredContent)
	}

	// A non-JSON answer stays text-only rather than being forced into a shape.
	plain := NewServer("rospanel", "test", BuildTools(spec(), false),
		func(context.Context, Tool, map[string]any) (string, error) { return "(no content)", nil })
	body, _ = plain.HandleHTTP(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_users","arguments":{}}}`))
	if strings.Contains(string(body), "structuredContent") {
		t.Errorf("a plain-text answer was given a structured half: %s", body)
	}
}

// The read-only default is the safety property of this whole feature, so it gets a
// test of its own: nothing that changes state may be in the list.
func TestBuildToolsReadOnlyHidesMutations(t *testing.T) {
	for _, tool := range BuildTools(spec(), false) {
		if tool.Mutating() {
			t.Errorf("read-only mode offered %q", tool.Name)
		}
	}
	if len(BuildTools(spec(), false)) == 0 {
		t.Error("read-only mode offered nothing at all")
	}
}

// Turning a tool call into an HTTP request is where the arguments an assistant
// invented meet the panel's routes: a path parameter has to land in the path, a
// query parameter in the query, and neither in the body.
func TestToolRequest(t *testing.T) {
	byName := map[string]Tool{}
	for _, tool := range BuildTools(spec(), true) {
		byName[tool.Name] = tool
	}

	method, path, body, err := byName["get_users"].Request(map[string]any{"limit": float64(5)})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if method != "GET" || path != "/v1/users?limit=5" {
		t.Errorf("%s %s, want GET /v1/users?limit=5", method, path)
	}
	if body != nil {
		t.Errorf("a body was built for a GET: %s", body)
	}

	// A JSON number must land in the path as an integer: 42, not 42.000000, which the
	// panel would reject as an invalid id.
	_, path, body, err = byName["post_users_by_id_reset"].Request(map[string]any{"id": float64(42)})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if path != "/v1/users/42/reset" {
		t.Errorf("path = %q, want /v1/users/42/reset", path)
	}
	if body != nil {
		t.Errorf("a body was built for an operation that takes none: %s", body)
	}

	// The body travels as itself, and only for operations that declare one.
	_, _, body, err = byName["post_users"].Request(map[string]any{"body": map[string]any{"name": "alice"}})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if string(body) != `{"name":"alice"}` {
		t.Errorf("body = %s", body)
	}

	// A model that sends the body as a STRING of JSON — which is what actually
	// happened against the live panel, and what the endpoint answered "invalid
	// request body" to.
	_, _, body, err = byName["post_users"].Request(map[string]any{
		"body": `{"name":"МЦП","plan_id":3,"device_limit":3}`,
	})
	if err != nil {
		t.Fatalf("stringified body: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("stringified body did not become JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "МЦП" || decoded["plan_id"] != float64(3) {
		t.Errorf("body = %s, want the object the string described", body)
	}

	// A model that skips the wrapper and puts the fields at the top level.
	_, _, body, err = byName["post_users"].Request(map[string]any{"name": "flat", "plan_id": float64(2)})
	if err != nil {
		t.Fatalf("flat body: %v", err)
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("flat args did not become a body: %v (%s)", err, body)
	}
	if decoded["name"] != "flat" {
		t.Errorf("body = %s, want the loose fields gathered up", body)
	}

	// Path and query parameters are not body fields — they already went where they
	// belong, and repeating them in the body would be a second, contradictory value.
	_, _, body, err = byName["get_users"].Request(map[string]any{"limit": float64(5)})
	if err != nil {
		t.Fatalf("query only: %v", err)
	}
	if body != nil {
		t.Errorf("a query parameter leaked into the body: %s", body)
	}

	// Text that is not JSON is a real mistake and is reported as one rather than
	// being posted for the panel to reject.
	if _, _, _, err := byName["post_users"].Request(map[string]any{"body": "just words"}); err == nil {
		t.Error("a non-JSON string body was accepted")
	}

	// A missing path argument fails before anything is sent.
	if _, _, _, err := byName["post_users_by_id_reset"].Request(nil); err == nil {
		t.Error("a call with no id built a request anyway")
	}
}

// The handshake and one call, end to end over the transport the panel serves.
func TestHandleHTTPHandshakeAndCall(t *testing.T) {
	called := ""
	srv := NewServer("rospanel", "test", BuildTools(spec(), false),
		func(_ context.Context, tool Tool, _ map[string]any) (string, error) {
			called = tool.Name
			return `{"data":[]}`, nil
		})
	ctx := context.Background()

	body, status := srv.HandleHTTP(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if status != http.StatusOK {
		t.Fatalf("initialize: status %d", status)
	}
	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &initResp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if initResp.Result.ProtocolVersion == "" || initResp.Result.ServerInfo.Name != "rospanel" {
		t.Errorf("initialize reply = %s", body)
	}

	// A notification is acknowledged, never answered.
	if body, status := srv.HandleHTTP(ctx, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); body != nil || status != http.StatusAccepted {
		t.Errorf("notification: body %s status %d, want none and 202", body, status)
	}

	if _, status := srv.HandleHTTP(ctx,
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_users","arguments":{}}}`)); status != http.StatusOK {
		t.Fatalf("call: status %d", status)
	}
	if called != "get_users" {
		t.Errorf("tool called = %q", called)
	}

	// A tool the server never offered — here a write, with the server read-only — is
	// a protocol error rather than a call that quietly fails.
	body, _ = srv.HandleHTTP(ctx,
		[]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"post_users","arguments":{}}}`))
	if !strings.Contains(string(body), "unknown tool") {
		t.Errorf("calling a hidden write tool answered %s", body)
	}

	if _, status := srv.HandleHTTP(ctx, []byte(`not json`)); status != http.StatusBadRequest {
		t.Errorf("malformed message: status %d, want 400", status)
	}
}
