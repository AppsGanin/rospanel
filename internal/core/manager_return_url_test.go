package core

import "testing"

// The billing return address is where a bot payer lands: it wins over the Mini App
// and the sub page, and a value that is no full http(s) address is refused.
func TestBillingReturnURL(t *testing.T) {
	m, st, _ := walletFixture(t)
	u := walletUser(t, st, "payer")

	set, _ := st.GetSettings()
	for _, bad := range []string{"javascript:alert(1)", "example.com/paid", "https://user:pw@example.com/", "https://exa mple.com/"} {
		set.BillingReturnURL = bad
		if err := m.SaveBillingSettings(set); validationCode(err) != "err.badReturnURL" {
			t.Fatalf("%q saved: %v", bad, err)
		}
	}
	set.BillingReturnURL = "  https://example.com/paid  "
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if got := m.userReturnURL(u.ID); got != "https://example.com/paid" {
		t.Fatalf("return url = %q", got)
	}
	set, _ = st.GetSettings()
	set.BillingReturnURL = ""
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if got := m.userReturnURL(u.ID); got == "https://example.com/paid" {
		t.Fatal("cleared setting still used")
	}
}
