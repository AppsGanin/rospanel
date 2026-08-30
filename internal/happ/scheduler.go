package happ

import (
	"context"
	"log/slog"
	"time"
)

// SyncFunc is the callback the Scheduler calls for every active subscription.
// Returns (added, updated, removed, error) for logging.
type SyncFunc func(ctx context.Context, subID int64) (SyncResult, error)

// Scheduler runs periodic subscription syncs. It is started once at server
// startup and stopped via context cancellation (graceful shutdown).
type Scheduler struct {
	interval time.Duration
	syncFn   SyncFunc
	listFn   func() ([]int64, error) // returns IDs of enabled subscriptions
}

// NewScheduler creates a Scheduler that syncs every interval.
// listFn should return the IDs of all enabled subscriptions.
// syncFn is called for each ID.
func NewScheduler(interval time.Duration, listFn func() ([]int64, error), syncFn SyncFunc) *Scheduler {
	if interval <= 0 {
		interval = 59 * time.Minute
	}
	return &Scheduler{
		interval: interval,
		listFn:   listFn,
		syncFn:   syncFn,
	}
}

// Run blocks until ctx is cancelled, running the sync loop in the background.
// Should be started as a goroutine: go s.Run(ctx).
func (s *Scheduler) Run(ctx context.Context) {
	slog.Info("happ scheduler started", "interval", s.interval)
	// Run once immediately on startup, then tick.
	s.runOnce(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("happ scheduler stopped")
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context) {
	ids, err := s.listFn()
	if err != nil {
		slog.Error("happ scheduler: list subscriptions", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	slog.Debug("happ scheduler: syncing subscriptions", "count", len(ids))
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}
		res, err := s.syncFn(ctx, id)
		if err != nil {
			slog.Warn("happ scheduler: sync failed", "sub_id", id, "err", err)
			continue
		}
		slog.Debug("happ scheduler: sync done",
			"sub_id", id,
			"added", res.Added,
			"updated", res.Updated,
			"removed", res.Removed,
			"total", res.Total,
		)
	}
}
