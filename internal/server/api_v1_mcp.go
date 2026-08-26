package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/mcp"
	"github.com/Shu1t3/rospanel-shu1t3/internal/version"
)

// The panel's own MCP endpoint, so an assistant can be pointed at a URL instead of
// being handed a binary to run. Same protocol as `rospanel mcp`, same tool list —
// only the transport differs: JSON-RPC over HTTPS (MCP's "Streamable HTTP") rather
// than over a pipe.
//
// Two things are unusual and deliberate:
//
//   - The key rides in the PATH, not in a header. The dialogs that add a remote MCP
//     server take a URL and nothing else, so a header-only credential cannot be
//     entered at all. The panel already builds every public surface this way — the
//     subscription token, the node segment, the payment-webhook secret — and the URL
//     is therefore exactly as secret as the key inside it. Treat it like a password.
//   - Read-only unless the URL says otherwise. `…/mcp/<key>` cannot change anything,
//     even though the key behind it could; `…/mcp/<key>/write` opens the mutating
//     half. An assistant acting on a misread sentence should not be able to delete a
//     customer because the operator pasted the shorter URL.
const (
	mcpPathPrefix  = "/v1/mcp/"
	mcpWriteSuffix = "/write"
)

// maxMCPResult bounds what one tool call hands back.
//
// The unit that matters is the model's context, not disk: half a megabyte of JSON is
// already on the order of a hundred thousand tokens, which is most of a large
// context window spent on one call. It sits well above the window below (50 users
// with their share links is ~100 KB), so in normal use it never bites; what it stops
// is `limit=0` on a panel with thousands of rows silently filling the conversation.
//
// Measure before changing it. A user row is ~2 KB — the REST maximum of a thousand
// of them is 2 MB and cannot be made to fit by raising a ceiling.
const maxMCPResult = 512 << 10

// mcpListDefaultLimit is the window a list call gets when the assistant asked for
// none. The REST default of 100 suits an integration writing rows into a database;
// it does not suit a caller that pays for every row in context. An explicit limit
// still wins — `limit<=0`, meaning everything, included: a caller that means it can
// still say so, and the ceiling above is what catches them if they were wrong.
const mcpListDefaultLimit = 50

// maxMCPBody bounds one JSON-RPC message. Requests are tiny; this only stops a
// stranger who guessed the URL from streaming into memory.
const maxMCPBody = 1 << 20

// handleMCP answers one MCP request. Registered outside the bearer-authenticated
// mux because the credential is in the path — the check below is the same store
// lookup and the same per-IP lockout apiAuth uses.
func (rt *Router) handleMCP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, mcpPathPrefix)
	allowWrite := false
	if s, ok := strings.CutSuffix(rest, mcpWriteSuffix); ok {
		rest, allowWrite = s, true
	}
	key, err := url.PathUnescape(strings.Trim(rest, "/"))
	if err != nil || key == "" || strings.Contains(key, "/") {
		rt.currentDecoy().ServeHTTP(w, r) // a malformed URL is not a caller we owe an answer
		return
	}

	// CORS before auth: a browser-based client sends a preflight it cannot
	// authenticate, and answering that costs nothing (the URL is the credential).
	mcpCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		// The transport allows a server-initiated SSE stream on GET; this one has
		// nothing to push, and the spec says to answer 405 rather than hold the
		// connection open.
		w.Header().Set("Allow", "POST, OPTIONS")
		writeAPIErr(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"this MCP endpoint accepts POST only")
		return
	}

	ak, ok := rt.mcpAuth(w, r, key)
	if !ok {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "unreadable body")
		return
	}
	srv := mcp.NewServer("rospanel", version.Version,
		mcp.BuildTools(OpenAPISpec(apiBaseURL(r, rt.currentAPIPath())), allowWrite),
		rt.mcpDispatch(key))
	resp, status := srv.HandleHTTP(r.Context(), body)
	if resp == nil {
		w.WriteHeader(status) // a notification: acknowledged, nothing to answer
		return
	}
	slog.Debug("mcp: request served", "key", ak.Name, "write", allowWrite, "bytes", len(resp))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(resp)
}

// mcpAuth verifies the key from the path, with the same lockout the bearer surface
// uses so guessing the URL is no cheaper than guessing a key.
func (rt *Router) mcpAuth(w http.ResponseWriter, r *http.Request, key string) (*apiKeyIdentity, bool) {
	ip := clientIP(r)
	if rt.apiKeys.blocked(ip, "") {
		writeAPIErr(w, http.StatusTooManyRequests, "too_many_requests",
			"too many invalid keys, try again later")
		return nil, false
	}
	ak, err := rt.mgr.Store().LookupAPIKey(key)
	if err != nil {
		writeAPIErr(w, http.StatusInternalServerError, "internal", "authentication failed")
		return nil, false
	}
	if ak == nil {
		rt.apiKeys.fail(ip, "")
		slog.Warn("mcp: invalid key in URL", "ip", ip)
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked API key")
		return nil, false
	}
	rt.apiKeys.success(ip, "")
	return &apiKeyIdentity{Name: ak.Name}, true
}

// apiKeyIdentity is the little the MCP path needs to know about the caller.
type apiKeyIdentity struct{ Name string }

// mcpDispatch runs a tool call against the panel's own REST surface, in process.
//
// It goes through the very same handler an external client reaches — including its
// authentication and its audit trail — rather than reimplementing the calls here.
// That is what keeps the two from drifting: an endpoint added to /v1 is reachable
// from the assistant on the next build, with the same permissions and the same
// error shapes, and nothing about it has to be written twice.
func (rt *Router) mcpDispatch(key string) func(context.Context, mcp.Tool, map[string]any) (string, error) {
	return func(ctx context.Context, t mcp.Tool, args map[string]any) (string, error) {
		method, path, body, err := t.Request(withMCPWindow(t, args))
		if err != nil {
			return "", err
		}
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, path, rdr)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := &captureWriter{header: http.Header{}}
		rt.api.ServeHTTP(rec, req)
		// The lists are windowed server-side (see api_v1_paging.go), so this is the
		// backstop rather than the plan: a single answer must not be able to fill the
		// assistant's context.
		text := shrinkMCPResult(strings.TrimSpace(rec.body.String()))
		if rec.status >= 400 {
			return "", fmt.Errorf("panel answered %d: %s", rec.status, text)
		}
		if text == "" {
			return "(no content)", nil
		}
		return text, nil
	}
}

// captureWriter collects an in-process response. Deliberately not httptest's
// recorder: that package exists for tests, and this is the serving path.
type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (c *captureWriter) Header() http.Header { return c.header }

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(b)
}

func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

// mcpCORS lets a browser-based client talk to this endpoint. Permissive on purpose:
// the URL carries the credential, so an origin restriction would protect nothing
// that the URL itself doesn't already decide.
func mcpCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers",
		"content-type, authorization, mcp-protocol-version, mcp-session-id, last-event-id")
	h.Set("Access-Control-Max-Age", "600")
}

// currentAPIPath is the live external-API segment (the tool list's base URL).
func (rt *Router) currentAPIPath() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.apiPath
}

// MCPURLs are the two addresses an operator pastes into an assistant, for the API
// settings page. Empty when the external API is switched off.
func MCPURLs(base, key string) (readOnly, readWrite string) {
	if base == "" || key == "" {
		return "", ""
	}
	ro := strings.TrimRight(base, "/") + mcpPathPrefix + url.PathEscape(key)
	return ro, ro + mcpWriteSuffix
}
