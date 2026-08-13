package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rpc posts one JSON-RPC message to the MCP endpoint and returns the raw reply.
func rpc(t *testing.T, h http.Handler, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rpcResult decodes the "result" object of a reply.
func rpcResult(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if out.Error != nil {
		t.Fatalf("rpc error: %s", out.Error.Message)
	}
	return out.Result
}

// The whole point of the remote transport: an assistant is given a URL and nothing
// else, and gets a working handshake, a tool list and a real answer out of it.
func TestMCPOverHTTP(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	if _, err := mgr.CreateUser(t.Context(), "mcp-user", 0, 0); err != nil {
		t.Fatalf("create user: %v", err)
	}
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key

	init := rpcResult(t, rpc(t, h, url, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if init["protocolVersion"] == "" {
		t.Errorf("handshake carries no protocol version: %v", init)
	}
	if info, _ := init["serverInfo"].(map[string]any); info["name"] != "rospanel" {
		t.Errorf("serverInfo = %v", init["serverInfo"])
	}

	list := rpcResult(t, rpc(t, h, url, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	tools, _ := list["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools offered")
	}

	// A real call: the endpoint dispatches it against the panel's own API in
	// process, so the answer is the same JSON an external client would get.
	call := rpcResult(t, rpc(t, h, url,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_users","arguments":{}}}`))
	content, _ := call["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("empty tool result: %v", call)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "mcp-user") {
		t.Errorf("tool result doesn't carry the panel's data: %s", text)
	}
	// The call asked for no window, so the dispatcher supplied one — this is the wiring
	// the unit test in api_v1_mcp_result_test.go can't see.
	var page struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("tool result is not JSON: %v", err)
	}
	if page.Meta["limit"] != float64(mcpListDefaultLimit) {
		t.Errorf("unwindowed list answered with limit %v, want %d", page.Meta["limit"], mcpListDefaultLimit)
	}

	// A notification is acknowledged and not answered — replying to one is itself a
	// protocol error.
	if rec := rpc(t, h, url, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); rec.Code != http.StatusAccepted {
		t.Errorf("notification: status %d, want 202", rec.Code)
	}
	if rec := rpc(t, h, url, `not json at all`); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed body: status %d, want 400", rec.Code)
	}
}

// The short URL must not be able to change anything, even though the key inside it
// could: that is the difference between the two addresses.
func TestMCPReadOnlyURLHidesMutations(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "victim", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	base, key := apiFixture(t, h, st)

	names := func(url string) map[string]bool {
		list := rpcResult(t, rpc(t, h, url, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		tools, _ := list["tools"].([]any)
		out := map[string]bool{}
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			name, _ := tool["name"].(string)
			out[name] = true
		}
		return out
	}
	ro := names(base + "/v1/mcp/" + key)
	rw := names(base + "/v1/mcp/" + key + "/write")
	if ro["delete_users_by_id"] {
		t.Error("the read-only URL offers a delete tool")
	}
	if !rw["delete_users_by_id"] {
		t.Error("the write URL is missing the delete tool")
	}

	// Asking for it anyway must not work — the gate is the tool list AND the call.
	rec := rpc(t, h, base+"/v1/mcp/"+key,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"delete_users_by_id","arguments":{"id":`+uid(u.ID)+`}}}`)
	if !strings.Contains(rec.Body.String(), "unknown tool") {
		t.Errorf("read-only URL answered a delete call with: %s", rec.Body.String())
	}
	if _, err := st.GetUser(u.ID); err != nil {
		t.Errorf("the user was deleted through the read-only URL: %v", err)
	}
}

// The URL is the credential, so a wrong one must behave like every other bad
// credential on this surface — and must not hint that the path itself was right.
func TestMCPRejectsBadKeys(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)

	if rec := rpc(t, h, base+"/v1/mcp/rp_not-a-real-key",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad key: status %d, want 401", rec.Code)
	}
	// A revoked key stops working immediately.
	keys, err := st.ListAPIKeys()
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	for _, k := range keys {
		if err := st.RevokeAPIKey(k.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
	}
	if rec := rpc(t, h, base+"/v1/mcp/"+key,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked key: status %d, want 401", rec.Code)
	}
}

// A browser-based client preflights before it can authenticate, and a GET is a
// request for a stream this endpoint doesn't offer.
func TestMCPTransportNiceties(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	url := base + "/v1/mcp/" + key

	req := httptest.NewRequest(http.MethodOptions, url, nil)
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight: status %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("preflight carries no CORS headers")
	}

	get := httptest.NewRequest(http.MethodGet, url, nil)
	get.RemoteAddr = testClientIP + ":40000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", rec.Code)
	}
}

func TestMCPURLs(t *testing.T) {
	ro, rw := MCPURLs("https://vpn.example.com/apiseg", "rp_abc")
	if ro != "https://vpn.example.com/apiseg/v1/mcp/rp_abc" {
		t.Errorf("read-only URL = %q", ro)
	}
	if rw != ro+"/write" {
		t.Errorf("write URL = %q", rw)
	}
	if ro, rw := MCPURLs("", "rp_abc"); ro != "" || rw != "" {
		t.Errorf("with the API off = %q / %q, want empty", ro, rw)
	}
}
