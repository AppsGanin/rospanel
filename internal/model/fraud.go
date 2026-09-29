package model

// Fraud signal kinds (FraudSignal.Kind) and the counts that raise them.
const (
	FraudTrialFarm      = "trial_farm"      // Key: an address several trial users who never paid connect from
	FraudSharedDevice   = "shared_device"   // Key: a device id (HWID) bound on several accounts
	FraudSelfReferral   = "self_referral"   // Key: "hwid:…" or "ip:…" the referrer and the invitee share
	FraudPromoBurst     = "promo_burst"     // Key: a promo code taken many times within ten minutes
	FraudFailedPayments = "failed_payments" // Key: the user; payment attempts that keep failing
	FraudPaymentBurst   = "payment_burst"   // Key: the user; many payments within an hour
	FraudRefunds        = "refunds"         // Key: the user; money taken back through the payment system

	FraudTrialFarmMin    = 4
	FraudPromoBurstMin   = 20
	FraudFailedMin       = 5
	FraudPaymentBurstMin = 5
	FraudRefundsMin      = 2

	FraudWindowDays = 30
)

// FraudSignal is one pattern worth an operator's look: what the accounts share
// (Key), how many times it happened (Count), when last, and the accounts.
type FraudSignal struct {
	Kind  string      `json:"kind"`
	Key   string      `json:"key"`
	Count int         `json:"count"`
	At    int64       `json:"at"`
	Users []FraudUser `json:"users"`
}

// FraudUser is an account a signal names.
type FraudUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
