package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// Purchases beyond a plain plan: a plan with extra devices, a plan change, devices
// and traffic added to the plan held. Each travels in callback data as a short code:
//
//	p<plan>.<periods>.<devices>  a plan with extra devices
//	c<plan>                       a change to another plan (keeping the devices held)
//	d<n>                          n devices added
//	t<i>                          traffic pack i
//
// "vu:x:<code>" shows the price and the ways to pay, "vu:px:<method>:<code>" pays
// with a method, "vu:bx:<expiry>:<code>" pays from the balance (or takes a free
// change), the expiry being the one the offer was made at.

// purchaseCode encodes a purchase for callback data.
func purchaseCode(p core.Purchase) string {
	switch p.Kind {
	case model.OrderChange:
		return fmt.Sprintf("c%d", p.PlanID)
	case model.OrderDevices:
		return fmt.Sprintf("d%d", p.Devices)
	case model.OrderTraffic:
		return fmt.Sprintf("t%d", p.Pack)
	}
	return fmt.Sprintf("p%d.%d.%d", p.PlanID, max(p.Periods, 1), max(p.Devices, 0))
}

// parsePurchase decodes purchaseCode.
func parsePurchase(code string) (core.Purchase, bool) {
	if code == "" {
		return core.Purchase{}, false
	}
	rest := code[1:]
	switch code[0] {
	case 'c':
		id, err := strconv.ParseInt(rest, 10, 64)
		return core.Purchase{Kind: model.OrderChange, PlanID: id, Devices: core.KeepDevices}, err == nil && id > 0
	case 'd':
		n, err := strconv.Atoi(rest)
		return core.Purchase{Kind: model.OrderDevices, Devices: n}, err == nil && n > 0
	case 't':
		i, err := strconv.Atoi(rest)
		return core.Purchase{Kind: model.OrderTraffic, Pack: i}, err == nil && i >= 0
	case 'p':
		parts := strings.Split(rest, ".")
		if len(parts) != 3 {
			return core.Purchase{}, false
		}
		id, e1 := strconv.ParseInt(parts[0], 10, 64)
		n, e2 := strconv.Atoi(parts[1])
		d, e3 := strconv.Atoi(parts[2])
		return core.Purchase{Kind: model.OrderPlan, PlanID: id, Periods: n, Devices: d},
			e1 == nil && e2 == nil && e3 == nil && id > 0 && n > 0 && d >= 0
	}
	return core.Purchase{}, false
}

// handlePurchaseCallback runs the purchase buttons; false when data is not one.
func (s *UserService) handlePurchaseCallback(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, data string) bool {
	switch {
	case data == "vu:chg":
		s.showChangeOffers(ctx, client, chatID, msgID, u)
	case data == "vu:add":
		s.showAddons(ctx, client, chatID, msgID, u)
	case strings.HasPrefix(data, "vu:x:"):
		if p, ok := parsePurchase(strings.TrimPrefix(data, "vu:x:")); ok {
			s.offerPurchase(ctx, client, chatID, msgID, u, p)
		}
	case strings.HasPrefix(data, "vu:px:"):
		if method, code, ok := strings.Cut(strings.TrimPrefix(data, "vu:px:"), ":"); ok {
			if p, ok := parsePurchase(code); ok {
				s.payPurchase(ctx, client, chatID, msgID, u, p, method)
			}
		}
	case strings.HasPrefix(data, "vu:bx:"):
		if exp, code, ok := strings.Cut(strings.TrimPrefix(data, "vu:bx:"), ":"); ok {
			expect, err := strconv.ParseInt(exp, 10, 64)
			if p, ok := parsePurchase(code); ok && err == nil && expect >= 0 {
				s.buyPurchase(ctx, client, chatID, msgID, set, u, p, expect)
			}
		}
	default:
		return false
	}
	return true
}

// showChangeOffers lists the plans the user may move to and what each move does.
func (s *UserService) showChangeOffers(ctx context.Context, client *Client, chatID, msgID int64, u model.User) {
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	offers := s.panel.ChangeOffers(u)
	var rows [][]InlineButton
	for _, o := range offers {
		rows = append(rows, []InlineButton{{
			Text:         changeButton(lang, o, s.panel.Location()),
			CallbackData: "vu:x:" + purchaseCode(core.Purchase{Kind: model.OrderChange, PlanID: o.Plan.ID}),
		}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:plans"}})
	text := i18n.T(lang, "user.changeTitle")
	if len(offers) == 0 {
		text = i18n.T(lang, "user.changeNone")
	}
	s.edit(ctx, client, chatID, msgID, text, rows)
}

// changeButton names a plan to move to with what the move costs or gives.
func changeButton(lang i18n.Lang, o core.ChangeOffer, loc *time.Location) string {
	if o.Quote.Upgrade {
		return i18n.T(lang, "user.changeUp", o.Plan.Name, o.Quote.TotalRub)
	}
	return i18n.T(lang, "user.changeDown", o.Plan.Name, time.Unix(o.Quote.ExpireAt, 0).In(loc).Format("02.01.2006"))
}

// showAddons offers devices and traffic for the plan held.
func (s *UserService) showAddons(ctx context.Context, client *Client, chatID, msgID int64, u model.User) {
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	a := s.panel.Addons(u)
	var rows [][]InlineButton
	for n := 1; n <= min(a.DevicesMax, 5); n++ {
		rows = append(rows, []InlineButton{{
			Text:         i18n.TN(lang, "user.addDevices", n, n*a.DevicePrice),
			CallbackData: "vu:x:" + purchaseCode(core.Purchase{Kind: model.OrderDevices, Devices: n}),
		}})
	}
	for i, p := range a.Packs {
		rows = append(rows, []InlineButton{{
			Text:         i18n.T(lang, "user.addPack", p.GB, p.PriceRub),
			CallbackData: "vu:x:" + purchaseCode(core.Purchase{Kind: model.OrderTraffic, Pack: i}),
		}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:plans"}})
	text := i18n.T(lang, "user.addTitle")
	if !a.Any() {
		text = i18n.T(lang, "user.addNone")
	}
	s.edit(ctx, client, chatID, msgID, text, rows)
}

// askDevices asks how many extra devices a plan should come with.
func (s *UserService) askDevices(ctx context.Context, client *Client, chatID, msgID int64, plan *model.TariffPlan, periods int) {
	lang := s.lang(chatID)
	rows := [][]InlineButton{{{
		Text:         i18n.T(lang, "user.devicesNone", plan.DeviceLimit),
		CallbackData: fmt.Sprintf("vu:buy:%d:%d:0", plan.ID, periods),
	}}}
	for n := 1; n <= min(plan.DeviceMax, 8); n++ {
		rows = append(rows, []InlineButton{{
			Text:         i18n.T(lang, "user.devicesMore", plan.DeviceLimit+n, n*plan.DevicePrice*periods),
			CallbackData: fmt.Sprintf("vu:buy:%d:%d:%d", plan.ID, periods, n),
		}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}})
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.pickDevices", esc(plan.Name), plan.DevicePrice), rows)
}

// purchaseText says what a purchase is and what it costs.
func (s *UserService) purchaseText(lang i18n.Lang, u model.User, p core.Purchase, q core.PlanQuote) string {
	loc := s.panel.Location()
	switch p.Kind {
	case model.OrderChange:
		name := s.panel.PlanName(p.PlanID)
		if !q.Upgrade {
			return i18n.T(lang, "user.changeDownText", esc(name), time.Unix(q.ExpireAt, 0).In(loc).Format("02.01.2006"))
		}
		return i18n.T(lang, "user.changeUpText", esc(name), time.Unix(q.ExpireAt, 0).In(loc).Format("02.01.2006"), q.PriceRub) + quoteLines(q, lang)
	case model.OrderDevices:
		return i18n.TN(lang, "user.addDevicesText", q.Devices, q.PriceRub,
			time.Unix(u.ExpireAt, 0).In(loc).Format("02.01.2006")) + quoteLines(q, lang)
	case model.OrderTraffic:
		return i18n.T(lang, "user.addPackText", q.PackGB, q.PriceRub) + quoteLines(q, lang)
	}
	name := termName(s.panel.PlanName(p.PlanID), q.Periods)
	if q.Devices > 0 {
		name += i18n.TN(lang, "user.withDevices", q.Devices)
	}
	return i18n.T(lang, "user.balanceConfirm", esc(name), q.PriceRub) + quoteLines(q, lang)
}

// offerPurchase shows a purchase's price and the ways to pay for it.
func (s *UserService) offerPurchase(ctx context.Context, client *Client, chatID, msgID int64, u model.User, p core.Purchase) {
	lang := s.lang(chatID)
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	q, err := s.panel.QuotePurchase(u, p)
	if err != nil {
		s.editErr(ctx, client, chatID, msgID, err, "vu:plans")
		return
	}
	code := purchaseCode(p)
	text := s.purchaseText(lang, u, p, q)
	back := []InlineButton{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}}
	if q.MoneyRub == 0 {
		label := payBalanceLabel(q, lang)
		if p.Kind == model.OrderChange && !q.Upgrade {
			label = i18n.T(lang, "user.btnChangeFree")
		}
		s.edit(ctx, client, chatID, msgID, text, [][]InlineButton{
			{{Text: label, CallbackData: fmt.Sprintf("vu:bx:%d:%s", u.ExpireAt, code)}}, back,
		})
		return
	}
	methods := s.payMethods()
	if len(methods) == 0 {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(i18n.T(lang, "user.noPayMethod")), [][]InlineButton{back})
		return
	}
	var rows [][]InlineButton
	for _, m := range methods {
		rows = append(rows, []InlineButton{{Text: s.providerButton(lang, m), CallbackData: "vu:px:" + m + ":" + code}})
	}
	rows = append(rows, back)
	s.edit(ctx, client, chatID, msgID, text+"\n\n"+i18n.T(lang, "user.pickPayMethod"), rows)
}

// payPurchase opens a payment for a purchase with the chosen method.
func (s *UserService) payPurchase(ctx context.Context, client *Client, chatID, msgID int64, u model.User, p core.Purchase, method string) {
	lang := s.lang(chatID)
	nav := [][]InlineButton{
		{{Text: i18n.T(lang, "user.btnToPlans"), CallbackData: "vu:plans"}},
		{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
	}
	if method == sub.ManualPayKey {
		_, msg, err := s.panel.RequestPurchaseManual(ctx, lang, u.ID, p)
		if err != nil {
			s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)), nav)
			return
		}
		s.edit(ctx, client, chatID, msgID, esc(msg), nav)
		return
	}
	order, err := s.panel.StartPurchase(ctx, lang, u.ID, p, method, "https://t.me/")
	if err != nil {
		s.edit(ctx, client, chatID, msgID, "⚠️ "+esc(core.UserError(err, lang)), nav)
		return
	}
	msg := i18n.T(lang, "user.orderPay", order.ID, order.AmountRub)
	if order.BalanceKop > 0 {
		msg += "\n" + i18n.T(lang, "user.quoteBalance", kop(order.BalanceKop))
	}
	s.edit(ctx, client, chatID, msgID, msg, [][]InlineButton{
		{{Text: i18n.T(lang, "user.btnPay"), URL: order.PayURL},
			{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}},
	})
}

// buyPurchase pays for a purchase from the balance (or takes a free change).
func (s *UserService) buyPurchase(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings, u model.User, p core.Purchase, expect int64) {
	lang := s.lang(chatID)
	order, err := s.panel.BuyFromBalance(ctx, u.ID, p, expect)
	if err != nil {
		s.editErr(ctx, client, chatID, msgID, err, "vu:plans")
		return
	}
	if fresh, ok := s.findLinkedUser(chatID); ok {
		u = fresh
	}
	s.edit(ctx, client, chatID, msgID,
		i18n.T(lang, "user.purchaseDone", esc(core.OrderSubject(lang, order)))+"\n\n"+userSelfCard(u, set, s.panel, lang),
		s.menuRows(set, u, lang))
}
