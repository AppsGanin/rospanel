package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// The wallet over the external API: a user's balance and ledger, balance
// corrections, and the promo code roster.

type (
	apiWalletResp struct {
		Wallet  model.Wallet      `json:"wallet"`
		History []model.BalanceTx `json:"history"`
		// RefLink is the user's invite link to the bot, while the referral programme
		// and the user bot are on.
		RefLink string `json:"ref_link,omitempty"`
	}
	apiAutoRenewReq struct {
		On *bool `json:"on"` // renew the plan from the balance when its term ends
	}
	apiRedeemReq struct {
		Code string `json:"code"`
		Lang string `json:"lang,omitempty"` // ru | en: the language of message (default en)
	}
	apiRedeemResp struct {
		Result  *core.PromoResult `json:"result"`
		Message string            `json:"message"` // what the code did, in words
	}
	apiTelegramReq struct {
		ChatID *int64 `json:"chat_id"` // the user's Telegram ID; 0 unlinks
	}
	apiSourceReq struct {
		Source string `json:"source"` // lower case, letters, digits, "_" and "-", up to 64
	}
	// apiExtrasResp is what can be bought on top of the plan held.
	apiExtrasResp struct {
		Changes []core.ChangeOffer `json:"changes"`
		Addons  core.AddonOffers   `json:"addons"`
	}
	apiReferrerReq struct {
		ReferrerID int64  `json:"referrer_id,omitempty"` // the inviting user's id
		RefCode    string `json:"ref_code,omitempty"`    // or their invite code, as the /start link carries it
	}
	// apiPlanQuotes is what one plan costs the user now, for each term on sale.
	apiPlanQuotes struct {
		PlanID   int64            `json:"plan_id"`
		PlanName string           `json:"plan_name"`
		Quotes   []core.PlanQuote `json:"quotes"`
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
	// The invite code exists whenever the programme runs — an outside bot builds its
	// own link from it, with or without the panel's user bot.
	_, _ = rt.mgr.RefCode(id)
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
	resp := apiWalletResp{Wallet: wal, History: txs}
	if set, err := rt.mgr.Settings(); err == nil {
		resp.RefLink = rt.userRefLink(set, id)
	}
	writeAPIData(w, http.StatusOK, resp)
}

// apiSetAutoRenew turns renewal from the balance on or off, and answers the wallet.
func (rt *Router) apiSetAutoRenew(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiAutoRenewReq
	if !apiDecode(w, r, &req) {
		return
	}
	if req.On == nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "on is required")
		return
	}
	if _, err := rt.mgr.Store().GetUser(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if err := rt.mgr.SetAutoRenew(r.Context(), id, *req.On); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserWallet(w, r, id)
}

// apiLinkTelegram binds (or with 0 unbinds) the user's Telegram, and answers the user.
func (rt *Router) apiLinkTelegram(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiTelegramReq
	if !apiDecode(w, r, &req) {
		return
	}
	if req.ChatID == nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "chat_id is required (0 unlinks)")
		return
	}
	if err := rt.mgr.LinkUserTelegram(r.Context(), id, *req.ChatID); err != nil {
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

// apiSetReferrer records who invited the user, and answers the wallet.
func (rt *Router) apiSetReferrer(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiReferrerReq
	if !apiDecode(w, r, &req) {
		return
	}
	if _, err := rt.mgr.Store().GetUser(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if (req.ReferrerID == 0) == (strings.TrimSpace(req.RefCode) == "") {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "referrer_id or ref_code, one of them")
		return
	}
	if err := rt.mgr.SetReferrer(r.Context(), id, req.ReferrerID, req.RefCode); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.apiUserWallet(w, r, id)
}

// apiUserSubscription is the user's subscription page as data — for a page drawn
// elsewhere: status, traffic, term, the subscription link, one-tap imports into each
// app, every config, devices, the payment block and the Telegram bind link, worded in
// ?lang (en by default), under the subscription settings the panel's page keeps.
func (rt *Router) apiUserSubscription(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	lang := i18n.EN
	if v := r.URL.Query().Get("lang"); v != "" {
		lang = i18n.Normalize(v)
	}
	servers, err := rt.subServers(set, u.ID, "")
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if hasTurnInbound(servers) {
		_ = rt.mgr.ClaimTunnelIdentity(u) // the TURN link carries the user's tunnel key
	}
	view, err := sub.PageView(*u, set, servers, rt.buildBilling(*u, set, lang, rt.mgr.PaymentMethods()),
		rt.buildDevices(*u, set, lang), lang)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	view.RefCode, _ = rt.mgr.RefCode(u.ID)
	view.TGLink = rt.buildAccess(r, *u, set).TGLink
	view.TGLinked = u.TgChatID != 0
	view.TermsURL, view.PrivacyURL = rt.legalLinks(set)
	writeAPIData(w, http.StatusOK, view)
}

// apiRedeemPromo enters a promo code for the user, as the bot or the subscription
// page would: a discount attaches to their next payment, days go onto the plan, a
// balance code credits the balance.
func (rt *Router) apiRedeemPromo(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiRedeemReq
	if !apiDecode(w, r, &req) {
		return
	}
	if _, err := rt.mgr.Store().GetUser(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	res, err := rt.mgr.RedeemPromo(r.Context(), id, req.Code)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	lang := i18n.EN
	if req.Lang != "" {
		lang = i18n.Normalize(req.Lang)
	}
	writeAPIData(w, http.StatusOK, apiRedeemResp{
		Result:  res,
		Message: core.PromoMessage(res, lang, rt.mgr.Location(), func(s string) string { return s }),
	})
}

// apiUserQuotes prices what the user can buy now — the plan they hold, while a paid
// one runs; otherwise every paid plan on sale — with their discount code and balance,
// one quote per term on sale.
func (rt *Router) apiUserQuotes(w http.ResponseWriter, _ *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	var plans []model.TariffPlan
	if active := rt.mgr.ActivePaidPlan(*u); active != nil {
		if active.PeriodDays > 0 { // a lifetime plan has nothing to renew
			plans = []model.TariffPlan{*active}
		}
	} else {
		all, err := rt.mgr.ListTariffPlans(false)
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		for _, p := range all {
			if !p.IsFree() {
				plans = append(plans, p)
			}
		}
	}
	out := []apiPlanQuotes{}
	for i := range plans {
		p := &plans[i]
		quotes := rt.mgr.PeriodOffers(*u, p)
		if len(quotes) == 0 {
			quotes = []core.PlanQuote{rt.mgr.QuotePlan(*u, p)}
		}
		out = append(out, apiPlanQuotes{PlanID: p.ID, PlanName: p.Name, Quotes: quotes})
	}
	writeAPIData(w, http.StatusOK, out)
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

// apiSetSource records where a user came from — the tag an outside bot's /start
// carried — and answers the tag as stored.
func (rt *Router) apiSetSource(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiSourceReq
	if !apiDecode(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserSource(r.Context(), id, req.Source); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiSourceReq{Source: rt.mgr.UserSource(id)})
}

// apiUserExtras lists the plan changes and add-ons the user can buy now; each is
// bought with POST /v1/billing/orders (kind change, devices or traffic).
func (rt *Router) apiUserExtras(w http.ResponseWriter, _ *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	out := apiExtrasResp{Changes: rt.mgr.ChangeOffers(*u), Addons: rt.mgr.Addons(*u)}
	if out.Changes == nil {
		out.Changes = []core.ChangeOffer{}
	}
	if out.Addons.Packs == nil {
		out.Addons.Packs = []model.TrafficPack{}
	}
	writeAPIData(w, http.StatusOK, out)
}
