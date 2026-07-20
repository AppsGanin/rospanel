package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
)

// paymentStats returns the revenue dashboard for the Payments page.
func (rt *Router) paymentStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := rt.mgr.PaymentStats()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if stats.ByProvider == nil {
		stats.ByProvider = []model.ProviderStat{}
	}
	writeJSON(w, http.StatusOK, stats)
}

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
	// Per-plan user counts so the UI can show how many users are on each plan and
	// offer to migrate them before a plan is disabled/deleted.
	planUsers := map[string]int{}
	for _, p := range plans {
		if n, err := rt.mgr.Store().CountUsersOnPlan(p.ID); err == nil && n > 0 {
			planUsers[strconv.FormatInt(p.ID, 10)] = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       set.BillingEnabled,
		"free_plan_id":  set.BillingFreePlanID,
		"trial_plan_id": set.BillingTrialPlanID,
		"payment_note":  set.BillingPaymentNote,
		"plans":         plans,
		"plan_users":    planUsers,
	})
}

func (rt *Router) saveBilling(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled     bool   `json:"enabled"`
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
	set.BillingFreePlanID = req.FreePlanID
	set.BillingTrialPlanID = req.TrialPlanID
	set.BillingPaymentNote = strings.TrimSpace(req.PaymentNote)
	if err := rt.mgr.SaveBillingSettings(set); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// getPayments returns every provider in the registry with its settings form
// (fields), whether it's enabled, its current non-secret values, which secrets are
// set, and the webhook URL to paste into the provider's dashboard. Secret values
// themselves are never returned — only whether they hold a value.
func (rt *Router) getPayments(w http.ResponseWriter, _ *http.Request) {
	descs, saved, err := rt.mgr.PaymentProviders()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(descs))
	for _, d := range descs {
		p := saved[d.Key]
		fields := make([]map[string]any, 0, len(d.Fields)+1)
		// Universal, injected for every provider: the custom pay-button name shown to
		// users. Optional — empty falls back to the provider's default label.
		fields = append(fields, map[string]any{
			"key":         payments.DisplayNameKey,
			"label":       "Название для кнопки оплаты",
			"kind":        string(payments.FieldText),
			"placeholder": d.Label,
			"help":        "Показывается пользователю в боте и на странице подписки. Пусто — «" + d.Label + "».",
			"optional":    true,
			"value":       p.Config[payments.DisplayNameKey],
		})
		for _, f := range d.Fields {
			fj := map[string]any{
				"key":         f.Key,
				"label":       f.Label,
				"kind":        string(f.Kind),
				"placeholder": f.Placeholder,
				"help":        f.Help,
				"optional":    f.Optional,
			}
			// Secrets report only whether a value is stored; everything else round-trips
			// its value so the form can show what's set.
			switch f.Kind {
			case payments.FieldSecret:
				fj["is_set"] = p.Config[f.Key] != ""
			case payments.FieldBool:
				fj["value"] = p.Config[f.Key] == "1"
			default: // text, select
				fj["value"] = p.Config[f.Key]
			}
			if len(f.Options) > 0 {
				opts := make([]map[string]string, 0, len(f.Options))
				for _, o := range f.Options {
					opts = append(opts, map[string]string{"value": o.Value, "label": o.Label})
				}
				fj["options"] = opts
			}
			fields = append(fields, fj)
		}
		out = append(out, map[string]any{
			"key":         d.Key,
			"label":       d.Label,
			"note":        d.Note,
			"enabled":     p.Enabled,
			"fields":      fields,
			"webhook_url": rt.mgr.PaymentWebhookURL(d.Key),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (rt *Router) savePayments(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key     string            `json:"key"`
		Enabled bool              `json:"enabled"`
		Config  map[string]string `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SavePaymentProvider(req.Key, req.Enabled, req.Config); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setPaySecret(rt.mgr.PaymentWebhookSecret())
	rt.getPayments(w, r)
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

// migratePlanUsers moves all users on {id} to the plan in the body, so a retired
// plan can be emptied before disabling/deleting it.
func (rt *Router) migratePlanUsers(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		ToPlanID int64 `json:"to_plan_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	n, err := rt.mgr.MigratePlanUsers(r.Context(), id, req.ToPlanID)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"migrated": n})
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
	if err := rt.mgr.ConfirmPayment(r.Context(), id); err != nil {
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
	if err := rt.mgr.CancelPayment(r.Context(), id); err != nil {
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
	if err := rt.mgr.ApplyPlanToUser(r.Context(), userID, req.PlanID, false); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
