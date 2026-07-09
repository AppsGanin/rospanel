package sub

import (
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
	html, err := Page(u, set, billing)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{
		"Текущий тариф",
		"Стандарт",
		"199 ₽ / 30 дн.",
		"pay( 3 , this)", // html/template pads numeric JS values with spaces
		"pay( 4 , this)",
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
	html, err := Page(u, set, billing)
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

// TestPageBillingManual renders the block with no automatic provider: the pay
// button still shows (creates a manual order) and the manual note appears.
func TestPageBillingManual(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	billing := Billing{
		Show:    true,
		Manual:  true,
		PayPath: "https://vpn.example.com/sub/tok123/pay",
		Plans:   []BillingPlan{{ID: 5, Name: "Месяц", Label: "199 ₽ / 30 дн."}},
		Note:    "Переведите на карту 0000",
	}
	html, err := Page(u, set, billing)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{
		"pay( 5 , this)", // pay button present even without a provider
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
	html, err := Page(u, set, Billing{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(html), "Текущий тариф") {
		t.Error("billing block rendered when hidden")
	}
}
