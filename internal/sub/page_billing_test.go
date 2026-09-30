package sub

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TestPageBillingBlock renders the subscription page with a paid-renewal block and
// checks the plan rows, provider selector, and pay wiring all appear.
func TestPageBillingBlock(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123", PlanID: 3}
	set := &model.Settings{Host: "vpn.example.com"}
	billing := Billing{
		Show:        true,
		CurrentPlan: "Стандарт",
		ExpireText:  "до 10.08.2026",
		PayPath:     "https://vpn.example.com/sub/tok123/pay",
		Plans: []BillingPlan{
			{ID: 3, Name: "Стандарт", Label: "199 ₽ / 30 дн.", Current: true},
			{ID: 4, Name: "Год", Label: "1990 ₽ / 365 дн."},
		},
		Providers: []BillingPay{
			{Key: "yookassa", Label: "Картой (ЮКасса)"},
			{Key: "cryptobot", Label: "Криптовалютой (CryptoBot)"},
		},
	}
	html, err := Page(u, set, One(set), billing, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{
		"Текущий тариф",
		"Стандарт",
		"199 ₽ / 30 дн.",
		"buyPlan( 3 , this)", // html/template pads numeric JS values with spaces
		"buyPlan( 4 , this)",
		`id="paymodal"`,              // provider-choice modal present (2 providers)
		`payWith('cryptobot', this)`, // provider button wired
		"Криптовалютой (CryptoBot)",  // provider label in the modal
		`"yookassa", "cryptobot"`,    // PAY_PROVIDERS array
		"PAY_PATH =",                 // pay endpoint wired into the script
		"tok123",                     // sub token present in the (js-escaped) pay path
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

// TestPageBillingLocked renders the block for an active paid plan: only renewal +
// cancellation are offered, and the "switch after cancel" hint is shown.
func TestPageBillingLocked(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123", PlanID: 3}
	set := &model.Settings{Host: "vpn.example.com"}
	billing := Billing{
		Show:        true,
		Locked:      true,
		Cancelable:  true,
		CurrentPlan: "Стандарт",
		ExpireText:  "до 10.08.2026",
		PayPath:     "https://vpn.example.com/sub/tok123/pay",
		CancelPath:  "https://vpn.example.com/sub/tok123/cancel",
		Plans:       []BillingPlan{{ID: 3, Name: "Стандарт", Label: "199 ₽ / 30 дн.", Current: true}},
		Providers:   []BillingPay{{Key: "cryptobot", Label: "Криптовалютой (CryptoBot)"}},
	}
	html, err := Page(u, set, One(set), billing, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{"Продлить", "Отменить подписку", "cancelSub(this)", "бесплатный тариф"} {
		if !strings.Contains(s, want) {
			t.Errorf("locked page missing %q", want)
		}
	}
	if strings.Contains(s, ">Оплатить<") {
		t.Error("locked page should say Продлить, not Оплатить")
	}
}

// TestPageBillingManual renders the block with manual payment as the only method:
// the pay button creates a manual order, and the page carries both the operator's
// details and the line about an admin confirming the transfer.
func TestPageBillingManual(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	billing := Billing{
		Show:       true,
		Manual:     true,
		ManualOnly: true,
		Providers:  []BillingPay{{Key: ManualPayKey, Label: "Вручную"}},
		PayPath:    "https://vpn.example.com/sub/tok123/pay",
		Plans:      []BillingPlan{{ID: 5, Name: "Месяц", Label: "199 ₽ / 30 дн."}},
		Note:       "Переведите на карту 0000",
	}
	html, err := Page(u, set, One(set), billing, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{
		"buyPlan( 5 , this)", // pay button present even without a provider
		"администратор подтвердит платёж", // manual note
		`id="msgmodal"`, // instructions modal
		"Переведите на карту 0000", // configured manual note
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manual page missing %q", want)
		}
	}
	if strings.Contains(s, `id="paymodal"`) {
		t.Error("no provider modal expected in manual mode")
	}
}

// TestPageBillingHidden renders with a zero Billing and confirms the block is gone.
func TestPageBillingHidden(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, set, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(html), "Текущий тариф") {
		t.Error("billing block rendered when hidden")
	}
}

// TestPageTabs: with billing on the page splits into tabs — the subscription shown,
// payment and referrals hidden until picked; the referral tab only with a link.
// Without billing there are no tabs and the subscription is the whole page.
func TestPageTabs(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123", PlanID: 3}
	set := &model.Settings{Host: "vpn.example.com"}
	render := func(b Billing) string {
		t.Helper()
		html, err := Page(u, set, One(set), b, Devices{}, Access{}, true, i18n.RU)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return string(html)
	}
	s := render(Billing{Show: true, CurrentPlan: "Стандарт", Wallet: true, Balance: "40",
		RefLink: "https://t.me/bot?start=r_abc", RefShare: "https://t.me/share/url?url=x"})
	for _, want := range []string{
		`data-tab="sub"`, `data-tab="pay"`, `data-tab="ref"`,
		`data-pane="sub"`, `data-pane="pay" hidden`, `data-pane="ref" hidden`,
		"Подписка", "Оплата", "Рефералы", "r_abc", "Поделиться ссылкой",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if s := render(Billing{Show: true, CurrentPlan: "Стандарт"}); strings.Contains(s, `data-tab="ref"`) {
		t.Error("a referral tab without a referral link")
	}
	if s := render(Billing{}); strings.Contains(s, `class="tabs"`) || !strings.Contains(s, `data-pane="sub"`) {
		t.Error("without billing the page must be the subscription alone, no tabs")
	}
}

// The referral tab lists who came by the link and what each one earned.
func TestPageInvitees(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123", PlanID: 3}
	set := &model.Settings{Host: "vpn.example.com"}
	billing := Billing{
		Show:    true,
		RefLink: "https://t.me/bot?start=r_x",
		Invitees: []HistoryLine{
			{Title: "Petya", When: "с 01.09.2026", Amount: "+19.90 ₽", In: true},
			{Title: "Vasya", When: "с 02.09.2026", Amount: "без оплаты", Muted: true},
		},
		InviteesMore: "и ещё 3",
	}
	html, err := Page(u, set, One(set), billing, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{"Приглашённые", "Petya", "19.90 ₽", `tx-amt muted">без оплаты`, "и ещё 3"} {
		if !strings.Contains(s, want) {
			t.Errorf("referral tab missing %q", want)
		}
	}
}
