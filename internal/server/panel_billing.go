package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
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
		"trial_days":    set.BillingTrialDays,
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

// getPayments returns the payment-provider config. Secrets are never returned —
// only whether they're set — plus the webhook URLs to paste into provider panels.
func (rt *Router) getPayments(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	yooURL, cryptoURL := rt.mgr.PaymentWebhookURLs()
	writeJSON(w, http.StatusOK, map[string]any{
		"yookassa_enabled":    set.YooKassaEnabled,
		"yookassa_shop_id":    set.YooKassaShopID,
		"yookassa_test":       set.YooKassaTest,
		"yookassa_key_set":    set.YooKassaSecretKey != "",
		"cryptobot_enabled":   set.CryptoBotEnabled,
		"cryptobot_testnet":   set.CryptoBotTestnet,
		"cryptobot_token_set": set.CryptoBotToken != "",
		"webhook_yookassa":    yooURL,
		"webhook_cryptobot":   cryptoURL,
	})
}

func (rt *Router) savePayments(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YooKassaEnabled   bool   `json:"yookassa_enabled"`
		YooKassaShopID    string `json:"yookassa_shop_id"`
		YooKassaSecretKey string `json:"yookassa_secret_key"` // empty = keep current
		YooKassaTest      bool   `json:"yookassa_test"`
		CryptoBotEnabled  bool   `json:"cryptobot_enabled"`
		CryptoBotToken    string `json:"cryptobot_token"` // empty = keep current
		CryptoBotTestnet  bool   `json:"cryptobot_testnet"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set.YooKassaEnabled = req.YooKassaEnabled
	set.YooKassaShopID = req.YooKassaShopID
	set.YooKassaSecretKey = req.YooKassaSecretKey
	set.YooKassaTest = req.YooKassaTest
	set.CryptoBotEnabled = req.CryptoBotEnabled
	set.CryptoBotToken = req.CryptoBotToken
	set.CryptoBotTestnet = req.CryptoBotTestnet
	if err := rt.mgr.SavePaymentSettings(set); err != nil {
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
	n, err := rt.mgr.MigratePlanUsers(id, req.ToPlanID)
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
