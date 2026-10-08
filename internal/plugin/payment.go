package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// A plugin that provides a payment method is one more payments.Descriptor, keyed
// "plugin.<id>". Everything around a provider stays the panel's own: the switch in
// Settings → Payments, the pay button in the bot and on the subscription page, the
// webhook route, the polling fallback — and the check that what was paid matches
// the order, which is why a plugin must always report the amount.

// PaymentTimeout bounds one payment call into a plugin.
const PaymentTimeout = 15 * time.Second

// PaymentKeyPrefix starts every plugin provider's key.
const PaymentKeyPrefix = "plugin."

// PaymentDescriptors lists the active plugins' payment methods, in plugin order.
// Their settings live in the plugin, so they have no fields of their own.
func (h *Host) PaymentDescriptors() []payments.Descriptor {
	ids := h.Active(func(m *manifest.Manifest) bool { return m.Provides.Payment != nil })
	out := make([]payments.Descriptor, 0, len(ids))
	for _, id := range ids {
		inst, err := h.get(id)
		if err != nil {
			continue
		}
		p := inst.pub.Load()
		if p == nil || p.manifest == nil || p.manifest.Provides.Payment == nil {
			continue
		}
		pay := p.manifest.Provides.Payment
		out = append(out, payments.Descriptor{
			Key:   PaymentKeyPrefix + id,
			Label: pay.Label.Get("ru"),
			Note:  pay.Note.Get("ru"),
			New:   func(payments.Config) payments.Client { return &paymentClient{host: h, id: id} },
		})
	}
	return out
}

// paymentClient is payments.Client over a plugin's payment.* exports.
type paymentClient struct {
	host *Host
	id   string
}

type pluginPayment struct {
	ProviderID    string `json:"provider_id"`
	PayURL        string `json:"pay_url"`
	Status        string `json:"status"`
	AmountKopecks int64  `json:"amount_kopecks"`
	Currency      string `json:"currency"`
}

func (c *paymentClient) call(ctx context.Context, export string, arg any) (pluginPayment, error) {
	return c.callWith(ctx, export, arg, callOpts{})
}

// callPublic is call for what anyone on the internet can send: a refusal there (a
// forged callback) is not the plugin failing.
func (c *paymentClient) callPublic(ctx context.Context, export string, arg any) (pluginPayment, error) {
	return c.callWith(ctx, export, arg, callOpts{public: true})
}

func (c *paymentClient) callWith(ctx context.Context, export string, arg any, o callOpts) (pluginPayment, error) {
	var p pluginPayment
	out, err := c.host.call(ctx, c.id, export, arg, PaymentTimeout, o)
	if err != nil {
		if errors.Is(err, ErrBusy) || errors.Is(err, ErrNotActive) || errors.Is(err, ErrPaused) ||
			errors.Is(err, ErrReentry) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return p, fmt.Errorf("plugin %s: %w: %w", c.id, payments.ErrUnavailable, err)
		}
		return p, fmt.Errorf("plugin %s: %w", c.id, err)
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return p, fmt.Errorf("plugin %s: %s returned %s, not an object", c.id, export, firstLine(string(out)))
	}
	return p, nil
}

func (c *paymentClient) Create(ctx context.Context, req payments.CreateReq) (string, string, error) {
	p, err := c.call(ctx, "payment.create", map[string]any{
		"amount_rub": req.AmountRub, "order_id": req.OrderID, "description": req.Description,
		"return_url": req.ReturnURL, "webhook_url": req.WebhookURL, "email": req.Email,
	})
	if err != nil {
		return "", "", err
	}
	if p.ProviderID == "" || !strings.HasPrefix(p.PayURL, "https://") && !strings.HasPrefix(p.PayURL, "http://") {
		return "", "", fmt.Errorf("plugin %s: payment.create must return provider_id and an http(s) pay_url", c.id)
	}
	return p.ProviderID, p.PayURL, nil
}

func (c *paymentClient) Status(ctx context.Context, providerID string) (payments.Result, error) {
	p, err := c.call(ctx, "payment.status", providerID)
	if err != nil {
		return payments.Result{}, err
	}
	return c.result(p)
}

func (c *paymentClient) Webhook(ctx context.Context, body []byte, h http.Header) (string, payments.Result, error) {
	headers := map[string]string{}
	for k, v := range h {
		if strings.EqualFold(k, "Cookie") { // the panel's session never reaches a plugin
			continue
		}
		headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	p, err := c.callPublic(ctx, "payment.webhook", map[string]any{"body": string(body), "headers": headers})
	if err != nil {
		return "", payments.Result{}, err
	}
	if p.ProviderID == "" {
		return "", payments.Result{}, fmt.Errorf("plugin %s: payment.webhook returned no provider_id", c.id)
	}
	res, err := c.result(p)
	return p.ProviderID, res, err
}

// result maps the plugin's answer onto the panel's. A built-in provider whose
// callback carries no amount is let through (the panel fails open on an unknown
// amount); a plugin is held to more — a "paid" without the amount and currency is
// refused, so the order check always has something to compare.
func (c *paymentClient) result(p pluginPayment) (payments.Result, error) {
	var st payments.Status
	switch p.Status {
	case "paid":
		st = payments.StatusPaid
	case "pending":
		st = payments.StatusPending
	case "cancelled", "canceled":
		st = payments.StatusCanceled
	case "refunded":
		st = payments.StatusRefunded
	default:
		return payments.Result{}, fmt.Errorf("plugin %s: status %q is not one of paid, pending, cancelled, refunded", c.id, p.Status)
	}
	cur := strings.ToUpper(strings.TrimSpace(p.Currency))
	if (st == payments.StatusPaid || st == payments.StatusRefunded) && (p.AmountKopecks <= 0 || cur == "") {
		return payments.Result{}, errors.New("plugin " + c.id + ": a paid payment must report amount_kopecks and currency")
	}
	return payments.Result{Status: st, AmountKopecks: p.AmountKopecks, Currency: cur}, nil
}
