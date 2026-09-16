package store

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The settings are kept decoded and read again only when the revision a trigger keeps
// has moved — so every way the row can change has to move it: a typed setter, a whole
// settings save, raw SQL no store method knows about, and a row replaced outright.
func TestSettingsCacheFollowsEveryWrite(t *testing.T) {
	st := newStore(t)
	get := func() *model.Settings {
		t.Helper()
		s, err := st.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := get()
	for _, step := range []struct {
		name  string
		write func() error
		seen  func(*model.Settings) bool
	}{
		{"a typed setter", func() error { return st.SetMasterLabel("Frankfurt") },
			func(s *model.Settings) bool { return s.MasterLabel == "Frankfurt" }},
		{"a setter of several columns", func() error { return st.SetLocalBackup("0 4 * * *", 9) },
			func(s *model.Settings) bool { return s.LocalBackupCron == "0 4 * * *" && s.LocalBackupKeep == 9 }},
		{"a setter in another file", func() error {
			p := model.DefaultConnPolicy()
			p.Countries = []string{"NL"}
			return st.SetConnPolicy(p)
		}, func(s *model.Settings) bool { return slices.Equal(s.ConnPolicy.Countries, []string{"NL"}) }},
		{"the config revision", st.MarkConfigApplied,
			func(s *model.Settings) bool { return s.ConfigRevision > before.ConfigRevision }},
		{"raw SQL", func() error {
			_, err := st.db.Exec(`UPDATE settings SET vless_fp = 'safari' WHERE id = 1`)
			return err
		}, func(s *model.Settings) bool { return s.VLESSFp == "safari" }},
		{"the row replaced whole", func() error {
			_, err := st.db.Exec(`REPLACE INTO settings (id, master_label) VALUES (1, 'replaced')`)
			return err
		}, func(s *model.Settings) bool { return s.MasterLabel == "replaced" && s.VLESSFp != "safari" }},
	} {
		get() // the copy kept is current before the write
		if err := step.write(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if !step.seen(get()) {
			t.Errorf("%s: the settings read after the write do not show it", step.name)
		}
	}
}

// Callers change the settings they are handed (applyTLSHints does), so each gets a copy:
// nothing one of them does reaches the next.
func TestSettingsReadsAreIndependent(t *testing.T) {
	st := newStore(t)
	rc := model.RoutingConfig{BlockDomains: []string{"a.example"}, Lanes: []model.EgressLane{{ID: "l1", Domains: []string{"x.example"}}}}
	if err := st.SetRoutingConfig(rc); err != nil {
		t.Fatal(err)
	}
	change := func(s *model.Settings) {
		s.MasterLabel = "changed by a caller"
		s.Routing.BlockDomains[0] = "changed.example"
		s.Routing.BlockDomains = append(s.Routing.BlockDomains, "appended.example")
		s.Routing.Lanes[0].Domains[0] = "changed.example"
	}
	untouched := func(when string) {
		t.Helper()
		s, err := st.GetSettings()
		if err != nil {
			t.Fatal(err)
		}
		if s.MasterLabel == "changed by a caller" ||
			!slices.Equal(s.Routing.BlockDomains, []string{"a.example"}) ||
			s.Routing.Lanes[0].Domains[0] != "x.example" {
			t.Errorf("%s: a caller's changes reached the next read: label %q, block %v, lane %v",
				when, s.MasterLabel, s.Routing.BlockDomains, s.Routing.Lanes[0].Domains)
		}
	}
	// The read right after the write decodes the row; the ones after it are served
	// from the copy kept. Neither may hand out what the next caller will get.
	decoded, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	change(decoded)
	untouched("changed what was read from the row")
	kept, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	change(kept)
	untouched("changed what was served from the copy kept")
}

// The copy kept is really what is served while the revision stands — the point of it —
// and a moved revision is really what replaces it. Shown by changing the row behind the
// trigger's back, which nothing but this test can do.
func TestSettingsAreServedFromTheCopyUntilTheRevisionMoves(t *testing.T) {
	st := newStore(t)
	if err := st.SetMasterLabel("kept"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetSettings(); s.MasterLabel != "kept" {
		t.Fatalf("label %q", s.MasterLabel)
	}
	if _, err := st.db.Exec(`DROP TRIGGER settings_rev_on_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE settings SET master_label = 'behind its back' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetSettings(); s.MasterLabel != "kept" {
		t.Errorf("the settings were read again with the revision unchanged: %q", s.MasterLabel)
	}
	if _, err := st.db.Exec(`UPDATE settings_rev SET v = v + 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetSettings(); s.MasterLabel != "behind its back" {
		t.Errorf("a moved revision did not bring the row back: %q", s.MasterLabel)
	}
}

// Many callers reading the settings and changing their copies, while others save: no
// caller's change reaches another, and once the saves stop the settings read are the
// ones last saved.
func TestSettingsCacheUnderConcurrentReadsAndSaves(t *testing.T) {
	st := newStore(t)
	if err := st.SetRoutingConfig(model.RoutingConfig{BlockDomains: []string{"base.example"}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 200 {
				if i < 2 && j%10 == 0 {
					if err := st.SetMasterLabel(fmt.Sprintf("label %d-%d", i, j)); err != nil {
						t.Error(err)
					}
					continue
				}
				s, err := st.GetSettings()
				if err != nil {
					t.Error(err)
					return
				}
				if len(s.Routing.BlockDomains) != 1 || s.Routing.BlockDomains[0] != "base.example" {
					t.Errorf("a reader was handed another caller's change: %v", s.Routing.BlockDomains)
					return
				}
				s.Routing.BlockDomains[0] = "changed.example"
				s.Routing.BlockDomains = append(s.Routing.BlockDomains, "appended.example")
				s.MasterLabel = "changed by a reader"
			}
		}()
	}
	wg.Wait()
	if err := st.SetMasterLabel("the last save"); err != nil {
		t.Fatal(err)
	}
	s, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.MasterLabel != "the last save" || !slices.Equal(s.Routing.BlockDomains, []string{"base.example"}) {
		t.Errorf("after the saves: label %q, block %v", s.MasterLabel, s.Routing.BlockDomains)
	}
}
