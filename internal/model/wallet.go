package model

import "fmt"

// Referral reward modes (Settings.RefMode).
const (
	RefOff     = "off"
	RefPercent = "percent"
	RefDays    = "days"
)

// RefEnabled reports whether the referral programme pays anything.
func (s *Settings) RefEnabled() bool {
	return s.BillingEnabled && (s.RefMode == RefPercent || s.RefMode == RefDays)
}

// Payment order kinds. A plan order buys a tariff; a top-up puts its amount on the
// balance and has no plan. A change moves an active plan to another for the rest of
// its term; devices and traffic add to the plan the user holds.
const (
	OrderPlan    = "plan"
	OrderTopup   = "topup"
	OrderChange  = "change"
	OrderDevices = "devices"
	OrderTraffic = "traffic"
)

// TrafficPack is extra traffic on sale on top of a plan's quota.
type TrafficPack struct {
	GB       int `json:"gb"`
	PriceRub int `json:"price_rub"`
}

// BalanceProvider is the provider of an order paid entirely from the balance: no
// money arrived, so its amount_rub is 0 and it never counts as revenue.
const BalanceProvider = "balance"

// Balance ledger kinds (BalanceTx.Kind).
const (
	TxTopup    = "topup"    // money from a paid order landed on the balance
	TxPurchase = "purchase" // a plan bought from the balance
	TxRenew    = "renew"    // a plan renewed from the balance when it ran out
	TxReferral = "referral" // a share of an invited user's payment
	TxPromo    = "promo"    // a balance promo code
	TxAdmin    = "admin"    // an operator's correction
	TxRefund   = "refund"   // an order's money returned to the balance
	// TxChargeback takes back what the balance got from money the payment system
	// then returned to the payer.
	TxChargeback = "chargeback"
)

// WinbackSettings is the win-back offer: AfterDays after a paid term lapses, the user
// gets a one-use code for Percent % off any plan, valid for ValidDays.
type WinbackSettings struct {
	Enabled   bool `json:"enabled"`
	AfterDays int  `json:"after_days"`
	Percent   int  `json:"percent"`
	ValidDays int  `json:"valid_days"`
}

// WinbackStats is what the win-back codes did: how many went out, how many were used,
// and the money the orders they discounted brought.
type WinbackStats struct {
	Sent       int `json:"sent"`
	Used       int `json:"used"`
	RevenueRub int `json:"revenue_rub"`
}

// Funnel follows the users who joined since a moment: how many took a trial, paid
// for a plan, and paid for one again.
type Funnel struct {
	Joined  int `json:"joined"`
	Trial   int `json:"trial"`
	Paid    int `json:"paid"`
	Renewed int `json:"renewed"`
}

// SourceFunnel is the funnel for the users who came from one source: a /start tag,
// "~ref" for an invite link without one, "" for neither.
type SourceFunnel struct {
	Source     string `json:"source"`
	Joined     int    `json:"joined"`
	Trial      int    `json:"trial"`
	Paid       int    `json:"paid"`
	Renewed    int    `json:"renewed"`
	RevenueRub int64  `json:"revenue_rub"`
}

// PeriodOffer is a discount for buying several of a plan's periods at once.
type PeriodOffer struct {
	Periods int `json:"periods"` // 2 or more
	Percent int `json:"percent"` // off the total
}

// Promo code kinds.
const (
	PromoPercent = "percent" // Value % off the next payment
	PromoAmount  = "amount"  // Value ₽ off the next payment
	PromoDays    = "days"    // Value days on a plan, no payment
	PromoBalance = "balance" // Value ₽ on the balance
)

// PromoKinds lists every promo kind, in the order the editor offers them.
var PromoKinds = []string{PromoPercent, PromoAmount, PromoDays, PromoBalance}

// IsDiscount reports whether the kind takes money off a payment (and so waits for
// one), rather than acting the moment it is entered.
func IsDiscount(kind string) bool { return kind == PromoPercent || kind == PromoAmount }

// PromoCode is one code an operator hands out.
type PromoCode struct {
	ID    int64  `json:"id"`
	Code  string `json:"code"`
	Kind  string `json:"kind"`
	Value int    `json:"value"`
	// PlanIDs limits a discount to these plans (empty = any paid plan). PlanID is the
	// plan a days code grants (0 = the days go onto the user's active paid plan).
	PlanIDs   []int64 `json:"plan_ids"`
	PlanID    int64   `json:"plan_id"`
	FirstOnly bool    `json:"first_only"` // discount only for someone who has never bought a plan
	MaxUses   int     `json:"max_uses"`   // 0 = unlimited
	Uses      int     `json:"uses"`
	ExpiresAt int64   `json:"expires_at"` // 0 = never
	Enabled   bool    `json:"enabled"`
	Note      string  `json:"note"`
	CreatedAt int64   `json:"created_at"`
}

// AppliesTo reports whether a discount code may be used on the plan.
func (p PromoCode) AppliesTo(planID int64) bool {
	if len(p.PlanIDs) == 0 {
		return true
	}
	for _, id := range p.PlanIDs {
		if id == planID {
			return true
		}
	}
	return false
}

// Discount is what the code takes off a price, never more than the price itself.
func (p PromoCode) Discount(priceRub int) int {
	var d int
	switch p.Kind {
	case PromoPercent:
		d = priceRub * p.Value / 100
	case PromoAmount:
		d = p.Value
	}
	return min(max(d, 0), priceRub)
}

// KopText renders kopecks as roubles: "199", or "19.90" when there are kopecks.
func KopText(kop int64) string {
	sign := ""
	if kop < 0 {
		sign, kop = "-", -kop
	}
	if kop%100 == 0 {
		return fmt.Sprintf("%s%d", sign, kop/100)
	}
	return fmt.Sprintf("%s%d.%02d", sign, kop/100, kop%100)
}

// BalanceTx is one line of a user's balance ledger.
type BalanceTx struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	AmountKop  int64  `json:"amount_kop"`  // signed: + credit, − debit
	BalanceKop int64  `json:"balance_kop"` // the balance this change left
	Kind       string `json:"kind"`
	OrderID    int64  `json:"order_id,omitempty"`
	RefUserID  int64  `json:"ref_user_id,omitempty"`
	RefName    string `json:"ref_name,omitempty"`
	PromoID    int64  `json:"promo_id,omitempty"`
	PromoCode  string `json:"promo_code,omitempty"`
	PlanName   string `json:"plan_name,omitempty"` // the plan the order behind this line bought
	Note       string `json:"note,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// Referral is one user someone invited, as the inviter's card lists them.
type Referral struct {
	UserID    int64  `json:"user_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	PaidRub   int    `json:"paid_rub"`   // money they paid in all
	EarnedKop int64  `json:"earned_kop"` // what their payments put on the inviter's balance
}

// PromoUse is one use of a promo code.
type PromoUse struct {
	UserID      int64  `json:"user_id"`
	Name        string `json:"name"`
	OrderID     int64  `json:"order_id,omitempty"`
	UsedAt      int64  `json:"used_at"`
	AmountRub   int    `json:"amount_rub"`   // money the discounted order brought
	DiscountRub int    `json:"discount_rub"` // what the code took off it
}

// Referrer is one line of the referral programme's leaderboard.
type Referrer struct {
	UserID    int64  `json:"user_id"`
	Name      string `json:"name"`
	Invited   int    `json:"invited"`
	Paying    int    `json:"paying"`
	EarnedKop int64  `json:"earned_kop"`
}

// ReferralStats is the referral programme at a glance.
type ReferralStats struct {
	Invited    int        `json:"invited"`     // users who came through a link
	Paying     int        `json:"paying"`      // of them, those who paid money
	RevenueRub int        `json:"revenue_rub"` // money those users paid
	PaidOutKop int64      `json:"paid_out_kop"`
	Top        []Referrer `json:"top"`
}

// Wallet is a user's balance and referral standing, as the panel and the user see it.
type Wallet struct {
	BalanceKop int64 `json:"balance_kop"`
	AutoRenew  bool  `json:"auto_renew"`
	// ReferrerID is who invited this user (0 = nobody); ReferrerName their name.
	ReferrerID   int64  `json:"referrer_id"`
	ReferrerName string `json:"referrer_name,omitempty"`
	RefCode      string `json:"ref_code,omitempty"`
	// Invited counts the users this one invited, Paying how many of them have paid,
	// EarnedKop what their payments put on this balance.
	Invited      int   `json:"invited"`
	Paying       int   `json:"paying"`
	EarnedKop    int64 `json:"earned_kop"`
	RefBonusDays int   `json:"ref_bonus_days"`
	// PromoID/PromoCode is the discount code waiting for the next payment.
	PromoID   int64  `json:"promo_id"`
	PromoCode string `json:"promo_code,omitempty"`
}
