package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
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
	}
	// apiPatchUserReq fields are pointers: a nil field is left unchanged, so a
	// caller can update just one attribute.
	apiPatchUserReq struct {
		Name        *string `json:"name,omitempty"`
		Enabled     *bool   `json:"enabled,omitempty"`
		DataLimit   *int64  `json:"data_limit,omitempty"`
		ExpireAt    *int64  `json:"expire_at,omitempty"`
		DeviceLimit *int    `json:"device_limit,omitempty"`
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
	mux.HandleFunc("GET /v1/openapi.json", rt.apiOpenAPI)
	mux.HandleFunc("GET /v1/docs", rt.apiDocs)
	mux.Handle("/", rt.apiAuth(rt.apiMux()))
	return mux
}

// apiMux builds the /v1 route table for the external API. Auth is applied by the
// caller (apiAuth), so every route here already has a valid key.
func (rt *Router) apiMux() http.Handler {
	mux := http.NewServeMux()
	id := func(pattern string, h func(http.ResponseWriter, *http.Request, int64)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid id")
				return
			}
			h(w, r, v)
		})
	}

	mux.HandleFunc("GET /v1/health", rt.apiHealth)

	mux.HandleFunc("GET /v1/users", rt.apiListUsers)
	mux.HandleFunc("POST /v1/users", rt.apiCreateUser)
	mux.HandleFunc("POST /v1/users/bulk", rt.apiBulkUsers)
	id("GET /v1/users/{id}", rt.apiGetUser)
	id("PATCH /v1/users/{id}", rt.apiPatchUser)
	id("DELETE /v1/users/{id}", rt.apiDeleteUser)
	id("POST /v1/users/{id}/reset", rt.apiResetUser)
	id("POST /v1/users/{id}/reset-period", rt.apiSetResetPeriod)
	id("POST /v1/users/{id}/rotate-sub", rt.apiRotateSub)
	id("POST /v1/users/{id}/plan", rt.apiApplyPlan)
	id("GET /v1/users/{id}/connections", rt.apiUserConnections)

	mux.HandleFunc("GET /v1/billing/providers", rt.apiListProviders)
	mux.HandleFunc("GET /v1/billing/plans", rt.apiListPlans)
	mux.HandleFunc("POST /v1/billing/plans", rt.apiSavePlan)
	id("DELETE /v1/billing/plans/{id}", rt.apiDeletePlan)
	mux.HandleFunc("GET /v1/billing/orders", rt.apiListOrders)
	mux.HandleFunc("POST /v1/billing/orders", rt.apiCreateOrder)
	id("POST /v1/billing/orders/{id}/confirm", rt.apiConfirmOrder)
	id("POST /v1/billing/orders/{id}/cancel", rt.apiCancelOrder)

	mux.HandleFunc("GET /v1/stats/series", rt.apiStatsSeries)
	mux.HandleFunc("GET /v1/stats/users", rt.apiStatsUsers)

	mux.HandleFunc("GET /v1/summary", rt.apiSummary)
	mux.HandleFunc("GET /v1/system", rt.apiSystem)
	mux.HandleFunc("GET /v1/health/report", rt.apiHealthReport)

	// Any unmatched /v1 path (or a wrong method) returns a JSON 404 in-envelope
	// rather than the default plain-text one.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIErr(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return mux
}

// apiAuth authenticates every API request by its bearer key. The key may be sent
// as "Authorization: Bearer <key>" or the "X-API-Key: <key>" header. An
// absent/invalid/revoked key gets a 401; the lookup is a constant-time hash match
// in the store (the raw key never touches the DB).
func (rt *Router) apiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := apiKeyFromRequest(r)
		if key == "" {
			writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "missing API key")
			return
		}
		ak, err := rt.mgr.Store().LookupAPIKey(key)
		if err != nil {
			writeAPIErr(w, http.StatusInternalServerError, "internal", "authentication failed")
			return
		}
		if ak == nil {
			writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked API key")
			return
		}
		next.ServeHTTP(w, r)
	})
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

// writeAPIManagerErr maps a core.Manager error onto the API envelope: a
// ValidationError (bad caller input) → 400 bad_request, anything else → 500.
func writeAPIManagerErr(w http.ResponseWriter, err error) {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeAPIErr(w, http.StatusInternalServerError, "internal", err.Error())
}

// apiDecode reads a size-limited JSON body into dst using the API envelope for
// errors. Like decodeJSON it requires application/json.
func apiDecode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeAPIErr(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return false
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(dst); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return false
	}
	return true
}

// apiUserView builds the share-link-carrying view for a user, reusing the panel's
// makeUserView so the API and panel expose identical fields. The Telegram user-bot
// @username is left unresolved ("") — the API view doesn't surface bot deep links.
func (rt *Router) apiUserView(w http.ResponseWriter, u model.User) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeAPIData(w, http.StatusOK, makeUserView(u, set, ""))
}

// ---- handlers ----

func (rt *Router) apiHealth(w http.ResponseWriter, _ *http.Request) {
	writeAPIData(w, http.StatusOK, map[string]any{"status": "ok"})
}

// apiListUsers lists users with optional filtering (?status, ?search) and
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
	filtered := users[:0:0]
	for _, u := range users {
		if status != "" && u.Status != status {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(u.Name), search) {
			continue
		}
		filtered = append(filtered, u)
	}
	total := len(filtered)

	// Window the slice. limit<=0 means "all remaining from offset".
	offset := clampNonNeg(atoiOr(q.Get("offset"), 0))
	limit := atoiOr(q.Get("limit"), 0)
	if offset > total {
		offset = total
	}
	page := filtered[offset:]
	if limit > 0 && limit < len(page) {
		page = page[:limit]
	}

	views := make([]userView, 0, len(page))
	for _, u := range page {
		views = append(views, makeUserView(u, set, ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": views,
		"meta": map[string]int{"total": total, "offset": offset, "limit": limit},
	})
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
	affected, err := rt.mgr.BulkUserAction(req.IDs, req.Action, req.Days)
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
	if err := rt.mgr.SetResetPeriod(id, req.Period); err != nil {
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

func (rt *Router) apiUserConnections(w http.ResponseWriter, _ *http.Request, id int64) {
	conns, err := rt.mgr.Connections(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if conns == nil {
		conns = []model.Connection{}
	}
	writeAPIData(w, http.StatusOK, conns)
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
	u, err := rt.mgr.CreateUser(req.Name, req.DataLimit, req.ExpireAt)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *u)
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
		if err := rt.mgr.RenameUser(id, name); err != nil {
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
		if err := rt.mgr.SetUserLimits(id, dataLimit, expireAt, deviceLimit); err != nil {
			writeAPIManagerErr(w, err)
			return
		}
	}
	if req.Enabled != nil {
		if err := rt.mgr.SetUserEnabled(id, *req.Enabled); err != nil {
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

func (rt *Router) apiDeleteUser(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteUser(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (rt *Router) apiResetUser(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.ResetTraffic(id); err != nil {
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

func (rt *Router) apiRotateSub(w http.ResponseWriter, _ *http.Request, id int64) {
	u, err := rt.mgr.RotateSubToken(id)
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
	if err := rt.mgr.ApplyPlanToUser(id, req.PlanID, req.ExtendFromCurrent); err != nil {
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
	if orders == nil {
		orders = []model.PaymentOrder{}
	}
	writeAPIData(w, http.StatusOK, orders)
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
		order, msg, err := rt.mgr.RequestPlanPayment(req.UserID, req.PlanID)
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		writeAPIData(w, http.StatusCreated, map[string]any{"order": order, "message": msg})
		return
	}
	order, err := rt.mgr.StartPlanPayment(req.UserID, req.PlanID, req.Provider)
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
		out = append(out, map[string]string{"key": m, "label": payProviderLabel(m)})
	}
	writeAPIData(w, http.StatusOK, out)
}

func (rt *Router) apiConfirmOrder(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.ConfirmPayment(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"confirmed": true})
}

func (rt *Router) apiCancelOrder(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.CancelPayment(id); err != nil {
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
	series, err := rt.mgr.StatsSeries(userID, q.Get("from"), q.Get("to"))
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if series == nil {
		series = []model.DailyPoint{}
	}
	writeAPIData(w, http.StatusOK, series)
}

func (rt *Router) apiStatsUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	totals, err := rt.mgr.StatsByUser(q.Get("from"), q.Get("to"))
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if totals == nil {
		totals = []model.UserTotal{}
	}
	writeAPIData(w, http.StatusOK, totals)
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
