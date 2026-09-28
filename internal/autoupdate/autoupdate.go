// Package autoupdate installs new releases on a schedule: at the operator's cron (in
// the panel's timezone) it asks GitHub for the latest release and, when it is newer,
// installs it the way the Update button does — a backup first, the binary checked
// against SHA256SUMS, swapped in, the service restarted — and, if the operator wants,
// sends the servers after it. A panel already on the latest release still sends the
// servers that lag behind it.
//
// The outcome of the last attempt is kept in settings (auto_update_last) as one of:
//
//	updated:<version>   the panel installed <version> and restarted
//	latest              nothing to do
//	nodes:<n>           the panel was current; n lagging servers were told to update
//	unsupported         the panel does not run as a systemd service, which the restart
//	                    after an install needs (a container updates with its image)
//	error:<message>     the check or the install failed
//
// The admin bot hears of an install every time, and of anything else only when it
// differs from the last attempt's outcome — a server that cannot update, or a
// failure that repeats, is told once, not every day.
package autoupdate

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/updater"
	"github.com/AppsGanin/rospanel/internal/version"
)

// Panel is the slice of core.Manager this needs.
type Panel interface {
	Location() *time.Location // the cron is evaluated in the panel's timezone
	RequestAllNodesUpdate() (int, error)
	RequestOutdatedNodesUpdate(current string) (int, error)
	NotifyUpdate(key string, args ...any)
}

// Service is the scheduler. The hooks are the updater's own functions, swapped out
// in tests.
type Service struct {
	panel   Panel
	store   *store.Store
	dataDir string
	current string // the running version

	latest    func(ctx context.Context) (*updater.Release, error)
	apply     func(ctx context.Context, rel *updater.Release, backupFn func() error) error
	restart   func()
	supported func() bool

	// installed is the version this process swapped in and is waiting to restart
	// into. Should the restart not come, the next attempt must not download it again:
	// a second swap would move the new binary onto <exe>.bak, over the only copy of
	// the old one.
	installed string

	// lastFired is the minute an attempt last ran, seeded to the startup minute so the
	// restart an update ends in cannot fire the same schedule again. Only touched from
	// the loop goroutine.
	lastFired time.Time
}

// New builds the scheduler for the running binary.
func New(panel Panel, st *store.Store, dataDir string) *Service {
	return &Service{
		panel:   panel,
		store:   st,
		dataDir: dataDir,
		current: version.Version,
		latest: func(ctx context.Context) (*updater.Release, error) {
			return updater.Latest(ctx, updater.ConfiguredRepo())
		},
		apply:     updater.Apply,
		restart:   updater.Restart,
		supported: updater.UnderSystemd,
		lastFired: time.Now().In(panel.Location()).Truncate(time.Minute),
	}
}

// Run wakes every minute and lets the schedule decide.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.maybeUpdate(ctx)
		}
	}
}

func (s *Service) maybeUpdate(ctx context.Context) {
	set, err := s.store.GetSettings()
	if err != nil {
		return
	}
	expr := strings.TrimSpace(set.AutoUpdateCron)
	if expr == "" {
		return
	}
	sched, err := cron.Parse(expr)
	if err != nil {
		slog.Warn("autoupdate: bad cron expression", "cron", expr, "err", err)
		return
	}
	now := time.Now().In(s.panel.Location())
	minute := now.Truncate(time.Minute)
	if !sched.Match(now) || minute.Equal(s.lastFired) {
		return
	}
	s.lastFired = minute
	s.RunOnce(ctx, set.AutoUpdateNodes)
}

// RunOnce makes one attempt and records its outcome. On an install it ends by
// restarting the service, so nothing after it runs in this process.
func (s *Service) RunOnce(ctx context.Context, nodes bool) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	prev := ""
	if set, err := s.store.GetSettings(); err == nil {
		prev = set.AutoUpdateLast
	}
	result, notice := s.attempt(ctx, nodes)
	if err := s.store.SetAutoUpdateResult(time.Now().Unix(), result); err != nil {
		slog.Error("autoupdate: result not recorded", "err", err)
	}
	installed := strings.HasPrefix(result, "updated:")
	if notice != nil && (installed || result != prev) {
		s.panel.NotifyUpdate(notice.key, notice.args...)
	}
	if installed {
		s.restart()
	}
	return result
}

// notice is what the admin bot is told about an attempt.
type notice struct {
	key  string
	args []any
}

func (s *Service) attempt(ctx context.Context, nodes bool) (string, *notice) {
	if !s.supported() {
		slog.Warn("autoupdate: skipped — the panel does not run as a systemd service")
		return "unsupported", &notice{key: "notify.autoUpdateUnsupported"}
	}
	rel, err := s.latest(ctx)
	if err != nil {
		return failed(fmt.Errorf("release check: %w", err))
	}
	// A release still being published — its binary or its SHA256SUMS not uploaded yet
	// — is not there yet, rather than a failure: the next attempt finds it whole.
	ready := rel.AssetURL != "" && rel.ChecksumURL != ""
	if ready && updater.IsNewer(rel.Version, s.current) {
		to := strings.TrimPrefix(rel.Version, "v")
		if s.installed != "" && !updater.IsNewer(to, s.installed) {
			return failed(fmt.Errorf("v%s is installed, but the panel has not restarted into it", s.installed))
		}
		return s.install(ctx, rel, to, nodes)
	}
	if !nodes {
		return "latest", nil
	}
	// The servers update to the latest release GitHub has, so that — not a panel
	// build ahead of it — is what they are measured against.
	target := s.current
	if ready && updater.IsNewer(s.current, rel.Version) {
		target = strings.TrimPrefix(rel.Version, "v")
	}
	n, err := s.panel.RequestOutdatedNodesUpdate(target)
	if err != nil {
		return failed(fmt.Errorf("servers: %w", err))
	}
	if n == 0 {
		return "latest", nil
	}
	slog.Info("autoupdate: lagging servers told to update", "count", n, "version", target)
	return fmt.Sprintf("nodes:%d", n), &notice{key: "notify.autoUpdateNodes", args: []any{target, n}}
}

// install puts the release in place of the running binary, after a backup, and
// sends the servers after it.
func (s *Service) install(ctx context.Context, rel *updater.Release, to string, nodes bool) (string, *notice) {
	backupFn := func() error {
		_ = s.store.Checkpoint() // flush the WAL so the snapshot is current
		return backup.Create(s.dataDir, filepath.Join(s.dataDir, "pre-update-backup.tgz"))
	}
	if err := s.apply(ctx, rel, backupFn); err != nil {
		return failed(fmt.Errorf("install v%s: %w", to, err))
	}
	s.installed = to
	// The servers are sent only once the panel's own binary is in place: a panel whose
	// download failed never leaves them a release ahead of it. The command waits on
	// disk, so a server takes it on its next sync, before the restart or after.
	if !nodes {
		slog.Info("autoupdate: release installed, restarting", "from", s.current, "to", to)
		return "updated:" + to, &notice{key: "notify.autoUpdated", args: []any{s.current, to}}
	}
	sent, err := s.panel.RequestAllNodesUpdate()
	if err != nil {
		slog.Error("autoupdate: the servers were not told to update", "err", err)
	}
	slog.Info("autoupdate: release installed, restarting", "from", s.current, "to", to, "servers", sent)
	return "updated:" + to, &notice{key: "notify.autoUpdatedNodes", args: []any{s.current, to, sent}}
}

func failed(err error) (string, *notice) {
	slog.Error("autoupdate: failed", "err", err)
	return "error:" + err.Error(), &notice{key: "notify.autoUpdateFailed", args: []any{err.Error()}}
}
