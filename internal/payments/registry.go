package payments

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// The provider registry. Every payment provider is one Descriptor: its display
// metadata, the credential fields the operator must fill in (the panel renders its
// settings form straight from Fields — there is no per-provider UI), and a
// constructor for its Client. Everything else in the panel — the settings API, the
// webhook route, the polling fallback, the bot's "choose a method" keyboard — is
// generic and driven by this list.
//
// Adding a provider = one file with a Descriptor + one line in descriptors below.

// FieldKind is how the panel renders a credential field.
type FieldKind string

const (
	FieldText   FieldKind = "text"   // plain value (shop id, merchant id)
	FieldSecret FieldKind = "secret" // masked; encrypted at rest; empty on save = keep current
	FieldBool   FieldKind = "bool"   // toggle (test mode, sandbox)
	FieldSelect FieldKind = "select" // one of Options (e.g. payment method)
)

// FieldOption is one choice in a FieldSelect.
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one credential input in the provider's settings form.
type Field struct {
	Key         string        `json:"key"`
	Label       string        `json:"label"`
	Kind        FieldKind     `json:"kind"`
	Placeholder string        `json:"placeholder,omitempty"`
	Help        string        `json:"help,omitempty"`
	Optional    bool          `json:"optional,omitempty"` // not required to consider the provider configured
	Options     []FieldOption `json:"options,omitempty"`  // choices for FieldSelect
}

// Config is a provider's saved credentials: field key → value. Bools are "1"/"".
type Config map[string]string

func (c Config) Get(key string) string { return strings.TrimSpace(c[key]) }
func (c Config) Bool(key string) bool  { v := c.Get(key); return v == "1" || v == "true" }

// CreateReq is the panel's request to open a payment for one order. Amounts are
// whole rubles — the panel prices plans in rubles and every provider here bills in
// RUB (crypto ones convert at their own rate).
type CreateReq struct {
	AmountRub   int
	OrderID     int64  // our order id; providers echo it back in the webhook
	Description string // human text shown on the payment page
	ReturnURL   string // where to send the payer after paying
	WebhookURL  string // our callback URL, for providers that take it per-payment
	Email       string // payer email, for providers that require a receipt
}

// Client talks to one configured provider.
type Client interface {
	// Create opens a payment and returns the provider's id for it plus the hosted
	// URL to send the payer to.
	Create(ctx context.Context, req CreateReq) (providerID, payURL string, err error)

	// Status re-reads a payment (the fallback for a missed webhook). Providers with
	// no status endpoint return ErrNoStatusAPI and are confirmed by webhook only.
	Status(ctx context.Context, providerID string) (Result, error)

	// Webhook authenticates a callback and reports what it says about the payment.
	// It MUST reject anything it cannot prove came from the provider: either by
	// checking a signature, or — when the provider signs nothing — by re-fetching the
	// payment over the API and reporting that instead of trusting the body.
	Webhook(ctx context.Context, body []byte, h http.Header) (providerID string, res Result, err error)
}

// ErrNoStatusAPI is returned by Status for providers that expose no way to query a
// payment. The polling fallback skips them (the webhook is the only confirmation).
var ErrNoStatusAPI = errors.New("провайдер не поддерживает опрос статуса")

// Descriptor is a provider's registry entry.
type Descriptor struct {
	Key    string  // stable id, stored on the order row and used as the webhook path leaf
	Label  string  // display name, e.g. "ЮКасса"
	Note   string  // one line under the name: methods, currency, who can sign up
	Fields []Field // credentials; also the settings form
	New    func(cfg Config) Client
}

// DisplayNameKey is a reserved config key holding the operator's custom label for
// this provider's pay button (shown to users in the bot and on the subscription
// page). Empty ⇒ fall back to the descriptor's Label. It's a display setting, not a
// credential, so it isn't part of Fields — the settings API injects it into the form
// for every provider.
const DisplayNameKey = "display_name"

// DisplayName is the operator-set pay-button label, or the default Label if unset.
func (d Descriptor) DisplayName(cfg Config) string {
	if n := cfg.Get(DisplayNameKey); n != "" {
		return n
	}
	return d.Label
}

// Configured reports whether every required field has a value.
func (d Descriptor) Configured(cfg Config) bool {
	for _, f := range d.Fields {
		if f.Optional || f.Kind == FieldBool {
			continue
		}
		if cfg.Get(f.Key) == "" {
			return false
		}
	}
	return true
}

// descriptors is the registry, in the order the panel shows them: the two providers
// the panel shipped with first, then RUB acquirers, then the crypto/fiat-to-crypto
// ones.
//
// More providers from the same family can be added one file at a time — each is a
// self-contained <name>.go with its Descriptor, dropped in here.
var descriptors = []Descriptor{
	yooKassaDescriptor(),
	pal24Descriptor(),
	rioPayDescriptor(),
	rollyPayDescriptor(),
	severPayDescriptor(),
	plategaDescriptor(),
	payPearDescriptor(),
	auraPayDescriptor(),
	heleketDescriptor(),
	cryptoBotDescriptor(),
}

// All returns every known provider, in display order.
func All() []Descriptor { return descriptors }

// Get looks up a provider by key.
func Get(key string) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.Key == key {
			return d, true
		}
	}
	return Descriptor{}, false
}

// Label is the display name for a provider key ("" ⇒ a manual order).
func Label(key string) string {
	if d, ok := Get(key); ok {
		return d.Label
	}
	if key == "" {
		return "вручную"
	}
	return key
}
