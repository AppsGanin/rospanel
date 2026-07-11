// Package autobackup runs scheduled local backups of the data directory.
//
// Scheduled backups previously existed only inside the Telegram service, so an
// operator who never configured a bot had no automatic backups at all. This runs the
// same schedule against local disk instead, independent of Telegram (both can be on
// — they're separate schedules and neither knows about the other).
package autobackup

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Panel is the slice of core.Manager this needs.
type Panel interface {
	BackupManifest() backup.Manifest
	Location() *time.Location // operator timezone; the cron is evaluated in it
}

type Service struct {
	panel   Panel
	store   *store.Store
	dataDir string

	// lastFired is the minute a backup last ran, seeded to the startup minute so a
	// restart can't re-fire a schedule that already matched the minute we came up.
	// Only touched from the single loop goroutine.
	lastFired time.Time
}

func New(panel Panel, st *store.Store, dataDir string) *Service {
	return &Service{
		panel:     panel,
		store:     st,
		dataDir:   dataDir,
		lastFired: time.Now().In(panel.Location()).Truncate(time.Minute),
	}
}

// Run wakes every minute and lets maybeBackup decide whether the schedule is due.
// A minute tick is the finest granularity a 5-field cron can express.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.maybeBackup()
		}
	}
}

// maybeBackup writes a backup when the operator's cron matches the current minute
// (in the operator timezone), then prunes old archives.
func (s *Service) maybeBackup() {
	set, err := s.store.GetSettings()
	if err != nil {
		return
	}
	expr := strings.TrimSpace(set.LocalBackupCron)
	if expr == "" {
		return
	}
	sched, err := cron.Parse(expr)
	if err != nil {
		slog.Warn("autobackup: bad cron expression", "cron", expr, "err", err)
		return
	}
	now := time.Now().In(s.panel.Location())
	if !sched.Match(now) {
		return
	}
	minute := now.Truncate(time.Minute)
	if minute.Equal(s.lastFired) {
		return
	}
	s.lastFired = minute

	if _, err := s.RunOnce(now, set.LocalBackupKeep); err != nil {
		slog.Error("autobackup: scheduled backup failed", "err", err)
	}
}

// RunOnce writes one archive and rotates the directory down to keep. Exported so the
// panel can offer a "back up now" action against the same code path the timer uses.
func (s *Service) RunOnce(now time.Time, keep int) (string, error) {
	path, err := backup.WriteLocal(s.dataDir, s.panel.BackupManifest(), s.store.Checkpoint, now)
	if err != nil {
		return "", err
	}
	slog.Info("autobackup: backup written", "path", path)

	// A rotation failure doesn't invalidate the backup we just took, so it's logged
	// rather than returned — the archive on disk is the thing that matters.
	if removed, rerr := backup.Rotate(s.dataDir, keep); rerr != nil {
		slog.Warn("autobackup: rotation failed", "err", rerr)
	} else if removed > 0 {
		slog.Info("autobackup: old archives removed", "count", removed, "keep", keep)
	}
	return path, nil
}
