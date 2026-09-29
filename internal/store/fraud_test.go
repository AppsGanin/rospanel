package store

import (
	"fmt"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestFraudSignals(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	const now = int64(1_800_000_000)
	mk := func(name string) int64 {
		u, err := s.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	exec := func(q string, args ...any) {
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Four trial users who never paid, one address; a fifth on it who paid is no farm.
	var farm []int64
	for i := range 5 {
		id := mk(fmt.Sprintf("trial%d", i))
		farm = append(farm, id)
		exec(`UPDATE users SET trial_used = 1 WHERE id = ?`, id)
		exec(`INSERT INTO connections (user_id, ip, last_seen, count) VALUES (?, '5.5.5.5', ?, 1)`, id, now)
	}
	exec(`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at, paid_at) VALUES (?, 1, 100, 'paid', ?, ?)`, farm[4], now, now)
	// One device on two accounts; the invitee also shares it with the referrer.
	ref, inv := mk("ref"), mk("inv")
	exec(`UPDATE users SET referrer_id = ? WHERE id = ?`, ref, inv)
	for _, id := range []int64{ref, inv} {
		exec(`INSERT INTO devices (user_id, hwid, first_seen, last_seen) VALUES (?, 'HW1', ?, ?)`, id, now, now)
	}
	// Five failed card attempts in the week.
	card := mk("card")
	for range 5 {
		var oid int64
		if err := s.db.QueryRow(`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at, provider)
			VALUES (?, 1, 100, 'cancelled', ?, 'yookassa') RETURNING id`, card, now).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO payment_webhooks (at, provider, order_id, outcome) VALUES (?, 'yookassa', ?, 'cancelled')`, now, oid)
	}
	// Orders swept unpaid are no signal.
	browse := mk("browse")
	for range 6 {
		exec(`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at, provider) VALUES (?, 1, 100, 'cancelled', ?, 'yookassa')`, browse, now)
	}
	// Old data stays out.
	old := mk("old")
	exec(`INSERT INTO devices (user_id, hwid, first_seen, last_seen) VALUES (?, 'HW-OLD', 1, 1)`, old)
	exec(`INSERT INTO devices (user_id, hwid, first_seen, last_seen) VALUES (?, 'HW-OLD', 1, 1)`, card)

	sigs, err := s.FraudSignals(FraudWindow{Since: now - 30*86400, WeekSince: now - 7*86400})
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string][]model.FraudSignal{}
	for _, sig := range sigs {
		byKind[sig.Kind] = append(byKind[sig.Kind], sig)
	}
	if f := byKind[model.FraudTrialFarm]; len(f) != 1 || f[0].Key != "5.5.5.5" || f[0].Count != 4 || len(f[0].Users) != 4 {
		t.Fatalf("trial farm = %+v", f)
	}
	if d := byKind[model.FraudSharedDevice]; len(d) != 1 || d[0].Key != "HW1" || d[0].Users[0].Name == "" {
		t.Fatalf("shared device = %+v", d)
	}
	if r := byKind[model.FraudSelfReferral]; len(r) != 1 || r[0].Key != "hwid:HW1" || len(r[0].Users) != 2 {
		t.Fatalf("self referral = %+v", r)
	}
	if c := byKind[model.FraudFailedPayments]; len(c) != 1 || c[0].Count != 5 || c[0].Users[0].ID != card {
		t.Fatalf("failed payments = %+v", c)
	}
	if len(byKind[model.FraudPromoBurst])+len(byKind[model.FraudPaymentBurst])+len(byKind[model.FraudRefunds]) != 0 {
		t.Fatalf("signals from nothing: %+v", sigs)
	}
}
