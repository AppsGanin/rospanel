// Package mcp exposes the panel's REST API to an AI assistant over the Model
// Context Protocol.
//
// It is a thin translator, not a second API: every tool is one operation from the
// generated OpenAPI document, and calling one is dispatched against the panel's own
// REST surface with the caller's API key. Nothing here can do anything a key holder
// could not do with curl — which is the point, because an assistant is exactly the
// kind of caller whose reach should be bounded by a credential rather than by its
// manners.
//
// Two things are deliberate:
//
//   - The tool list is DERIVED from the OpenAPI spec the panel already generates
//     from its route table, so a new endpoint appears here without anyone
//     remembering to add it, and a removed one disappears.
//   - Write operations are off unless asked for. An assistant that misreads a
//     sentence can delete a user, and "it had a key" is no comfort afterwards.
//
// The panel serves this over HTTPS itself (internal/server/api_v1_mcp.go). There is
// no separate binary to run: an assistant is pointed at a URL.
package mcp

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// Tool is one MCP tool: an operation the assistant may call.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Annotations are the hints a client uses to decide how much ceremony a call
	// deserves — chiefly whether to ask the human first. Without them every tool
	// looks alike, and "delete this user" is presented exactly as gently as "list
	// users". They are hints, not enforcement: the read-only URL and --allow-write
	// are what actually stop a mutation.
	Annotations map[string]any `json:"annotations,omitempty"`

	// method and path are how the call is turned back into an HTTP request;
	// takesBody records whether the operation declares one, so arguments a model put
	// at the top level can be recognised as body fields rather than dropped.
	method    string
	path      string
	takesBody bool
}

// Mutating reports whether calling this tool changes state.
func (t Tool) Mutating() bool { return t.method != "GET" }

// HasParam reports whether the operation declares an argument by this name. Used by
// the caller to fill in a default the spec documents but the assistant omitted.
func (t Tool) HasParam(name string) bool {
	props, _ := t.InputSchema["properties"].(map[string]any)
	_, ok := props[name]
	return ok
}

// BuildTools turns an OpenAPI document into the tool list. allowWrite=false keeps
// only the read operations.
func BuildTools(spec map[string]any, allowWrite bool) []Tool {
	paths, _ := spec["paths"].(map[string]any)
	// The document keeps request/response shapes in components and points at them
	// with $ref. That is correct OpenAPI and useless to an MCP client: it is handed
	// one tool's inputSchema, not the document, so a $ref reads as "a body, contents
	// unknown" — which is exactly what an assistant asked to create a user sees when
	// it cannot name a single field. Resolve them here, once.
	defs := components(spec)
	var out []Tool
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for method, raw := range ops {
			op, _ := raw.(map[string]any)
			if op == nil {
				continue
			}
			m := strings.ToUpper(method)
			if m != "GET" && !allowWrite {
				continue
			}
			// An operation can opt out. Deriving the list from the spec is what keeps
			// it from drifting, but a route whose body is a binary download has no
			// useful reading as a tool result — see api_v1_mcp_result.go.
			if optOut, ok := op["x-mcp"].(bool); ok && !optOut {
				continue
			}
			out = append(out, buildTool(m, path, op, defs))
		}
	}
	// Stable order: an assistant's tool list should not reshuffle between runs, and
	// Go's map iteration would guarantee it does.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildTool renders one operation as a tool.
func buildTool(method, path string, op map[string]any, defs map[string]any) Tool {
	summary, _ := op["summary"].(string)
	declared, _ := op["x-destructive"].(bool)
	t := Tool{
		Name:        toolName(method, path),
		Title:       summary,
		Description: description(method, path, op),
		Annotations: annotations(method, path, summary, declared),
		method:      method,
		path:        path,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
	props, _ := t.InputSchema["properties"].(map[string]any)
	var required []string

	// Path parameters come from the path itself, not only from the spec's parameter
	// list. The panel's generator declares them, but a tool that can't be called
	// because a document omitted one is a worse failure than a duplicate entry —
	// which this isn't, since the loop below overwrites by name.
	for _, seg := range strings.Split(path, "/") {
		if !strings.HasPrefix(seg, "{") {
			continue
		}
		name := strings.Trim(seg, "{}")
		props[name] = map[string]any{"type": "integer", "description": "path parameter"}
		required = append(required, name)
	}

	for _, raw := range asSlice(op["parameters"]) {
		p, _ := raw.(map[string]any)
		if p == nil {
			continue
		}
		name, _ := p["name"].(string)
		if name == "" {
			continue
		}
		schema, _ := p["schema"].(map[string]any)
		if schema == nil {
			schema = map[string]any{"type": "string"}
		}
		entry, _ := inline(schema, defs, nil).(map[string]any)
		if entry == nil {
			entry = map[string]any{"type": "string"}
		}
		if d, ok := p["description"].(string); ok && d != "" {
			entry["description"] = d
		}
		props[name] = entry
		if req, _ := p["required"].(bool); req {
			required = append(required, name)
		}
	}

	// The request body arrives as one object rather than as flattened fields: the
	// schema is already documented that way, and flattening would collide with the
	// path/query names above the moment an endpoint has both.
	if schema := bodySchema(op, defs); schema != nil {
		props["body"] = schema
		required = append(required, "body")
		t.takesBody = true
	}
	if len(required) > 0 {
		// A path parameter that the spec ALSO declares as required would be listed
		// twice — harmless to most clients, invalid to a strict one.
		sort.Strings(required)
		t.InputSchema["required"] = dedupe(required)
	}
	return t
}

// bodySchema pulls the JSON request-body schema out of an operation, with every
// $ref resolved, or nil when the operation takes no body.
func bodySchema(op map[string]any, defs map[string]any) map[string]any {
	body, _ := op["requestBody"].(map[string]any)
	if body == nil {
		return nil
	}
	content, _ := body["content"].(map[string]any)
	json, _ := content["application/json"].(map[string]any)
	schema, _ := json["schema"].(map[string]any)
	if schema == nil {
		return nil
	}
	out, _ := inline(schema, defs, nil).(map[string]any)
	if out == nil {
		return nil
	}
	if _, ok := out["description"]; !ok {
		// Spelled out because a model reading "request body" sometimes sends the
		// document as a string. It is accepted either way (see Tool.body), but asking
		// for the right shape costs one sentence.
		out["description"] = "request body — pass a JSON object, not a string"
	}
	return out
}

// components is the document's schema dictionary, the thing $ref points into.
func components(spec map[string]any) map[string]any {
	comp, _ := spec["components"].(map[string]any)
	defs, _ := comp["schemas"].(map[string]any)
	return defs
}

// inline returns v with every $ref replaced by the schema it names, recursively.
//
// `expanding` is the chain of names currently being resolved: a schema that refers
// to itself (directly or through another) would otherwise recurse forever, so a
// repeat becomes a bare object — the shape is still true, it just stops describing
// itself one level down.
func inline(v any, defs map[string]any, expanding []string) any {
	switch node := v.(type) {
	case map[string]any:
		if name, ok := refName(node); ok {
			if slices.Contains(expanding, name) {
				return map[string]any{"type": "object"}
			}
			target, ok := defs[name].(map[string]any)
			if !ok {
				// A dangling reference: say "some object" rather than hand the client a
				// pointer it cannot follow.
				return map[string]any{"type": "object"}
			}
			return inline(target, defs, append(expanding, name))
		}
		out := make(map[string]any, len(node))
		for k, child := range node {
			out[k] = inline(child, defs, expanding)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			out[i] = inline(child, defs, expanding)
		}
		return out
	default:
		return v
	}
}

// refName pulls the component name out of a {"$ref": "#/components/schemas/X"} node.
func refName(node map[string]any) (string, bool) {
	ref, ok := node["$ref"].(string)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(ref, "#/components/schemas/"), true
}

// toolName is a stable, collision-free name for one operation: the method, then the
// path with its parameters spelled out.
//
//	GET  /v1/users            → get_users
//	GET  /v1/users/{id}       → get_users_by_id
//	POST /v1/users/{id}/reset → post_users_by_id_reset
//
// Mechanical rather than pretty on purpose. A hand-written vocabulary
// ("list_users", "reset_traffic") reads better right up to the first collision or
// the first endpoint nobody renamed, and the assistant is told what each one does
// by the description anyway.
func toolName(method, path string) string {
	parts := []string{strings.ToLower(method)}
	for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		switch {
		case seg == "" || seg == "v1":
			continue
		case strings.HasPrefix(seg, "{"):
			parts = append(parts, "by_"+strings.Trim(seg, "{}"))
		default:
			parts = append(parts, sanitize(seg))
		}
	}
	name := strings.Join(parts, "_")
	// MCP caps tool names at 64 characters; nothing here comes close, but a future
	// deeply-nested route should be truncated rather than rejected by the client.
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// sanitize keeps a path segment to the characters a tool name may contain.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// destructiveVerbs are the path/summary words that mark a call as one a human would
// want to be asked about. DELETE is obvious; the rest are POSTs that throw something
// away or interrupt service, and reading them as ordinary writes is how an assistant
// ends up resetting somebody's traffic counters because the sentence was ambiguous.
var destructiveVerbs = []string{
	"delete", "reset", "cancel", "unbind", "revoke", "restart", "migrate", "rotate",
}

// annotations describes a tool's disposition to the client.
//
// declared is the operation's own x-destructive flag. It ADDS to the word match below —
// a route can mark itself destructive when its wording gives no hint, which is the case
// that matters:
// the match reads English prose, so it calls a routing rewrite an ordinary update (none
// of its words appear in the list) while flagging a rollback only because its summary
// happens to say "restarts" — reword that sentence and the warning disappears with it.
func annotations(method, path, summary string, declared bool) map[string]any {
	readOnly := method == http.MethodGet
	destructive := false
	if !readOnly {
		hay := strings.ToLower(path + " " + summary)
		destructive = declared || method == http.MethodDelete
		for _, v := range destructiveVerbs {
			if strings.Contains(hay, v) {
				destructive = true
				break
			}
		}
	}
	return map[string]any{
		"readOnlyHint": readOnly,
		// Only meaningful for a tool that writes; the spec's default for those is
		// "assume destructive", so saying so explicitly is what earns the calmer
		// treatment for the ones that merely create or update.
		"destructiveHint": destructive,
		"idempotentHint": method == http.MethodGet || method == http.MethodPut ||
			method == http.MethodPatch || method == http.MethodDelete,
		// Every one of these talks to the operator's own panel and nothing else.
		"openWorldHint": false,
	}
}

// description is what the assistant reads to decide whether a tool fits. It carries
// the summary plus the raw method and path, which is what an operator debugging a
// transcript actually wants to see.
func description(method, path string, op map[string]any) string {
	summary, _ := op["summary"].(string)
	if summary == "" {
		summary = method + " " + path
	}
	return fmt.Sprintf("%s (%s %s)", summary, method, path)
}

// dedupe removes repeats from a sorted slice, in place.
func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}
