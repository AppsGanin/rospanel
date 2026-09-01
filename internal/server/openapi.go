package server

import (
	"embed"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
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
	// reqRequired names the body fields the handler actually refuses to work
	// without. Nil means none: every field has a zero value the handler already
	// reads as "not set". It is declared per route rather than reflected because
	// the struct cannot know — see requestBodySchema.
	reqRequired []string
	resp        reflect.Type // response data type, nil ⇒ generic object
	list        bool
	meta        bool
	status      int  // success status; 0 ⇒ 200
	noAuth      bool // key-free route; overrides the document-wide bearerAuth
	// noMCP keeps a route out of the MCP tool list. Two reasons, both deliberate: a
	// body no assistant can read (a tarball), and an operation an assistant should not
	// be able to reach at all — restoring state is a human decision, and the whole
	// point of handing an assistant a key is that its reach is narrower than the key's.
	noMCP bool
	// destructive marks a write an assistant should ask a human about. It is declared
	// here rather than guessed from the summary: the guess reads English words out of
	// the prose, so rewording a sentence silently changes how a tool is presented, and
	// a call that reroutes every user's traffic reads as an ordinary update.
	destructive bool
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
// pageParams documents the window every list endpoint accepts. One helper rather
// than the pair repeated per route: they must stay identical, and a copy that drifts
// is a caller reading the wrong contract.
func pageParams() []oaParam {
	return []oaParam{
		{name: "limit", typ: "integer",
			desc: "page size (default 100, max 1000; <=0 means everything from offset)"},
		{name: "offset", typ: "integer", desc: "rows to skip"},
	}
}

func apiSpecRoutes() []oaRoute {
	return []oaRoute{
		{method: "GET", path: "/v1/users", tag: "Users", summary: "List users",
			query: []oaParam{
				{name: "status", typ: "string", desc: "active | disabled | expired | limited | device_limited"},
				{name: "search", typ: "string", desc: "substring match on the user name, note or tags"},
				{name: "tag", typ: "string", desc: "only users carrying this tag (exact, case-insensitive)"},
				{name: "limit", typ: "integer", desc: "page size (<=0 = all from offset)"},
				{name: "offset", typ: "integer", desc: "number of users to skip"},
			},
			resp: t(userView{}), list: true, meta: true},
		{method: "POST", path: "/v1/users", tag: "Users", summary: "Create a user",
			req: t(apiCreateUserReq{}), reqRequired: []string{"name"},
			resp: t(userView{}), status: 201},
		{method: "POST", path: "/v1/users/bulk", tag: "Users", summary: "Apply one action to many users",
			req: t(apiBulkReq{}), reqRequired: []string{"ids", "action"}, resp: t(oaAffectedResp{})},
		{method: "GET", path: "/v1/users/{id}", tag: "Users", summary: "Get a user",
			resp: t(userView{})},
		{method: "PATCH", path: "/v1/users/{id}", tag: "Users", summary: "Update a user",
			req: t(apiPatchUserReq{}), resp: t(userView{})},
		{method: "DELETE", path: "/v1/users/{id}", tag: "Users", summary: "Delete a user"},
		{method: "POST", path: "/v1/users/{id}/reset", tag: "Users", summary: "Reset traffic counters",
			resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/reset-period", tag: "Users", summary: "Set auto-reset period",
			req: t(apiResetPeriodReq{}), reqRequired: []string{"period"}, resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/rotate-sub", tag: "Users", summary: "Issue a new subscription URL",
			resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/plan", tag: "Users", summary: "Apply a tariff plan",
			req: t(apiApplyPlanReq{}), reqRequired: []string{"plan_id"}, resp: t(userView{})},
		{method: "POST", path: "/v1/users/{id}/plan/cancel", tag: "Users",
			summary: "Cancel a paid subscription (drops to the free plan, or ends access)",
			resp:    t(userView{})},
		{method: "GET", path: "/v1/users/{id}/connections", tag: "Users", summary: "List the user's source IPs",
			query: pageParams(), resp: t(model.Connection{}), list: true, meta: true},
		{method: "GET", path: "/v1/users/{id}/devices", tag: "Users",
			summary: "List the installs bound to the user by HWID, with the cap they count against",
			query:   pageParams(), resp: t(apiDeviceList{}), meta: true},
		{method: "POST", path: "/v1/users/{id}/devices/unbind", tag: "Users",
			summary: "Release one bound device (or all of them), freeing the slot",
			req:     t(apiUnbindDeviceReq{}), resp: t(apiUnbindResp{})},
		{method: "GET", path: "/v1/users/{id}/events", tag: "Users", summary: "One user's journal",
			query: []oaParam{
				{name: "limit", typ: "integer", desc: "page size"},
				{name: "before", typ: "integer", desc: "id of the oldest row already held (paging cursor)"},
			},
			resp: t(apiEventsResp{})},
		{method: "GET", path: "/v1/users/{id}/abuse", tag: "Users", summary: "One user's blocklist matches",
			query: []oaParam{{name: "limit", typ: "integer", desc: "max rows (default 20, max 200)"}},
			resp:  t(store.AbuseMatch{}), list: true},

		{method: "GET", path: "/v1/billing/providers", tag: "Billing", summary: "List enabled payment providers",
			resp: t(oaProviderResp{}), list: true},
		{method: "GET", path: "/v1/billing/plans", tag: "Billing", summary: "List tariff plans",
			query: []oaParam{{name: "include_disabled", typ: "boolean", desc: "include disabled plans"}},
			resp:  t(model.TariffPlan{}), list: true},
		{method: "POST", path: "/v1/billing/plans", tag: "Billing", summary: "Create or update a plan",
			// id is what picks between the two: omit it to create, pass it to update.
			req: t(model.TariffPlan{}), reqRequired: []string{"name"}, resp: t(model.TariffPlan{})},
		{method: "DELETE", path: "/v1/billing/plans/{id}", tag: "Billing", summary: "Delete a plan"},
		{method: "POST", path: "/v1/billing/plans/{id}/migrate", tag: "Billing",
			summary:     "Move every user on this plan to another one",
			req:         t(apiMigratePlanReq{}),
			reqRequired: []string{"to_plan_id"}, resp: t(apiMigratedResp{})},
		{method: "GET", path: "/v1/billing/orders", tag: "Billing", summary: "List payment orders",
			query: append([]oaParam{{name: "status", typ: "string", desc: "pending | paid | cancelled"}},
				pageParams()...),
			resp: t(model.PaymentOrder{}), list: true, meta: true},
		{method: "POST", path: "/v1/billing/orders", tag: "Billing", summary: "Open a payment order",
			req: t(apiCreateOrderReq{}), reqRequired: []string{"user_id", "plan_id"},
			resp: t(oaOrderResp{}), status: 201},
		{method: "GET", path: "/v1/billing/orders/{id}", tag: "Billing", summary: "Get one order",
			resp: t(model.PaymentOrder{})},
		{method: "POST", path: "/v1/billing/orders/{id}/confirm", tag: "Billing", summary: "Mark an order paid"},
		{method: "POST", path: "/v1/billing/orders/{id}/cancel", tag: "Billing", summary: "Cancel an order"},
		{method: "GET", path: "/v1/billing/settings", tag: "Billing",
			summary: "Billing configuration (free/trial plan, manual payment note)",
			resp:    t(apiBillingSettingsReq{})},
		{method: "POST", path: "/v1/billing/settings", tag: "Billing",
			summary: "Replace the billing configuration",
			req:     t(apiBillingSettingsReq{}), resp: t(apiBillingSettingsReq{})},
		{method: "GET", path: "/v1/billing/stats", tag: "Billing",
			summary: "Revenue totals, per-provider split and the pending backlog",
			resp:    t(model.PaymentStats{})},
		{method: "GET", path: "/v1/payments", tag: "Billing",
			summary: "Payment providers with their settings form (secret values are never returned)"},
		{method: "POST", path: "/v1/payments", tag: "Billing",
			summary:     "Configure one payment provider (empty secrets keep their stored value)",
			req:         t(apiSaveProviderReq{}),
			reqRequired: []string{"key"}},

		{method: "GET", path: "/v1/stats/series", tag: "Stats",
			summary: "Daily traffic across the fleet — every day in the range, zeros included",
			query: []oaParam{
				{name: "user_id", typ: "integer", desc: "restrict to one user (omit for panel-wide)"},
				{name: "from", typ: "string", desc: "YYYY-MM-DD (default: 29 days ago)"},
				{name: "to", typ: "string", desc: "YYYY-MM-DD (default: today)"},
			},
			resp: t(model.DailyPoint{}), list: true},
		{method: "GET", path: "/v1/stats/nodes", tag: "Stats",
			summary: "Traffic totals per server over the period",
			query: []oaParam{
				{name: "user_id", typ: "integer", desc: "restrict to one user (omit for panel-wide)"},
				{name: "from", typ: "string", desc: "YYYY-MM-DD (default: 29 days ago)"},
				{name: "to", typ: "string", desc: "YYYY-MM-DD (default: today)"},
			},
			resp: t(core.NodeTraffic{}), list: true},
		{method: "GET", path: "/v1/stats/nodes/series", tag: "Stats",
			summary: "Daily traffic per server — one row per day per server, zeros included",
			query: []oaParam{
				{name: "user_id", typ: "integer", desc: "restrict to one user (omit for panel-wide)"},
				{name: "from", typ: "string", desc: "YYYY-MM-DD (default: 29 days ago)"},
				{name: "to", typ: "string", desc: "YYYY-MM-DD (default: today)"},
			},
			resp: t(core.NodeDailyTraffic{}), list: true},
		{method: "GET", path: "/v1/stats/users", tag: "Stats", summary: "Per-user traffic totals",
			query: append(pageParams(),
				oaParam{name: "from", typ: "string", desc: "YYYY-MM-DD"},
				oaParam{name: "to", typ: "string", desc: "YYYY-MM-DD"},
			),
			resp: t(model.UserTotal{}), list: true, meta: true},
		{method: "GET", path: "/v1/stats/abuse", tag: "Stats", summary: "Recent blocklist matches across the fleet",
			query: []oaParam{{name: "limit", typ: "integer", desc: "max rows (default 50, max 200)"}},
			resp:  t(store.AbuseMatch{}), list: true},
		{method: "GET", path: "/v1/stats/countries", tag: "Stats",
			summary: "Recent connections grouped by country",
			query:   pageParams(), resp: t(model.CountryStat{}), list: true, meta: true},
		{method: "GET", path: "/v1/stats/asns", tag: "Stats",
			summary: "Recent connections grouped by network operator (ASN)",
			query:   pageParams(), resp: t(model.ASNStat{}), list: true, meta: true},

		// Configuration. These change how the servers RUN, which is why every mutation
		// below is audited like a node change.
		{method: "GET", path: "/v1/settings", tag: "Settings",
			summary: "Read the panel settings (no credentials, no secret path)",
			resp:    t(apiSettingsView{})},
		{method: "PATCH", path: "/v1/settings", tag: "Settings",
			summary: "Update settings — only the fields present in the body are applied",
			req:     t(apiSettingsReq{}), resp: t(apiSettingsView{}), destructive: true},
		{method: "GET", path: "/v1/servers/{id}/routing", tag: "Routing",
			summary: "Read a server's routing, DNS and egress backends (server 0 is the master)",
			resp:    t(apiServerRouting{})},
		{method: "POST", path: "/v1/servers/{id}/routing", tag: "Routing",
			summary: "Update a server's routing, DNS and egress backends — omitted fields are left as they are, `routing` replaces the rule set wholesale",
			req:     t(apiServerRoutingReq{}), resp: t(apiServerRouting{}), destructive: true},
		{method: "POST", path: "/v1/servers/{id}/xray-restart", tag: "Routing",
			summary: "Restart a server's Xray (queued for a node; drops its live connections)",
			resp:    t(oaOKResp{}), destructive: true},
		{method: "GET", path: "/v1/config/snapshots", tag: "Settings",
			summary: "List the master's config save-points",
			query:   pageParams(), resp: t(model.ConfigSnapshot{}), list: true, meta: true},
		{method: "POST", path: "/v1/config/snapshots", tag: "Settings",
			summary: "Take a config save-point",
			req:     t(apiSnapshotReq{}), resp: t(model.ConfigSnapshot{}), status: 201},
		{method: "POST", path: "/v1/config/snapshots/{id}/rollback", tag: "Settings",
			summary: "Restore the whole server config from a save-point (restarts Xray fleet-wide)",
			resp:    t(oaOKResp{}), destructive: true},
		{method: "DELETE", path: "/v1/config/snapshots/{id}", tag: "Settings",
			summary: "Delete a save-point", resp: t(oaOKResp{})},

		// The journals. Both page backwards with ?before=<oldest id held>.
		{method: "GET", path: "/v1/events", tag: "Journal", summary: "User events across the panel",
			query: []oaParam{
				{name: "action", typ: "string", desc: "one event key (see /v1/events/catalog)"},
				{name: "actor", typ: "string", desc: "who caused it: admin | user | system | api"},
				{name: "user_id", typ: "integer", desc: "restrict to one user"},
				{name: "limit", typ: "integer", desc: "page size"},
				{name: "before", typ: "integer", desc: "id of the oldest row already held (paging cursor)"},
			},
			resp: t(apiEventsResp{})},
		{method: "GET", path: "/v1/events/catalog", tag: "Journal", summary: "Event keys a user event can carry",
			resp: t(apiEventKey{}), list: true},
		{method: "GET", path: "/v1/admin-audit", tag: "Journal",
			summary: "Admin trail — including everything done through this API",
			query: []oaParam{
				{name: "category", typ: "string", desc: "expands to the actions it holds (see /v1/admin-audit/catalog)"},
				{name: "action", typ: "string", desc: "one action key; ignored when category is set"},
				{name: "actor", typ: "string", desc: "admin name or API key label"},
				{name: "limit", typ: "integer", desc: "page size"},
				{name: "before", typ: "integer", desc: "id of the oldest row already held (paging cursor)"},
			},
			resp: t(apiAdminAuditResp{})},
		{method: "GET", path: "/v1/admin-audit/catalog", tag: "Journal",
			summary: "Admin-audit categories and the actions in each",
			resp:    t(apiAuditCatalogResp{})},

		{method: "GET", path: "/v1/health", tag: "Monitoring", summary: "API reachability check",
			resp: t(oaHealthResp{})},
		// The wording is load-bearing: total_up/total_down are the quota counters, so
		// they drop to zero when traffic is reset and do NOT agree with the sum of
		// /v1/stats/series over the same span. Reading them as lifetime totals is the
		// obvious mistake, and it looks like a double-counting bug from the outside.
		{method: "GET", path: "/v1/summary", tag: "Monitoring",
			summary: "Panel summary — total_up/total_down are usage in the current quota period " +
				"(reset with the counters), not lifetime; use /v1/stats/series for history",
			resp: t(core.Summary{})},
		{method: "GET", path: "/v1/system", tag: "Monitoring", summary: "Live system metrics",
			resp: t(core.SystemStatus{})},
		{method: "GET", path: "/v1/health/report", tag: "Monitoring", summary: "Self-diagnostics",
			resp: t(core.HealthReport{})},
		{method: "GET", path: "/v1/healthz", tag: "Monitoring", noAuth: true,
			summary: "Liveness probe (no key; 503 when Xray is down)",
			resp:    t(healthzResp{})},
		{method: "GET", path: "/v1/metrics", tag: "Monitoring",
			summary: "Prometheus metrics — text exposition (0.0.4), not the JSON envelope"},
		// Hidden from MCP: the body is a gzipped tarball, and the only thing an
		// assistant can do with half a megabyte of it is spend a context window.
		{method: "GET", path: "/v1/backup", tag: "Monitoring", noMCP: true,
			summary: "Download a full backup — responds with a .tar.gz body, not the JSON envelope"},
		{method: "GET", path: "/v1/backup/info", tag: "Monitoring", noMCP: true,
			summary: "What a backup taken now would contain",
			resp:    t(backup.Manifest{})},

		{method: "GET", path: "/v1/nodes", tag: "Nodes", summary: "List nodes (local server is node 0)",
			resp: t(core.NodeView{}), list: true},
		{method: "POST", path: "/v1/nodes", tag: "Nodes", summary: "Register a node (returns the install command)",
			req: t(apiCreateNodeReq{}), reqRequired: []string{"name", "host"},
			resp: t(oaNodeCreateResp{}), status: 201},
		{method: "GET", path: "/v1/nodes/{id}", tag: "Nodes",
			summary: "Get a node (id 0 is the local server, same shape as the list)",
			resp:    t(core.NodeView{})},
		{method: "PATCH", path: "/v1/nodes/{id}", tag: "Nodes", summary: "Edit a node (name, host, protocol/routing/DNS overrides, WARP/Opera egress)",
			req: t(apiPatchNodeReq{}), resp: t(oaOKResp{})},
		{method: "DELETE", path: "/v1/nodes/{id}", tag: "Nodes", summary: "Delete a node"},
		{method: "POST", path: "/v1/nodes/{id}/enabled", tag: "Nodes", summary: "Enable or disable a node",
			req: t(apiSetNodeEnabledReq{}), reqRequired: []string{"enabled"}, resp: t(oaOKResp{})},
		{method: "POST", path: "/v1/nodes/{id}/regen-join", tag: "Nodes", summary: "Issue a fresh install command",
			resp: t(oaNodeCreateResp{})},
		{method: "POST", path: "/v1/nodes/{id}/update", tag: "Nodes", summary: "Ask a node to self-update to the latest release",
			resp: t(oaOKResp{})},
		{method: "POST", path: "/v1/nodes/update-all", tag: "Nodes", summary: "Ask every connected node to self-update",
			resp: t(oaNodeCountResp{})},
		{method: "POST", path: "/v1/nodes/{id}/proxy", tag: "Nodes",
			summary: "Configure a server's system proxy (SOCKS/HTTP forward listeners; id 0 = the master)",
			req:     t(model.SystemProxy{}), resp: t(model.SystemProxy{})},
		{method: "GET", path: "/v1/nodes/{id}/health", tag: "Nodes", summary: "One server's self-diagnostics",
			resp: t(core.HealthReport{})},
		{method: "GET", path: "/v1/nodes/{id}/logs", tag: "Nodes",
			summary: "A node's recent log lines (collected on its next poll; `at` says how fresh)",
			resp:    t(apiNodeLogsResp{})},

		// Custom inbounds. {id} is a SERVER id on the list/create pair (0 = the panel's
		// own server, a node id otherwise) and the INBOUND id on the rest — an inbound
		// belongs to exactly one server, so its id already says which.
		{method: "GET", path: "/v1/inbounds/catalog", tag: "Inbounds",
			summary: "Which protocol × transport × security combinations exist, and what each enum accepts",
			resp:    t(inboundCatalogView{})},
		{method: "GET", path: "/v1/servers/{id}/inbounds", tag: "Inbounds",
			summary: "List a server's custom inbounds (id 0 = the master)",
			resp:    t(core.InboundView{}), list: true},
		{method: "POST", path: "/v1/servers/{id}/inbounds", tag: "Inbounds",
			summary:     "Add a custom inbound to a server (id 0 = the master)",
			req:         t(inboundReq{}),
			reqRequired: []string{"name", "protocol", "port"},
			resp:        t(core.InboundView{}), status: 201},
		{method: "POST", path: "/v1/inbounds/{id}", tag: "Inbounds", summary: "Update a custom inbound",
			req: t(inboundReq{}), reqRequired: []string{"name", "protocol", "port"},
			resp: t(core.InboundView{})},
		{method: "DELETE", path: "/v1/inbounds/{id}", tag: "Inbounds", summary: "Delete a custom inbound"},

		{method: "GET", path: "/v1/groups", tag: "Groups", summary: "List user groups", resp: t(model.Group{}), list: true},
		{method: "GET", path: "/v1/groups/targets", tag: "Groups",
			summary: "Grantable connections per server, each with the token to put in `grants`",
			resp:    t(core.GroupTarget{}), list: true},
		{method: "POST", path: "/v1/groups", tag: "Groups", summary: "Create a group",
			req: t(groupReq{}), reqRequired: []string{"name"}, resp: t(model.Group{}), status: 201},
		{method: "POST", path: "/v1/groups/{id}", tag: "Groups", summary: "Update a group",
			req: t(groupReq{}), reqRequired: []string{"name"}},
		{method: "DELETE", path: "/v1/groups/{id}", tag: "Groups", summary: "Delete a group"},
		{method: "POST", path: "/v1/groups/{id}/members", tag: "Groups", summary: "Set a group's members",
			req: t(oaGroupMembersReq{}), reqRequired: []string{"user_ids"}},
		{method: "POST", path: "/v1/users/{id}/groups", tag: "Users", summary: "Set a user's group membership",
			req: t(oaUserGroupsReq{}), reqRequired: []string{"group_ids"}},

		{method: "GET", path: "/v1/webhooks", tag: "Webhooks", summary: "List webhook endpoints",
			resp: t(model.Webhook{}), list: true},
		{method: "GET", path: "/v1/webhooks/events", tag: "Webhooks", summary: "Event keys a webhook can subscribe to",
			resp: t(apiEventKey{}), list: true},
		{method: "POST", path: "/v1/webhooks", tag: "Webhooks", summary: "Add a webhook endpoint",
			req: t(apiWebhookReq{}), reqRequired: []string{"url", "events"},
			resp: t(model.Webhook{}), status: 201},
		{method: "POST", path: "/v1/webhooks/{id}", tag: "Webhooks", summary: "Update a webhook endpoint",
			req: t(apiWebhookReq{}), reqRequired: []string{"url", "events"}, resp: t(oaOKResp{})},
		{method: "DELETE", path: "/v1/webhooks/{id}", tag: "Webhooks", summary: "Delete a webhook endpoint"},
		{method: "POST", path: "/v1/webhooks/{id}/test", tag: "Webhooks",
			summary: "Send a test delivery (200 with ok=false when the endpoint fails)",
			resp:    t(apiWebhookTestResp{})},

		{method: "GET", path: "/v1/registrations", tag: "Registrations",
			summary: "Moderated signup queue", resp: t(apiRegistrationsResp{})},
		{method: "POST", path: "/v1/registrations/{id}/approve", tag: "Registrations",
			summary: "Approve a signup — creates the account and links its Telegram chat",
			resp:    t(oaOKResp{})},
		{method: "POST", path: "/v1/registrations/{id}/reject", tag: "Registrations",
			summary: "Reject a signup", resp: t(oaOKResp{})},
	}
}

// oaUserGroupsReq / oaGroupMembersReq document the two set-membership bodies.
type oaUserGroupsReq struct {
	GroupIDs []int64 `json:"group_ids"`
}
type oaGroupMembersReq struct {
	UserIDs []int64 `json:"user_ids"`
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

// OpenAPISpec is the generated API description, exported so the MCP server
// (cmd/rospanel mcp) can build its tool list from the same source the Swagger page
// renders. Generating the tools from the spec rather than from a hand-kept list is
// what stops the two from drifting: a route added without an OpenAPI entry already
// fails TestAPISpecCoversEveryRoute, and now it also cannot silently go missing
// from the assistant's toolbox.
func OpenAPISpec(serverURL string) map[string]any { return buildOpenAPI(serverURL) }

// buildOpenAPI assembles the full OpenAPI 3.0 document for the given server URL.
func buildOpenAPI(serverURL string) map[string]any {
	schemas := map[string]any{
		"ErrorResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"error": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{
							"type":        "string",
							"description": "coarse class: bad_request | unauthorized | not_found | unsupported_media_type | internal",
						},
						"message": map[string]any{
							"type":        "string",
							"description": "human-readable English text, parameters already filled in",
						},
						"key": map[string]any{
							"type":        "string",
							"description": "stable identifier of the specific reason (e.g. err.planHasUsers) — branch on this, not on the text; absent when the panel raised no code",
						},
						"args": map[string]any{
							"type":                 "object",
							"additionalProperties": true,
							"description":          "the message's parameters, so a client can render it in its own language; absent when there are none",
						},
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
	// A vendor extension rather than a list of paths inside the mcp package: that
	// package builds its tools from this document and should not also have to know
	// the panel's route table.
	if r.destructive {
		op["x-destructive"] = true
	}
	if r.noMCP {
		op["x-mcp"] = false
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
				"application/json": map[string]any{"schema": requestBodySchema(r, schemas)},
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

// requestBodySchema renders a route's request body: the struct's own shape, but
// with `required` replaced by what the HANDLER demands rather than what the Go
// type happens to look like.
//
// The reflected list is a fact about Go — collectFields marks every field that is
// neither a pointer nor omitempty — and for a request body that means "everything".
// Published as-is it is not merely noise, it is wrong in a way that costs real
// calls: a caller reading it must invent an `id` to create a plan and fill in all
// 24 fields to add an inbound. A strict client refuses to call at all; a lenient
// one makes the values up.
//
// The schema is expanded rather than referenced because the override belongs to
// this operation: the same struct is often also a response (a tariff plan comes
// back from GET /v1/billing/plans), where every field genuinely is always present.
func requestBodySchema(r oaRoute, schemas map[string]any) map[string]any {
	s := schemaFor(r.req, schemas)
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		target, ok := schemas[name].(map[string]any)
		if !ok {
			return s
		}
		s = maps.Clone(target)
	}
	delete(s, "required")
	if len(r.reqRequired) > 0 {
		req := make([]any, len(r.reqRequired))
		for i, name := range r.reqRequired {
			req[i] = name
		}
		s["required"] = req
	}
	return s
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

// swaggerAssets holds the Swagger UI shell (CSS + JS bundle), vendored so the docs
// page loads entirely from our own origin — see apiDocs.
//
//go:embed swaggerui/swagger-ui.css swaggerui/swagger-ui-bundle.js
var swaggerAssets embed.FS

// apiDocs serves a Swagger UI page pointed at the generated spec. Both the shell and
// the spec are local: a CDN link would render a half-drawn page where that CDN is
// blocked (the networks this panel serves), and leak to a third party who opened it.
// The links are relative to /v1/docs, so they resolve to /v1/swagger-ui* regardless
// of the secret API path in front.
func (rt *Router) apiDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerHTML))
}

// swaggerAsset serves one embedded Swagger UI file with a long cache lifetime (the
// bundle is versioned by its bytes and changes only on a rebuild).
func (rt *Router) swaggerAsset(name, contentType string) http.HandlerFunc {
	body, err := swaggerAssets.ReadFile("swaggerui/" + name)
	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType+"; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(body)
	}
}

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>RosPanel API</title>
  <link rel="stylesheet" href="swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "openapi.json",
      dom_id: "#swagger-ui",
      persistAuthorization: true,
    });
  </script>
</body>
</html>`
