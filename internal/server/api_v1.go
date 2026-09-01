package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The external REST API is a stable, versioned contract for a surrounding system
// (billing, provisioning, a Telegram shop) to manage the panel over HTTP with an
// API key. It is deliberately thin: every handler validates input, then calls the
// same core.Manager methods the admin panel uses, so the two surfaces can never
// drift in behaviour. Responses use a fixed envelope: {"data": ...} on success,
// {"error": {"code","message"}} on failure.

// Request bodies are named types (not inline structs) so the OpenAPI generator
// can reflect their exact shape — keeping the published spec in lockstep with the
// code that decodes them.
type (
	apiCreateUserReq struct {
		Name      string `json:"name"`
		DataLimit int64  `json:"data_limit"` // bytes, 0 = unlimited
		ExpireAt  int64  `json:"expire_at"`  // unix seconds, 0 = never
		// The rest are optional and applied to the fresh account in one call, because
		// the alternative was three: create, then set a device limit, then a plan. Each
		// of those is a separate reconcile, and a caller that failed halfway left a user
		// live on terms nobody chose. PlanID overrides DataLimit/ExpireAt with the
		// plan's own — a plan IS the limits.
		DeviceLimit int     `json:"device_limit,omitempty"` // 0 = unlimited
		SpeedLimit  int     `json:"speed_limit,omitempty"`  // kbit/s, 0 = unlimited
		PlanID      int64   `json:"plan_id,omitempty"`      // 0 = no plan (manual limits)
		GroupIDs    []int64 `json:"group_ids,omitempty"`    // access groups, by hand
		// Note and Tags are the operator's own annotations; see model.User.
		Note string   `json:"note,omitempty"` // up to model.MaxUserNoteLen characters
		Tags []string `json:"tags,omitempty"` // normalised: lower-cased, sorted, no commas
	}
	// apiPatchUserReq fields are pointers: a nil field is left unchanged, so a
	// caller can update just one attribute.
	apiPatchUserReq struct {
		Name        *string `json:"name,omitempty"`
		Enabled     *bool   `json:"enabled,omitempty"`
		DataLimit   *int64  `json:"data_limit,omitempty"`
		ExpireAt    *int64  `json:"expire_at,omitempty"`
		DeviceLimit *int    `json:"device_limit,omitempty"`
		SpeedLimit  *int    `json:"speed_limit,omitempty"` // kbit/s, 0 = unlimited
		// Note and Tags are the operator's own annotations; see model.User. An empty
		// note or an empty tag list clears the field; a missing one leaves it alone.
		Note *string   `json:"note,omitempty"` // up to model.MaxUserNoteLen characters
		Tags *[]string `json:"tags,omitempty"` // normalised: lower-cased, sorted, no commas
	}
	apiBulkReq struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"` // enable | disable | delete | reset | extend
		Days   int     `json:"days"`   // required for action=extend
	}
	apiResetPeriodReq struct {
		Period string `json:"period"` // none | daily | weekly | monthly | yearly
	}
	apiApplyPlanReq struct {
		PlanID            int64 `json:"plan_id"`
		ExtendFromCurrent bool  `json:"extend_from_current"`
	}
	apiCreateOrderReq struct {
		UserID int64 `json:"user_id"`
		PlanID int64 `json:"plan_id"`
		// Provider is the automatic payment method ("yookassa" | "cryptobot"). Empty
		// ⇒ a manual order (admin confirms it); set ⇒ a hosted provider payment whose
		// pay_url is returned.
		Provider string `json:"provider,omitempty"`
	}
)

// apiHandler is the full external-API surface: the docs (OpenAPI spec + Swagger
// UI) are served key-free so a browser can load them, while every real /v1
// operation goes through apiAuth. More specific patterns win in Go's ServeMux, so
// the two docs routes take precedence over the authenticated catch-all.
func (rt *Router) apiHandler() http.Handler {
	mux := http.NewServeMux()
	// Recorded like the authenticated routes below, so the OpenAPI coverage test sees
	// the whole /v1 surface and not just the part behind apiAuth.
	for pattern, h := range map[string]http.HandlerFunc{
		"GET /v1/openapi.json": rt.apiOpenAPI,
		"GET /v1/docs":         rt.apiDocs,
		"GET /v1/healthz":      rt.apiHealthz,
	} {
		rt.apiRoutes = append(rt.apiRoutes, pattern)
		mux.HandleFunc(pattern, h)
	}
	// The Swagger UI shell, served from our own origin rather than a CDN — the same
	// reason the decoys and the subscription page carry their own assets: a jsdelivr
	// link renders half a docs page where jsdelivr is blocked, and it tells a third
	// party who opened them. Not recorded in apiRoutes: these are static assets, not
	// REST resources the OpenAPI document should list.
	mux.HandleFunc("GET /v1/swagger-ui.css", rt.swaggerAsset("swagger-ui.css", "text/css"))
	mux.HandleFunc("GET /v1/swagger-ui-bundle.js", rt.swaggerAsset("swagger-ui-bundle.js", "text/javascript"))
	// The MCP endpoint authenticates from the path (see api_v1_mcp.go), so it is
	// mounted before the bearer-authenticated catch-all. Deliberately NOT recorded in
	// apiRoutes: it is a JSON-RPC transport rather than a REST resource, so there is
	// nothing for the OpenAPI document to describe — and keeping it out of that
	// document is also what stops the generated tool list from handing an assistant a
	// tool that calls the tool server.
	mux.HandleFunc(mcpPathPrefix+"{key}", rt.handleMCP)
	mux.HandleFunc(mcpPathPrefix+"{key}/write", rt.handleMCP)
	mux.Handle("/", rt.apiAuth(rt.apiMux()))
	return mux
}

// apiHealthz is the liveness probe for an external uptime monitor or load balancer:
// key-free, since a monitor shouldn't have to carry a credential that can delete
// users, and since the check must still answer when the DB is unhappy.
//
// It deliberately lives under the API path rather than at the root. The whole point
// of the masquerade is that an unknown path is indistinguishable from ordinary
// hosting, and a root /healthz answering JSON would fingerprint the panel in one
// request. Under the API segment the decoy still covers anyone who doesn't know it,
// and the segment is stable across secret rotation — so the monitor's URL survives.
//
// Xray being down means the node carries no VPN traffic even though the panel is
// fine, so that's a 503: an operator wants to be paged for it.
func (rt *Router) apiHealthz(w http.ResponseWriter, _ *http.Request) {
	running, startedAt := rt.mgr.XrayStatus()
	body := healthzResp{Status: "ok", Xray: "running", XrayStartedAt: startedAt}
	code := http.StatusOK
	if !running {
		body.Status, body.Xray = "degraded", "down"
		code = http.StatusServiceUnavailable
	}
	writeAPIData(w, code, body)
}

// apiMux builds the /v1 route table for the external API. Auth is applied by the
// caller (apiAuth), so every route here already has a valid key.
func (rt *Router) apiMux() http.Handler {
	mux := http.NewServeMux()
	// Every /v1 registration goes through hf so the route table is recorded as it is
	// built: TestAPISpecCoversEveryRoute reads it back and fails when an endpoint
	// ships without an OpenAPI entry. GET /v1/health had drifted that way — reachable,
	// documented in docs/api.md, absent from the generated spec.
	hf := func(pattern string, h http.HandlerFunc) {
		rt.apiRoutes = append(rt.apiRoutes, pattern)
		mux.HandleFunc(pattern, h)
	}
	id := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		hf(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid id")
				return
			}
			h(w, r, v)
		})
	}

	hf("GET /v1/health", rt.apiHealth)

	hf("GET /v1/users", rt.apiListUsers)
	hf("POST /v1/users", rt.apiCreateUser)
	hf("POST /v1/users/bulk", rt.apiBulkUsers)
	id("GET /v1/users/{id}", rt.apiGetUser)
	id("PATCH /v1/users/{id}", rt.apiPatchUser)
	id("DELETE /v1/users/{id}", rt.apiDeleteUser)
	id("POST /v1/users/{id}/reset", rt.apiResetUser)
	id("POST /v1/users/{id}/reset-period", rt.apiSetResetPeriod)
	id("POST /v1/users/{id}/rotate-sub", rt.apiRotateSub)
	id("POST /v1/users/{id}/plan", rt.apiApplyPlan)
	id("POST /v1/users/{id}/plan/cancel", rt.apiCancelUserPlan)
	id("GET /v1/users/{id}/connections", rt.apiUserConnections)
	id("GET /v1/users/{id}/devices", rt.apiUserDevices)
	id("POST /v1/users/{id}/devices/unbind", rt.apiUnbindDevice)
	id("GET /v1/users/{id}/events", rt.apiUserEvents)
	id("GET /v1/users/{id}/abuse", rt.apiUserAbuse)

	hf("GET /v1/billing/providers", rt.apiListProviders)
	hf("GET /v1/billing/plans", rt.apiListPlans)
	hf("POST /v1/billing/plans", rt.apiSavePlan)
	id("DELETE /v1/billing/plans/{id}", rt.apiDeletePlan)
	id("POST /v1/billing/plans/{id}/migrate", rt.apiMigratePlanUsers)
	hf("GET /v1/billing/orders", rt.apiListOrders)
	hf("POST /v1/billing/orders", rt.apiCreateOrder)
	id("GET /v1/billing/orders/{id}", rt.apiGetOrder)
	id("POST /v1/billing/orders/{id}/confirm", rt.apiConfirmOrder)
	id("POST /v1/billing/orders/{id}/cancel", rt.apiCancelOrder)
	hf("GET /v1/billing/stats", rt.apiPaymentStats)

	hf("GET /v1/stats/series", rt.apiStatsSeries)
	hf("GET /v1/stats/nodes", rt.apiStatsNodes)
	hf("GET /v1/stats/nodes/series", rt.apiStatsNodeSeries)
	hf("GET /v1/stats/users", rt.apiStatsUsers)
	hf("GET /v1/stats/abuse", rt.apiStatsAbuse)
	hf("GET /v1/stats/countries", rt.apiStatsCountries)
	hf("GET /v1/stats/asns", rt.apiStatsASNs)

	// The journals. Read-only, and the admin trail includes what this very key does.
	hf("GET /v1/events", rt.apiEvents)
	hf("GET /v1/events/catalog", rt.apiEventCatalog)
	hf("GET /v1/admin-audit", rt.apiAdminAudit)
	hf("GET /v1/admin-audit/catalog", rt.apiAdminAuditCatalog)

	// Prometheus scrape target. Text, not the JSON envelope — see metrics.go.
	hf("GET /v1/metrics", rt.apiMetrics)
	hf("GET /v1/summary", rt.apiSummary)
	hf("GET /v1/system", rt.apiSystem)
	hf("GET /v1/health/report", rt.apiHealthReport)
	hf("GET /v1/backup", rt.apiBackup)
	hf("GET /v1/backup/info", rt.apiBackupInfo)

	// Node mutations over the external API must land in the admin audit trail too —
	// the panel's audited-middleware wraps only the panel mux, not this one. idFn adds
	// {id} parsing so an audited node route can still take an int64 handler.
	idFn := func(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			v, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid id")
				return
			}
			h(w, r, v)
		}
	}
	nodeAudit := func(pattern, section string, h http.HandlerFunc) {
		hf(pattern, rt.apiAudited(section, h))
	}

	hf("GET /v1/nodes", rt.apiListNodes)
	nodeAudit("POST /v1/nodes", "apiNodeAdded", rt.apiCreateNode)
	id("GET /v1/nodes/{id}", rt.apiGetNode)
	nodeAudit("PATCH /v1/nodes/{id}", "apiNodeChanged", idFn(rt.apiPatchNode))
	nodeAudit("DELETE /v1/nodes/{id}", "apiNodeDeleted", idFn(rt.apiDeleteNode))
	nodeAudit("POST /v1/nodes/{id}/enabled", "apiNodeToggled", idFn(rt.apiSetNodeEnabled))
	nodeAudit("POST /v1/nodes/{id}/regen-join", "apiNodeRejoin", idFn(rt.apiRegenNodeJoin))
	nodeAudit("POST /v1/nodes/{id}/update", "apiNodeUpdate", idFn(rt.apiUpdateNode))
	nodeAudit("POST /v1/nodes/update-all", "apiNodesUpdateAll", rt.apiUpdateAllNodes)
	nodeAudit("POST /v1/nodes/{id}/proxy", "apiSystemProxy", idFn(rt.apiSetServerProxy))
	id("GET /v1/nodes/{id}/health", rt.apiNodeHealth)
	id("GET /v1/nodes/{id}/logs", rt.apiNodeLogs)

	// Custom inbounds. Every mutation opens, changes or closes a public listener on a
	// server, so all three are audited the same way node mutations are.
	hf("GET /v1/groups", rt.apiListGroups)
	hf("GET /v1/groups/targets", rt.apiGroupTargets)
	nodeAudit("POST /v1/groups", "apiGroupAdded", rt.apiCreateGroup)
	nodeAudit("POST /v1/groups/{id}", "apiGroupChanged", idFn(rt.apiUpdateGroup))
	nodeAudit("DELETE /v1/groups/{id}", "apiGroupDeleted", idFn(rt.apiDeleteGroup))
	nodeAudit("POST /v1/groups/{id}/members", "apiGroupMembers", idFn(rt.apiSetGroupMembers))
	nodeAudit("POST /v1/users/{id}/groups", "apiUserGroups", idFn(rt.apiSetUserGroups))

	hf("GET /v1/inbounds/catalog", rt.apiInboundCatalog)
	id("GET /v1/servers/{id}/inbounds", rt.apiListInbounds)
	nodeAudit("POST /v1/servers/{id}/inbounds", "apiInboundAdded", idFn(rt.apiCreateInbound))
	nodeAudit("POST /v1/inbounds/{id}", "apiInboundChanged", idFn(rt.apiUpdateInbound))
	nodeAudit("DELETE /v1/inbounds/{id}", "apiInboundDeleted", idFn(rt.apiDeleteInbound))

	// The configuration surface. Everything below changes how the servers RUN, so each
	// mutation is audited exactly like a node change — see api_v1_config.go for what is
	// deliberately absent (the admin roster, API keys, the panel's secret path).
	hf("GET /v1/settings", rt.apiGetSettings)
	nodeAudit("PATCH /v1/settings", "apiSettings", rt.apiPatchSettings)
	id("GET /v1/servers/{id}/routing", rt.apiGetServerRouting)
	nodeAudit("POST /v1/servers/{id}/routing", "apiRouting", idFn(rt.apiSetServerRouting))
	nodeAudit("POST /v1/servers/{id}/xray-restart", "apiXrayRestart", idFn(rt.apiXrayRestart))
	hf("GET /v1/config/snapshots", rt.apiConfigSnapshots)
	nodeAudit("POST /v1/config/snapshots", "apiSnapshotTaken", rt.apiCreateConfigSnapshot)
	nodeAudit("POST /v1/config/snapshots/{id}/rollback", "apiSnapshotRollback", idFn(rt.apiRollbackConfigSnapshot))
	nodeAudit("DELETE /v1/config/snapshots/{id}", "apiSnapshotDeleted", idFn(rt.apiDeleteConfigSnapshot))

	// Webhooks: where the panel pushes events. Mutations are audited — an endpoint
	// added here starts receiving user and payment data.
	hf("GET /v1/webhooks", rt.apiListWebhooks)
	hf("GET /v1/webhooks/events", rt.apiWebhookEvents)
	nodeAudit("POST /v1/webhooks", "apiWebhookAdded", rt.apiCreateWebhook)
	nodeAudit("POST /v1/webhooks/{id}", "apiWebhookChanged", idFn(rt.apiUpdateWebhook))
	nodeAudit("DELETE /v1/webhooks/{id}", "apiWebhookDeleted", idFn(rt.apiDeleteWebhook))
	id("POST /v1/webhooks/{id}/test", rt.apiTestWebhook)

	// Billing configuration and payment providers — the setup half of selling.
	hf("GET /v1/billing/settings", rt.apiGetBillingSettings)
	nodeAudit("POST /v1/billing/settings", "apiBillingSettings", rt.apiSaveBillingSettings)
	hf("GET /v1/payments", rt.apiPaymentProviders)
	nodeAudit("POST /v1/payments", "apiPaymentProvider", rt.apiSavePaymentProvider)

	// The moderated signup queue.
	hf("GET /v1/registrations", rt.apiListRegistrations)
	nodeAudit("POST /v1/registrations/{id}/approve", "apiRegApproved", idFn(rt.apiApproveRegistration))
	nodeAudit("POST /v1/registrations/{id}/reject", "apiRegRejected", idFn(rt.apiRejectRegistration))

	// Any unmatched /v1 path (or a wrong method) returns a JSON 404 in-envelope
	// rather than the default plain-text one.
	hf("/", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return mux
}

// apiAuth authenticates every API request by its bearer key. The key may be sent
// as "Authorization: Bearer <key>" or the "X-API-Key: <key>" header. An
// absent/invalid/revoked key gets a 401; the lookup is a constant-time hash match
// in the store (the raw key never touches the DB).
//
// Wrong keys are also counted per source IP: the apiLimiter out front only caps
// request rate, so without this a source could keep guessing at 600/min forever.
// After enough failures the IP is locked out for the guard's window; presenting a
// valid key clears its record immediately, so a misconfigured integration recovers
// as soon as it's fixed.
func (rt *Router) apiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if rt.apiKeys.blocked(ip, "") {
			slog.Warn("api: key attempts locked out", "ip", ip)
			writeAPIErr(w, http.StatusTooManyRequests, "too_many_requests",
				"too many invalid API keys, try again later")
			return
		}
		key := apiKeyFromRequest(r)
		if key == "" {
			// No credential offered at all — a bare probe of the path, not a guess.
			// Don't spend the IP's failure budget on it.
			writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "missing API key")
			return
		}
		ak, err := rt.mgr.Store().LookupAPIKey(key)
		if err != nil {
			writeAPIErr(w, http.StatusInternalServerError, "internal", "authentication failed")
			return
		}
		if ak == nil {
			rt.apiKeys.fail(ip, "")
			slog.Warn("api: invalid key", "ip", ip)
			writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked API key")
			return
		}
		rt.apiKeys.success(ip, "")
		// The key's name is the actor in the audit log, so a mutation made over the
		// external API is attributable to the integration that made it.
		next.ServeHTTP(w, r.WithContext(actor.With(r.Context(), actor.APIKey(ak.Name))))
	})
}

// apiAudited wraps a mutating /v1 node handler so a successful request lands in the
// admin audit trail, attributed to the API key's name — the panel's audited middleware
// doesn't cover this mux. Nothing is recorded on a 4xx/5xx (it didn't happen).
// section is a dictionary key leaf under audit.sec.*, the same namespace the
// panel's own audited routes use — the two land in one log and are read by one
// reader, so they must be worded by one dictionary.
func (rt *Router) apiAudited(section string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &auditStatus{ResponseWriter: w, code: http.StatusOK}
		h(sw, r)
		if sw.code >= http.StatusBadRequest {
			return
		}
		a := actor.From(r.Context())
		rt.mgr.AddAdminAudit(model.AdminAudit{
			Action:    model.AuditSettings,
			Target:    model.AuditSectionPrefix + section,
			ActorKind: a.Kind,
			ActorName: a.Name,
			IP:        clientIP(r),
		})
	}
}

// apiKeyFromRequest extracts the raw key from the Authorization bearer header or
// the X-API-Key header.
func apiKeyFromRequest(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		return h
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

// ---- response envelope ----

// writeAPIData writes a {"data": v} success body.
func writeAPIData(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, map[string]any{"data": v})
}

// writeAPIErr writes an {"error": {"code","message"}} failure body.
func writeAPIErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

// writeAPIRejected writes a 400 for input the panel refused, in English, and keeps
// what the panel knows about the refusal: `key` is the specific reason (stable, and
// the thing to branch on — `code` stays the coarse "bad_request" every client already
// switches on), `args` are its parameters, so a caller can render the same message in
// its own language instead of parsing prose.
func writeAPIRejected(w http.ResponseWriter, key, fallback string, args map[string]any) {
	msg, ok := i18n.ErrorEN(key, args)
	if !ok {
		// No English text for this code: the panel's own fallback beats an empty
		// message, even in the wrong language. TestAPIErrorsAreTranslated keeps this
		// path from becoming the normal one.
		msg = fallback
	}
	body := map[string]any{"code": "bad_request", "message": msg}
	if key != "" {
		body["key"] = key
	}
	if len(args) > 0 {
		body["args"] = args
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": body})
}

// writeAPIManagerErr maps a core.Manager error onto the API envelope: a
// ValidationError (bad caller input) → 400 bad_request, anything else → 500.
func writeAPIManagerErr(w http.ResponseWriter, err error) {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		writeAPIRejected(w, ve.Code, ve.Msg, ve.Args)
		return
	}
	// A model-level field error is a rejected input too. The manager converts those
	// (fromFieldErr) so they used to arrive here already wrapped, but a handler that
	// validates directly against model — as the webhook and branding surfaces do —
	// hands over the raw one, and answering "500 internal" to a bad URL blames the
	// server for the caller's typo.
	var fe *model.FieldError
	if errors.As(err, &fe) {
		writeAPIRejected(w, fe.Code, fe.Msg, fe.Args)
		return
	}
	// An id that matches nothing is the caller's mistake, not the panel's. Several
	// routes already checked for this by hand; the ones that forgot answered "500
	// internal: sql: no rows in result set", which blames the server, leaks the
	// storage layer, and tells an assistant retrying is worth a try. Handled here so
	// the answer is the same on every route.
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	writeAPIErr(w, http.StatusInternalServerError, "internal", err.Error())
}

// apiDecode reads a size-limited JSON body into dst using the API envelope for
// errors. Like decodeJSON it requires application/json.
//
// Unknown fields are rejected rather than ignored. Silently dropping them is the
// worse failure by a distance: POST /v1/users/{id}/groups with {"groups": [2,4]}
// used to answer {"ok": true} having set nothing, and the caller had no way to
// learn that except by reading back. A 400 naming the field is a bug found in one
// call. This is also what makes the generated spec load-bearing — a field name it
// doesn't list is now an error instead of a no-op.
func apiDecode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeAPIErr(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return false
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", decodeErrMessage(err, dst))
		return false
	}
	return true
}

// decodeErrMessage turns a decoder error into something the caller can act on.
//
// The unknown field splits into two very different mistakes, and saying which one
// it is saves the caller the guess:
//
//   - a name the API never accepts anywhere — a typo, like the `groups` that should
//     have been `group_ids`;
//   - a name this resource RETURNS but does not take, which is what "read the
//     object, change a field, send it back" produces. Nothing is wrong with the
//     caller's intent there; they just have to drop the server-owned keys.
//
// Both stay a 400. Quietly accepting the second kind would mean quietly accepting a
// `member_ids` that changes no membership, and a caller who cannot tell an applied
// field from an ignored one is back to the bug this strictness exists to prevent.
func decodeErrMessage(err error, dst any) string {
	const unknown = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, unknown) {
		return "invalid request body"
	}
	field := strings.Trim(strings.TrimPrefix(msg, unknown), `"`)
	if readOnlyBodyFields[reflect.TypeOf(dst)][field] {
		return "field " + strconv.Quote(field) + " is read-only here: this endpoint returns it but does not accept it" +
			" — drop it from the body (see GET /v1/openapi.json)"
	}
	return "unknown field " + strconv.Quote(field) +
		" — see GET /v1/openapi.json for the fields this endpoint accepts"
}

// readOnlyBodyFields maps a request-body type to the field names that the same
// resource's RESPONSE carries but its request does not — the keys a round-trip
// picks up. Derived from the route table by the same reflection that generates the
// spec, so it cannot drift from either.
var readOnlyBodyFields = buildReadOnlyBodyFields()

func buildReadOnlyBodyFields() map[reflect.Type]map[string]bool {
	out := map[reflect.Type]map[string]bool{}
	for _, r := range apiSpecRoutes() {
		if r.req == nil || r.resp == nil {
			continue
		}
		// Keyed by the type a handler decodes INTO, which is what apiDecode holds.
		key := reflect.PointerTo(r.req)
		set := out[key]
		if set == nil {
			set = map[string]bool{}
			out[key] = set
		}
		sent := jsonFieldNames(r.req)
		for name := range jsonFieldNames(r.resp) {
			if !sent[name] {
				set[name] = true
			}
		}
	}
	return out
}

// jsonFieldNames is the set of top-level JSON keys a struct marshals to, following
// the same embedded-struct promotion the spec generator uses.
func jsonFieldNames(rt reflect.Type) map[string]bool {
	props := map[string]any{}
	var required []string
	collectFields(rt, props, &required, map[string]any{})
	out := make(map[string]bool, len(props))
	for name := range props {
		out[name] = true
	}
	return out
}

// apiUserView builds the share-link-carrying view for a user, reusing the panel's
// makeUserView so the API and panel expose identical fields. The Telegram user-bot
// @username is left unresolved ("") — the API view doesn't surface bot deep links.
func (rt *Router) apiUserView(w http.ResponseWriter, u model.User) {
	rt.apiUserViewStatus(w, u, http.StatusOK)
}

// apiUserViewStatus is the same with an explicit status, so creation can answer the
// 201 the published spec promises — it had been answering 200, which a client
// generated from that spec is entitled to treat as an unexpected response.
func (rt *Router) apiUserViewStatus(w http.ResponseWriter, u model.User, code int) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeAPIData(w, code, rt.userViewFor(u, set, ""))
}

// ---- handlers ----

func (rt *Router) apiHealth(w http.ResponseWriter, _ *http.Request) {
	writeAPIData(w, http.StatusOK, oaHealthResp{Status: "ok"})
}

// userMatches is the ?search predicate: a case-insensitive substring of the name,
// the operator's note or any tag. q must already be lower-cased.
func userMatches(u model.User, q string) bool {
	if strings.Contains(strings.ToLower(u.Name), q) || strings.Contains(strings.ToLower(u.Note), q) {
		return true
	}
	for _, t := range u.Tags {
		if strings.Contains(t, q) {
			return true
		}
	}
	return false
}

// apiListUsers lists users with optional filtering (?status, ?search, ?tag) and
// pagination (?limit, ?offset). The result carries a "meta" block with the total
// count (after filtering, before the page window) so callers can paginate.
func (rt *Router) apiListUsers(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	users, err := rt.mgr.Store().ListUsers()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)

	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	tag := strings.ToLower(strings.TrimSpace(q.Get("tag")))
	filtered := users[:0:0]
	for _, u := range users {
		if status != "" && u.Status != status {
			continue
		}
		if search != "" && !userMatches(u, search) {
			continue
		}
		if tag != "" && !slices.Contains(u.Tags, tag) {
			continue
		}
		filtered = append(filtered, u)
	}

	// Window the slice — see api_v1_paging.go for what limit/offset mean.
	window, meta := page(r, filtered)

	custom := rt.localInbounds()
	groupsMap, _ := rt.mgr.GroupsForAllUsers()
	accessMap, _ := rt.mgr.Store().AccessMap()
	views := make([]userView, 0, len(window))
	for _, u := range window {
		views = append(views, makeUserView(u, set, "", custom, groupsMap[u.ID], model.AccessOf(accessMap, u.ID)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": views, "meta": meta})
}

// atoiOr parses s as an int, returning def on any failure (empty or malformed).
func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

func clampNonNeg(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func (rt *Router) apiBulkUsers(w http.ResponseWriter, r *http.Request) {
	var req apiBulkReq
	if !apiDecode(w, r, &req) {
		return
	}
	affected, err := rt.mgr.BulkUserAction(r.Context(), req.IDs, req.Action, req.Days)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"affected": affected})
}

func (rt *Router) apiSetResetPeriod(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiResetPeriodReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetResetPeriod(r.Context(), id, req.Period); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
}

func (rt *Router) apiUserConnections(w http.ResponseWriter, r *http.Request, id int64) {
	conns, err := rt.mgr.Connections(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIPage(w, r, conns)
}

func (rt *Router) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	var req apiCreateUserReq
	if !apiDecode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	u, err := rt.mgr.CreateUser(r.Context(), req.Name, req.DataLimit, req.ExpireAt)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	// The optional extras. Each is reported as an error but does NOT undo the account:
	// deleting a user someone may already have been handed credentials for, because a
	// device limit was rejected, is the worse outcome. The response body is the user as
	// it actually ended up, so a caller can see what landed.
	if len(req.GroupIDs) > 0 {
		if err := rt.mgr.SetUserGroups(u.ID, req.GroupIDs); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	// The plan goes on BEFORE the device limit and rewrites quota, expiry and devices
	// with its own — so an explicit device_limit has to be re-applied on top, keeping
	// the plan's freshly-written quota and expiry rather than the request's (which the
	// plan just superseded). The other order silently dropped the caller's number.
	if req.PlanID > 0 {
		if err := rt.mgr.ApplyPlanToUser(r.Context(), u.ID, req.PlanID, false); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.DeviceLimit > 0 {
		cur, err := rt.mgr.Store().GetUser(u.ID)
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		if err := rt.mgr.SetUserLimits(r.Context(), u.ID, cur.DataLimit, cur.ExpireAt, req.DeviceLimit); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	// After the plan, for the same reason the device limit is: a plan writes its own
	// speed cap, and an explicit one in the request is the caller overriding it.
	if req.SpeedLimit > 0 {
		if err := rt.mgr.SetUserSpeedLimit(r.Context(), u.ID, req.SpeedLimit); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if strings.TrimSpace(req.Note) != "" {
		if err := rt.mgr.SetUserNote(r.Context(), u.ID, req.Note); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if len(req.Tags) > 0 {
		if err := rt.mgr.SetUserTags(r.Context(), u.ID, req.Tags); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	fresh, err := rt.mgr.Store().GetUser(u.ID)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserViewStatus(w, *fresh, http.StatusCreated)
}

func (rt *Router) apiGetUser(w http.ResponseWriter, _ *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIErr(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
}

func (rt *Router) apiPatchUser(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiPatchUserReq
	if !apiDecode(w, r, &req) {
		return
	}
	cur, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIErr(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeAPIManagerErr(w, err)
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "name cannot be empty")
			return
		}
		if err := rt.mgr.RenameUser(r.Context(), id, name); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	// Limits are set as a unit; unspecified fields keep the user's current value.
	if req.DataLimit != nil || req.ExpireAt != nil || req.DeviceLimit != nil {
		dataLimit, expireAt, deviceLimit := cur.DataLimit, cur.ExpireAt, cur.DeviceLimit
		if req.DataLimit != nil {
			dataLimit = *req.DataLimit
		}
		if req.ExpireAt != nil {
			expireAt = *req.ExpireAt
		}
		if req.DeviceLimit != nil {
			deviceLimit = *req.DeviceLimit
		}
		if deviceLimit < 0 {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "device_limit cannot be negative")
			return
		}
		if err := rt.mgr.SetUserLimits(r.Context(), id, dataLimit, expireAt, deviceLimit); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.SpeedLimit != nil {
		if err := rt.mgr.SetUserSpeedLimit(r.Context(), id, *req.SpeedLimit); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.Enabled != nil {
		if err := rt.mgr.SetUserEnabled(r.Context(), id, *req.Enabled); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.Note != nil {
		if err := rt.mgr.SetUserNote(r.Context(), id, *req.Note); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.Tags != nil {
		if err := rt.mgr.SetUserTags(r.Context(), id, *req.Tags); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
}

func (rt *Router) apiDeleteUser(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteUser(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (rt *Router) apiResetUser(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.ResetTraffic(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
}

func (rt *Router) apiRotateSub(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := rt.mgr.RotateSubToken(r.Context(), id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
}

func (rt *Router) apiApplyPlan(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiApplyPlanReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.ApplyPlanToUser(r.Context(), id, req.PlanID, req.ExtendFromCurrent); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
}

func (rt *Router) apiListPlans(w http.ResponseWriter, r *http.Request) {
	includeDisabled := r.URL.Query().Get("include_disabled") == "true"
	plans, err := rt.mgr.ListTariffPlans(includeDisabled)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if plans == nil {
		plans = []model.TariffPlan{}
	}
	writeAPIData(w, http.StatusOK, plans)
}

func (rt *Router) apiListOrders(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	orders, err := rt.mgr.ListPaymentOrders(status)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIPage(w, r, orders)
}

// apiSavePlan creates a plan (no id) or updates an existing one (id set). The
// body is a full TariffPlan object.
func (rt *Router) apiSavePlan(w http.ResponseWriter, r *http.Request) {
	var p model.TariffPlan
	if !apiDecode(w, r, &p) {
		return
	}
	if err := rt.mgr.SaveTariffPlan(&p); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, p)
}

func (rt *Router) apiDeletePlan(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteTariffPlan(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"deleted": true})
}

// apiCreateOrder opens a payment order for a user+plan. With no provider it's a
// manual order (message carries the payment instructions, admin confirms it);
// with a provider it's a hosted payment whose pay_url the user should be sent to.
func (rt *Router) apiCreateOrder(w http.ResponseWriter, r *http.Request) {
	var req apiCreateOrderReq
	if !apiDecode(w, r, &req) {
		return
	}
	if req.Provider == "" {
		order, msg, err := rt.mgr.RequestPlanPayment(r.Context(), i18n.EN, req.UserID, req.PlanID)
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		writeAPIData(w, http.StatusCreated, map[string]any{"order": order, "message": msg})
		return
	}
	order, err := rt.mgr.StartPlanPayment(r.Context(), i18n.EN, req.UserID, req.PlanID, req.Provider)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusCreated, map[string]any{"order": order, "pay_url": order.PayURL})
}

// apiListProviders lists the enabled automatic payment methods (empty ⇒ only
// manual orders are possible). Keys are usable as the `provider` on create-order.
func (rt *Router) apiListProviders(w http.ResponseWriter, _ *http.Request) {
	methods := rt.mgr.PaymentMethods()
	out := make([]map[string]string, 0, len(methods))
	for _, m := range methods {
		out = append(out, map[string]string{"key": m, "label": rt.mgr.ProviderLabel(m)})
	}
	writeAPIData(w, http.StatusOK, out)
}

func (rt *Router) apiConfirmOrder(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.ConfirmPayment(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"confirmed": true})
}

func (rt *Router) apiCancelOrder(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.CancelPayment(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"cancelled": true})
}

func (rt *Router) apiStatsSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var userID int64
	if s := q.Get("user_id"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid user_id")
			return
		}
		userID = v
	}
	from, to, ok := rt.apiDateRange(w, r)
	if !ok {
		return
	}
	series, err := rt.mgr.StatsSeries(userID, from, to)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if series == nil {
		series = []model.DailyPoint{}
	}
	writeAPIData(w, http.StatusOK, series)
}

// apiStatsNodes is the /v1 twin of the panel's per-server split: which server
// carried the period's traffic. Offered here because a caller building their own
// reporting on top of /v1/stats/series would otherwise have no way to break the same
// numbers down by server.
func (rt *Router) apiStatsNodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var userID int64
	if s := q.Get("user_id"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v < 0 {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid user_id")
			return
		}
		userID = v
	}
	from, to, ok := rt.apiDateRange(w, r)
	if !ok {
		return
	}
	rows, err := rt.mgr.NodeTrafficBreakdown(userID, from, to)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if rows == nil {
		rows = []core.NodeTraffic{}
	}
	writeAPIData(w, http.StatusOK, rows)
}

// apiStatsNodeSeries is the two dimensions at once: a day column and a server
// column. Without it, one line per server meant one call per day.
func (rt *Router) apiStatsNodeSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var userID int64
	if s := q.Get("user_id"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v < 0 {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid user_id")
			return
		}
		userID = v
	}
	from, to, ok := rt.apiDateRange(w, r)
	if !ok {
		return
	}
	rows, err := rt.mgr.NodeTrafficSeries(userID, from, to)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if rows == nil {
		rows = []core.NodeDailyTraffic{}
	}
	writeAPIData(w, http.StatusOK, rows)
}

func (rt *Router) apiStatsUsers(w http.ResponseWriter, r *http.Request) {
	// The same window every other stats route resolves. Reading the two parameters
	// raw — as this one did — means an omitted range becomes `day BETWEEN '' AND ''`,
	// so a caller asking "who used what" with no window gets an empty array rather
	// than the last 30 days, and a malformed date is answered the same way instead of
	// being rejected.
	from, to, ok := rt.apiDateRange(w, r)
	if !ok {
		return
	}
	totals, err := rt.mgr.StatsByUser(from, to)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIPage(w, r, totals)
}

func (rt *Router) apiSummary(w http.ResponseWriter, _ *http.Request) {
	s, err := rt.mgr.Summary()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, s)
}

func (rt *Router) apiSystem(w http.ResponseWriter, _ *http.Request) {
	s, err := rt.mgr.SystemStatus()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, s)
}

func (rt *Router) apiHealthReport(w http.ResponseWriter, _ *http.Request) {
	writeAPIData(w, http.StatusOK, rt.mgr.Health())
}
