// Package payments is the payment-provider registry: one Descriptor per provider
// (see registry.go), each wrapping that provider's HTTP API behind a Client —
// create a payment for an order, query its status (the polling fallback), and
// authenticate a webhook. All amounts are in whole rubles; crypto providers are
// invoiced in RUB so the payer sends the crypto equivalent.
package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// Provider identifiers for the two providers the panel shipped with. Every other
// provider's key lives in its own file; use payments.Get to look one up.
const (
	ProviderYooKassa  = keyYooKassa
	ProviderCryptoBot = keyCryptoBot
)

func httpClient() *http.Client { return &http.Client{Timeout: 15 * time.Second} }

// parseKopecks converts a decimal money string ("100", "100.5", "100.00") to
// integer kopecks without floating point. Fraction digits beyond kopecks are
// truncated (money has no sub-kopeck). Returns ok=false on a malformed value.
func parseKopecks(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.ReplaceAll(s, ",", ".")
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
	// Guard the *100 against int64 overflow: a bogus astronomically-large amount
	// would wrap negative and read as "unknown" (fail-open) in amountMatches. Real
	// plan prices are tiny, so anything past this cap is malformed, not a payment.
	if rub > (1<<62)/100 {
		return 0, false
	}
	return rub*100 + kop, true
}

// amountResult builds a Result from a status plus a decimal amount string. An
// unreadable amount is reported as "unknown" (empty currency) rather than a bogus 0
// — the caller fails open on unknown, but would refuse the payment on a wrong 0.
func amountResult(st Status, amount, currency string) Result {
	res := Result{Status: st, Currency: strings.ToUpper(strings.TrimSpace(currency))}
	if k, ok := parseKopecks(amount); ok {
		res.AmountKopecks = k
	} else {
		res.Currency = ""
	}
	return res
}

// rubles renders whole rubles the way most providers want the amount: "199.00".
func rubles(amountRub int) string { return fmt.Sprintf("%d.00", amountRub) }

// decimalRub renders a float amount as a 2-decimal money string ("150.00"), for
// providers that return the charged amount as a JSON number.
func decimalRub(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// --- HTTP helpers -----------------------------------------------------------
//
// Every provider client is the same three calls over JSON or form-encoded HTTP,
// so the plumbing (marshal, headers, size-limited read, HTTP error, unmarshal)
// lives here once.

// httpErr is a non-2xx response, kept typed so callers can inspect the body.
type httpErr struct {
	provider string
	code     int
	body     string
}

func (e *httpErr) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.provider, e.code, e.body)
}

// callJSON sends body as JSON (nil ⇒ no request body) and decodes the JSON reply
// into out (nil ⇒ reply discarded). name prefixes errors.
func callJSON(ctx context.Context, name, method, endpoint string, headers map[string]string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return send(name, req, out)
}

func send(name string, req *http.Request, out any) error {
	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &httpErr{provider: name, code: resp.StatusCode, body: string(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: не удалось разобрать ответ: %w", name, err)
	}
	return nil
}

// --- signature helpers ------------------------------------------------------

func hmacSHA256Hex(key, msg string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func hmacSHA512Hex(key, msg string) string {
	mac := hmac.New(sha512.New, []byte(key))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func md5Hex(msg string) string {
	sum := md5.Sum([]byte(msg)) //nolint:gosec // provider-mandated signature algorithm
	return hex.EncodeToString(sum[:])
}

// eqSig compares two signatures in constant time, case-insensitively (providers
// disagree on hex case).
func eqSig(a, b string) bool {
	return hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(a))), []byte(strings.ToLower(strings.TrimSpace(b))))
}

// canonicalJSON marshals v to compact JSON with keys sorted (Go sorts map keys) and
// HTML escaping off, matching the reference python `json.dumps(sorted, ensure_ascii=
// False, separators=(",",":"))` that some gateways sign the request body with. Use
// only int/string values (no floats) so number formatting agrees across languages.
func canonicalJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
