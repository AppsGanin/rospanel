package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// The half of billing that was panel-only: which plan is the free/trial one, the
// payment note users are shown, the revenue totals, provider configuration, moving
// users off a retired plan, and cancelling a subscription.
//
// Without these an integration could sell (create orders, confirm payments) but not
// SET UP selling, and could not undo a sale either — cancellation lived only on the
// user's own subscription page and in the user bot.

type (
	// apiBillingSettingsReq is the billing configuration. Every field is required on
	// write: this is a settings object, and a partial PATCH-style write here would make
	// "no free plan" (a real, meaningful state) indistinguishable from "unspecified".
	apiBillingSettingsReq struct {
		Enabled     bool   `json:"enabled"`
		FreePlanID  int64  `json:"free_plan_id"`  // 0 = none
		TrialPlanID int64  `json:"trial_plan_id"` // 0 = none
		PaymentNote string `json:"payment_note"`  // shown with manual payment instructions
	}
	apiMigratePlanReq struct {
		ToPlanID int64 `json:"to_plan_id"`
	}
	apiMigratedResp struct {
		Migrated int `json:"migrated"`
	}
	apiSaveProviderReq struct {
		Key     string            `json:"key"`
		Enabled bool              `json:"enabled"`
		Config  map[string]string `json:"config"`
	}
)

func (rt *Router) apiGetBillingSettings(w http.ResponseWriter, _ *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiBillingSettingsReq{
		Enabled:     set.BillingEnabled,
		FreePlanID:  set.BillingFreePlanID,
		TrialPlanID: set.BillingTrialPlanID,
		PaymentNote: set.BillingPaymentNote,
	})
}

func (rt *Router) apiSaveBillingSettings(w http.ResponseWriter, r *http.Request) {
	var req apiBillingSettingsReq
	if !apiDecode(w, r, &req) {
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	set.BillingEnabled = req.Enabled
	set.BillingFreePlanID = req.FreePlanID
	set.BillingTrialPlanID = req.TrialPlanID
	set.BillingPaymentNote = strings.TrimSpace(req.PaymentNote)
	// Designating a plan free/trial also makes it free and re-applies it to everyone
	// already on it — see core.SaveBillingSettings.
	if err := rt.mgr.SaveBillingSettings(set); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiGetBillingSettings(w, r)
}

// apiMigratePlanUsers moves everyone on {id} to another plan, applying the target's
// limits, period and access groups. The only way to empty a plan before deleting it —
// DELETE refuses while users are still on it.
func (rt *Router) apiMigratePlanUsers(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiMigratePlanReq
	if !apiDecode(w, r, &req) {
		return
	}
	n, err := rt.mgr.MigratePlanUsers(r.Context(), id, req.ToPlanID)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiMigratedResp{Migrated: n})
}

// apiGetOrder returns one order. Polling a single order beats listing every pending
// one to find it, which is what a caller that just created an order had to do.
func (rt *Router) apiGetOrder(w http.ResponseWriter, _ *http.Request, id int64) {
	order, err := rt.mgr.Store().GetPaymentOrder(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIErr(w, http.StatusNotFound, "not_found", "order not found")
			return
		}
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, order)
}

// apiPaymentStats is the revenue dashboard: all-time and per-provider paid totals,
// today's and this month's takings (in the operator's timezone) and the pending
// backlog.
func (rt *Router) apiPaymentStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := rt.mgr.PaymentStats()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if stats.ByProvider == nil {
		stats.ByProvider = []model.ProviderStat{}
	}
	writeAPIData(w, http.StatusOK, stats)
}

// apiPaymentProviders describes every provider in the registry: its settings form,
// whether it is enabled, its current non-secret values, which secrets are set (never
// their values) and the webhook URL to paste into the provider's dashboard.
//
// The field list is per provider and generated from the registry, so the payload is
// deliberately free-form — a new provider must not need a new API version.
func (rt *Router) apiPaymentProviders(w http.ResponseWriter, _ *http.Request) {
	out, err := rt.paymentProvidersView()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"providers": out})
}

// apiSavePaymentProvider stores one provider's settings. Secrets left empty keep
// their stored value, so a caller can toggle a provider without re-sending its keys.
func (rt *Router) apiSavePaymentProvider(w http.ResponseWriter, r *http.Request) {
	var req apiSaveProviderReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SavePaymentProvider(req.Key, req.Enabled, req.Config); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	// The panel keeps its cached webhook secret in step; do the same here so a
	// provider configured through the API can verify callbacks immediately.
	rt.setPaySecret(rt.mgr.PaymentWebhookSecret())
	rt.apiPaymentProviders(w, r)
}

// apiCancelUserPlan ends a paid subscription now: the user drops to the free plan
// (losing the remaining paid time), or, with no free plan configured, their access
// ends. Distinct from applying the free plan by hand — this is audited as a
// cancellation and emits plan.cancelled, which billing integrations key off.
func (rt *Router) apiCancelUserPlan(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIErr(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeAPIManagerErr(w, err)
		return
	}
	if err := rt.mgr.CancelUserPlan(r.Context(), u.ID); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	after, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserView(w, *after)
}
