package server

import (
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/version"
)

// The OpenAPI document is generated from the code, not hand-written: component
// schemas are reflected from the very Go structs the handlers decode and encode
// (so the data shapes can't drift), and the path table below mirrors apiMux one
// line per route. The spec is served live at GET /v1/openapi.json and rendered by
// Swagger UI at GET /v1/docs — both without a key (the unguessable API path is the
// obscurity layer; the spec reveals structure, not secrets), so a browser can load
// the docs and the operator pastes a key into "Authorize" to try calls.

// oaParam is a query or path parameter description for a route.
type oaParam struct {
	name, typ, desc string
	required        bool
}

// oaRoute declares one operation for the generated spec. resp is the Go type of
// the payload inside the {"data": ...} envelope (nil ⇒ a free-form object); list
// wraps it in an array; meta adds the pagination block.
type oaRoute struct {
	method, path, tag, summary string
	query                      []oaParam
	req                        reflect.Type // request body type, nil if none
	resp                       reflect.Type // response data type, nil ⇒ generic object
	list                       bool
	meta                       bool
	status                     int  // success status; 0 ⇒ 200
	noAuth                     bool // key-free route; overrides the document-wide bearerAuth
}

// oaHealthResp is what GET /v1/health answers. Named rather than an inline map so
// the generated spec reflects its real shape — the same reason request bodies are
// named types here.
type oaHealthResp struct {
	Status string `json:"status"` // "ok"
}

// oaOrderResp / oaAffectedResp document the two non-model JSON responses so the
// spec types them precisely (they mirror the maps the handlers write).
type oaOrderResp struct {
	Order   *model.PaymentOrder `json:"order"`
	PayURL  string              `json:"pay_url,omitempty"` // hosted provider URL (when a provider is set)
	Message string              `json:"message,omitempty"` // manual-payment instructions (no provider)
}

// oaProviderResp is one enabled payment method returned by GET /v1/billing/providers.
type oaProviderResp struct {
	Key   string `json:"key"`   // provider id usable as `provider` on create-order
	Label string `json:"label"` // human-readable name
}
type oaAffectedResp struct {
	Affected int `json:"affected"`
}

func t(v any) reflect.Type { return reflect.TypeOf(v) }

// apiSpecRoutes is the single source for the generated paths. It is kept in the
// same order and shape as apiMux so the two stay easy to diff by eye.
func apiSpecRoutes() []oaRoute {
	return []oaRoute{
		{method: "GET", path: "/v1/users", tag: "Users", summary: "List users",
			query: []oaParam{
				{name: "status", typ: "string", desc: "active | disabled | expired | limited | device_limited"},
				{name: "search", typ: "string", desc: "substring match on the user name"},
				{name: "limit", typ: "integer", desc: "page size (<=0 = all from offset)"},
				{name: "offset", typ: "integer", desc: "number of users to skip"},
			},
			resp: t(userView{}), list: true, meta: true},
		{method: "POST", path: "/v1/users", tag: "Users", summary: "Create a user",
			req: t(apiCreateUserReq{}), resp: t(userView{}), status: 201},
		{method: "POST", path: "/v1/users/bulk", tag: "Users", summary: "Apply one action to many users",
			req: t(apiBulkReq{}), resp: t(oaAffectedResp{})},
		{method: "GET", path: "/v1/users/{id}", tag: "Users", summary: "Get a user",
			resp: t(userView{})},
		{method: "PATCH", path: "/v1/users/{id}", tag: "Users", summary: "Update a user",
			req: t(apiPatchUserReq{}), resp: t(userView{})},
		{method: "DELETE", path: "/v1/users/{id}", tag: "Users", summary: "Delete a user"},
		{method: "POST", path: "/v1/users/{id}/reset", tag: "Users", summary: "Reset traffic counters",
			resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/reset-period", tag: "Users", summary: "Set auto-reset period",
			req: t(apiResetPeriodReq{}), resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/rotate-sub", tag: "Users", summary: "Issue a new subscription URL",
			resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/plan", tag: "Users", summary: "Apply a tariff plan",
			req: t(apiApplyPlanReq{}), resp: t(userView{})},
		{method: "GET", path: "/v1/users/{id}/connections", tag: "Users", summary: "List the user's source IPs",
			resp: t(model.Connection{}), list: true},

		{method: "GET", path: "/v1/billing/providers", tag: "Billing", summary: "List enabled payment providers",
			resp: t(oaProviderResp{}), list: true},
		{method: "GET", path: "/v1/billing/plans", tag: "Billing", summary: "List tariff plans",
			query: []oaParam{{name: "include_disabled", typ: "boolean", desc: "include disabled plans"}},
			resp:  t(model.TariffPlan{}), list: true},
		{method: "POST", path: "/v1/billing/plans", tag: "Billing", summary: "Create or update a plan",
			req: t(model.TariffPlan{}), resp: t(model.TariffPlan{})},
		{method: "DELETE", path: "/v1/billing/plans/{id}", tag: "Billing", summary: "Delete a plan"},
		{method: "GET", path: "/v1/billing/orders", tag: "Billing", summary: "List payment orders",
			query: []oaParam{{name: "status", typ: "string", desc: "pending | paid | cancelled"}},
			resp:  t(model.PaymentOrder{}), list: true},
		{method: "POST", path: "/v1/billing/orders", tag: "Billing", summary: "Open a payment order",
			req: t(apiCreateOrderReq{}), resp: t(oaOrderResp{}), status: 201},
		{method: "POST", path: "/v1/billing/orders/{id}/confirm", tag: "Billing", summary: "Mark an order paid"},
		{method: "POST", path: "/v1/billing/orders/{id}/cancel", tag: "Billing", summary: "Cancel an order"},

		{method: "GET", path: "/v1/stats/series", tag: "Stats", summary: "Daily traffic points",
			query: []oaParam{
				{name: "user_id", typ: "integer", desc: "restrict to one user (omit for panel-wide)"},
				{name: "from", typ: "string", desc: "YYYY-MM-DD"},
				{name: "to", typ: "string", desc: "YYYY-MM-DD"},
			},
			resp: t(model.DailyPoint{}), list: true},
		{method: "GET", path: "/v1/stats/nodes", tag: "Stats", summary: "Traffic split by server",
			query: []oaParam{
				{name: "user_id", typ: "integer", desc: "restrict to one user (omit for panel-wide)"},
				{name: "from", typ: "string", desc: "YYYY-MM-DD"},
				{name: "to", typ: "string", desc: "YYYY-MM-DD"},
			},
			resp: t(core.NodeTraffic{}), list: true},
		{method: "GET", path: "/v1/stats/users", tag: "Stats", summary: "Per-user traffic totals",
			query: []oaParam{
				{name: "from", typ: "string", desc: "YYYY-MM-DD"},
				{name: "to", typ: "string", desc: "YYYY-MM-DD"},
			},
			resp: t(model.UserTotal{}), list: true},

		{method: "GET", path: "/v1/health", tag: "Monitoring", summary: "API reachability check",
			resp: t(oaHealthResp{})},
		{method: "GET", path: "/v1/summary", tag: "Monitoring", summary: "Panel summary",
			resp: t(core.Summary{})},
		{method: "GET", path: "/v1/system", tag: "Monitoring", summary: "Live system metrics",
			resp: t(core.SystemStatus{})},
		{method: "GET", path: "/v1/health/report", tag: "Monitoring", summary: "Self-diagnostics",
			resp: t(core.HealthReport{})},
		{method: "GET", path: "/v1/healthz", tag: "Monitoring", noAuth: true,
			summary: "Liveness probe (no key; 503 when Xray is down)",
			resp:    t(healthzResp{})},

		{method: "GET", path: "/v1/nodes", tag: "Nodes", summary: "List nodes (local server is node 0)",
			resp: t(core.NodeView{}), list: true},
		{method: "POST", path: "/v1/nodes", tag: "Nodes", summary: "Register a node (returns the install command)",
			req: t(apiCreateNodeReq{}), resp: t(oaNodeCreateResp{}), status: 201},
		{method: "GET", path: "/v1/nodes/{id}", tag: "Nodes", summary: "Get a node",
			resp: t(model.Node{})},
		{method: "PATCH", path: "/v1/nodes/{id}", tag: "Nodes", summary: "Edit a node (name, host, protocol/routing/DNS overrides, WARP/Opera egress)",
			req: t(apiPatchNodeReq{}), resp: t(oaOKResp{})},
		{method: "DELETE", path: "/v1/nodes/{id}", tag: "Nodes", summary: "Delete a node"},
		{method: "POST", path: "/v1/nodes/{id}/enabled", tag: "Nodes", summary: "Enable or disable a node",
			req: t(apiSetNodeEnabledReq{}), resp: t(oaOKResp{})},
		{method: "POST", path: "/v1/nodes/{id}/regen-join", tag: "Nodes", summary: "Issue a fresh install command",
			resp: t(oaNodeCreateResp{})},
		{method: "POST", path: "/v1/nodes/{id}/update", tag: "Nodes", summary: "Ask a node to self-update to the latest release",
			resp: t(oaOKResp{})},
		{method: "POST", path: "/v1/nodes/update-all", tag: "Nodes", summary: "Ask every connected node to self-update",
			resp: t(oaNodeCountResp{})},
	}
}

// oaNodeCountResp types the update-all response.
type oaNodeCountResp struct {
	Nodes int `json:"nodes"`
}

// oaNodeCreateResp / oaOKResp type the node responses for the spec.
type oaNodeCreateResp struct {
	ID             int64  `json:"id"`
	JoinToken      string `json:"join_token"`
	InstallCommand string `json:"install_command"`
}
type oaOKResp struct {
	OK bool `json:"ok"`
}

// healthzResp types the key-free liveness payload for the spec.
type healthzResp struct {
	Status        string `json:"status"` // "ok" | "degraded"
	Xray          string `json:"xray"`   // "running" | "down"
	XrayStartedAt int64  `json:"xray_started_at"`
}

// buildOpenAPI assembles the full OpenAPI 3.0 document for the given server URL.
func buildOpenAPI(serverURL string) map[string]any {
	schemas := map[string]any{
		"ErrorResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"error": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code":    map[string]any{"type": "string"},
						"message": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	paths := map[string]any{}
	for _, rt := range apiSpecRoutes() {
		item, _ := paths[rt.path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[rt.path] = item
		}
		item[strings.ToLower(rt.method)] = buildOperation(rt, schemas)
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "RosPanel API",
			"version":     version.Version,
			"description": "External REST API for managing RosPanel users, billing and stats.",
		},
		"servers": []any{map[string]any{"url": serverURL}},
		"tags": []any{
			map[string]any{"name": "Users"},
			map[string]any{"name": "Billing"},
			map[string]any{"name": "Stats"},
			map[string]any{"name": "Monitoring"},
			map[string]any{"name": "Nodes"},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
			},
			"schemas": schemas,
		},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"paths":    paths,
	}
}

// buildOperation renders one route into an OpenAPI operation object, registering
// any referenced component schemas into schemas.
func buildOperation(r oaRoute, schemas map[string]any) map[string]any {
	op := map[string]any{
		"summary": r.summary,
		"tags":    []any{r.tag},
	}
	// An empty security list opts this operation out of the document-wide bearerAuth,
	// so Swagger UI doesn't demand a key for a route that never wanted one.
	if r.noAuth {
		op["security"] = []any{}
	}

	var params []any
	// A {id} path segment is always an integer path parameter.
	if strings.Contains(r.path, "{id}") {
		params = append(params, map[string]any{
			"name": "id", "in": "path", "required": true,
			"schema": map[string]any{"type": "integer", "format": "int64"},
		})
	}
	for _, q := range r.query {
		params = append(params, map[string]any{
			"name": q.name, "in": "query", "required": q.required,
			"description": q.desc,
			"schema":      map[string]any{"type": q.typ},
		})
	}
	if len(params) > 0 {
		op["parameters"] = params
	}

	if r.req != nil {
		op["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{"schema": schemaFor(r.req, schemas)},
			},
		}
	}

	// data payload schema inside the {"data": ...} envelope.
	var dataSchema map[string]any
	if r.resp == nil {
		dataSchema = map[string]any{"type": "object"}
	} else if r.list {
		dataSchema = map[string]any{"type": "array", "items": schemaFor(r.resp, schemas)}
	} else {
		dataSchema = schemaFor(r.resp, schemas)
	}
	envProps := map[string]any{"data": dataSchema}
	if r.meta {
		envProps["meta"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"total":  map[string]any{"type": "integer"},
				"offset": map[string]any{"type": "integer"},
				"limit":  map[string]any{"type": "integer"},
			},
		}
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	op["responses"] = map[string]any{
		itoa(status): map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "properties": envProps},
				},
			},
		},
		"default": map[string]any{
			"description": "Error",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
				},
			},
		},
	}
	return op
}

var timeType = reflect.TypeOf(time.Time{})

// schemaFor returns the JSON-Schema fragment for a Go type. Named structs are
// registered as reusable components and returned as a $ref; everything else is
// inlined.
func schemaFor(rt reflect.Type, schemas map[string]any) map[string]any {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch rt.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s := map[string]any{"type": "integer"}
		if rt.Kind() == reflect.Int64 || rt.Kind() == reflect.Uint64 {
			s["format"] = "int64"
		}
		return s
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaFor(rt.Elem(), schemas)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(rt.Elem(), schemas)}
	case reflect.Interface:
		return map[string]any{} // any
	case reflect.Struct:
		name := rt.Name()
		if name == "" {
			return structSchema(rt, schemas) // anonymous — inline
		}
		if _, ok := schemas[name]; !ok {
			schemas[name] = map[string]any{} // reserve to break recursion cycles
			schemas[name] = structSchema(rt, schemas)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	default:
		return map[string]any{}
	}
}

// structSchema builds an object schema, promoting the fields of anonymous
// embedded structs exactly as encoding/json flattens them into the parent object.
func structSchema(rt reflect.Type, schemas map[string]any) map[string]any {
	props := map[string]any{}
	var required []string
	collectFields(rt, props, &required, schemas)
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		reqAny := make([]any, len(required))
		for i, v := range required {
			reqAny[i] = v
		}
		s["required"] = reqAny
	}
	return s
}

func collectFields(rt reflect.Type, props map[string]any, required *[]string, schemas map[string]any) {
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		// Promote anonymous embedded structs (no explicit json name) — matches how
		// encoding/json flattens their fields into this object.
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && ft != timeType {
				collectFields(ft, props, required, schemas)
				continue
			}
		}
		if f.PkgPath != "" {
			continue // unexported
		}
		if name == "" {
			name = f.Name
		}
		props[name] = schemaFor(f.Type, schemas)
		// Required when the field is neither a pointer nor tagged omitempty.
		if f.Type.Kind() != reflect.Pointer && !strings.Contains(opts, "omitempty") {
			*required = append(*required, name)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// apiOpenAPI serves the generated OpenAPI document. No key required (see the
// package note); the server URL is derived from the request so the spec always
// points at this install's real base URL.
func (rt *Router) apiOpenAPI(w http.ResponseWriter, r *http.Request) {
	rt.mu.RLock()
	apiPath := rt.apiPath
	rt.mu.RUnlock()
	writeJSON(w, http.StatusOK, buildOpenAPI(apiBaseURL(r, apiPath)))
}

// apiDocs serves a Swagger UI page pointed at the generated spec. The UI shell is
// loaded from a CDN (this page is a developer convenience, reached only by someone
// who already knows the secret API path); the spec it renders is fully local.
func (rt *Router) apiDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerHTML))
}

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>RosPanel API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "openapi.json",
      dom_id: "#swagger-ui",
      persistAuthorization: true,
    });
  </script>
</body>
</html>`
