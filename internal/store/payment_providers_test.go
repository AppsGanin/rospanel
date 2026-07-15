package store

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestPaymentProviderRoundtrip(t *testing.T) {
	st := newStore(t)

	// An unsaved provider reads back as a disabled, empty row (not an error).
	got, err := st.GetPaymentProvider("heleket")
	if err != nil {
		t.Fatalf("GetPaymentProvider: %v", err)
	}
	if got.Enabled || len(got.Config) != 0 {
		t.Fatalf("unsaved provider = %+v, want disabled/empty", got)
	}

	want := model.PaymentProvider{
		Key:     "heleket",
		Enabled: true,
		Config:  map[string]string{"merchant_id": "m-1", "api_key": "secret-key"},
	}
	if err := st.SavePaymentProvider(want); err != nil {
		t.Fatalf("SavePaymentProvider: %v", err)
	}
	got, err = st.GetPaymentProvider("heleket")
	if err != nil {
		t.Fatalf("GetPaymentProvider: %v", err)
	}
	if !got.Enabled || got.Config["merchant_id"] != "m-1" || got.Config["api_key"] != "secret-key" {
		t.Fatalf("roundtrip = %+v", got)
	}

	// The raw column is the encryption envelope (a no-op without a datasec key in
	// tests, but the config must at least round-trip through encField/decField).
	var raw string
	if err := st.db.QueryRow(`SELECT config FROM payment_providers WHERE key = 'heleket'`).Scan(&raw); err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	if !strings.Contains(raw, "merchant_id") {
		t.Fatalf("stored config not a JSON object: %q", raw)
	}

	// Upsert replaces enabled + config.
	if err := st.SavePaymentProvider(model.PaymentProvider{Key: "heleket", Enabled: false, Config: map[string]string{"merchant_id": "m-2"}}); err != nil {
		t.Fatalf("SavePaymentProvider upsert: %v", err)
	}
	got, _ = st.GetPaymentProvider("heleket")
	if got.Enabled || got.Config["merchant_id"] != "m-2" {
		t.Fatalf("after upsert = %+v", got)
	}

	// ListPaymentProviders keys by provider.
	all, err := st.ListPaymentProviders()
	if err != nil {
		t.Fatalf("ListPaymentProviders: %v", err)
	}
	if _, ok := all["heleket"]; !ok || len(all) != 1 {
		t.Fatalf("list = %+v", all)
	}
}
