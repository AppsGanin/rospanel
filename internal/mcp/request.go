package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Request turns a tool call into the HTTP request that performs it: the method, the
// path with its parameters filled in and its query appended, and the JSON body when
// the operation takes one (nil otherwise).
//
// It lives on the Tool because both transports need it: the stdio client dials the
// panel over the network, while the panel's own MCP endpoint dispatches the same
// request in process. Building it twice is how those two would drift.
func (t Tool) Request(args map[string]any) (method, path string, body []byte, err error) {
	if args == nil {
		args = map[string]any{}
	}
	path, err = fillPath(t.path, args)
	if err != nil {
		return "", "", nil, err
	}
	if q := queryString(t, args); q != "" {
		path += "?" + q
	}
	if body, err = t.body(args); err != nil {
		return "", "", nil, err
	}
	return t.method, path, body, nil
}

// body builds the JSON request body from the arguments.
//
// It accepts the two shapes a model actually produces, not only the one the schema
// asks for. Both were real failures against this panel:
//
//   - `body` as a STRING containing JSON. Marshalling that gives a JSON string where
//     the endpoint wants an object, and the panel answers "invalid request body" —
//     technically correct and useless to everyone involved.
//   - no `body` at all, with the fields spread across the top level, which is what a
//     model does when it reads a flat list of properties and skips the wrapper.
//
// Neither is ambiguous: the panel takes an object, path and query parameters are
// known by name, and anything left over can only be body fields.
func (t Tool) body(args map[string]any) ([]byte, error) {
	if raw, ok := args["body"]; ok && raw != nil {
		if s, isString := raw.(string); isString {
			trimmed := strings.TrimSpace(s)
			if trimmed == "" {
				return nil, nil
			}
			if !json.Valid([]byte(trimmed)) {
				return nil, fmt.Errorf("body was sent as text that is not JSON: %.60s", trimmed)
			}
			return []byte(trimmed), nil
		}
		out, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("encode body: %w", err)
		}
		return out, nil
	}
	if !t.takesBody {
		return nil, nil
	}
	// Gather the strays. A path or query parameter belongs where it already went.
	fields := map[string]any{}
	for k, v := range args {
		if k == "body" || t.HasParam(k) || strings.Contains(t.path, "{"+k+"}") {
			continue
		}
		fields[k] = v
	}
	if len(fields) == 0 {
		return nil, nil
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return out, nil
}

// fillPath substitutes {id}-style path parameters from the arguments.
func fillPath(path string, args map[string]any) (string, error) {
	var b strings.Builder
	for _, seg := range strings.Split(path, "/") {
		if !strings.HasPrefix(seg, "{") {
			if seg != "" {
				b.WriteByte('/')
				b.WriteString(seg)
			}
			continue
		}
		name := strings.Trim(seg, "{}")
		v, ok := args[name]
		if !ok {
			return "", fmt.Errorf("missing required argument %q", name)
		}
		b.WriteByte('/')
		b.WriteString(url.PathEscape(scalar(v)))
	}
	return b.String(), nil
}

// queryString builds the query from the arguments the operation declares, ignoring
// anything else the assistant invented. Path parameters and the body are excluded —
// they travel elsewhere.
func queryString(t Tool, args map[string]any) string {
	props, _ := t.InputSchema["properties"].(map[string]any)
	q := url.Values{}
	for name := range props {
		if name == "body" || strings.Contains(t.path, "{"+name+"}") {
			continue
		}
		v, ok := args[name]
		if !ok || v == nil {
			continue
		}
		q.Set(name, scalar(v))
	}
	return q.Encode()
}

// scalar renders a JSON value as a URL/path component. Numbers arrive from JSON as
// float64, so an id would otherwise become "42.000000" — which the panel rejects
// as an invalid id, and which is a confusing thing to debug from the far side of an
// assistant.
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}
