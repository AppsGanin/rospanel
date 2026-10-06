package telegram

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// The user bot's wallet screens: balance and top-up, renewal from the balance, promo
// codes, the invite link, and paying for a plan from the balance.

// refStartPrefix marks an invite code in a /start argument ("r_<code>").
const refStartPrefix = "r_"

// UserRefLink is the invite link a user shares: the bot, started with their code.
func UserRefLink(botUsername, code string) string {
	base := UserBotLink(botUsername)
	if base == "" || code == "" {
		return ""
	}
	return base + "?start=" + refStartPrefix + code
}

// kop renders kopecks as roubles.
func kop(v int64) string { return model.KopText(v) }

// botName is the bot's own @username, looked up once per token (and again at most
// once a minute while the lookup keeps failing).
func (s *UserService) botName(ctx context.Context, client *Client) string {
	s.mu.Lock()
	name, at, token := s.meName, s.meAt, s.meToken
	s.mu.Unlock()
	if token == client.token && (name != "" || time.Since(at) < time.Minute) {
		return name
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	name = ""
	if me, err := client.GetMe(lookupCtx); err == nil && me != nil {
		name = me.Username
	}
	s.mu.Lock()
	s.meName, s.meAt, s.meToken = name, time.Now(), client.token
	s.mu.Unlock()
	return name
}

// menuRows is the account menu: the plain rows plus whatever the wallet offers.
func (s *UserService) menuRows(set *model.Settings, u model.User, lang i18n.Lang) [][]InlineButton {
	rows := userMenuRows(set, u, lang)
	// Above the last row, the refresh button: the documents, then the mailing switch.
	extraRows := s.mailingRows(set, u.TgChatID, lang)
	if row := s.legalMenuRow(set, lang); row != nil {
		extraRows = append([][]InlineButton{row}, extraRows...)
	}
	rows = append(append(rows[:len(rows)-1:len(rows)-1], extraRows...), rows[len(rows)-1])
	if !set.BillingEnabled {
		return rows
	}
	var extra [][]InlineButton
	if set.WalletEnabled {
		extra = append(extra, []InlineButton{{Text: i18n.T(lang, "user.btnWallet"), CallbackData: "vu:wallet"}})
	}
	var line []InlineButton
	if set.RefEnabled() {
		line = append(line, InlineButton{Text: i18n.T(lang, "user.btnInvite"), CallbackData: "vu:ref"})
	}
	if s.panel.PromosOfferedTo(u.ID) {
		line = append(line, InlineButton{Text: i18n.T(lang, "user.btnPromo"), CallbackData: "vu:promo"})
	}
	if len(line) > 0 {
		extra = append(extra, line)
	}
	// Right after the plans button: the money rows sit together.
	for i, r := range rows {
		if len(r) > 0 && r[0].CallbackData == "vu:plans" {
			out := append([][]InlineButton{}, rows[:i+1]...)
			out = append(out, extra...)
			return append(out, rows[i+1:]...)
		}
	}
	return append(extra, rows...)
}

// handleWalletCallback answers the wallet's buttons; false = not one of them.
func (s *UserService) handleWalletCallback(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, data string) bool {
	switch {
	case data == "vu:wallet":
		s.showWallet(ctx, client, chatID, msgID, set, u)
	case data == "vu:ar:1" || data == "vu:ar:0":
		if err := s.panel.SetAutoRenew(ctx, u.ID, data == "vu:ar:1"); err != nil {
			s.editErr(ctx, client, chatID, msgID, err, "vu:wallet")
			return true
		}
		s.showWallet(ctx, client, chatID, msgID, set, u)
	case data == "vu:topup":
		s.showTopup(ctx, client, chatID, msgID, set)
	case data == "vu:topother":
		lang := s.lang(chatID)
		s.setPending(chatID, "topup")
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.topupEnter", topupMin(set), topupMax),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:wallet"}}})
	case strings.HasPrefix(data, "vu:tamt:"):
		if amount, err := strconv.Atoi(strings.TrimPrefix(data, "vu:tamt:")); err == nil {
			s.pickTopupMethod(ctx, client, chatID, msgID, u, amount)
		}
	case strings.HasPrefix(data, "vu:tpm:"):
		// "vu:tpm:<method>:<amount>"
		if method, amountStr, ok := strings.Cut(strings.TrimPrefix(data, "vu:tpm:"), ":"); ok {
			if amount, err := strconv.Atoi(amountStr); err == nil {
				s.startTopup(ctx, client, chatID, msgID, u, method, amount)
			}
		}
	case data == "vu:promo":
		lang := s.lang(chatID)
		s.setPending(chatID, "promo")
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.promoEnter"),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}}})
	case data == "vu:ref":
		s.showReferral(ctx, client, chatID, msgID, set, u)
	case strings.HasPrefix(data, "vu:bal:"):
		// "vu:bal:<plan>:<expiry the offer was made at>" — a second tap on the same
		// button finds the expiry moved by the first and buys nothing.
		parts := strings.Split(strings.TrimPrefix(data, "vu:bal:"), ":")
		var planID, expect int64
		err1, err2 := strconv.ErrSyntax, strconv.ErrSyntax
		periods := 1
		if len(parts) >= 2 {
			planID, err1 = strconv.ParseInt(parts[0], 10, 64)
			expect, err2 = strconv.ParseInt(parts[1], 10, 64)
		}
		if len(parts) >= 3 {
			periods, _ = strconv.Atoi(parts[2])
		}
		if err1 == nil && err2 == nil && expect >= 0 {
			s.payFromBalance(ctx, client, chatID, msgID, set, u, planID, expect, max(periods, 1))
		} else {
			s.showPlans(ctx, client, chatID, msgID, set, u)
		}
	default:
		return false
	}
	return true
}

// editErr shows an error with a way back.
func (s *UserService) editErr(ctx context.Context, client *Client, chatID, msgID int64, err error, back string) {
	lang := s.lang(chatID)
	s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)),
		[][]InlineButton{
			{{Text: i18n.T(lang, "user.btnBack"), CallbackData: back}},
			{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
		})
}

func (s *UserService) showWallet(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
	if !set.BillingEnabled || !set.WalletEnabled {
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
		return
	}
	w, err := s.panel.Wallet(u.ID)
	if err != nil {
		s.editErr(ctx, client, chatID, msgID, err, "vu:menu")
		return
	}
	// Renewal only means something for a paid plan with a term.
	renewable := false
	if plan, err := s.store.GetTariffPlan(u.PlanID); err == nil {
		renewable = !plan.IsFree() && plan.PeriodDays > 0
	}
	var b strings.Builder
	b.WriteString(i18n.T(lang, "user.walletCard", kop(w.BalanceKop)))
	if renewable && w.AutoRenew {
		b.WriteString("\n\n" + i18n.T(lang, "user.walletAutoOn"))
	} else if renewable {
		b.WriteString("\n\n" + i18n.T(lang, "user.walletAutoOff"))
	}
	if w.RefBonusDays > 0 {
		b.WriteString("\n" + i18n.T(lang, "user.walletBonusDays", w.RefBonusDays))
	}
	if w.PromoCode != "" {
		b.WriteString("\n" + i18n.T(lang, "user.walletPromo", esc(w.PromoCode)))
	}
	if txs, err := s.store.ListBalanceTx(u.ID, 5); err == nil && len(txs) > 0 {
		b.WriteString("\n\n" + i18n.T(lang, "user.walletHistory"))
		for _, t := range txs {
			sign := "+"
			if t.AmountKop < 0 {
				sign = ""
			}
			fmt.Fprintf(&b, "\n%s · %s%s ₽ · %s",
				time.Unix(t.CreatedAt, 0).In(s.panel.Location()).Format("02.01"),
				sign, kop(t.AmountKop), txLabel(t.Kind, lang))
		}
	}
	var rows [][]InlineButton
	if len(s.payMethods()) > 0 {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnTopup"), CallbackData: "vu:topup"}})
	}
	switch {
	case renewable && w.AutoRenew:
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnAutoOff"), CallbackData: "vu:ar:0"}})
	case renewable:
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnAutoOn"), CallbackData: "vu:ar:1"}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}})
	s.edit(ctx, client, chatID, msgID, b.String(), rows)
}

// txLabel names a ledger line's kind.
func txLabel(kind string, lang i18n.Lang) string {
	switch kind {
	case model.TxTopup:
		return i18n.T(lang, "user.tx.topup")
	case model.TxPurchase:
		return i18n.T(lang, "user.tx.purchase")
	case model.TxRenew:
		return i18n.T(lang, "user.tx.renew")
	case model.TxReferral:
		return i18n.T(lang, "user.tx.referral")
	case model.TxPromo:
		return i18n.T(lang, "user.tx.promo")
	case model.TxRefund:
		return i18n.T(lang, "user.tx.refund")
	case model.TxChargeback:
		return i18n.T(lang, "user.tx.chargeback")
	default:
		return i18n.T(lang, "user.tx.admin")
	}
}

// topupMax mirrors the core's ceiling so the prompt can name it.
const topupMax = 1_000_000

func topupMin(set *model.Settings) int { return max(set.WalletTopupMin, 1) }

// topupPresets are the one-tap amounts: the minimum and a few round sums above it.
func topupPresets(set *model.Settings) []int {
	lo := topupMin(set)
	out := []int{lo}
	for _, v := range []int{300, 500, 1000, 2000} {
		if v > lo && len(out) < 4 {
			out = append(out, v)
		}
	}
	return out
}

func (s *UserService) showTopup(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings) {
	lang := s.lang(chatID)
	var row []InlineButton
	for _, v := range topupPresets(set) {
		row = append(row, InlineButton{Text: fmt.Sprintf("%d ₽", v), CallbackData: fmt.Sprintf("vu:tamt:%d", v)})
	}
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.topupPick"),
		[][]InlineButton{
			row,
			{{Text: i18n.T(lang, "user.btnTopupOther"), CallbackData: "vu:topother"}},
			{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:wallet"}},
		})
}

// doTopupAmount handles a typed top-up amount.
func (s *UserService) doTopupAmount(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User, text string) {
	lang := s.lang(chatID)
	amount, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "₽")))
	if err != nil || amount < topupMin(set) || amount > topupMax {
		s.setPending(chatID, "topup")
		s.sendMenu(ctx, client, chatID, i18n.T(lang, "user.topupBadAmount", topupMin(set), topupMax),
			[][]InlineButton{
				{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:wallet"}},
				{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
			})
		return
	}
	s.pickTopupMethod(ctx, client, chatID, 0, u, amount)
}

// pickTopupMethod asks how to pay a top-up (or goes straight on with the only one).
// msgID 0 answers with a new message — the amount was typed, there is nothing to edit.
func (s *UserService) pickTopupMethod(ctx context.Context, client *Client, chatID, msgID int64, u model.User, amount int) {
	lang := s.lang(chatID)
	methods := s.payMethods()
	switch len(methods) {
	case 0:
		s.reply(ctx, client, chatID, msgID, "⚠️ "+esc(i18n.T(lang, "user.noPayMethod")),
			[][]InlineButton{{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:wallet"}}})
	case 1:
		s.startTopup(ctx, client, chatID, msgID, u, methods[0], amount)
	default:
		var rows [][]InlineButton
		for _, p := range methods {
			rows = append(rows, []InlineButton{{Text: s.providerButton(lang, p), CallbackData: fmt.Sprintf("vu:tpm:%s:%d", p, amount)}})
		}
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:wallet"}})
		s.reply(ctx, client, chatID, msgID, i18n.T(lang, "user.topupMethod", amount), rows)
	}
}

func (s *UserService) startTopup(ctx context.Context, client *Client, chatID, msgID int64, u model.User, method string, amount int) {
	lang := s.lang(chatID)
	back := [][]InlineButton{
		{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:wallet"}},
		{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
	}
	if method == sub.ManualPayKey {
		_, msg, err := s.panel.RequestTopupManual(ctx, lang, u.ID, amount)
		if err != nil {
			s.reply(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)), back)
			return
		}
		s.reply(ctx, client, chatID, msgID, esc(msg), back)
		return
	}
	order, err := s.panel.StartTopup(ctx, lang, u.ID, amount, method, "https://t.me/")
	if err != nil {
		s.reply(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)), back)
		return
	}
	s.reply(ctx, client, chatID, msgID, i18n.T(lang, "user.topupPay", order.ID, order.AmountRub),
		[][]InlineButton{{
			{Text: i18n.T(lang, "user.btnPay"), URL: order.PayURL},
			{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"},
		}})
}

// reply edits msgID, or sends a new message when there is none to edit.
func (s *UserService) reply(ctx context.Context, client *Client, chatID, msgID int64, html string, rows [][]InlineButton) {
	if msgID == 0 {
		s.sendMenu(ctx, client, chatID, html, rows)
		return
	}
	s.edit(ctx, client, chatID, msgID, html, rows)
}

// doPromo handles a typed promo code.
func (s *UserService) doPromo(ctx context.Context, client *Client, chatID int64, set *model.Settings, u model.User, code string) {
	lang := s.lang(chatID)
	back := [][]InlineButton{{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}}}
	res, err := s.panel.RedeemPromo(ctx, u.ID, code)
	if err != nil {
		s.sendMenu(ctx, client, chatID, "⚠️ "+esc(core.UserError(err, lang)),
			[][]InlineButton{
				{{Text: i18n.T(lang, "user.btnPromoRetry"), CallbackData: "vu:promo"}},
				{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
			})
		return
	}
	msg := core.PromoMessage(res, lang, s.panel.Location(), esc)
	if model.IsDiscount(res.Kind) {
		back = [][]InlineButton{{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}}}
	}
	s.sendMenu(ctx, client, chatID, msg, back)
}

func (s *UserService) showReferral(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User) {
	lang := s.lang(chatID)
	if !set.RefEnabled() {
		s.editUserMenu(ctx, client, chatID, msgID, set, u)
		return
	}
	code, err := s.panel.RefCode(u.ID)
	if err != nil {
		s.editErr(ctx, client, chatID, msgID, err, "vu:menu")
		return
	}
	link := UserRefLink(s.botName(ctx, client), code)
	w, _ := s.panel.Wallet(u.ID)
	shown := esc(link)
	if link == "" {
		shown = i18n.T(lang, "user.refNoLink")
	}
	msg := i18n.T(lang, "user.refCard", capitalize(refRewardText(set, lang)), shown, w.Invited, w.Paying)
	if w.EarnedKop > 0 {
		msg += i18n.T(lang, "user.refEarned", kop(w.EarnedKop))
	}
	rows := [][]InlineButton{}
	if link != "" {
		share := "https://t.me/share/url?url=" + url.QueryEscape(link) +
			"&text=" + url.QueryEscape(i18n.T(lang, "user.refShareText"))
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnShare"), URL: share}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}})
	s.edit(ctx, client, chatID, msgID, msg, rows)
}

// refRewardText says what one paying invitee earns, in the settings' own terms.
func refRewardText(set *model.Settings, lang i18n.Lang) string {
	switch {
	case set.RefMode == model.RefPercent && set.RefFirstOnly:
		return i18n.T(lang, "user.refRewardPercentFirst", set.RefPercent)
	case set.RefMode == model.RefPercent:
		return i18n.T(lang, "user.refRewardPercent", set.RefPercent)
	case set.RefFirstOnly:
		return i18n.T(lang, "user.refRewardDaysFirst", i18n.TN(lang, "sub.periodDays", set.RefDays))
	default:
		return i18n.T(lang, "user.refRewardDays", i18n.TN(lang, "sub.periodDays", set.RefDays))
	}
}

// quoteLines explains a price that is not simply the plan's: the discount, the part
// the balance pays, and what is left. Empty when the plan costs its price in money.
func quoteLines(q core.PlanQuote, lang i18n.Lang) string {
	if q.DiscountRub == 0 && q.BalanceKop == 0 && q.PeriodDiscountRub == 0 {
		return ""
	}
	var b strings.Builder
	if q.PeriodDiscountRub > 0 {
		b.WriteString("\n" + i18n.T(lang, "user.quotePeriods", q.Periods, q.PeriodDiscountRub))
	}
	if q.DiscountRub > 0 {
		b.WriteString("\n" + i18n.T(lang, "user.quoteDiscount", esc(q.PromoCode), q.DiscountRub))
	}
	if q.BalanceKop > 0 {
		b.WriteString("\n" + i18n.T(lang, "user.quoteBalance", kop(q.BalanceKop)))
	}
	b.WriteString("\n" + i18n.T(lang, "user.quoteToPay", q.MoneyRub))
	return b.String()
}

// confirmBalancePay asks before a plan is paid from the balance.
func (s *UserService) confirmBalancePay(ctx context.Context, client *Client, chatID, msgID int64, u model.User, plan *model.TariffPlan, q core.PlanQuote) {
	lang := s.lang(chatID)
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "user.balanceConfirm", esc(termName(plan.Name, q.Periods)), q.PriceRub)+quoteLines(q, lang),
		[][]InlineButton{
			{{Text: payBalanceLabel(q, lang), CallbackData: fmt.Sprintf("vu:bal:%d:%d:%d", plan.ID, u.ExpireAt, q.Periods)}},
			{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}},
		})
}

func (s *UserService) payFromBalance(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, planID, expect int64, periods int) {
	lang := s.lang(chatID)
	order, err := s.panel.BuyPlanFromBalance(ctx, u.ID, planID, expect, periods)
	if err != nil {
		s.editErr(ctx, client, chatID, msgID, err, "vu:plans")
		return
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "user.paidFromBalance", esc(order.PlanName))+"\n\n"+userSelfCard(u, set, s.panel, lang),
		s.menuRows(set, u, lang))
}

// capitalize upper-cases the first letter, for a phrase that opens a sentence.
func capitalize(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// payBalanceLabel names the confirm button: paying from the balance, or — when a
// discount takes the whole price — just taking the plan.
func payBalanceLabel(q core.PlanQuote, lang i18n.Lang) string {
	if q.TotalRub == 0 {
		return i18n.T(lang, "user.btnGetFree")
	}
	return i18n.T(lang, "user.btnPayBalance", kop(q.BalanceKop))
}

// termName is a plan's name with the number of periods, when more than one.
func termName(plan string, periods int) string {
	if periods > 1 {
		return fmt.Sprintf("%s × %d", plan, periods)
	}
	return plan
}
