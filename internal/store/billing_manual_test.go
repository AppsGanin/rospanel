package store

import (
	"path/filepath"
	"testing"
)

// Manual payment used to be what a panel fell back to while no provider was enabled.
// The switch that replaces that fallback starts where the fallback was in effect, so
// an upgrade changes nothing for either kind of install.
func TestManualPaymentMigrationKeepsWhatWasOffered(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		provider bool // an enabled provider row before the upgrade
		want     bool
	}{
		{"no provider — manual was the only way to pay", false, true},
		{"a provider was taking payments", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "pre-manual.db")
			db := dbBeforeMigration(t, path, "0082")
			if c.provider {
				if _, err := db.Exec(
					`INSERT INTO payment_providers (key, enabled, config) VALUES ('cryptobot', 1, '{}')`,
				); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()

			st, err := Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer st.Close()
			set, err := st.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			if set.BillingManualEnabled != c.want {
				t.Errorf("manual payment = %v, want %v", set.BillingManualEnabled, c.want)
			}
		})
	}
}

// The switch is stored and read back like every other billing setting.
func TestManualPaymentSettingRoundTrips(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.BillingManualEnabled = true
	set.BillingPaymentNote = "card 0000"
	set.BillingManualLabel = "By transfer"
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !got.BillingManualEnabled || got.BillingPaymentNote != "card 0000" ||
		got.BillingManualLabel != "By transfer" {
		t.Fatalf("manual=%v note=%q label=%q", got.BillingManualEnabled, got.BillingPaymentNote, got.BillingManualLabel)
	}
	got.BillingManualEnabled = false
	if err := st.SetBillingSettings(got); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.GetSettings(); again.BillingManualEnabled {
		t.Error("manual payment stayed on after it was switched off")
	}
}
