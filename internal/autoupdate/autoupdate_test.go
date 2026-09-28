package autoupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/updater"
)

type fakePanel struct {
	all, outdated int // what the node requests report
	askedAll      int
	askedOutdated []string
	notices       []string
}

func (p *fakePanel) Location() *time.Location { return time.UTC }
func (p *fakePanel) RequestAllNodesUpdate() (int, error) {
	p.askedAll++
	return p.all, nil
}
func (p *fakePanel) RequestOutdatedNodesUpdate(current string) (int, error) {
	p.askedOutdated = append(p.askedOutdated, current)
	return p.outdated, nil
}
func (p *fakePanel) NotifyUpdate(key string, _ ...any) { p.notices = append(p.notices, key) }

// newService runs "4.0.0" against a release the test picks; apply only records that
// it ran (after taking the backup, as the real one does) and restart is counted.
func newService(t *testing.T, rel *updater.Release, relErr, applyErr error) (*Service, *fakePanel, *store.Store, *int, *int) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := &fakePanel{all: 3, outdated: 2}
	applied, restarted := 0, 0
	s := &Service{
		panel: p, store: st, dataDir: dataDir, current: "4.0.0",
		latest: func(context.Context) (*updater.Release, error) { return rel, relErr },
		apply: func(_ context.Context, _ *updater.Release, backupFn func() error) error {
			if applyErr != nil {
				return applyErr
			}
			if err := backupFn(); err != nil {
				t.Fatalf("pre-update backup: %v", err)
			}
			applied++
			return nil
		},
		restart:   func() { restarted++ },
		supported: func() bool { return true },
	}
	return s, p, st, &applied, &restarted
}

func lastResult(t *testing.T, st *store.Store) string {
	t.Helper()
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	return set.AutoUpdateLast
}

// A newer release is installed after a backup, the servers are sent after it, and the
// service restarts.
func TestInstallsNewerRelease(t *testing.T) {
	s, p, st, applied, restarted := newService(t, &updater.Release{Version: "v4.1.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	if got := s.RunOnce(context.Background(), true); got != "updated:4.1.0" {
		t.Fatalf("result = %q", got)
	}
	if *applied != 1 || *restarted != 1 || p.askedAll != 1 {
		t.Fatalf("applied %d, restarted %d, servers asked %d", *applied, *restarted, p.askedAll)
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "pre-update-backup.tgz")); err != nil {
		t.Fatalf("no backup before the install: %v", err)
	}
	if lastResult(t, st) != "updated:4.1.0" || len(p.notices) != 1 || p.notices[0] != "notify.autoUpdatedNodes" {
		t.Fatalf("recorded %q, notices %v", lastResult(t, st), p.notices)
	}
}

// Without the servers following, only the panel updates.
func TestInstallsPanelOnly(t *testing.T) {
	s, p, _, applied, _ := newService(t, &updater.Release{Version: "4.1.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	s.RunOnce(context.Background(), false)
	if *applied != 1 || p.askedAll != 0 || p.notices[0] != "notify.autoUpdated" {
		t.Fatalf("applied %d, servers asked %d, notices %v", *applied, p.askedAll, p.notices)
	}
}

// A panel already current sends only the servers that lag; nothing restarts.
func TestCurrentPanelSendsLaggingServers(t *testing.T) {
	s, p, st, applied, restarted := newService(t, &updater.Release{Version: "4.0.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	if got := s.RunOnce(context.Background(), true); got != "nodes:2" {
		t.Fatalf("result = %q", got)
	}
	if *applied != 0 || *restarted != 0 || len(p.askedOutdated) != 1 || p.askedOutdated[0] != "4.0.0" {
		t.Fatalf("applied %d, restarted %d, outdated asks %v", *applied, *restarted, p.askedOutdated)
	}
	p.outdated = 0
	if got := s.RunOnce(context.Background(), true); got != "latest" || lastResult(t, st) != "latest" {
		t.Fatalf("nothing to do = %q", got)
	}
}

// A failed check or install is recorded and reported, and nothing restarts or moves
// the servers.
func TestFailuresAreReportedNotActedOn(t *testing.T) {
	s, p, st, _, restarted := newService(t, nil, errors.New("github down"), nil)
	if got := s.RunOnce(context.Background(), true); got != "error:release check: github down" {
		t.Fatalf("result = %q", got)
	}
	if *restarted != 0 || p.askedAll != 0 || lastResult(t, st) == "" || p.notices[0] != "notify.autoUpdateFailed" {
		t.Fatalf("restarted %d, servers asked %d, notices %v", *restarted, p.askedAll, p.notices)
	}
	s2, p2, _, _, restarted2 := newService(t, &updater.Release{Version: "4.1.0", AssetURL: "x", ChecksumURL: "y"}, nil, errors.New("checksum mismatch"))
	if got := s2.RunOnce(context.Background(), true); got != "error:install v4.1.0: checksum mismatch" {
		t.Fatalf("result = %q", got)
	}
	if *restarted2 != 0 || p2.askedAll != 0 {
		t.Fatal("a failed install restarted or sent the servers ahead")
	}
}

// The schedule fires once in its minute, and not at all while it is off.
func TestScheduleFiresOncePerMinute(t *testing.T) {
	s, _, st, applied, _ := newService(t, &updater.Release{Version: "4.1.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	s.maybeUpdate(context.Background()) // off: no cron
	if err := st.SetAutoUpdate("* * * * *", false); err != nil {
		t.Fatal(err)
	}
	s.maybeUpdate(context.Background())
	s.maybeUpdate(context.Background())
	if *applied != 1 {
		t.Fatalf("applied %d times in one minute", *applied)
	}
}

// A restart that never comes (no systemd to do it) must not have the next attempt
// install the same release again — a second swap would overwrite the rollback copy.
func TestNoReinstallWhileWaitingForRestart(t *testing.T) {
	s, p, _, applied, _ := newService(t, &updater.Release{Version: "4.1.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	s.RunOnce(context.Background(), true)
	for range 3 {
		if got := s.RunOnce(context.Background(), true); got == "updated:4.1.0" {
			t.Fatal("the same release was installed again")
		}
	}
	if *applied != 1 {
		t.Fatalf("applied %d times", *applied)
	}
	// Told once that it is stuck, not on every attempt.
	failures := 0
	for _, n := range p.notices {
		if n == "notify.autoUpdateFailed" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("failure notices = %d (%v)", failures, p.notices)
	}
}

// Outside systemd nothing is installed, and the operator hears it once.
func TestUnsupportedWithoutSystemd(t *testing.T) {
	s, p, _, applied, _ := newService(t, &updater.Release{Version: "4.1.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	s.supported = func() bool { return false }
	s.RunOnce(context.Background(), true)
	if got := s.RunOnce(context.Background(), true); got != "unsupported" || *applied != 0 {
		t.Fatalf("result %q, applied %d", got, *applied)
	}
	if len(p.notices) != 1 || p.notices[0] != "notify.autoUpdateUnsupported" {
		t.Fatalf("notices = %v", p.notices)
	}
}

// The servers are measured against the release GitHub has, not a panel build ahead
// of it; a release still being published is not a failure.
func TestLaggingTargetAndUnfinishedRelease(t *testing.T) {
	s, p, _, _, _ := newService(t, &updater.Release{Version: "3.9.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	s.RunOnce(context.Background(), true)
	if len(p.askedOutdated) != 1 || p.askedOutdated[0] != "3.9.0" {
		t.Fatalf("servers measured against %v, want the published 3.9.0", p.askedOutdated)
	}
	s2, p2, _, applied, _ := newService(t, &updater.Release{Version: "4.1.0", AssetURL: "x"}, nil, nil)
	p2.outdated = 0
	if got := s2.RunOnce(context.Background(), true); got != "latest" || *applied != 0 {
		t.Fatalf("a release without SHA256SUMS: %q, applied %d", got, *applied)
	}
}

// The same servers lagging day after day are reported once.
func TestLaggingServersReportedOnce(t *testing.T) {
	s, p, _, _, _ := newService(t, &updater.Release{Version: "4.0.0", AssetURL: "x", ChecksumURL: "y"}, nil, nil)
	for range 3 {
		s.RunOnce(context.Background(), true)
	}
	if len(p.notices) != 1 {
		t.Fatalf("notices = %v, want one", p.notices)
	}
}
