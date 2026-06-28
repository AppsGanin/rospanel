// Package payments wraps the YooKassa (card, RUB) and CryptoBot (Telegram crypto)
// HTTP APIs: create a payment/invoice for an order, query its status (polling
// fallback), and verify a webhook. All amounts are in whole rubles — CryptoBot
// invoices are created as fiat (RUB) so the user pays the crypto equivalent.
package payments

import (
	"net/http"
	"time"
)

// Status is the normalised payment state across providers.
type Status string

const (
	StatusPending  Status = "pending"  // created, not yet paid
	StatusPaid     Status = "paid"     // money received
	StatusCanceled Status = "canceled" // cancelled / expired / failed
)

// Provider identifiers (also stored on the order row).
const (
	ProviderYooKassa  = "yookassa"
	ProviderCryptoBot = "cryptobot"
)

func httpClient() *http.Client { return &http.Client{Timeout: 15 * time.Second} }
