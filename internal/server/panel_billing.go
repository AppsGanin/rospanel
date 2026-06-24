package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

func (rt *Router) getBilling(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	plans, err := rt.mgr.ListTariffPlans(true)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if plans == nil {
		plans = []model.TariffPlan{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       set.BillingEnabled,
		"trial_days":    set.BillingTrialDays,
		"free_plan_id":  set.BillingFreePlanID,
		"trial_plan_id": set.BillingTrialPlanID,
		"payment_note":  set.BillingPaymentNote,
		"plans":         plans,
	})
}

func (rt *Router) saveBilling(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled     bool   `json:"enabled"`
		TrialDays   int    `json:"trial_days"`
		FreePlanID  int64  `json:"free_plan_id"`
		TrialPlanID int64  `json:"trial_plan_id"`
		PaymentNote string `json:"payment_note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set.BillingEnabled = req.Enabled
	set.BillingTrialDays = req.TrialDays
	set.BillingFreePlanID = req.FreePlanID
	set.BillingTrialPlanID = req.TrialPlanID
	set.BillingPaymentNote = strings.TrimSpace(req.PaymentNote)
	if err := rt.mgr.SaveBillingSettings(set); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) saveTariffPlan(w http.ResponseWriter, r *http.Request) {
	var p model.TariffPlan
	if !decodeJSON(w, r, &p) {
		return
	}
	if err := rt.mgr.SaveTariffPlan(&p); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (rt *Router) deleteTariffPlan(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteTariffPlan(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) listPaymentOrders(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	orders, err := rt.mgr.ListPaymentOrders(status)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if orders == nil {
		orders = []model.PaymentOrder{}
	}
	writeJSON(w, http.StatusOK, orders)
}

func (rt *Router) confirmPaymentOrder(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyStepUp(w, r, req.CurrentPassword) {
		return
	}
	if err := rt.mgr.ConfirmPayment(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) cancelPaymentOrder(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyStepUp(w, r, req.CurrentPassword) {
		return
	}
	if err := rt.mgr.CancelPayment(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setUserPlan(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		PlanID int64 `json:"plan_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.ApplyPlanToUser(userID, req.PlanID, false); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
