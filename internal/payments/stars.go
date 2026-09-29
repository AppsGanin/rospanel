package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/netguard"
)

// Telegram Stars: the user bot issues the invoice (createInvoiceLink, currency XTR)
// and Telegram tells the same bot about the payment — a pre_checkout_query to
// approve, then a successful_payment message. There is no HTTP callback, so
// Webhook refuses everything that reaches the public URL; the bot confirms through
// core.ConfirmStarsPayment instead.
//
// Plans are priced in roubles; the operator sets how many roubles one star is
// worth, and the invoice asks for the price divided by that, rounded up. The star
// count travels in the invoice payload, so the payment is checked against what the
// invoice asked for even if the rate changes in between.

const keyStars = "stars"

// ProviderStars is the Telegram Stars registry key.
const ProviderStars = keyStars

// Config keys the panel fills in for the Stars client: the user bot's token and the
// Telegram proxy. They are not form fields — Stars go through the bot the panel
// already runs.
const (
	StarsBotToken = "_bot_token"
	StarsProxy    = "_proxy"
)

const telegramAPI = "https://api.telegram.org/bot"

func starsDescriptor() Descriptor {
	return Descriptor{
		Key:   keyStars,
		Label: "Telegram Stars",
		Note:  "payNote.stars",
		Fields: []Field{
			{Key: "rate", Label: "payField.starsRate", Kind: FieldText, Placeholder: "1.8",
				Help: "payHelp.starsRate"},
		},
		New: func(cfg Config) Client {
			return &Stars{token: cfg.Get(StarsBotToken), proxy: cfg.Get(StarsProxy), rate: cfg.Get("rate")}
		},
	}
}

// Stars issues Telegram Stars invoices through the user bot.
type Stars struct {
	token string
	proxy string
	rate  string // roubles per star, decimal
	base  string // overrides the Bot API in tests
}

// StarsFor is how many stars amountRub costs at rate (roubles per star), rounded up.
func StarsFor(amountRub int, rate string) (int64, error) {
	kop, ok := parseKopecks(rate)
	if !ok || kop <= 0 {
		return 0, fmt.Errorf("Telegram Stars: set the rouble price of one star")
	}
	return (int64(amountRub)*100 + kop - 1) / kop, nil
}

// starsPayload names the order and the star count the invoice asks for.
func starsPayload(orderID, stars int64) string { return fmt.Sprintf("o%d-%d", orderID, stars) }

// StarsPayloadAmount reads the star count back out of an invoice payload.
func StarsPayloadAmount(payload string) (int64, bool) {
	_, n, ok := strings.Cut(payload, "-")
	if !ok || !strings.HasPrefix(payload, "o") {
		return 0, false
	}
	v, err := strconv.ParseInt(n, 10, 64)
	return v, err == nil && v > 0
}

// Create issues an invoice link. The payload is the provider id.
func (s *Stars) Create(ctx context.Context, req CreateReq) (string, string, error) {
	if s.token == "" {
		return "", "", fmt.Errorf("Telegram Stars: the user bot is off")
	}
	stars, err := StarsFor(req.AmountRub, s.rate)
	if err != nil {
		return "", "", err
	}
	payload := starsPayload(req.OrderID, stars)
	title := truncRunes(req.Description, 32)
	if title == "" {
		title = fmt.Sprintf("#%d", req.OrderID)
	}
	body, _ := json.Marshal(map[string]any{
		"title":       title,
		"description": truncRunes(req.Description, 255),
		"payload":     payload,
		"currency":    "XTR",
		"prices":      []map[string]any{{"label": title, "amount": stars}},
	})
	base := s.base
	if base == "" {
		base = telegramAPI
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+s.token+"/createInvoiceLink", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	hreq.Header.Set("Content-Type", "application/json")
	c := httpClient()
	if s.proxy != "" {
		c.Transport = netguard.ProxyTransport(s.proxy)
	}
	resp, err := c.Do(hreq)
	if err != nil {
		// The token is part of the URL; never let it reach a log.
		return "", "", fmt.Errorf("Telegram Stars: %s", strings.ReplaceAll(err.Error(), s.token, "…"))
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Result      string `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("Telegram Stars: could not parse the response: %w", err)
	}
	if !out.OK || out.Result == "" {
		return "", "", fmt.Errorf("Telegram Stars: %s", out.Description)
	}
	return payload, out.Result, nil
}

// Status implements Client: a Stars payment is only ever reported to the bot.
func (s *Stars) Status(context.Context, string) (Result, error) { return Result{}, ErrNoStatusAPI }

// Webhook implements Client. Nothing legitimate posts Stars payments to a URL, so
// anything that does is refused.
func (s *Stars) Webhook(context.Context, []byte, http.Header) (string, Result, error) {
	return "", Result{}, fmt.Errorf("Telegram Stars payments are confirmed by the bot, not by a callback")
}
