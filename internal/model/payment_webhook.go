package model

// PaymentWebhookRetentionDays is how long a provider callback stays in the journal.
// It explains a payment that went wrong, and those are asked about within days.
const PaymentWebhookRetentionDays = 30

// What the panel made of a provider callback (PaymentWebhook.Outcome).
const (
	WebhookOutcomePaid      = "paid"      // the order was confirmed by this callback
	WebhookOutcomeDuplicate = "duplicate" // the order was already paid
	WebhookOutcomeCancelled = "cancelled" // the provider reported the payment cancelled
	WebhookOutcomeRefunded  = "refunded"  // the provider returned the money
	WebhookOutcomePending   = "pending"   // not paid yet: nothing to do
	WebhookOutcomeMismatch  = "mismatch"  // the amount differs from the order's
	WebhookOutcomeNoOrder   = "no_order"  // no order carries this payment id
	WebhookOutcomeRejected  = "rejected"  // signature or format check failed
	WebhookOutcomeError     = "error"     // anything else that stopped it
)

// PaymentWebhook is one callback from a payment provider and its outcome.
type PaymentWebhook struct {
	ID         int64  `json:"id"`
	At         int64  `json:"at"`
	Provider   string `json:"provider"`
	RemoteIP   string `json:"remote_ip"`
	ProviderID string `json:"provider_id"`
	OrderID    int64  `json:"order_id"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome"`
	Error      string `json:"error,omitempty"`
	Headers    string `json:"headers,omitempty"`
	Body       string `json:"body,omitempty"`
}
