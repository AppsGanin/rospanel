package autobackup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/store"
)

type fakePanel struct{ loc *time.Location }

func (p fakePanel) BackupManifest() backup.Manifest {
	return backup.Manifest{Domain: "panel.example.com", UserCount: 3}
}
func (p fakePanel) Location() *time.Location { return p.loc }

// newService opens a real store inside the data dir (the archive is a tar of that
// dir, and Checkpoint needs a database) in the given operator zone.
func newService(t *testing.T, loc *time.Location) (*Service, *store.Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(fakePanel{loc: loc}, st, dataDir), st, dataDir
}

func archives(t *testing.T, dataDir string) []string {
	t.Helper()
	names, err := backup.ListLocal(dataDir)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	return names
}

// RunOnce is both the timer's path and the "back up now" button: one archive per
// call, named by the time given, and the directory trimmed to keep afterwards.
func TestRunOnceWritesAnArchiveAndPrunesToKeep(t *testing.T) {
	svc, _, dataDir := newService(t, time.UTC)
	base := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)

	var last string
	for i := range 4 {
		path, err := svc.RunOnce(base.Add(time.Duration(i)*time.Minute), 2)
		if err != nil {
			t.Fatalf("RunOnce #%d: %v", i, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("RunOnce returned %s but it does not exist", path)
		}
		if !strings.HasPrefix(path, filepath.Join(dataDir, backup.LocalBackupDir)) {
			t.Errorf("archive %s is outside the data dir's backup folder", path)
		}
		last = path
	}
	got := archives(t, dataDir)
	if len(got) != 2 {
		t.Fatalf("%d archives kept, want 2: %v", len(got), got)
	}
	// The newest two survive — including the one just written.
	if !strings.HasSuffix(last, got[len(got)-1]) && !strings.HasSuffix(last, got[0]) {
		t.Errorf("the archive just written (%s) was pruned; kept %v", last, got)
	}
	for _, n := range got {
		if !strings.Contains(n, "030200") && !strings.Contains(n, "030300") {
			t.Errorf("an older archive %s survived rotation", n)
		}
	}
}

// keep <= 0 retains everything: an unset setting must never mean "delete all".
func TestRunOnceWithZeroKeepDeletesNothing(t *testing.T) {
	svc, _, dataDir := newService(t, time.UTC)
	base := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	for i := range 3 {
		if _, err := svc.RunOnce(base.Add(time.Duration(i)*time.Minute), 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := archives(t, dataDir); len(got) != 3 {
		t.Errorf("%d archives, want all 3 with keep=0", len(got))
	}
}

// The schedule is a minute-granular cron and a restart must not re-fire the
// minute the panel came up in, so New seeds lastFired to that minute and every
// matching minute fires exactly once.
func TestMaybeBackupFiresOncePerMatchingMinute(t *testing.T) {
	svc, st, dataDir := newService(t, time.UTC)
	if err := st.SetLocalBackup("* * * * *", 5); err != nil {
		t.Fatal(err)
	}
	startMinute := time.Now().UTC().Truncate(time.Minute)
	if !svc.lastFired.Equal(startMinute) && !svc.lastFired.Equal(startMinute.Add(-time.Minute)) {
		t.Fatalf("lastFired seeded to %v, want the startup minute %v", svc.lastFired, startMinute)
	}

	svc.maybeBackup()
	if time.Now().UTC().Truncate(time.Minute).Equal(svc.lastFired) && len(archives(t, dataDir)) != 0 {
		t.Fatal("a backup ran in the startup minute; a restart loop would back up on every boot")
	}

	// Pretend the last run was a minute ago: the current minute is now due.
	svc.lastFired = svc.lastFired.Add(-time.Minute)
	svc.maybeBackup()
	if got := archives(t, dataDir); len(got) != 1 {
		t.Fatalf("%d archives after a due minute, want 1", len(got))
	}
	now := time.Now().UTC().Truncate(time.Minute)
	if !svc.lastFired.Equal(now) {
		t.Errorf("lastFired = %v after firing, want %v", svc.lastFired, now)
	}
	// Same minute again (the ticker can wake twice in one minute): no second run.
	svc.maybeBackup()
	if got := archives(t, dataDir); len(got) != 1 && time.Now().UTC().Truncate(time.Minute).Equal(now) {
		t.Errorf("%d archives after a repeat tick in the same minute, want 1", len(got))
	}
}

// No schedule, or one the parser rejects, is a silent no-op — never a backup on
// every tick, never a crash of the loop.
func TestMaybeBackupIgnoresEmptyAndBadCron(t *testing.T) {
	svc, st, dataDir := newService(t, time.UTC)
	for _, expr := range []string{"", "   ", "not a cron", "* * * *", "61 * * * *"} {
		if err := st.SetLocalBackup(expr, 5); err != nil {
			t.Fatal(err)
		}
		svc.lastFired = svc.lastFired.Add(-time.Hour)
		svc.maybeBackup()
		if got := archives(t, dataDir); len(got) != 0 {
			t.Errorf("cron %q produced a backup", expr)
		}
	}
}

// The cron is evaluated in the operator's timezone, not the server's: an operator
// in Vladivostok who writes "0 3 * * *" means 3 AM their time.
func TestMaybeBackupEvaluatesTheCronInTheOperatorZone(t *testing.T) {
	zone := time.FixedZone("far", 10*3600)
	svc, st, dataDir := newService(t, zone)

	nowThere := time.Now().In(zone)
	// Match the current hour in the operator zone but not in UTC (they differ by
	// ten hours, so they can never coincide).
	if err := st.SetLocalBackup("* "+itoa(nowThere.Hour())+" * * *", 5); err != nil {
		t.Fatal(err)
	}
	svc.lastFired = svc.lastFired.Add(-time.Hour)
	svc.maybeBackup()
	if time.Now().In(zone).Hour() != nowThere.Hour() {
		t.Skip("the hour rolled over during the test")
	}
	if got := archives(t, dataDir); len(got) != 1 {
		t.Fatalf("%d archives, want 1: the cron did not match in the operator zone", len(got))
	}

	// The same hour expressed in UTC must NOT match.
	if err := st.SetLocalBackup("* "+itoa(time.Now().UTC().Hour())+" * * *", 5); err != nil {
		t.Fatal(err)
	}
	svc.lastFired = svc.lastFired.Add(-time.Hour)
	svc.maybeBackup()
	if got := archives(t, dataDir); len(got) != 1 {
		t.Errorf("%d archives: the cron matched against server time", len(got))
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
