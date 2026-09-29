package model

// Automatic-message triggers (AutoRule.Trigger). Each names who is due and from
// when the delay counts.
const (
	TriggerNoSignup   = "no_signup"    // opened the bot, never registered — from the first /start
	TriggerNoConnect  = "no_connect"   // registered, never connected — from registration
	TriggerTrialNoPay = "trial_no_pay" // took the trial, never paid — from registration
	TriggerIdle       = "idle"         // has a live subscription, stopped using it — from the last activity
	TriggerLapsed     = "lapsed"       // a paid term ended without renewal — from its end
)

// AutoRuleTriggers lists the triggers in the order the panel offers them.
var AutoRuleTriggers = []string{TriggerNoSignup, TriggerNoConnect, TriggerTrialNoPay, TriggerIdle, TriggerLapsed}

// ValidAutoRuleTrigger reports whether t is a known trigger.
func ValidAutoRuleTrigger(t string) bool {
	for _, k := range AutoRuleTriggers {
		if k == t {
			return true
		}
	}
	return false
}

// AutoRule is an automatic message.
type AutoRule struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Enabled         bool              `json:"enabled"`
	Trigger         string            `json:"trigger"`
	DelayHours      int               `json:"delay_hours"`
	Text            string            `json:"text"`
	Buttons         []BroadcastButton `json:"buttons"`
	DiscountPercent int               `json:"discount_percent"` // 0 = no code
	DiscountDays    int               `json:"discount_days"`
	CreatedAt       int64             `json:"created_at"`
	UpdatedAt       int64             `json:"updated_at"`

	Stats AutoRuleStats `json:"stats"`
}

// AutoRuleStats is what a rule did: messages sent, how many of the people it wrote
// to converted within 30 days (paid; registered, for no_signup), the money those
// payments brought, and its codes used.
type AutoRuleStats struct {
	Sent       int   `json:"sent"`
	Converted  int   `json:"converted"`
	RevenueRub int64 `json:"revenue_rub"`
	CodesUsed  int   `json:"codes_used"`
}

// AutoRuleConvertDays is how long after a message a payment still counts for it.
const AutoRuleConvertDays = 30
