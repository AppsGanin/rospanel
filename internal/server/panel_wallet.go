package server

import (
	"net/http"
	"strconv"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The panel's side of the wallet: a user's balance, ledger and referral standing,
// the operator's balance corrections, and the promo code roster.

// userWallet returns a user's balance, referral standing and newest ledger lines.
func (rt *Router) userWallet(w http.ResponseWriter, _ *http.Request, id int64) {
	wal, err := rt.mgr.Wallet(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	txs, err := rt.mgr.BalanceHistory(id, 50)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallet": wal, "history": txs})
}

// adjustUserBalance is an operator's correction: amount_kop of either sign. It moves
// money, so it asks for the password like confirming a payment does.
func (rt *Router) adjustUserBalance(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		AmountKop       int64  `json:"amount_kop"`
		Note            string `json:"note"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyStepUp(w, r, req.CurrentPassword) {
		return
	}
	bal, err := rt.mgr.AdjustBalance(r.Context(), id, req.AmountKop, req.Note)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance_kop": bal})
}

func (rt *Router) setUserAutoRenew(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		On bool `json:"on"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetAutoRenew(r.Context(), id, req.On); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) listPromos(w http.ResponseWriter, _ *http.Request) {
	list, err := rt.mgr.ListPromos()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (rt *Router) savePromo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		model.PromoCode
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p := req.PromoCode
	// A balance code is money handed out, like a balance correction: the same password.
	if p.Kind == model.PromoBalance && !rt.verifyStepUp(w, r, req.CurrentPassword) {
		return
	}
	if err := rt.mgr.SavePromo(&p); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (rt *Router) deletePromo(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeletePromo(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// userReferrals lists the users someone invited, with what they paid.
func (rt *Router) userReferrals(w http.ResponseWriter, _ *http.Request, id int64) {
	list, err := rt.mgr.Referrals(id, 200)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// promoUses lists who used a promo code and what money it brought.
func (rt *Router) promoUses(w http.ResponseWriter, _ *http.Request, id int64) {
	usage, err := rt.mgr.PromoUsage(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

// referralStats sums up the referral programme.
func (rt *Router) referralStats(w http.ResponseWriter, _ *http.Request) {
	st, err := rt.mgr.ReferralStats()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// funnelView is the sales funnel over a period, and what the win-back codes did.
type funnelView struct {
	Funnel   model.Funnel         `json:"funnel"`
	Winback  model.WinbackStats   `json:"winback"`
	BySource []model.SourceFunnel `json:"by_source"`
}

// funnelFor reads the funnel for ?days= (30 by default; 0 = all time).
func (rt *Router) funnelFor(r *http.Request) (funnelView, error) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3650 {
			days = n
		}
	}
	var out funnelView
	var err error
	if out.Funnel, err = rt.mgr.Funnel(days); err != nil {
		return out, err
	}
	if out.BySource, err = rt.mgr.FunnelBySource(days); err != nil {
		return out, err
	}
	out.Winback, err = rt.mgr.WinbackStats()
	return out, err
}

func (rt *Router) paymentFunnel(w http.ResponseWriter, r *http.Request) {
	out, err := rt.funnelFor(r)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// refundOrder returns an order's money to the user's balance. Money moves, so the
// password is asked for, as for a balance correction.
func (rt *Router) refundOrder(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		CancelPlan      bool   `json:"cancel_plan"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !rt.verifyStepUp(w, r, req.CurrentPassword) {
		return
	}
	kop, err := rt.mgr.RefundOrder(r.Context(), id, req.CancelPlan)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refund_kop": kop})
}

// fraudSignals lists the patterns worth a look: trial farms, shared devices,
// self-invites, promo and payment bursts, failing cards, chargebacks.
func (rt *Router) fraudSignals(w http.ResponseWriter, _ *http.Request) {
	sigs, err := rt.mgr.FraudSignals()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sigs)
}
