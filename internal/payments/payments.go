// Package payments wraps the YooKassa (card, RUB) and CryptoBot (Telegram crypto)
// HTTP APIs: create a payment/invoice for an order, query its status (polling
// fallback), and verify a webhook. All amounts are in whole rubles — CryptoBot
// invoices are created as fiat (RUB) so the user pays the crypto equivalent.
package payments

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Status is the normalised payment state across providers.
type Status string

const (
	StatusPending  Status = "pending"  // created, not yet paid
	StatusPaid     Status = "paid"     // money received
	StatusCanceled Status = "canceled" // cancelled / expired / failed
)

// Result is a queried payment/invoice state plus the amount the provider recorded
// for it. The caller verifies AmountKopecks/Currency against the order's snapshot
// before applying a plan, so a tampered or misrouted callback can't grant a plan
// that was paid for with the wrong amount. AmountKopecks is 0 and Currency "" when
// the provider response carried no readable amount (the caller then fails open).
type Result struct {
	Status        Status
	AmountKopecks int64  // amount in kopecks (exact; no float), 0 if unknown
	Currency      string // ISO-4217 code, e.g. "RUB"; "" if unknown
}

// parseKopecks converts a decimal money string ("100", "100.5", "100.00") to
// integer kopecks without floating point. Fraction digits beyond kopecks are
// truncated (money has no sub-kopeck). Returns ok=false on a malformed value.
func parseKopecks(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if len(fracPart) > 2 {
		fracPart = fracPart[:2]
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}
	rub, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil || rub < 0 {
		return 0, false
	}
	kop, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil || kop < 0 {
		return 0, false
	}
	return rub*100 + kop, true
}

// Provider identifiers (also stored on the order row).
const (
	ProviderYooKassa  = "yookassa"
	ProviderCryptoBot = "cryptobot"
)

func httpClient() *http.Client { return &http.Client{Timeout: 15 * time.Second} }
