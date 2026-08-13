package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// protocolVersion is the MCP revision this server implements. A client asking for a
// different one is answered with ours — the handshake is a negotiation, and every
// client in the wild accepts a server that states its own version.
const protocolVersion = "2025-06-18"

// Server answers MCP messages. It is transport-agnostic and stateless: the panel
// builds one per request and hands it the message body (see HandleHTTP).
type Server struct {
	name    string
	version string
	tools   []Tool
	call    func(ctx context.Context, t Tool, args map[string]any) (string, error)
}

// NewServer builds a server over the given tool list. call performs one tool
// invocation and returns the text handed back to the assistant.
func NewServer(name, version string, tools []Tool,
	call func(ctx context.Context, t Tool, args map[string]any) (string, error),
) *Server {
	return &Server{name: name, version: version, tools: tools, call: call}
}

// rpcRequest is one JSON-RPC message. ID is absent on notifications, which must
// never be answered — a reply to one is a protocol error, and some clients drop the
// connection over it.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes used here (the subset from the spec that can happen).
const (
	codeParse         = -32700
	codeInvalidParams = -32602
	codeMethodMissing = -32601
	codeInternal      = -32603
)

// dispatch turns one request into its response, or nil when the message needs no
// answer.
func (s *Server) dispatch(ctx context.Context, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return s.result(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			// Tools only: this server has no resources or prompts to offer, and
			// claiming capabilities it doesn't implement makes a client call them.
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]any{"name": s.name, "version": s.version},
		})
	case "tools/list":
		return s.result(req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		return s.handleCall(ctx, req)
	case "ping":
		return s.result(req.ID, map[string]any{})
	default:
		// Notifications (no id) are silent by definition — including
		// notifications/initialized, which every client sends after the handshake.
		if len(req.ID) == 0 {
			return nil
		}
		return s.errorOf(req.ID, codeMethodMissing, "unknown method "+req.Method)
	}
}

// HandleHTTP answers one JSON-RPC message received over MCP's Streamable HTTP
// transport. It returns the response body and the status to send it with; a nil
// body means the message was a notification, which is acknowledged and not answered.
func (s *Server) HandleHTTP(ctx context.Context, body []byte) ([]byte, int) {
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b, _ := json.Marshal(rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: codeParse, Message: "malformed JSON-RPC message"},
		})
		return b, http.StatusBadRequest
	}
	resp := s.dispatch(ctx, req)
	if resp == nil {
		return nil, http.StatusAccepted
	}
	b, err := json.Marshal(resp)
	if err != nil {
		b, _ = json.Marshal(rpcResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInternal, Message: "marshal: " + err.Error()},
		})
	}
	return b, http.StatusOK
}

func (s *Server) handleCall(ctx context.Context, req rpcRequest) *rpcResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.errorOf(req.ID, codeInvalidParams, "bad params: "+err.Error())
	}
	tool, ok := s.tool(params.Name)
	if !ok {
		// A tool the server doesn't offer is a protocol-level error, not a failed
		// call: the assistant asked for something that was never in the list — most
		// often a mutating one while the server is read-only.
		return s.errorOf(req.ID, codeInvalidParams, "unknown tool "+params.Name)
	}
	text, err := s.call(ctx, tool, params.Arguments)
	if err != nil {
		// A failed CALL is reported as a successful result carrying isError, per the
		// MCP spec: the assistant is meant to read the failure and react to it, which
		// it cannot do with a transport-level error.
		return s.result(req.ID, map[string]any{
			"content": []any{map[string]any{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}
	out := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
	// The panel answers JSON, so hand it over as data as well as text. A client that
	// understands structured results gets fields it can address instead of a blob it
	// has to re-parse; the text stays for the ones that don't.
	if obj, ok := structured(text); ok {
		out["structuredContent"] = obj
	}
	return s.result(req.ID, out)
}

// structured re-reads a tool's answer as a JSON object. Only objects: the spec's
// structuredContent is an object, and the panel's envelope always is one.
func structured(text string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, false
	}
	return obj, true
}

// result builds a success response, or nil for a notification (which must not be
// answered at all).
func (s *Server) result(id json.RawMessage, res any) *rpcResponse {
	if len(id) == 0 {
		return nil
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: res}
}

// errorOf builds an error response, or nil for a notification.
func (s *Server) errorOf(id json.RawMessage, code int, msg string) *rpcResponse {
	if len(id) == 0 {
		return nil
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func (s *Server) tool(name string) (Tool, bool) {
	for _, t := range s.tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}
