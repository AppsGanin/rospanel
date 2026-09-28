package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The wallet over the external API: a user's balance and ledger, balance
// corrections, and the promo code roster.

type (
	apiWalletResp struct {
		Wallet  model.Wallet      `json:"wallet"`
		History []model.BalanceTx `json:"history"`
	}
	apiBalanceReq struct {
		AmountKop int64  `json:"amount_kop"` // kopecks, either sign
		Note      string `json:"note"`
	}
	apiBalanceResp struct {
		BalanceKop int64 `json:"balance_kop"`
	}
)

func (rt *Router) apiUserWallet(w http.ResponseWriter, _ *http.Request, id int64) {
	if _, err := rt.mgr.Store().GetUser(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	wal, err := rt.mgr.Wallet(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	txs, err := rt.mgr.BalanceHistory(id, 100)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiWalletResp{Wallet: wal, History: txs})
}

func (rt *Router) apiAdjustBalance(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiBalanceReq
	if !apiDecode(w, r, &req) {
		return
	}
	bal, err := rt.mgr.AdjustBalance(r.Context(), id, req.AmountKop, req.Note)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiBalanceResp{BalanceKop: bal})
}

func (rt *Router) apiListPromos(w http.ResponseWriter, _ *http.Request) {
	list, err := rt.mgr.ListPromos()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, list)
}

func (rt *Router) apiSavePromo(w http.ResponseWriter, r *http.Request) {
	var p model.PromoCode
	if !apiDecode(w, r, &p) {
		return
	}
	if err := rt.mgr.SavePromo(&p); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, p)
}

func (rt *Router) apiDeletePromo(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeletePromo(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

type (
	apiRefundReq struct {
		CancelPlan bool `json:"cancel_plan"` // also end the plan the order bought, if still active
	}
	apiRefundResp struct {
		RefundKop int64 `json:"refund_kop"`
	}
)

func (rt *Router) apiUserReferrals(w http.ResponseWriter, _ *http.Request, id int64) {
	if _, err := rt.mgr.Store().GetUser(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	list, err := rt.mgr.Referrals(id, 200)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, list)
}

func (rt *Router) apiPromoUses(w http.ResponseWriter, _ *http.Request, id int64) {
	usage, err := rt.mgr.PromoUsage(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, usage)
}

func (rt *Router) apiReferralStats(w http.ResponseWriter, _ *http.Request) {
	st, err := rt.mgr.ReferralStats()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, st)
}

func (rt *Router) apiFunnel(w http.ResponseWriter, r *http.Request) {
	out, err := rt.funnelFor(r)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, out)
}

func (rt *Router) apiRefundOrder(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiRefundReq
	if !apiDecode(w, r, &req) {
		return
	}
	kop, err := rt.mgr.RefundOrder(r.Context(), id, req.CancelPlan)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiRefundResp{RefundKop: kop})
}
