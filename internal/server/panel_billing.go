package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/store"
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
	// Refunds go to the balance: the page offers them only while there is one.
	wallet := false
	if set, err := rt.mgr.Settings(); err == nil {
		wallet = set.WalletEnabled
	}
	writeJSON(w, http.StatusOK, struct {
		*model.PaymentStats
		Wallet bool `json:"wallet"`
	}{stats, wallet})
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
		"manual":        set.BillingManualEnabled,
		"manual_label":  set.BillingManualLabel,
		"wallet":        set.WalletEnabled,
		"topup_min":     set.WalletTopupMin,
		"ref_mode":      set.RefMode,
		"ref_percent":   set.RefPercent,
		"ref_days":      set.RefDays,
		"ref_first":     set.RefFirstOnly,
		"periods":       periodOffersOrEmpty(set.BillingPeriods),
		"winback":       set.Winback,
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
		Manual      bool   `json:"manual"`
		ManualLabel string `json:"manual_label"`
		Wallet      *bool  `json:"wallet"`
		TopupMin    int    `json:"topup_min"`
		RefMode     string `json:"ref_mode"`
		RefPercent  int    `json:"ref_percent"`
		RefDays     int    `json:"ref_days"`
		RefFirst    *bool  `json:"ref_first"`
		// Periods replaces the multi-period discounts; left out (null) keeps them.
		Periods *[]model.PeriodOffer   `json:"periods"`
		Winback *model.WinbackSettings `json:"winback"` // left out keeps it
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
	set.BillingManualEnabled = req.Manual
	set.BillingManualLabel = strings.TrimSpace(req.ManualLabel)
	if req.Wallet != nil {
		set.WalletEnabled = *req.Wallet
	}
	// A value left out (zero) keeps what is stored: a caller written before these
	// fields existed must not have its save refused over them.
	if req.TopupMin != 0 {
		set.WalletTopupMin = req.TopupMin
	}
	if req.RefMode != "" {
		set.RefMode = req.RefMode
	}
	if req.RefPercent != 0 {
		set.RefPercent = req.RefPercent
	}
	if req.RefDays != 0 {
		set.RefDays = req.RefDays
	}
	if req.RefFirst != nil {
		set.RefFirstOnly = *req.RefFirst
	}
	if req.Periods != nil {
		set.BillingPeriods = *req.Periods
	}
	if req.Winback != nil {
		set.Winback = *req.Winback
	}
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
	out, err := rt.paymentProvidersView()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// paymentProvidersView builds that description. Shared with the external API so the
// panel form and an integration's form are generated from one place — a provider
// whose fields drifted between the two would be configurable in one and not the other.
func (rt *Router) paymentProvidersView() ([]map[string]any, error) {
	descs, saved, err := rt.mgr.PaymentProviders()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(descs))
	for _, d := range descs {
		p := saved[d.Key]
		fields := make([]map[string]any, 0, len(d.Fields)+1)
		// Universal, injected for every provider: the custom pay-button name shown to
		// users. Optional — empty falls back to the provider's default label.
		fields = append(fields, map[string]any{
			"key":         payments.DisplayNameKey,
			"label":       "payField.displayName",
			"kind":        string(payments.FieldText),
			"placeholder": d.Label,
			"help":        "payHelp.displayName",
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
	return out, nil
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

// periodOffersOrEmpty keeps "no offers" a list in JSON, not null.
func periodOffersOrEmpty(o []model.PeriodOffer) []model.PeriodOffer {
	if o == nil {
		return []model.PeriodOffer{}
	}
	return o
}

// listPaymentCallbacks is the journal of provider callbacks: what each one said and
// what the panel did with it. Filters: provider, order, failed=1 (only the ones that
// went wrong), before (an id, for the next page).
func (rt *Router) listPaymentCallbacks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.PaymentWebhookFilter{Provider: q.Get("provider"), Failed: q.Get("failed") == "1"}
	f.OrderID, _ = strconv.ParseInt(q.Get("order"), 10, 64)
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	out, err := rt.mgr.PaymentWebhooks(f)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
