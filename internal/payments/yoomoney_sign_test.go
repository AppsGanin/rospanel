package payments

import (
	"context"
	"net/url"
	"testing"
)

// The docs' worked example: HMAC-SHA256 over the alphabetically sorted,
// URL-encoded parameter list (minus sign) with the shared secret "secret123".
// sha1_hash is part of the signed set here because the example still carries it;
// real notifications after 2026-05-18 omit it and sign every remaining field.
func TestYooMoneySignFormat(t *testing.T) {
	body := "notification_type=p2p-incoming&operation_id=441361714955017004&amount=98.00" +
		"&withdraw_amount=100.00&currency=643&datetime=2013-12-26T08%3A28%3A34Z&sender=41000000000" +
		"&codepro=false&label=ML23045&unaccepted=false" +
		"&sha1_hash=ac13833bd6ba9eff1fa9e4bed76f3d6ebb57f6c0" +
		"&sign=a452af731650e2c5b39abcdc7c28dd27db7b3b654c2230ad2c386e64afb98605"
	y := &YooMoney{wallet: "4100", secret: "secret123"}
	label, res, err := y.Webhook(context.Background(), []byte(body), nil)
	if err != nil {
		t.Fatalf("webhook rejected the docs example: %v", err)
	}
	if label != "ML23045" {
		t.Fatalf("label = %q, want ML23045", label)
	}
	if res.Status != StatusPaid {
		t.Fatalf("status = %q, want paid", res.Status)
	}
	if res.AmountKopecks != 10000 {
		t.Fatalf("amount = %d kopecks, want 10000 (the charged sum)", res.AmountKopecks)
	}
}

// A tampered amount must not verify: the signature covers every field.
func TestYooMoneySignFormatTampered(t *testing.T) {
	f, _ := url.ParseQuery("notification_type=p2p-incoming&operation_id=1&amount=98.00" +
		"&withdraw_amount=100.00&currency=643&datetime=2013-12-26T08%3A28%3A34Z&sender=41000000000" +
		"&codepro=false&label=ML23045&unaccepted=false&sign=a452af731650e2c5b39abcdc7c28dd27db7b3b654c2230ad2c386e64afb98605")
	f.Set("amount", "1.00")
	y := &YooMoney{wallet: "4100", secret: "secret123"}
	if _, _, err := y.Webhook(context.Background(), []byte(f.Encode()), nil); err == nil {
		t.Fatal("tampered amount passed the signature check")
	}
}
